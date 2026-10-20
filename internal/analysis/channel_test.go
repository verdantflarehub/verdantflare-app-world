package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type analysisTransport func(*http.Request) (*http.Response, error)

func (f analysisTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGoogleChannelPreservesIdentityAndDisablesRedirectAndProxy(t *testing.T) {
	r, media, _ := executorFixture(t)
	c, err := NewGoogleChannel("https://channel.internal", strings.Repeat("x", 32), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Transport.(*http.Transport).Proxy != nil || c.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("unsafe transport")
	}
	n := 0
	c.http.Transport = analysisTransport(func(req *http.Request) (*http.Response, error) {
		n++
		if req.Header.Get("X-Caller-Id") != "world-analysis" || req.Header.Get("X-Organization-Id") != execOrg || req.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Fatal("wrong execution identity")
		}
		if req.Method == "POST" {
			var body map[string]any
			if json.NewDecoder(req.Body).Decode(&body) != nil {
				t.Fatal("bad body")
			}
			if body["capability"] != "analysis" || body["idempotency_key"] != r.Request.OperationID || body["task_id"] != execTask || body["budget_limit"] != "1.00" {
				t.Fatal("frozen request changed")
			}
			payload := body["payload"].(map[string]any)
			if payload["media"].(map[string]any)["sha256"] != r.Request.Prepared.MediaHash {
				t.Fatal("media not pinned")
			}
		}
		data, _ := json.Marshal(completeCall())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	if _, err = c.Submit(t.Context(), execOrg, r.Request, media); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Lookup(t.Context(), execOrg, r.Request.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Poll(t.Context(), execOrg, execCall); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatal("unexpected network operations")
	}
	if _, err = c.Poll(t.Context(), execOrg, "../../other"); !errors.Is(err, ErrInvalid) || n != 3 {
		t.Fatal("path injection allowed")
	}
}
func TestGoogleChannelNetworkErrorsNeverRetryAndKnownRejectionIsDistinct(t *testing.T) {
	r, media, _ := executorFixture(t)
	for _, scenario := range []struct {
		name         string
		status       int
		body         string
		transportErr bool
		want         error
	}{
		{"lost", 0, "", true, ErrUnavailable},
		{"rejected", 503, `{"schema_version":1,"state":"rejected","accepted":false}`, false, ErrRejected},
		{"ambiguous", 503, `{"error":"unavailable"}`, false, ErrUnavailable},
		{"duplicate", 503, `{"schema_version":1,"state":"rejected","accepted":true,"accepted":false}`, false, ErrUnavailable},
		{"redirect", 307, `{}`, false, ErrUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c, _ := NewGoogleChannel("https://channel.internal", strings.Repeat("x", 32), "")
			n := 0
			c.http.Transport = analysisTransport(func(*http.Request) (*http.Response, error) {
				n++
				if scenario.transportErr {
					return nil, errors.New("private credential must not escape")
				}
				return &http.Response{StatusCode: scenario.status, Body: io.NopCloser(strings.NewReader(scenario.body))}, nil
			})
			_, err := c.Submit(context.Background(), execOrg, r.Request, media)
			if !errors.Is(err, scenario.want) || n != 1 {
				t.Fatal("unsafe retry/error", err, n)
			}
		})
	}
}
func TestGoogleChannelRejectsUntrustedOrigins(t *testing.T) {
	for _, origin := range []string{"http://remote.internal", "https://user:password@remote.internal", "https://remote.internal/path", "https://remote.internal?x=1", "https://remote.internal#fragment"} {
		if _, err := NewGoogleChannel(origin, strings.Repeat("x", 32), ""); err == nil {
			t.Fatal("untrusted origin accepted")
		}
	}
}

func TestGoogleAnalysisCapabilitiesUsesLiveVerifiedModels(t *testing.T) {
	for _, scenario := range []string{"enabled", "disabled", "duplicate-model", "unknown-version", "wrong-channel", "missing-schema", "duplicate-json", "unsafe-model", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := NewGoogleChannel("https://channel.internal", strings.Repeat("x", 32), "")
			v := Capabilities{SchemaVersion: 1, ChannelID: "google", Capability: "analysis", State: "enabled", Models: []string{"gemini-3.1-pro-preview"}, MIME: []string{"video/mp4", "audio/wav"}, MaxMediaBytes: MaxMediaBytes, SourceText: true, VideoRange: true, ResponseSchema: true}
			switch scenario {
			case "disabled":
				v.State = "disabled"
				v.Models = []string{}
			case "duplicate-model":
				v.Models = append(v.Models, v.Models[0])
			case "unknown-version":
				v.SchemaVersion = 2
			case "wrong-channel":
				v.ChannelID = "other"
			case "missing-schema":
				v.ResponseSchema = false
			case "unsafe-model":
				v.Models = []string{"bad/model"}
			}
			raw, _ := json.Marshal(v)
			if scenario == "duplicate-json" {
				raw = append([]byte(`{"execution_state":"disabled",`), raw[1:]...)
			}
			n := 0
			c.http.Transport = analysisTransport(func(r *http.Request) (*http.Response, error) {
				n++
				if r.Method != "GET" || r.URL.Path != "/v1/analysis/capabilities" || r.Header.Get("X-Organization-Id") != execOrg {
					t.Fatal("unscoped discovery")
				}
				status := 200
				if scenario == "unavailable" {
					status = 503
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})
			got, err := c.Capabilities(t.Context(), execOrg)
			if scenario == "enabled" || scenario == "disabled" {
				if err != nil || got.State != scenario {
					t.Fatal("valid discovery rejected", err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatal("unsafe discovery accepted", err)
			}
			if n != 1 {
				t.Fatal("discovery retried")
			}
		})
	}
}
