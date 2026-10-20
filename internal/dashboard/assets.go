package dashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type Assets struct {
	declaration, index []byte
	navigation         []byte
	files              map[string][]byte
}
type navigationChild struct {
	EntryID       string `json:"entryId"`
	Title         string `json:"title"`
	Route         string `json:"route"`
	AccessRuleRef string `json:"accessRuleRef"`
	Order         int    `json:"order"`
}

func validNavigationChildren(version int, children []navigationChild) bool {
	if version == 1 {
		return children == nil
	}
	if version != 2 || len(children) == 0 || len(children) > 32 {
		return false
	}
	routes := map[string]bool{"/": true, "/character": true, "/scene": true, "/sound": true, "/music": true, "/shot": true, "/script": true, "/knowledge": true, "/collections": true, "/learn": true}
	ids := map[string]bool{"main": true}
	used := map[string]bool{}
	for _, child := range children {
		if !safeName.MatchString(child.EntryID) || strings.Contains(child.EntryID, "/") || len(child.EntryID) > 128 || ids[child.EntryID] || used[child.Route] || !routes[child.Route] || child.AccessRuleRef != "world-read" || child.Order < 0 || child.Order > 1000000 || strings.TrimSpace(child.Title) != child.Title || utf8.RuneCountInString(child.Title) < 1 || utf8.RuneCountInString(child.Title) > 128 || strings.ContainsAny(child.Title, "<>") || strings.ContainsFunc(child.Title, unicode.IsControl) {
			return false
		}
		ids[child.EntryID] = true
		used[child.Route] = true
	}
	return true
}

type declaration struct {
	SchemaVersion int    `json:"schemaVersion"`
	AppID         string `json:"appId"`
	AppVersion    string `json:"appVersion"`
	Dashboard     struct {
		EntryRef             string   `json:"entryRef"`
		ArtifactDigest       string   `json:"artifactDigest"`
		Adapter              string   `json:"adapter"`
		SDKMajor             int      `json:"sdkMajor"`
		MinimumSDKVersion    string   `json:"minimumSdkVersion"`
		RequiredCapabilities []string `json:"requiredCapabilities"`
		OptionalCapabilities []string `json:"optionalCapabilities"`
		AppAPIPolicyRef      string   `json:"appApiPolicyRef"`
		KeepAlive            bool     `json:"keepAlive"`
	} `json:"dashboard"`
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,255}$`)

func validName(name string) bool {
	return safeName.MatchString(name) && path.Clean(name) == name && !strings.Contains(name, "//") && !strings.Contains(name, "/.")
}
func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func Load(root, version string) (*Assets, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("dashboard assets unavailable")
	}
	defer r.Close()
	read := func(name string, max int64) ([]byte, error) {
		f, err := r.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, max+1))
		if err != nil || int64(len(b)) > max {
			return nil, errors.New("invalid dashboard asset size")
		}
		return b, nil
	}
	d, err := read("dashboard-declaration.json", 64<<10)
	if err != nil {
		return nil, err
	}
	index, err := read("asset-index.json", 64<<10)
	if err != nil {
		return nil, err
	}
	var metadata declaration
	if strictjson.Decode(bytes.NewReader(d), 64<<10, &metadata) != nil || metadata.SchemaVersion != 1 || metadata.AppID != "world" || metadata.AppVersion != version ||
		metadata.Dashboard.EntryRef != "dashboard/index.html" || metadata.Dashboard.Adapter != "wujie" || metadata.Dashboard.SDKMajor != 1 ||
		metadata.Dashboard.MinimumSDKVersion != "1.1.0" ||
		(metadata.Dashboard.AppAPIPolicyRef != "world-learning-v1" && metadata.Dashboard.AppAPIPolicyRef != "world-analysis-v1") || metadata.Dashboard.ArtifactDigest != "sha256:"+hash(index) {
		return nil, errors.New("invalid dashboard declaration")
	}
	var entries struct {
		SchemaVersion int               `json:"schemaVersion"`
		Files         map[string]string `json:"files"`
	}
	if strictjson.Decode(bytes.NewReader(index), 64<<10, &entries) != nil || entries.SchemaVersion != 1 || len(entries.Files) < 1 || len(entries.Files) > 256 || entries.Files["index.html"] == "" {
		return nil, errors.New("invalid dashboard index")
	}
	navigation, err := read("navigation-declaration.json", 64<<10)
	if err != nil {
		return nil, err
	}
	var nav struct {
		SchemaVersion  int    `json:"schemaVersion"`
		AppID          string `json:"appId"`
		AppVersion     string `json:"appVersion"`
		ArtifactDigest string `json:"artifactDigest"`
		Entry          struct {
			Children      []navigationChild `json:"children,omitempty"`
			EntryID       string            `json:"entryId"`
			Title         string            `json:"title"`
			IconRef       string            `json:"iconRef"`
			Route         string            `json:"route"`
			AccessRuleRef string            `json:"accessRuleRef"`
		} `json:"entry"`
	}
	if strictjson.Decode(bytes.NewReader(navigation), 64<<10, &nav) != nil || !validNavigationChildren(nav.SchemaVersion, nav.Entry.Children) || nav.AppID != metadata.AppID || nav.AppVersion != version || nav.ArtifactDigest != metadata.Dashboard.ArtifactDigest || nav.Entry.EntryID != "main" || nav.Entry.Title != "数字世界" || nav.Entry.IconRef != "globe" || nav.Entry.Route != "/" || nav.Entry.AccessRuleRef != "world-read" {
		return nil, errors.New("invalid navigation declaration")
	}
	a := &Assets{declaration: d, index: index, navigation: navigation, files: map[string][]byte{}}
	for name, digest := range entries.Files {
		if !validName(name) || MIME(name) == "" {
			return nil, errors.New("invalid dashboard path")
		}
		b, err := read(name, 4<<20)
		if err != nil || hash(b) != digest {
			return nil, errors.New("dashboard integrity check failed")
		}
		a.files[name] = b
	}
	return a, nil
}
func MIME(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	}
	return ""
}
func (a *Assets) Declaration() []byte { return a.declaration }
func (a *Assets) Index() []byte       { return a.index }
func (a *Assets) Navigation() []byte  { return a.navigation }
func (a *Assets) File(name string) ([]byte, string, bool) {
	if !validName(name) {
		return nil, "", false
	}
	data, ok := a.files[name]
	return data, MIME(name), ok
}
