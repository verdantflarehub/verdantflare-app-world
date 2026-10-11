package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	html := []byte("<!doctype html><title>World</title>")
	index, _ := json.Marshal(map[string]any{"schemaVersion": 1, "files": map[string]string{"index.html": hash(html)}})
	declaration, _ := json.Marshal(map[string]any{"schemaVersion": 1, "appId": "world", "appVersion": "0.1.0", "dashboard": map[string]any{"entryRef": "dashboard/index.html", "artifactDigest": "sha256:" + hash(index), "adapter": "wujie", "sdkMajor": 1, "minimumSdkVersion": "1.1.0", "requiredCapabilities": []string{"app.request", "navigation.request"}, "optionalCapabilities": []string{}, "appApiPolicyRef": "world-browser-v1", "keepAlive": true}})
	navigation, _ := json.Marshal(map[string]any{"schemaVersion": 1, "appId": "world", "appVersion": "0.1.0", "artifactDigest": "sha256:" + hash(index), "entry": map[string]any{"entryId": "main", "title": "数字世界", "iconRef": "globe", "route": "/", "accessRuleRef": "world-read"}})
	for name, data := range map[string][]byte{"index.html": html, "asset-index.json": index, "dashboard-declaration.json": declaration, "navigation-declaration.json": navigation} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestPackageBindsReleaseAndServesOriginalBytesBehindTrustedIdentity(t *testing.T) {
	root := fixture(t)
	a, err := Load(root, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.Handler(strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, authenticated := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/internal/dashboard/navigation", nil)
		if authenticated {
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
			for _, header := range []string{"X-User-Id", "X-Organization-Id", "X-Request-Id"} {
				req.Header.Set(header, "01900000-0000-7000-8000-000000000001")
			}
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if !authenticated && res.Code != 403 {
			t.Fatal("public package access")
		}
		if authenticated && (res.Code != 200 || !bytes.Equal(res.Body.Bytes(), a.Navigation())) {
			t.Fatal("navigation bytes were changed")
		}
	}
	if _, _, ok := a.File("navigation-declaration.json"); ok {
		t.Fatal("navigation in frontend index")
	}
	if _, _, ok := a.File("../index.html"); ok {
		t.Fatal("path escaped")
	}
	if _, err = Load(root, "0.2.0"); err == nil {
		t.Fatal("wrong release accepted")
	}
}
func TestPackageRejectsAssetAndNavigationTampering(t *testing.T) {
	for _, name := range []string{"index.html", "navigation-declaration.json"} {
		root := fixture(t)
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root, "0.1.0"); err == nil {
			t.Fatalf("tampered %s accepted", name)
		}
	}
}
