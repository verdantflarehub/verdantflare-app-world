package artifactclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
)

type analysisTransport func(*http.Request) (*http.Response, error)

func (f analysisTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// In-memory transport only: no local service or socket is started.
func TestAnalysisWriteReplayAndMetadata(t *testing.T) {
	const id = "01900000-0000-7000-8000-000000000001"
	const other = "01900000-0000-7000-8000-000000000002"
	p := world.Principal{OrganizationID: id, SubjectID: id, RequestID: id}
	w := world.TextWrite{WriteID: id, FileID: other, Text: `{"notes":"拆解证据"}`, MIME: "application/json"}
	source := world.Source{Kind: "task_output", ServiceID: "world", RunID: "analysis-v1:" + id, TaskID: id}
	sum := sha256.Sum256([]byte(w.Text))
	good := world.ContentVersion{SchemaVersion: 2, ContentRef: world.ContentRef{StoreID: id, ArtifactID: id, VersionID: other}, OrganizationID: id, CreatedBy: id, VersionNo: 1, CreatedAt: time.Now().UTC(), MIME: w.MIME, Size: int64(len(w.Text)), SHA256: hex.EncodeToString(sum[:]), Source: source}
	for _, kind := range []string{"replay", "creator", "source", "size", "hash", "mime"} {
		t.Run(kind, func(t *testing.T) {
			committed, puts, prepares, commits := false, 0, 0, 0
			client := &Client{base: "https://artifact.invalid", token: "synthetic", http: &http.Client{Transport: analysisTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("X-User-Id") != id || r.Header.Get("X-Organization-Id") != id {
					t.Fatal("actor lost")
				}
				var output any
				switch r.Method + " " + r.URL.Path {
				case "POST /v2/artifacts/uploads":
					prepares++
					var got struct {
						WriteID string       `json:"write_id"`
						Source  world.Source `json:"source"`
						SHA256  string       `json:"sha256"`
						Size    int          `json:"size"`
						MIME    string       `json:"mime"`
					}
					if json.NewDecoder(r.Body).Decode(&got) != nil || got.WriteID != w.WriteID || got.Source != source || got.SHA256 != good.SHA256 || got.Size != len(w.Text) || got.MIME != w.MIME {
						t.Fatal("prepare changed frozen document")
					}
					state := "prepared"
					if committed {
						state = "committed"
					}
					output = map[string]any{"upload_id": id, "artifact_id": id, "version_id": other, "state": state, "content_path": "/v2/artifacts/uploads/" + id + "/content"}
				case "PUT /v2/artifacts/uploads/" + id + "/content":
					puts++
					b, _ := io.ReadAll(r.Body)
					if string(b) != w.Text {
						t.Fatal("bytes changed")
					}
					output = map[string]any{}
				case "POST /v2/artifacts/uploads/" + id + "/commit":
					commits++
					committed = true
					if kind == "replay" && commits == 1 {
						return nil, errors.New("synthetic lost commit response")
					}
					v := good
					switch kind {
					case "creator":
						v.CreatedBy = other
					case "source":
						v.Source.RunID = "wrong"
					case "size":
						v.Size++
					case "hash":
						v.SHA256 = strings.Repeat("a", 64)
					case "mime":
						v.MIME = "text/plain"
					}
					output = v
				default:
					t.Fatal("unexpected request", r.Method, r.URL.Path)
				}
				b, _ := json.Marshal(output)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
			})}}
			if _, err := client.WriteAnalysisDocument(t.Context(), p, id, w); !errors.Is(err, world.ErrDependency) {
				t.Fatal("invalid or lost response accepted", err)
			}
			if kind == "replay" {
				v, err := client.WriteAnalysisDocument(t.Context(), p, id, w)
				if err != nil || v != good || puts != 1 || prepares != 2 || commits != 2 {
					t.Fatal("retry did not reuse committed bytes", err, puts, prepares, commits)
				}
			}
		})
	}
}
func TestAnalysisWriteRejectsInvalidBeforeTransport(t *testing.T) {
	const id = "01900000-0000-7000-8000-000000000001"
	client := &Client{http: &http.Client{Transport: analysisTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid write reached transport")
		return nil, nil
	})}}
	good := world.TextWrite{WriteID: id, FileID: id, Text: `{}`, MIME: "application/json"}
	for _, mutate := range []func(*world.TextWrite){func(w *world.TextWrite) { w.WriteID = "" }, func(w *world.TextWrite) { w.FileID = "" }, func(w *world.TextWrite) { w.MIME = "text/plain" }, func(w *world.TextWrite) { w.Text = "" }, func(w *world.TextWrite) { w.Text = strings.Repeat("x", world.MaxManifestBytes+1) }} {
		w := good
		mutate(&w)
		if _, err := client.WriteAnalysisDocument(t.Context(), world.Principal{}, id, w); !errors.Is(err, world.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := client.WriteAnalysisDocument(t.Context(), world.Principal{}, "not-a-task", good); !errors.Is(err, world.ErrInvalid) {
		t.Fatal(err)
	}
}
