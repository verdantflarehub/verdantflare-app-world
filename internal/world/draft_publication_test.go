package world

import (
	"encoding/json"
	"errors"
	"testing"
)

func publicationFixture() (WorldRegisterRequest, DraftRevision) {
	m := draftFixture()
	m.Files = []File{
		{ID: testID(10), Path: "prompt.json", Role: "knowledge", Content: ContentRef{testID(20), testID(21), testID(22)}},
		{ID: testID(11), Path: "private-notes.txt", Role: "notes", Content: ContentRef{testID(20), testID(23), testID(24)}},
	}
	m.DomainDocuments = []DomainDocument{{Type: "reference-learning", FileID: testID(10)}}
	deps := []AssetRef{}
	r := WorldRegisterRequest{CommitID: testID(31), SourceDraftID: m.DraftID, SourceRevisionID: testID(32), FileIDs: []string{testID(10)}, Name: "Reusable prompt", AssetType: "prompt", Subjects: []string{}, Relation: "curated_in", ReviewFileID: testID(11), DependsOn: &deps}
	return r, DraftRevision{DraftID: m.DraftID, RevisionID: r.SourceRevisionID, Manifest: m, ManifestRef: ContentRef{testID(20), testID(25), testID(26)}}
}
func TestDraftPublicationSelectsOnlyExplicitFiles(t *testing.T) {
	r, source := publicationFixture()
	plan, err := prepareDraftAsset(r, testID(40), source)
	if err != nil {
		t.Fatal(err)
	}
	m := plan.Manifest
	if m.SchemaVersion != 2 || m.Source.ProjectID != "" || m.Source.DraftID != source.DraftID || len(m.Files) != 1 || m.Files[0] != source.Manifest.Files[0] || m.DomainDocuments == nil || len(*m.DomainDocuments) != 1 {
		t.Fatal("invalid independent asset selection")
	}
	if m.Source.ReviewFileID != r.ReviewFileID || len(plan.Refs) != 0 {
		t.Fatal("private review file implicitly imported")
	}
	r.FileIDs = []string{testID(11)}
	plan, err = prepareDraftAsset(r, testID(40), source)
	if err != nil || len(*plan.Manifest.DomainDocuments) != 0 {
		t.Fatal("unselected domain document included", err)
	}
	for _, ids := range [][]string{{testID(99)}, {testID(10), testID(10)}, {}} {
		r.FileIDs = ids
		if _, err = prepareDraftAsset(r, testID(40), source); !errors.Is(err, ErrInvalid) {
			t.Fatal("bad file selection accepted", err)
		}
	}
	r.FileIDs = []string{testID(10)}
	r.ReviewFileID = testID(99)
	if _, err = prepareDraftAsset(r, testID(40), source); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown review file accepted")
	}
	r.ReviewFileID = ""
	deps := []AssetRef{{AssetID: testID(41), VersionID: testID(42), Purpose: "style"}}
	r.DependsOn = &deps
	if _, err = prepareDraftAsset(r, testID(40), source); !errors.Is(err, ErrInvalid) {
		t.Fatal("undeclared dependency accepted")
	}
}
func TestDraftPublicationCannotMixSourceScopes(t *testing.T) {
	r, _ := publicationFixture()
	if _, err := normalizeWorld(r); err != nil {
		t.Fatal(err)
	}
	r.SourceProjectID = testID(50)
	if _, err := normalizeWorld(r); !errors.Is(err, ErrInvalid) {
		t.Fatal("mixed source accepted")
	}
	r.SourceDraftID = ""
	if _, err := normalizeWorld(r); err != nil {
		t.Fatal("legacy project rejected", err)
	}
	r.SourceProjectID = ""
	if _, err := normalizeWorld(r); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing source accepted")
	}
}
func TestDraftPublicationLocalDependenciesRequireSelection(t *testing.T) {
	// The learning validator receives the selected inventory, not all source files.
	data := learningPromptFixture(t)
	var doc contentObject
	if json.Unmarshal(data, &doc) != nil {
		t.Fatal("fixture")
	}
	result := objectOf(listOf(doc["results"])[0])
	objectOf(result["payload"])["variables"] = []any{contentObject{"key": "reference", "value_type": "ref", "required": false, "description": "Selected reference", "default": contentObject{"kind": "file", "file_id": testID(11)}}}
	raw, _ := json.Marshal(doc)
	_, source := publicationFixture()
	if _, err := ValidateLearningContent(raw, source.Manifest.Files); err != nil {
		t.Fatal("fixture dependency invalid", err)
	}
	if _, err := ValidateLearningContent(raw, source.Manifest.Files[:1]); !errors.Is(err, ErrInvalid) {
		t.Fatal("unselected dependency silently accepted", err)
	}
}
