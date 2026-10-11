// Package artifactclient implements the central Artifact v2 internal protocol.
package artifactclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
)

type Client struct {
	base, token string
	http        *http.Client
}

func New(base, token string) (*Client, error) {
	return NewWithCA(base, token, "")
}

// NewWithCA optionally extends the system trust store for an internal HTTPS
// Artifact endpoint. The endpoint and bearer token remain independently
// validated; the CA file is never sent to the service.
func NewWithCA(base, token, caFile string) (*Client, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return nil, world.ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return nil, world.ErrInvalid
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, world.ErrInvalid
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, world.ErrInvalid
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, p world.Principal, method, path string, body []byte) (*http.Response, error) {
	if !p.Valid() {
		return nil, world.ErrForbidden
	}
	r, e := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if e != nil {
		return nil, world.ErrDependency
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("X-User-Id", p.SubjectID)
	r.Header.Set("X-Organization-Id", p.OrganizationID)
	r.Header.Set("X-Request-Id", p.RequestID)
	r.Header.Set("Content-Type", "application/json")
	response, e := c.http.Do(r)
	if e != nil {
		return nil, world.ErrDependency
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	var failure struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if strictjson.Decode(response.Body, 4096, &failure) != nil {
		return nil, world.ErrDependency
	}
	for code, err := range map[string]error{"INVALID_ARGUMENT": world.ErrInvalid, "PERMISSION_DENIED": world.ErrForbidden, "NOT_FOUND": world.ErrNotFound, "IDEMPOTENCY_CONFLICT": world.ErrIdempotency, "CONTENT_NOT_READY": world.ErrNotReady} {
		if failure.Code == code {
			return nil, err
		}
	}
	return nil, world.ErrDependency
}
func (c *Client) json(ctx context.Context, p world.Principal, method, path string, input, output any) error {
	var data []byte
	var e error
	if input != nil {
		data, e = json.Marshal(input)
		if e != nil {
			return world.ErrInvalid
		}
	}
	r, e := c.request(ctx, p, method, path, data)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return world.ErrDependency
	}
	if strictjson.Decode(r.Body, 4<<20, output) != nil {
		return world.ErrDependency
	}
	return nil
}
func (c *Client) Write(ctx context.Context, p world.Principal, projectID string, w world.TextWrite) (world.ContentVersion, error) {
	return c.write(ctx, p, world.Source{Kind: "user_edit", ProjectID: projectID}, w)
}
func (c *Client) WriteAssetManifest(ctx context.Context, p world.Principal, assetID, versionID string, w world.TextWrite) (world.ContentVersion, error) {
	return c.write(ctx, p, world.Source{Kind: "asset_manifest", AssetID: assetID, AssetVersionID: versionID}, w)
}
func (c *Client) write(ctx context.Context, p world.Principal, source world.Source, w world.TextWrite) (world.ContentVersion, error) {
	var v world.ContentVersion
	sum := sha256.Sum256([]byte(w.Text))
	hash := hex.EncodeToString(sum[:])
	req := struct {
		WriteID string       `json:"write_id"`
		Source  world.Source `json:"source"`
		SHA256  string       `json:"sha256"`
		Size    int          `json:"size"`
		MIME    string       `json:"mime"`
	}{w.WriteID, source, hash, len(w.Text), w.MIME}
	var upload struct {
		UploadID    string `json:"upload_id"`
		VersionID   string `json:"version_id"`
		ArtifactID  string `json:"artifact_id"`
		State       string `json:"state"`
		ContentPath string `json:"content_path"`
	}
	if e := c.json(ctx, p, "POST", "/v2/artifacts/uploads", req, &upload); e != nil {
		return v, e
	}
	if !world.ValidID(upload.UploadID) || !world.ValidID(upload.VersionID) || !world.ValidID(upload.ArtifactID) || (upload.State != "prepared" && upload.State != "committed") || upload.ContentPath != "/v2/artifacts/uploads/"+upload.UploadID+"/content" {
		return v, world.ErrDependency
	}
	if upload.State == "prepared" {
		r, e := c.request(ctx, p, "PUT", upload.ContentPath, []byte(w.Text))
		if e != nil {
			return v, e
		}
		io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
		r.Body.Close()
		if r.StatusCode != 200 {
			return v, world.ErrDependency
		}
	}
	e := c.json(ctx, p, "POST", "/v2/artifacts/uploads/"+upload.UploadID+"/commit", nil, &v)
	if e != nil {
		return v, e
	}
	if !validVersion(v, p) || v.ArtifactID != upload.ArtifactID || v.VersionID != upload.VersionID || v.SHA256 != hash || v.Size != int64(len(w.Text)) || v.MIME != w.MIME || v.Source != req.Source {
		return world.ContentVersion{}, world.ErrDependency
	}
	return v, nil
}
func validVersion(v world.ContentVersion, p world.Principal) bool {
	return v.SchemaVersion == 2 && v.ContentRef.Valid() && v.OrganizationID == p.OrganizationID && v.VersionNo > 0 && world.ValidID(v.CreatedBy) && v.Size >= 0 && len(v.SHA256) == 64 && !v.CreatedAt.IsZero()
}
func path(ref world.ContentRef, a world.Access, content bool) string {
	q := url.Values{"store_id": {ref.StoreID}, "artifact_id": {ref.ArtifactID}}
	if a.ProjectID != "" {
		q.Set("project_id", a.ProjectID)
	}
	if a.RevisionID != "" {
		q.Set("project_revision_id", a.RevisionID)
	}
	if a.AssetID != "" {
		q.Set("asset_id", a.AssetID)
	}
	if a.AssetVersionID != "" {
		q.Set("asset_version_id", a.AssetVersionID)
	}
	suffix := ""
	if content {
		suffix = "/content"
	}
	return "/v2/artifacts/" + ref.VersionID + suffix + "?" + q.Encode()
}
func (c *Client) Metadata(ctx context.Context, p world.Principal, ref world.ContentRef, a world.Access) (world.ContentVersion, error) {
	var v world.ContentVersion
	if !ref.Valid() {
		return v, world.ErrInvalid
	}
	e := c.json(ctx, p, "GET", path(ref, a, false), nil, &v)
	if e != nil {
		return v, e
	}
	if !validVersion(v, p) || v.ContentRef != ref {
		return v, world.ErrDependency
	}
	return v, nil
}
func (c *Client) Read(ctx context.Context, p world.Principal, ref world.ContentRef, a world.Access, max int64) ([]byte, error) {
	if max <= 0 || max > 4<<20 {
		return nil, world.ErrInvalid
	}
	v, e := c.Metadata(ctx, p, ref, a)
	if e != nil {
		return nil, e
	}
	if v.Size > max {
		return nil, world.ErrInvalid
	}
	r, e := c.request(ctx, p, "GET", path(ref, a, true), nil)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, world.ErrDependency
	}
	data, e := io.ReadAll(io.LimitReader(r.Body, max+1))
	if e != nil {
		return nil, world.ErrDependency
	}
	h := sha256.Sum256(data)
	if int64(len(data)) != v.Size || hex.EncodeToString(h[:]) != v.SHA256 {
		return nil, world.ErrNotReady
	}
	return data, nil
}

