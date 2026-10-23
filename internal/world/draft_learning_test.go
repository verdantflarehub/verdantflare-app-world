package world

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

func learningPromptFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/reference_learning_cases.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Case     string          `json:"case"`
		Valid    bool            `json:"valid"`
		Document json.RawMessage `json:"document"`
	}
	if json.Unmarshal(data, &cases) != nil {
		t.Fatal("invalid central fixtures")
	}
	for _, v := range cases {
		if v.Case == "test_original_prompt_needs_no_project_or_source" && v.Valid {
			return v.Document
		}
	}
	t.Fatal("missing independent prompt fixture")
	return nil
}

type learningTestArtifact struct {
	version ContentVersion
	data    []byte
	access  Access
	deny    bool
	reads   int
}

func (a *learningTestArtifact) Write(context.Context, Principal, string, TextWrite) (ContentVersion, error) {
	return ContentVersion{}, ErrForbidden
}
func (a *learningTestArtifact) WriteAssetManifest(context.Context, Principal, string, string, TextWrite) (ContentVersion, error) {
	return ContentVersion{}, ErrForbidden
}
func (a *learningTestArtifact) Metadata(_ context.Context, p Principal, ref ContentRef, access Access) (ContentVersion, error) {
	if a.deny || access != a.access || ref != a.version.ContentRef || p.OrganizationID != a.version.OrganizationID {
		return ContentVersion{}, ErrForbidden
	}
	return a.version, nil
}
func (a *learningTestArtifact) Read(ctx context.Context, p Principal, ref ContentRef, access Access, _ int64) ([]byte, error) {
	if _, err := a.Metadata(ctx, p, ref, access); err != nil {
		return nil, err
	}
	a.reads++
	return a.data, nil
}
func (a *learningTestArtifact) Retain(context.Context, Principal, RetentionOwner, []ContentRef) error {
	return ErrDependency
}
func TestDraftLearningReadsBytesAndRejectsInvalidContent(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	m := draftFixture()
	m.Purpose = "learning"
	ref := ContentRef{testID(1), testID(2), testID(3)}
	m.Files = []File{{ID: testID(4), Path: "learning.json", Role: "knowledge", Content: ref}}
	m.DomainDocuments = []DomainDocument{{Type: "reference-learning", FileID: testID(4)}}
	good := learningPromptFixture(t)
	a := &learningTestArtifact{version: ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_edit", WorldDraftID: m.DraftID}}, data: good}
	svc := &Service{content: a}
	c := draftCommit{DraftID: m.DraftID, Plan: draftPlan{Manifest: m}}
	refs, grants, err := svc.checkDraftContents(context.Background(), p, c)
	if err != nil || len(refs) != 1 || a.reads != 1 || len(grants["world"]) != 1 {
		t.Fatal("valid independent prompt failed", err)
	}
	var d contentObject
	_ = json.Unmarshal(good, &d)
	r := objectOf(listOf(d["results"])[0])
	r["parent_key"] = r["key"]
	a.data, _ = json.Marshal(d)
	if _, _, err = svc.checkDraftContents(context.Background(), p, c); !errors.Is(err, ErrInvalid) {
		t.Fatal("cyclic saved body accepted", err)
	}
	a.data = good
	a.deny = true
	if _, _, err = svc.checkDraftContents(context.Background(), p, c); !errors.Is(err, ErrForbidden) {
		t.Fatal("unreadable body accepted", err)
	}
	a.deny = false
	_ = json.Unmarshal(good, &d)
	d["origin"] = "generated"
	d["provenance"] = map[string]any{"producer": "test", "version": "1", "run_refs": []any{map[string]any{"service_id": "video", "run_id": "unverified-task"}}}
	a.data, _ = json.Marshal(d)
	if _, _, err = svc.checkDraftContents(context.Background(), p, c); !errors.Is(err, ErrNotReady) {
		t.Fatal("native task declaration treated as verified", err)
	}
}
func TestDraftLearningResolvesExactSourceObject(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	data := learningPromptFixture(t)
	doc, err := ValidateLearningContent(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for k := range doc.Results {
		key = k
	}
	content := ContentRef{testID(1), testID(2), testID(3)}
	file := File{ID: testID(4), Path: "learning.json", Role: "knowledge", Content: content}
	ref := DraftSourceRef{Kind: "world_draft", DraftID: testID(70), RevisionID: testID(71), FileID: file.ID, Purpose: "reference", ObjectKey: key}
	access := Access{WorldDraftID: ref.DraftID, WorldDraftRevisionID: ref.RevisionID}
	a := &learningTestArtifact{version: ContentVersion{ContentRef: content, OrganizationID: p.OrganizationID}, data: data, access: access}
	svc := &Service{content: a}
	if _, err = svc.readLearningSourceObject(context.Background(), p, ref, file, []File{file}, access); err != nil {
		t.Fatal(err)
	}
	ref.ObjectKey = "missing"
	if _, err = svc.readLearningSourceObject(context.Background(), p, ref, file, []File{file}, access); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing object accepted", err)
	}
	ref.ObjectKey = key
	access.WorldDraftRevisionID = testID(72)
	if _, err = svc.readLearningSourceObject(context.Background(), p, ref, file, []File{file}, access); !errors.Is(err, ErrForbidden) {
		t.Fatal("source version silently changed", err)
	}
}
func TestDraftLearningExternalReferencesMustBeDeclaredAndTyped(t *testing.T) {
	ref := DraftSourceRef{Kind: "world_draft", DraftID: testID(70), RevisionID: testID(71), FileID: testID(72), Purpose: "reference", ObjectKey: "method"}
	d := LearningContent{Document: contentObject{"relations": []any{}, "uses": []any{}}, Sources: map[string]contentObject{}, Results: map[string]contentObject{}, RemoteRefs: []DraftSourceRef{ref}}
	m := draftFixture()
	inputs := draftInputs{Objects: map[DraftSourceRef]contentObject{}, Grants: coordination.Plan{"world": {{ResourceID: m.DraftID, Write: true}}}}
	svc := &Service{}
	if err := svc.checkLearningBindings(context.Background(), Principal{}, d, m, &inputs); !errors.Is(err, ErrInvalid) {
		t.Fatal("undeclared external context accepted", err)
	}
	m.SourceRefs = []DraftSourceRef{ref}
	if err := svc.checkLearningBindings(context.Background(), Principal{}, d, m, &inputs); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ref)
	var object contentObject
	_ = json.Unmarshal(raw, &object)
	if _, err := resolveLearningObject(d, inputs, object); !errors.Is(err, ErrInvalid) {
		t.Fatal("unresolved external object accepted", err)
	}
	inputs.Objects[ref] = contentObject{"key": "method", "type_key": "style", "payload": contentObject{"rules": []any{contentObject{"key": "light"}}}}
	d.Document["relations"] = []any{contentObject{"relation_type": "constrains", "from_ref": object, "attributes": contentObject{"rule_key": "missing"}}}
	if err := svc.checkLearningBindings(context.Background(), Principal{}, d, m, &inputs); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing external rule accepted", err)
	}
	objectOf(objectOf(listOf(d.Document["relations"])[0])["attributes"])["rule_key"] = "light"
	if err := svc.checkLearningBindings(context.Background(), Principal{}, d, m, &inputs); err != nil {
		t.Fatal("declared external rule rejected", err)
	}
}
func TestDraftHTTPAndMCPShareStrictModeValidation(t *testing.T) {
	serviceToken, authorityToken := strings.Repeat("a", 32), strings.Repeat("b", 32)
	svc := &Service{}
	h, err := svc.HTTPHandler(serviceToken, authorityToken)
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := svc.MCPHandler(serviceToken, authorityToken)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, body string) *http.Request {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+serviceToken)
		r.Header.Set("X-User-Id", testID(31))
		r.Header.Set("X-Organization-Id", testID(30))
		r.Header.Set("X-Request-Id", testID(32))
		return r
	}
	for _, path := range []string{"/internal/v1/world/draft", "/api/v1/world/draft"} {
		out := httptest.NewRecorder()
		h.ServeHTTP(out, request(path, `{"mode":"list","draft_id":"`+testID(70)+`"}`))
		if out.Code != 400 || !strings.Contains(out.Body.String(), ErrInvalid.Error()) {
			t.Fatal("draft handler missing or permissive", out.Code)
		}
	}
	out := httptest.NewRecorder()
	mcp.ServeHTTP(out, request("/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if out.Code != 200 {
		t.Fatal(out.Code)
	}
	var reply struct {
		Result struct {
			Tools []MCPTool `json:"tools"`
		} `json:"result"`
	}
	if json.Unmarshal(out.Body.Bytes(), &reply) != nil {
		t.Fatal("invalid tool list")
	}
	found := false
	for _, tool := range reply.Result.Tools {
		if tool.Name == "world.draft" {
			found = true
			if tool.InputSchema["type"] != "object" || len(listOf(tool.InputSchema["oneOf"])) != 5 {
				t.Fatal("mode schema incomplete")
			}
		}
	}
	if !found {
		t.Fatal("draft MCP not discoverable")
	}
	out = httptest.NewRecorder()
	mcp.ServeHTTP(out, request("/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"world.draft","arguments":{"mode":"get"}}}`))
	if out.Code != 400 || !strings.Contains(out.Body.String(), ErrInvalid.Error()) {
		t.Fatal("MCP bypassed HTTP validation", out.Code)
	}
}
