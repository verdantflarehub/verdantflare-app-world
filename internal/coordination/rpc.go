package coordination

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type Peer interface {
	Prepare(context.Context, Operation, []Grant) error
	Finalize(context.Context, Operation) error
	State(context.Context, Operation) (Status, error)
}

// Engine retains unresolved reservations after transport failures. Finalization
// notifications are retried from an outbox written with the business CAS.
type Engine struct {
	Store *Store
	Peers map[string]Peer
}

func (e *Engine) Prepare(ctx context.Context, o Operation, plan Plan) error {
	plan, err := Normalize(plan)
	if err != nil {
		return err
	}
	if err = e.Store.Register(ctx, o, plan); err != nil {
		return err
	}
	for _, domain := range []string{"project", "world"} {
		grants, needed := plan[domain]
		if !needed {
			continue
		}
		if domain == e.Store.Domain {
			err = e.Store.Prepare(ctx, o, grants)
		} else if peer := e.Peers[domain]; peer != nil {
			err = peer.Prepare(ctx, o, grants)
		} else {
			err = ErrUnknown
		}
		if err != nil {
			return err
		}
		if err = e.Store.RecordPrepared(ctx, o, domain); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Finalize(ctx context.Context, o Operation) error {
	var proof Status
	var err error
	if o.Coordinator == e.Store.Domain {
		proof, err = e.Store.Status(ctx, o)
	} else if peer := e.Peers[o.Coordinator]; peer != nil {
		proof, err = peer.State(ctx, o)
	} else {
		return ErrUnknown
	}
	if err != nil {
		return err
	}
	return e.Store.Finalize(ctx, o, proof)
}

func (e *Engine) Deliver(ctx context.Context) error {
	items, err := e.Store.PendingOutbox(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range items {
		if item.Participant == e.Store.Domain {
			err = e.Finalize(ctx, item.Operation)
		} else if peer := e.Peers[item.Participant]; peer != nil {
			err = peer.Finalize(ctx, item.Operation)
		} else {
			err = ErrUnknown
		}
		if err == nil {
			err = e.Store.Delivered(ctx, item)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

type request struct {
	Operation Operation `json:"operation"`
	Grants    []Grant   `json:"grants,omitempty"`
}
type response struct {
	Status *Status `json:"status,omitempty"`
	OK     bool    `json:"ok"`
	Code   string  `json:"code,omitempty"`
}

// Handler maps an independently configured service credential to a caller domain.
// Clients cannot submit a terminal outcome, endpoint or arbitrary grant role.
func (e *Engine) Handler(tokens map[string]string) (http.Handler, error) {
	secrets := map[string][32]byte{}
	for domain, token := range tokens {
		if (domain != "project" && domain != "world") || len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
			return nil, ErrInvalid
		}
		secrets[domain] = sha256.Sum256([]byte("Bearer " + token))
	}
	if len(secrets) == 0 {
		return nil, ErrInvalid
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(code int, err error) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(response{Code: err.Error()})
		}
		if r.Method != "POST" || r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 1 {
			fail(403, ErrDenied)
			return
		}
		hash := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		caller := ""
		for domain, expected := range secrets {
			if subtle.ConstantTimeCompare(hash[:], expected[:]) == 1 {
				if caller != "" {
					fail(403, ErrDenied)
					return
				}
				caller = domain
			}
		}
		if caller == "" {
			fail(403, ErrDenied)
			return
		}
		var input request
		if strictjson.Decode(r.Body, 1<<20, &input) != nil || !input.Operation.Valid() {
			fail(400, ErrInvalid)
			return
		}
		var err error
		out := response{OK: true}
		switch r.URL.Path {
		case "/internal/v1/coordination/prepare":
			if caller != input.Operation.Coordinator {
				fail(403, ErrDenied)
				return
			}
			err = e.Store.Prepare(r.Context(), input.Operation, input.Grants)
		case "/internal/v1/coordination/finalize":
			if caller != input.Operation.Coordinator || input.Grants != nil {
				fail(403, ErrDenied)
				return
			}
			err = e.Finalize(r.Context(), input.Operation)
		case "/internal/v1/coordination/state":
			if input.Operation.Coordinator != e.Store.Domain || input.Grants != nil {
				fail(403, ErrDenied)
				return
			}
			var status Status
			status, err = e.Store.Status(r.Context(), input.Operation)
			out.Status = &status
		default:
			fail(404, ErrInvalid)
			return
		}
		if err != nil {
			code := 503
			if errors.Is(err, ErrDenied) {
				code = 403
			} else if errors.Is(err, ErrInvalid) {
				code = 400
			} else if errors.Is(err, ErrConflict) || errors.Is(err, ErrFenced) {
				code = 409
			}
			fail(code, err)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}), nil
}

type Client struct {
	origin, token string
	http          *http.Client
}

func NewClient(origin, token string) (*Client, error) {
	return NewClientWithCA(origin, token, "")
}
func NewClientWithCA(origin, token, caFile string) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
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
	return &Client{strings.TrimSuffix(origin, "/"), token, &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) call(ctx context.Context, path string, input request) (response, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return response{}, ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, "POST", c.origin+"/internal/v1/coordination/"+path, bytes.NewReader(data))
	if err != nil {
		return response{}, ErrInvalid
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(r)
	if err != nil {
		return response{}, ErrUnknown
	}
	defer res.Body.Close()
	var out response
	if strictjson.Decode(res.Body, 1<<20, &out) != nil {
		return out, ErrUnknown
	}
	if res.StatusCode != 200 || !out.OK {
		switch out.Code {
		case ErrDenied.Error():
			return out, ErrDenied
		case ErrConflict.Error():
			return out, ErrConflict
		case ErrFenced.Error():
			return out, ErrFenced
		case ErrInvalid.Error():
			return out, ErrInvalid
		default:
			return out, ErrUnknown
		}
	}
	return out, nil
}
func (c *Client) Prepare(ctx context.Context, o Operation, g []Grant) error {
	_, err := c.call(ctx, "prepare", request{o, g})
	return err
}
func (c *Client) Finalize(ctx context.Context, o Operation) error {
	_, err := c.call(ctx, "finalize", request{Operation: o})
	return err
}
func (c *Client) State(ctx context.Context, o Operation) (Status, error) {
	out, err := c.call(ctx, "state", request{Operation: o})
	if err != nil {
		return Status{}, err
	}
	if out.Status == nil || out.Status.Operation != o {
		return Status{}, ErrConflict
	}
	return *out.Status, nil
}