// Download streams an authorized immutable version into a caller-owned staging
// writer. The caller must not expose partial output when this returns an error.
func (c *Client) Download(ctx context.Context, p world.Principal, ref world.ContentRef, a world.Access, dst io.Writer, max int64) error {
	if dst == nil || max < 0 || max > 1<<50 {
		return world.ErrInvalid
	}
	v, e := c.Metadata(ctx, p, ref, a)
	if e != nil {
		return e
	}
	if v.Size > max {
		return world.ErrInvalid
	}
	r, e := c.request(ctx, p, "GET", path(ref, a, true), nil)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return world.ErrDependency
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(dst, h), io.LimitReader(r.Body, v.Size+1))
	if e != nil {
		return e
	}
	if n != v.Size || hex.EncodeToString(h.Sum(nil)) != v.SHA256 {
		return world.ErrNotReady
	}
	return nil
}
func (c *Client) Retain(ctx context.Context, p world.Principal, o world.RetentionOwner, refs []world.ContentRef) error {
	req := struct {
		world.RetentionOwner
		Refs []world.ContentRef `json:"refs"`
	}{o, refs}
	var result struct {
		world.RetentionOwner
		Refs     []world.ContentRef `json:"refs"`
		Released bool               `json:"released"`
	}
	if e := c.json(ctx, p, "POST", "/v2/artifacts/retentions", req, &result); e != nil {
		return e
	}
	if result.RetentionOwner != o || result.Released || len(result.Refs) != len(refs) {
		return world.ErrDependency
	}
	seen := map[world.ContentRef]bool{}
	for _, ref := range refs {
		if seen[ref] {
			return world.ErrInvalid
		}
		seen[ref] = true
	}
	for _, ref := range result.Refs {
		if !seen[ref] {
			return world.ErrDependency
		}
		delete(seen, ref)
	}
	if len(seen) != 0 {
		return world.ErrDependency
	}
	return nil
}
