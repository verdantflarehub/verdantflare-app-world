package world

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/verdantflarehub/verdantflare-app-world/internal/buildinfo"
	"github.com/verdantflarehub/verdantflare-app-world/internal/mcprpc"
)

//go:embed mcp_schemas.json
var generatedTools []byte

type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func MCPTools() []MCPTool {
	var tools []MCPTool
	if json.Unmarshal(generatedTools, &tools) != nil {
		panic("invalid embedded MCP tool schema")
	}
	return tools
}

// MCPHandler shares HTTP parsing and persistent operations.
func (s *Service) MCPHandler(token string, _ ...string) (http.Handler, error) {
	h, e := s.HTTPHandler(token)
	if e != nil {
		return nil, e
	}
	wanted := sha256.Sum256([]byte("Bearer " + token))
	tools := MCPTools()
	routes := map[string]string{}
	for _, tool := range tools {
		routes[tool.Name] = "/internal/v1/" + strings.Replace(tool.Name, ".", "/", 1)
	}
	for _, action := range []string{"open", "append", "status"} {
		routes["world.review_"+action] = "/internal/v1/world/review/" + action
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		var empty mcprpc.Request
		p := Principal{OrganizationID: r.Header.Get("X-Organization-Id"), SubjectID: r.Header.Get("X-User-Id"), RequestID: r.Header.Get("X-Request-Id")}
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		for _, key := range []string{"Authorization", "X-Organization-Id", "X-User-Id", "X-Request-Id"} {
			if len(r.Header.Values(key)) != 1 {
				mcprpc.Reject(w, r, empty, 403, -32000, "Forbidden")
				return
			}
		}
		if !p.Valid() || subtle.ConstantTimeCompare(wanted[:], actual[:]) != 1 {
			mcprpc.Reject(w, r, empty, 403, -32000, "Forbidden")
			return
		}
		mt, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if r.Method != "POST" || r.URL.RawQuery != "" || mt != "application/json" || e != nil || r.Header.Get("Content-Encoding") != "" {
			mcprpc.Reject(w, r, empty, 400, -32600, "Invalid request")
			return
		}
		request, e := mcprpc.Decode(r.Body)
		if e != nil {
			mcprpc.Reject(w, r, empty, 400, -32700, "Invalid JSON-RPC request")
			return
		}
		w.Header().Set("X-Request-Id", p.RequestID)
		if mcprpc.Common(w, request, "verdantflare-app-world", buildinfo.Version) {
			return
		}
		switch request.Method {
		case "tools/list":
			if len(request.ID) == 0 {
				mcprpc.Reject(w, r, request, 400, -32600, "Request ID required")
				return
			}
			mcprpc.Result(w, request, map[string]any{"tools": tools})
		case "tools/call":
			name, _, args, e := request.Call()
			if e != nil {
				mcprpc.Reject(w, r, request, 400, -32602, "Invalid tool parameters")
				return
			}
			path, ok := routes[name]
			if !ok {
				mcprpc.Reject(w, r, request, 404, -32601, "Unknown tool")
				return
			}
			inner := r.Clone(r.Context())
			inner.Header.Set("X-Review-Origin", "agent")
			inner.URL.Path = path
			inner.Body = io.NopCloser(bytes.NewReader(args))
			inner.ContentLength = int64(len(args))
			out := &capture{headers: make(http.Header), status: 200}
			h.ServeHTTP(out, inner)
			data := json.RawMessage(out.body.Bytes())
			if !json.Valid(data) {
				mcprpc.Reject(w, r, request, 503, -32603, "Service response unavailable")
				return
			}
			// Preserve the business HTTP status from the internal handler. The
			// structured MCP result remains available to callers, while the
			// aggregate gateway can keep authentication, conflict and dependency
			// errors observable to Web and desktop clients.
			if out.status >= 400 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(out.status)
			}
			mcprpc.Result(w, request, map[string]any{"content": []map[string]string{{"type": "text", "text": out.body.String()}}, "structuredContent": data, "isError": out.status >= 400})
		default:
			mcprpc.Reject(w, r, request, 404, -32601, "Method not found")
		}
	}), nil
}

type capture struct {
	headers http.Header
	body    bytes.Buffer
	status  int
}

func (c *capture) Header() http.Header         { return c.headers }
func (c *capture) WriteHeader(status int)      { c.status = status }
func (c *capture) Write(b []byte) (int, error) { return c.body.Write(b) }
