package world

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func draftFixture() DraftManifest {
	subjects := []string{"镜头", "车辆"}
	return emptyDraft(testID(70), DraftRequest{Purpose: "collection", Name: "参考视频", Subjects: &subjects})
}
func draftRequestFixture() DraftRequest {
	m := draftFixture()
	return DraftRequest{Mode: "commit", DraftID: m.DraftID, ExpectedRevisionID: testID(71), CommitID: testID(72), Manifest: &m}
}
func TestDraftRequestModesAndExactShape(t *testing.T) {
	valid := []string{
		`{"mode":"create","commit_id":"` + testID(72) + `","name":"参考","purpose":"collection","subjects":[]}`,
		`{"mode":"get","draft_id":"` + testID(70) + `"}`,
		`{"mode":"status","commit_id":"` + testID(72) + `"}`,
		`{"mode":"list"}`,
	}
	data, _ := json.Marshal(draftRequestFixture())
	valid = append(valid, string(data))
	for _, v := range valid {
		if _, err := DecodeDraftRequest([]byte(v)); err != nil {
			t.Fatalf("valid request rejected: %v", err)
		}
	}
	bad := []string{
		`{"mode":"list","commit_id":"` + testID(72) + `"}`,
		`{"mode":"create","commit_id":"` + testID(72) + `","name":"参考","purpose":"collection"}`,
		`{"mode":"create","commit_id":"` + testID(72) + `","name":"参考","purpose":"collection","subjects":null}`,
		`{"mode":"get","Draft_ID":"` + testID(70) + `"}`,
		`{"mode":"get","draft_id":"` + testID(70) + `","owner_id":"` + testID(31) + `"}`,
		`{"mode":"list","mode":"list"}`,
		`{"mode":"list","limit":0}`,
		`{"mode":"list","limit":101}`,
		`{"mode":"list","q":""}`,
		`{"mode":"status","draft_id":"` + testID(70) + `","commit_id":"` + testID(72) + `"}`,
	}
	for _, v := range bad {
		if _, err := DecodeDraftRequest([]byte(v)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid request accepted: %v", err)
		}
	}
	r := draftRequestFixture()
	r.Manifest.DraftID = testID(73)
	if _, err := normalizeDraftRequest(r); !errors.Is(err, ErrInvalid) {
		t.Fatal("mismatched draft identity accepted")
	}
}
func TestDraftManifestPortableFilesAndReferences(t *testing.T) {
	base := draftFixture()
	base.Files = []File{{ID: testID(1), Path: "shots/One.txt", Role: "reference", Content: ContentRef{testID(2), testID(3), testID(4)}}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"case_path", "duplicate_id", "traversal", "domain", "duplicate_domain", "source_revision", "mixed_source", "arrays"} {
		t.Run(kind, func(t *testing.T) {
			data, _ := json.Marshal(base)
			var m DraftManifest
			_ = json.Unmarshal(data, &m)
			switch kind {
			case "case_path":
				f := m.Files[0]
				f.ID = testID(5)
				f.Path = "SHOTS/one.TXT"
				m.Files = append(m.Files, f)
			case "duplicate_id":
				f := m.Files[0]
				f.Path = "two.txt"
				m.Files = append(m.Files, f)
			case "traversal":
				m.Files[0].Path = "../private.txt"
			case "domain":
				m.DomainDocuments = []DomainDocument{{Type: "reference_learning", FileID: testID(5)}}
			case "duplicate_domain":
				m.DomainDocuments = []DomainDocument{{Type: "reference_learning", FileID: testID(1)}, {Type: "reference_learning", FileID: testID(1)}}
			case "source_revision":
				m.SourceRefs = []DraftSourceRef{{Kind: "world_draft", DraftID: testID(6), FileID: testID(7), Purpose: "reference"}}
			case "mixed_source":
				m.SourceRefs = []DraftSourceRef{{Kind: "world_draft", DraftID: testID(6), ProjectID: testID(8), RevisionID: testID(9), FileID: testID(7), Purpose: "reference"}}
			case "arrays":
				m.SourceRefs = nil
			}
			if err := m.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid manifest accepted", err)
			}
		})
	}
}
func TestDraftNormalizationDoesNotMutateAndPinsIntent(t *testing.T) {
	a := draftRequestFixture()
	b := draftRequestFixture()
	b.Manifest.Subjects = []string{"车辆", "镜头"}
	left, e1 := normalizeDraftRequest(a)
	right, e2 := normalizeDraftRequest(b)
	h1, _ := digest(left)
	h2, _ := digest(right)
	if e1 != nil || e2 != nil || h1 != h2 {
		t.Fatal("set ordering changed idempotency", e1, e2)
	}
	if a.Manifest.Subjects[0] != "镜头" {
		t.Fatal("normalization mutated caller")
	}
	right.ExpectedRevisionID = testID(99)
	h3, _ := digest(right)
	if h1 == h3 {
		t.Fatal("parent revision excluded from intent")
	}
	right = left
	copyManifest := *left.Manifest
	copyManifest.Name = "different"
	right.Manifest = &copyManifest
	h3, _ = digest(right)
	if h1 == h3 {
		t.Fatal("edited content excluded from intent")
	}
}
func TestDraftCursorCannotCrossPrincipalOrFilters(t *testing.T) {
	p := Principal{testID(30), testID(31), testID(32)}
	r := DraftRequest{Mode: "list", Purpose: "collection", Query: "视频", Subject: "镜头"}
	r.Cursor = encodeDraftCursor(p, r, testID(70))
	after, err := decodeDraftCursor(p, r)
	if err != nil || after != testID(70) {
		t.Fatal(err)
	}
	for _, field := range []string{"org", "subject", "purpose", "query", "tag"} {
		p2, r2 := p, r
		switch field {
		case "org":
			p2.OrganizationID = testID(90)
		case "subject":
			p2.SubjectID = testID(90)
		case "purpose":
			r2.Purpose = "learning"
		case "query":
			r2.Query = "歌曲"
		case "tag":
			r2.Subject = "声音"
		}
		if _, err := decodeDraftCursor(p2, r2); !errors.Is(err, ErrInvalid) {
			t.Fatal("cursor crossed", field)
		}
	}
}
func TestDraftScopeRejectsForgedSourcesBeforeDatabase(t *testing.T) {
	p := Principal{testID(30), testID(31), testID(32)}
	svc := &Service{}
	good := Source{Kind: "user_edit", WorldDraftID: testID(70)}
	if !draftUserSource(good, testID(70)) {
		t.Fatal("valid source rejected")
	}
	for _, field := range []string{"project", "asset", "revision", "task", "kind"} {
		v := good
		switch field {
		case "project":
			v.ProjectID = testID(1)
		case "asset":
			v.AssetID = testID(1)
		case "revision":
			v.WorldDraftRevisionID = testID(2)
		case "task":
			v.TaskID = "native-task"
		case "kind":
			v.Kind = "task_output"
		}
		if err := svc.AuthorizeArtifact(context.Background(), p, Permission{Action: "write", Source: &v}); !errors.Is(err, ErrForbidden) {
			t.Fatal("mixed source accepted", field, err)
		}
	}
	v := ContentVersion{OrganizationID: p.OrganizationID, ContentRef: ContentRef{testID(1), testID(2), testID(3)}}
	for _, a := range []Access{{WorldDraftID: testID(70)}, {WorldDraftRevisionID: testID(71)}, {WorldDraftID: testID(70), WorldDraftRevisionID: testID(71), AssetID: testID(72), AssetVersionID: testID(73)}} {
		if err := svc.AuthorizeArtifact(context.Background(), p, Permission{Action: "read", Version: &v, Access: &a}); !errors.Is(err, ErrForbidden) {
			t.Fatal("mixed access accepted", err)
		}
	}
}
func TestDraftResponsesMatchContract(t *testing.T) {
	r := DraftRevision{Kind: "revision", DraftID: testID(70), RevisionID: testID(71), HeadRevisionID: testID(71), CreatedAt: time.Now().UTC(), ManifestRef: ContentRef{testID(1), testID(2), testID(3)}, Manifest: draftFixture()}
	hash := strings.Repeat("a", 64)
	cases := []any{r, DraftList{Kind: "list", Items: []DraftListItem{}}, DraftStatus{Kind: "status", State: "preparing", RequestHash: hash}, DraftStatus{Kind: "status", State: "committed", RequestHash: hash, Result: &r}, DraftStatus{Kind: "status", State: "conflict", RequestHash: hash, ErrorCode: ErrConflict.Error(), CurrentRevisionID: testID(71)}, DraftStatus{Kind: "status", State: "failed", RequestHash: hash, ErrorCode: ErrForbidden.Error()}}
	for _, v := range cases {
		data, _ := json.Marshal(v)
		if err := validateLearningJSON(data, "world-draft-response"); err != nil {
			t.Fatal("response contract mismatch", err)
		}
	}
}
func TestDraftUnvalidatedDomainAndNativeRunStayUnavailable(t *testing.T) {
	svc := &Service{}
	m := draftFixture()
	m.RunRefs = []RunRef{{ServiceID: "video", RunID: "native-task"}}
	if _, _, err := svc.checkDraftContents(context.Background(), Principal{}, draftCommit{DraftID: m.DraftID, Plan: draftPlan{Manifest: m}}); !errors.Is(err, ErrNotReady) {
		t.Fatal("unverified task accepted", err)
	}
	m.RunRefs = []RunRef{}
	m.Files = []File{{ID: testID(1), Path: "learning.json", Role: "reference", Content: ContentRef{testID(2), testID(3), testID(4)}}}
	m.DomainDocuments = []DomainDocument{{Type: "reference_learning", FileID: testID(1)}}
	if _, _, err := svc.checkDraftContents(context.Background(), Principal{}, draftCommit{DraftID: m.DraftID, Plan: draftPlan{Manifest: m}}); !errors.Is(err, ErrNotReady) {
		t.Fatal("unverified domain accepted", err)
	}
}
