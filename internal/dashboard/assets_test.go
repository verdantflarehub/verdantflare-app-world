package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	html := []byte("<!doctype html><title>World</title>")
	index, _ := json.Marshal(map[string]any{"schemaVersion": 1, "files": map[string]string{"index.html": hash(html)}})
	declaration, _ := json.Marshal(map[string]any{"schemaVersion": 1, "appId": "world", "appVersion": "0.1.0", "dashboard": map[string]any{"entryRef": "dashboard/index.html", "artifactDigest": "sha256:" + hash(index), "adapter": "wujie", "sdkMajor": 1, "minimumSdkVersion": "1.1.0", "requiredCapabilities": []string{"app.request", "navigation.request"}, "optionalCapabilities": []string{}, "appApiPolicyRef": "world-learning-v1", "keepAlive": true}})
	navigation, _ := json.Marshal(map[string]any{"schemaVersion": 1, "appId": "world", "appVersion": "0.1.0", "artifactDigest": "sha256:" + hash(index), "entry": map[string]any{"entryId": "main", "title": "数字世界", "iconRef": "globe", "route": "/", "accessRuleRef": "world-read"}})
	for name, data := range map[string][]byte{"index.html": html, "asset-index.json": index, "dashboard-declaration.json": declaration, "navigation-declaration.json": navigation} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Exercise the actual release producer against the runtime consumer. A separately
// handwritten fixture cannot detect policy drift between these two components.
func TestReleaseProducerMetadataLoadsInRuntime(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is required for the release metadata producer")
	}
	root := t.TempDir()
	producer, err := filepath.Abs("../../dashboard/scripts/prepare-dashboard.mjs")
	if err != nil {
		t.Fatal(err)
	}
	// Track the producer as a test-process input so Go invalidates its cache
	// when only the external JavaScript release producer changes.
	if _, err := os.ReadFile(producer); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url';
import {writeFileSync} from 'node:fs';
import {join} from 'node:path';
const {metadata} = await import(pathToFileURL(process.argv[2]).href);
const html = Buffer.from('<!doctype html><title>World</title>');
const result = metadata('world', '0.1.6', [{path:'index.html',bytes:html}]);
for (const [name, bytes] of Object.entries({'index.html':html,'asset-index.json':result.index,'dashboard-declaration.json':result.declaration,'navigation-declaration.json':result.navigation})) writeFileSync(join(process.argv[3],name),bytes);`
	if output, err := exec.Command("node", "--input-type=module", "-e", script, "runtime-metadata-test", producer, root).CombinedOutput(); err != nil {
		t.Fatalf("release producer failed: %v: %s", err, output)
	}
	if _, err := Load(root, "0.1.6"); err != nil {
		t.Fatalf("actual release metadata rejected: %v", err)
	}
	path := filepath.Join(root, "dashboard-declaration.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(raw, []byte("world-learning-v1"), []byte("unknown-policy")), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "0.1.6"); err == nil {
		t.Fatal("unknown policy accepted")
	}
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

func TestNavigationChildrenStayWithinWorldRoutesAndPermissions(t *testing.T) {
	valid := navigationChild{EntryID: "knowledge", Title: "知识", Route: "/knowledge", AccessRuleRef: "world-read", Order: 7}
	if !validNavigationChildren(2, []navigationChild{valid}) || !validNavigationChildren(1, nil) {
		t.Fatal("valid navigation rejected")
	}
	for _, version := range []int{0, 1, 3} {
		if validNavigationChildren(version, []navigationChild{valid}) {
			t.Fatal("children accepted under wrong version")
		}
	}
	for _, mutate := range []func(*navigationChild){
		func(c *navigationChild) { c.EntryID = "main" },
		func(c *navigationChild) { c.Route = "/projects" },
		func(c *navigationChild) { c.Route = "https://example.invalid" },
		func(c *navigationChild) { c.AccessRuleRef = "project-read" },
		func(c *navigationChild) { c.Title = "<script>" },
		func(c *navigationChild) { c.Order = -1 },
	} {
		c := valid
		mutate(&c)
		if validNavigationChildren(2, []navigationChild{c}) {
			t.Fatalf("invalid child accepted: %#v", c)
		}
	}
	if validNavigationChildren(2, []navigationChild{valid, valid}) {
		t.Fatal("duplicate navigation accepted")
	}
}
