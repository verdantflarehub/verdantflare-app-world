package world

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type ProducerMedia struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	MIME   string `json:"mime"`
}
type ProducerProof struct {
	Version    int             `json:"schema_version"`
	ServiceID  string          `json:"service_id"`
	RunID      string          `json:"run_id"`
	ProjectID  string          `json:"project_id"`
	State      string          `json:"state"`
	ModelID    string          `json:"model_id"`
	Route      string          `json:"route"`
	PromptHash string          `json:"prompt_sha256"`
	Parameters map[string]any  `json:"parameters"`
	Inputs     []ProducerMedia `json:"inputs"`
	Outputs    []ProducerMedia `json:"outputs"`
}
type ProducerReader interface {
	Read(context.Context, Principal, RunRef, string) (ProducerProof, error)
}
type producerClient struct {
	origin, token string
	http          *http.Client
}

var producerRunPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,255}$`)

func NewProducerClient(origin, token string) (ProducerReader, error) {
	u, err := url.Parse(origin)
	// Explicit private service only; no browser endpoint or configurable proxy.
	if err != nil || u.String() != "http://studio-blender-content.verdantflare-station.svc.cluster.local:8001" || len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
		return nil, ErrInvalid
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &producerClient{origin, token, &http.Client{Timeout: 35 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *producerClient) Read(ctx context.Context, p Principal, run RunRef, project string) (ProducerProof, error) {
	var out ProducerProof
	if !p.Valid() || !ValidID(project) || !producerRunPattern.MatchString(run.RunID) || (run.ServiceID != "image" && run.ServiceID != "video") {
		return out, ErrInvalid
	}
	body, _ := json.Marshal(map[string]string{"service_id": run.ServiceID, "run_id": run.RunID, "project_id": project})
	r, err := http.NewRequestWithContext(ctx, "POST", c.origin+"/world/producer-result", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-User-Id", p.SubjectID)
	r.Header.Set("X-Organization-Id", p.OrganizationID)
	r.Header.Set("X-Request-Id", p.RequestID)
	response, err := c.http.Do(r)
	if err != nil {
		return out, ErrDependency
	}
	defer response.Body.Close()
	if response.StatusCode == 403 || response.StatusCode == 401 {
		return out, ErrForbidden
	}
	if response.StatusCode != 200 {
		return out, ErrNotReady
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return out, ErrNotReady
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.Version != 1 || out.RunID != run.RunID || out.ServiceID != run.ServiceID || out.ProjectID != project {
		return ProducerProof{}, ErrNotReady
	}
	return out, nil
}
func (s *Service) ConfigureProducer(reader ProducerReader) { s.producer = reader }
