package mcpregistration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPListenerConfigurationSeparatesBusinessAndProtocol(t *testing.T) {
	if err := ValidateListener(":8443", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		listen, advertise, registry string
		tls                         bool
	}{
		{":8444", "", "", true}, {"", "https://app.studio.svc:8444/mcp", "http://etcd.studio.svc:2379", true},
		{":8443", "https://app.studio.svc:8443/mcp", "http://etcd.studio.svc:2379", true},
		{":0", "https://app.studio.svc/mcp", "http://etcd.studio.svc:2379", true},
		{":8444", "https://app.studio.svc:8444/mcp", "http://etcd.studio.svc:2379", false},
		{"127.0.0.1:8444", "https://127.0.0.1:8444/mcp", "http://etcd.studio.svc:2379", false},
	} {
		if err := ValidateListener(":8443", value.listen, value.advertise, value.registry, value.tls); err == nil {
			t.Fatal("unsafe listener config accepted")
		}
	}
	if err := ValidateListener(":8443", ":8444", "https://app.studio.svc:8444/mcp", "http://etcd.studio.svc:2379", true); err != nil {
		t.Fatal(err)
	}
}
func TestMCPHandlerNeverExposesBusinessAndBusinessNeverExposesMCP(t *testing.T) {
	protocol := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	handler := MCPHandler(protocol, health)
	for path, expected := range map[string]int{"/mcp": 202, "/healthz": 204, "/internal/v1/project/open": 404, "/internal/v1/world/get": 404, "/internal/v1/artifact/authorize": 404, "/internal/v1/coordination/prepare": 404, "/internal/dashboard/index": 404, "/api/v1/project/list": 404, "/mc%70": 404, "/mcp?token=x": 404, "/mcp/": 404} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != expected {
			t.Fatalf("path escaped listener: %s %d", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/healthz", nil))
	if response.Code != 405 {
		t.Fatal("health allows mutation")
	}
	business := BusinessHandler(protocol)
	for _, path := range []string{"/mcp", "/mcp/", "/mcp/tools", "/mc%70"} {
		response := httptest.NewRecorder()
		business.ServeHTTP(response, httptest.NewRequest("POST", path, nil))
		if response.Code != 404 {
			t.Fatal("business listener exposes MCP")
		}
	}
}
