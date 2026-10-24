package world

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

func testID(n int) string { return fmt.Sprintf("0199b501-0000-7000-8000-%012x", n) }
func sourceFixture() OpenResult {
	m := Manifest{SchemaVersion: 1, Kind: "project", ProjectID: testID(1), Name: "Source", Category: "image", Status: "active", EntryDocumentID: testID(2), Files: []File{{ID: testID(2), Path: "entry.md", Role: "review", Content: ContentRef{testID(3), testID(4), testID(5)}}, {ID: testID(6), Path: "private.md", Role: "review", Content: ContentRef{testID(3), testID(7), testID(8)}}}, Selections: []Selection{}, AssetRefs: []AssetRef{}, RunRefs: []RunRef{}, DomainDocuments: []DomainDocument{}}
	return OpenResult{Result: Result{ProjectID: m.ProjectID, RevisionID: testID(9), Manifest: m, ManifestRef: ContentRef{testID(3), testID(10), testID(11)}, Invalidated: []string{}}, HeadRevisionID: testID(9)}
}
func registrationFixture() WorldRegisterRequest {
	return WorldRegisterRequest{CommitID: testID(20), SourceProjectID: testID(1), SourceRevisionID: testID(9), FileIDs: []string{testID(2)}, Name: "Reference", AssetType: "image", Subjects: []string{}, Relation: "produced_in"}
}
func TestWorldPreparationPreservesFixedSourceAndSelectedFiles(t *testing.T) {
	r, err := normalizeWorld(registrationFixture())
	if err != nil {
		t.Fatal(err)
	}
	source := sourceFixture()
	plan, err := prepareWorld(r, testID(21), source)
	if err != nil || len(plan.Manifest.Files) != 1 || plan.Manifest.Files[0].ID != testID(2) || plan.SourceRef != source.ManifestRef || plan.Manifest.Source.RevisionID != source.RevisionID {
		t.Fatal("selected source changed", err)
	}
	for _, kind := range []string{"revision", "project", "file", "dependency", "review"} {
		t.Run(kind, func(t *testing.T) {
			request := r
			opened := source
			switch kind {
			case "revision":
				opened.RevisionID = testID(22)
			case "project":
				opened.ProjectID = testID(22)
			case "file":
				request.FileIDs = []string{testID(22)}
			case "dependency":
				deps := []AssetRef{{AssetID: testID(23), VersionID: testID(24), Purpose: "reference"}}
				request.DependsOn = &deps
			case "review":
				request.ReviewFileID = testID(25)
			}
			if _, err := prepareWorld(request, testID(21), opened); err == nil {
				t.Fatal("foreign source accepted")
			}
		})
	}
}
func TestWorldNormalizationIsStableAndRejectsAliases(t *testing.T) {
	a := registrationFixture()
	a.FileIDs = []string{testID(6), testID(2)}
	a.Subjects = []string{"two", "one"}
	b := a
	b.FileIDs = []string{testID(2), testID(6)}
	b.Subjects = []string{"one", "two"}
	left, e1 := normalizeWorld(a)
	right, e2 := normalizeWorld(b)
	h1, _ := digest(left)
	h2, _ := digest(right)
	if e1 != nil || e2 != nil || h1 != h2 {
		t.Fatal("logical retry changed fingerprint")
	}
	a.FileIDs = append(a.FileIDs, testID(2))
	if _, err := normalizeWorld(a); err == nil {
		t.Fatal("duplicate file accepted")
	}
}

type worldRow struct {
	values []any
	err    error
}

func (r worldRow) Scan(out ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, v := range r.values {
		switch target := out[i].(type) {
		case *string:
			*target = v.(string)
		case *[]byte:
			*target = v.([]byte)
		default:
			panic("unexpected destination")
		}
	}
	return nil
}

type worldQueries struct {
	t        *testing.T
	allowed  map[string]bool
	versions map[string]AssetManifest
	refs     map[string]ContentRef
}

