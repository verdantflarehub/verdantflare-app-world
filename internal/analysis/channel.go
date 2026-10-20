package analysis

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

var ErrRejected = errors.New("analysis submission explicitly rejected")

// GoogleChannel talks only to the configured internal execution origin. It has
// no provider key, no proxy inheritance and no redirect or automatic POST retry.
type GoogleChannel struct {
	origin, token string
	http          *http.Client
}

func NewGoogleChannel(origin, token, caFile string) (*GoogleChannel, error) {
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
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, e := os.ReadFile(caFile)
		if e != nil {
			return nil, ErrInvalid
		}
		pool, e := x509.SystemCertPool()
		if e != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, ErrInvalid
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	return &GoogleChannel{origin: strings.TrimSuffix(origin, "/"), token: token, http: &http.Client{Timeout: 20 * time.Minute, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *GoogleChannel) request(ctx context.Context, org, method, path string, body []byte) ([]byte, error) {
	if !validUUID(org) {
		return nil, ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Organization-Id", org)
	req.Header.Set("X-Caller-Id", "world-analysis")
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	data, err := strictjson.ReadValue(io.LimitReader(res.Body, (8<<20)+1), 8<<20)
	if err != nil {
		return nil, ErrUnavailable
	}
	if res.StatusCode == 404 && method == "GET" {
		return nil, ErrMissing
	}
	if res.StatusCode != http.StatusOK {
		var rejected struct {
			SchemaVersion int    `json:"schema_version"`
			Accepted      *bool  `json:"accepted"`
			State         string `json:"state"`
			CallID        string `json:"call_id"`
		}
		if method == "POST" && json.Unmarshal(data, &rejected) == nil && rejected.SchemaVersion == 1 && rejected.Accepted != nil && !*rejected.Accepted && rejected.State == "rejected" && rejected.CallID == "" {
			return nil, ErrRejected
		}
		return nil, ErrUnavailable
	}
	return data, nil
}
func decodeCall(data []byte, err error) (Call, error) {
	if err != nil {
		return Call{}, err
	}
	var call Call
	if json.Unmarshal(data, &call) != nil {
		return Call{}, ErrUnavailable
	}
	return call, nil
}
func (c *GoogleChannel) Submit(ctx context.Context, org string, f FrozenRequest, media []byte) (Call, error) {
	if f.Validate() != nil || int64(len(media)) != f.MediaSize || hashBytes(media) != f.Prepared.MediaHash {
		return Call{}, ErrInvalid
	}
	var payload map[string]any
	if json.Unmarshal(f.Payload, &payload) != nil {
		return Call{}, ErrInvalid
	}
	payload["media"] = map[string]any{"mime_type": f.Prepared.MIME, "sha256": f.Prepared.MediaHash, "data": base64.StdEncoding.EncodeToString(media)}
	request := map[string]any{"idempotency_key": f.OperationID, "task_id": f.TaskID, "model_id": f.ModelID, "capability": "analysis", "payload": payload, "budget_limit": f.BudgetLimit}
	if f.AccountID != "" {
		request["account_id"] = f.AccountID
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > 68<<20 {
		return Call{}, ErrInvalid
	}
	return decodeCall(c.request(ctx, org, "POST", "/v1/calls", body))
}
func (c *GoogleChannel) Lookup(ctx context.Context, org, operation string) (Call, error) {
	if !identifier.MatchString(operation) {
		return Call{}, ErrInvalid
	}
	return decodeCall(c.request(ctx, org, "GET", "/v1/calls/by-idempotency/"+url.PathEscape(operation), nil))
}
func (c *GoogleChannel) Poll(ctx context.Context, org, id string) (Call, error) {
	if !validUUID(id) {
		return Call{}, ErrInvalid
	}
	return decodeCall(c.request(ctx, org, "GET", "/v1/calls/"+id, nil))
}
func (c *GoogleChannel) Result(ctx context.Context, org, id string) ([]byte, error) {
	if !validUUID(id) {
		return nil, ErrInvalid
	}
	return c.request(ctx, org, "GET", "/v1/calls/"+id+"/result", nil)
}
