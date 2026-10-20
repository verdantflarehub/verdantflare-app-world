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
