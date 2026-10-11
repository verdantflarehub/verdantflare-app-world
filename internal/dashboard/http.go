package dashboard

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Handler serves only startup-verified package bytes to the trusted Studio
// service. The App never exposes its filesystem or a browser-facing origin.
func (a *Assets) Handler(token string) (http.Handler, error) {
	if a == nil || len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
		return nil, errors.New("invalid dashboard handler")
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			w.WriteHeader(403)
			return
		}
		for _, name := range []string{"X-User-Id", "X-Organization-Id", "X-Request-Id"} {
			value := r.Header.Get(name)
			id, err := uuid.Parse(value)
			if len(r.Header.Values(name)) != 1 || err != nil || id.Version() != 7 || id.String() != value {
				w.WriteHeader(403)
				return
			}
		}
		if r.Method != "GET" || r.URL.RawQuery != "" || r.URL.ForceQuery || strings.Contains(r.URL.EscapedPath(), "%") || len(r.Header.Values("Content-Encoding")) != 0 {
			w.WriteHeader(400)
			return
		}
		if r.Body != nil {
			data, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(data) > 0 {
				w.WriteHeader(400)
				return
			}
		}
		var data []byte
		mime := "application/json"
		switch r.URL.Path {
		case "/internal/dashboard/declaration":
			data = a.Declaration()
		case "/internal/dashboard/index":
			data = a.Index()
		case "/internal/dashboard/navigation":
			data = a.Navigation()
		default:
			if !strings.HasPrefix(r.URL.Path, "/internal/dashboard/assets/") {
				w.WriteHeader(404)
				return
			}
			var ok bool
			data, mime, ok = a.File(strings.TrimPrefix(r.URL.Path, "/internal/dashboard/assets/"))
			if !ok {
				w.WriteHeader(404)
				return
			}
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Request-Id", r.Header.Get("X-Request-Id"))
		_, _ = w.Write(data)
	}), nil
}