func (q worldQueries) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "project_") {
		q.t.Fatal("World attempted Project SQL")
	}
	asset := args[1].(string)
	if strings.Contains(sql, "world_asset_grants") {
		if !q.allowed[asset] {
			return worldRow{err: pgx.ErrNoRows}
		}
		return worldRow{values: []any{"reader"}}
	}
	if strings.Contains(sql, "world_asset_versions") {
		manifest, ok := q.versions[asset]
		if !ok {
			return worldRow{err: pgx.ErrNoRows}
		}
		data, _ := json.Marshal(manifest)
		ref, _ := json.Marshal(q.refs[asset])
		return worldRow{values: []any{data, ref}}
	}
	q.t.Fatal("unexpected query")
	return worldRow{err: ErrDependency}
}
func TestWorldReferencesRequireEveryGrantAndExcludePrivateSource(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	base := sourceFixture()
	r, _ := normalizeWorld(registrationFixture())
	plan, err := prepareWorld(r, testID(40), base)
	if err != nil {
		t.Fatal(err)
	}
	root, child := plan.Manifest, plan.Manifest
	child.AssetID = testID(41)
	child.Files = []File{base.Manifest.Files[1]}
	root.DependsOn = []AssetRef{{AssetID: child.AssetID, VersionID: testID(42), Purpose: "nested"}}
	q := worldQueries{t: t, allowed: map[string]bool{root.AssetID: true, child.AssetID: true}, versions: map[string]AssetManifest{root.AssetID: root, child.AssetID: child}, refs: map[string]ContentRef{root.AssetID: {testID(3), testID(43), testID(44)}, child.AssetID: {testID(3), testID(45), testID(46)}}}
	grants := map[string]bool{}
	refs, err := assetReferences(context.Background(), q, p, AssetRef{root.AssetID, testID(47), "reuse"}, false, grants)
	if err != nil || len(refs) != 4 || len(grants) != 2 {
		t.Fatal("closure incomplete", err)
	}
	for _, ref := range refs {
		if ref == base.ManifestRef {
			t.Fatal("private source manifest leaked")
		}
	}
	q.allowed[child.AssetID] = false
	if _, err := assetReferences(context.Background(), q, p, AssetRef{root.AssetID, testID(47), "reuse"}, false, nil); !errors.Is(err, ErrForbidden) {
		t.Fatal("parent grant bypassed dependency", err)
	}
}

type projectRoundTripper func(*http.Request) (*http.Response, error)

func (f projectRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestWorldProjectClientVerifiesExactSourceAndFailsClosed(t *testing.T) {
	for _, kind := range []string{"valid", "foreign", "duplicate", "denied", "offline"} {
		t.Run(kind, func(t *testing.T) {
			p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
			c, err := NewProjectClient("https://project.invalid", strings.Repeat("p", 32))
			if err != nil {
				t.Fatal(err)
			}
			c.http.Transport = projectRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/internal/v1/project/open" || r.Header.Get("X-User-Id") != p.SubjectID || r.Header.Get("X-Organization-Id") != p.OrganizationID || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 32) || r.Header.Get("Cookie") != "" {
					t.Fatal("uncontrolled source request")
				}
				opened := sourceFixture()
				if kind == "foreign" {
					opened.RevisionID = testID(90)
				}
				data, _ := json.Marshal(opened)
				if kind == "duplicate" {
					data = []byte(strings.Replace(string(data), `"project_id":`, `"project_id":"`+testID(91)+`","project_id":`, 1))
				}
				status := 200
				if kind == "denied" {
					status = 403
				}
				if kind == "offline" {
					return nil, errors.New("offline")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(data))), Header: http.Header{}}, nil
			})
			_, err = c.Open(context.Background(), p, testID(1), testID(9))
			if (kind == "valid") != (err == nil) {
				t.Fatal("source binding result", err)
			}
		})
	}
}
func TestWorldHTTPAndAuthorityRejectProjectOperations(t *testing.T) {
	s := &Service{}
	token := strings.Repeat("w", 32)
	handler, err := s.HTTPHandler(token, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/internal/v1/artifact/authorize", "/internal/v1/project/list", "/api/v1/world/references", "/internal/v1/world/list?scope=foreign"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-User-Id", testID(31))
		r.Header.Set("X-Organization-Id", testID(30))
		r.Header.Set("X-Request-Id", testID(32))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("foreign operation accepted")
		}
	}
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	access := Access{ProjectID: testID(1), RevisionID: testID(9)}
	v := ContentVersion{ContentRef: sourceFixture().ManifestRef, OrganizationID: p.OrganizationID}
	if err = s.AuthorizeArtifact(context.Background(), p, Permission{Action: "read", Version: &v, Access: &access}); !errors.Is(err, ErrForbidden) {
		t.Fatal("World became Project authority", err)
	}
	for _, tool := range MCPTools() {
		if !strings.HasPrefix(tool.Name, "world.") {
			t.Fatal("Project tool exposed")
		}
	}
}
func TestWorldSavedOperationDoesNotAdoptNewRecoveryFence(t *testing.T) {
	op := coordination.Operation{Coordinator: "world", OrganizationID: testID(30), SubjectID: testID(31), ResourceID: testID(40), CommitID: testID(20), RequestHash: strings.Repeat("a", 64), Fence: 1}
	s := &Service{coord: &coordination.Engine{Store: &coordination.Store{Fence: 2}}}
	saved := s.operation(Principal{}, worldCommit{Plan: worldPlan{Operation: &op}})
	if saved != op || saved.Fence == s.coord.Store.Fence {
		t.Fatal("recovery rewrote operation epoch")
	}
	if s.operation(Principal{}, worldCommit{}).Valid() {
		t.Fatal("missing epoch invented")
	}
}

