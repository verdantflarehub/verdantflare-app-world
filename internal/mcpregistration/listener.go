package mcpregistration

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ValidateListener requires distinct ports so ordinary Kubernetes policy can
// restrict MCP to Studio without admitting business peers onto its socket.
func ValidateListener(business, listen, advertise, registry string, withTLS bool) error {
	if listen == "" && advertise == "" && registry == "" {
		return nil
	}
	if listen == "" || advertise == "" || registry == "" {
		return errors.New("MCP listener, advertisement and registry are required together")
	}
	_, businessPort, err := net.SplitHostPort(business)
	if err != nil {
		return errors.New("invalid business listener")
	}
	host, mcpPort, err := net.SplitHostPort(listen)
	if err != nil {
		return errors.New("invalid MCP listener")
	}
	port, err := strconv.Atoi(mcpPort)
	basePort, baseErr := strconv.Atoi(businessPort)
	if err != nil || baseErr != nil || port < 1 || port > 65535 || basePort == port || strconv.Itoa(port) != mcpPort {
		return errors.New("MCP requires a distinct fixed port")
	}
	ip := net.ParseIP(host)
	if !withTLS && (ip == nil || !ip.IsLoopback()) {
		return errors.New("MCP non-loopback listener requires TLS")
	}
	u, ok := cleanURL(advertise)
	if !ok || u.Path != "/mcp" || (u.Scheme == "https") != withTLS {
		return errors.New("MCP advertisement must match listener TLS")
	}
	if _, err = endpoints(registry); err != nil {
		return err
	}
	return nil
}

func MCPHandler(protocol, health http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/mcp":
			protocol.ServeHTTP(w, r)
		case "/healthz":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			health.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
func BusinessHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
