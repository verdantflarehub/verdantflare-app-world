package world

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type ProjectClient struct {
	origin, token string
	http          *http.Client
}

func NewProjectClient(origin, token string) (*ProjectClient, error) {
	return NewProjectClientWithCA(origin, token, "")
}
func NewProjectClientWithCA(origin, token, caFile string) (*ProjectClient, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
		return nil, ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, ErrInvalid
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, ErrInvalid
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, ErrInvalid
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	}
	return &ProjectClient{origin: strings.TrimSuffix(origin, "/"), token: token, http: &http.Client{Timeout: 30 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *ProjectClient) call(ctx context.Context, p Principal, path string, input any) ([]byte, error) {
	if !p.Valid() {
		return nil, ErrForbidden
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, "POST", c.origin+"/internal/v1/project/"+path, bytes.NewReader(data))
	if err != nil {
		return nil, ErrDependency
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Organization-Id", p.OrganizationID)
	r.Header.Set("X-User-Id", p.SubjectID)
	r.Header.Set("X-Request-Id", p.RequestID)
	response, err := c.http.Do(r)
	if err != nil {
		return nil, ErrDependency
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		switch response.StatusCode {
		case 401, 403:
			return nil, ErrForbidden
		case 404:
			return nil, ErrNotFound
		default:
			return nil, ErrDependency
		}
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, MaxManifestBytes+1))
	if err != nil || len(data) > MaxManifestBytes {
		return nil, ErrDependency
	}
	return data, nil
}
func (c *ProjectClient) Open(ctx context.Context, p Principal, projectID, revisionID string) (OpenResult, error) {
	var out OpenResult
	if !ValidID(projectID) || !ValidID(revisionID) {
		return out, ErrInvalid
	}
	data, err := c.call(ctx, p, "open", struct {
		ProjectID  string `json:"project_id"`
		RevisionID string `json:"revision_id"`
	}{projectID, revisionID})
	if err != nil {
		return out, err
	}
	if _, err = strictjson.ReadValue(bytes.NewReader(data), MaxManifestBytes); err != nil {
		return out, ErrDependency
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return out, ErrDependency
	}
	manifest, err := Decode(raw["manifest"])
	if err != nil {
		return out, ErrDependency
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw["manifest"], &fields)
	delete(fields, "extensions")
	raw["manifest"], _ = json.Marshal(fields)
	bounded, _ := json.Marshal(raw)
	// Project lifecycle metadata is part of the upstream open response. Keep
	// it in a bounded wire envelope; it must not grant write rights in World.
	var wire struct {
		OpenResult
		ParentRevisionID *string    `json:"parent_revision_id,omitempty"`
		CreatedAt        *time.Time `json:"created_at,omitempty"`
		CanWrite         *bool      `json:"can_write,omitempty"`
		CanDelete        *bool      `json:"can_delete,omitempty"`
		LifecycleVersion *int64     `json:"lifecycle_version,omitempty"`
	}
	if strictjson.Decode(bytes.NewReader(bounded), MaxManifestBytes, &wire) != nil {
		return out, ErrDependency
	}
	if wire.ParentRevisionID != nil && *wire.ParentRevisionID != "" && !ValidID(*wire.ParentRevisionID) || wire.LifecycleVersion != nil && *wire.LifecycleVersion < 0 {
		return out, ErrDependency
	}
	out = wire.OpenResult
	out.Manifest = manifest
	if out.ProjectID != projectID || out.RevisionID != revisionID || out.Manifest.ProjectID != projectID || !ValidID(out.HeadRevisionID) || !out.ManifestRef.Valid() {
		return OpenResult{}, ErrDependency
	}
	return out, nil
}
func (c *ProjectClient) Authorize(ctx context.Context, p Principal, permission Permission) error {
	if permission.Action != "read" || permission.Access == nil || !ValidID(permission.Access.ProjectID) || !ValidID(permission.Access.RevisionID) || permission.Access.AssetID != "" || permission.Access.AssetVersionID != "" {
		return ErrForbidden
	}
	data, err := c.call(ctx, p, "authorize_content", permission)
	if err != nil {
		return err
	}
	var out struct {
		Allowed bool `json:"allowed"`
	}
	if strictjson.Decode(bytes.NewReader(data), 4096, &out) != nil || !out.Allowed {
		return ErrForbidden
	}
	return nil
}