func makeMinimalGLB(nodeNames []string) []byte {
	type gltfNode struct {
		Name string `json:"name"`
	}
	var doc struct {
		Nodes []gltfNode `json:"nodes"`
	}
	for _, n := range nodeNames {
		doc.Nodes = append(doc.Nodes, gltfNode{Name: n})
	}
	jsonBytes, _ := json.Marshal(doc)
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	totalLen := uint32(20 + len(jsonBytes))
	buf := make([]byte, totalLen)
	binary.LittleEndian.PutUint32(buf[0:4], 0x46546C67)
	binary.LittleEndian.PutUint32(buf[4:8], 2)
	binary.LittleEndian.PutUint32(buf[8:12], totalLen)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(jsonBytes)))
	binary.LittleEndian.PutUint32(buf[16:20], 0x4E4F534A)
	copy(buf[20:], jsonBytes)
	return buf
}

func TestValidateWorldEmptySetPackageRejectsActors(t *testing.T) {
	emptySetJSON := []byte(`{"schema_version":"1.1","kind":"world_empty","cast":[]}`)
	emptyGLB := makeMinimalGLB([]string{"BLDG_main_hall", "CAM_overview"})
	if err := ValidateWorldEmptySetPackage("location", emptySetJSON, emptyGLB); err != nil {
		t.Fatalf("expected valid empty set, got %v", err)
	}
	setupSetJSON := []byte(`{"schema_version":"1.1","kind":"setup","cast":[]}`)
	if err := ValidateWorldEmptySetPackage("location", setupSetJSON, emptyGLB); !errors.Is(err, ErrEmptySetHasActors) {
		t.Fatalf("expected ErrEmptySetHasActors for kind=setup, got %v", err)
	}
	castSetJSON := []byte(`{"schema_version":"1.1","kind":"world_empty","cast":[{"character_key":"liubei","label":"刘备","stand_in":"ACTOR_liubei_01"}]}`)
	if err := ValidateWorldEmptySetPackage("scene", castSetJSON, emptyGLB); !errors.Is(err, ErrEmptySetHasActors) {
		t.Fatalf("expected ErrEmptySetHasActors for non-empty cast, got %v", err)
	}
	actorGLB := makeMinimalGLB([]string{"BLDG_main_hall", "ACTOR_liubei_01"})
	if err := ValidateWorldEmptySetPackage("environment", emptySetJSON, actorGLB); !errors.Is(err, ErrEmptySetHasActors) {
		t.Fatalf("expected ErrEmptySetHasActors for ACTOR_ node in GLB, got %v", err)
	}
	// Non-scene asset types are not restricted by empty-set rules
	if err := ValidateWorldEmptySetPackage("character", castSetJSON, actorGLB); err != nil {
		t.Fatalf("expected character asset to bypass empty-set check, got %v", err)
	}
}

