//go:build integration

package world

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

// Real revision, authorization and recovery persistence; Artifact is explicit test storage.
func TestLearningCurationMergePreservesFixedSourcesAndRecovers(t *testing.T) {
	ctx := t.Context()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	port := &draftTestArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{}}
	svc, err := NewService(ctx, db, port, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	port.svc = svc
	p := Principal{OrganizationID: newID(), SubjectID: newID(), RequestID: newID()}
	subjects := []string{"reference"}
	created, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "Curation recovery fixture", Purpose: "learning", Subjects: &subjects})
	if err != nil {
		t.Fatal(err)
	}
	base := created.(DraftRevision)
	fileID := newID()
	storeDocument := func(doc contentObject) ContentRef {
		t.Helper()
		raw, e := json.Marshal(doc)
		if e != nil {
			t.Fatal(e)
		}
		ref := ContentRef{newID(), newID(), newID()}
		port.versions[ref] = ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_edit", WorldDraftID: base.DraftID}, MIME: "application/json", SHA256: mediaHash(raw), Size: int64(len(raw))}
		port.bytes[ref] = raw
		return ref
	}
	original := learningCurationFixture(t)
	original["sources"] = []any{contentObject{"key": "original", "title": "Fixed original", "media_kind": "text", "role": "original", "acquisition_state": "ready", "content": contentObject{"kind": "inline", "text": "Explicit synthetic merge source"}}}
	objectOf(listOf(original["results"])[0])["source_refs"] = []any{contentObject{"kind": "source", "key": "original"}}
	originalRef := storeDocument(original)
	m := base.Manifest
	m.Files = []File{{ID: fileID, Path: "learning.json", Role: "knowledge", Content: originalRef}}
	m.DomainDocuments = []DomainDocument{{Type: "reference-learning", FileID: fileID}}
	savedRaw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: base.DraftID, ExpectedRevisionID: base.RevisionID, CommitID: newID(), Manifest: &m})
	if err != nil {
		t.Fatal(err)
	}
	saved := savedRaw.(DraftRevision)
	oldResult := objectOf(listOf(original["results"])[0])
	oldSource := objectOf(listOf(original["sources"])[0])
	ref := DraftSourceRef{Kind: "world_draft", DraftID: saved.DraftID, RevisionID: saved.RevisionID, FileID: fileID, ObjectKey: str(oldResult["key"]), Purpose: "Fixed merge input"}
	source := ref
	source.ObjectKey, source.Purpose = str(oldSource["key"]), "Preserved original provenance"
	toObject := func(r DraftSourceRef) contentObject {
		raw, _ := json.Marshal(r)
		var out contentObject
		_ = json.Unmarshal(raw, &out)
		return out
	}
	merged := learningCurationFixture(t)
	result := objectOf(listOf(merged["results"])[0])
	result["source_refs"] = []any{toObject(source)}
	a := curationAssessment(merged)
	a["merged_from_refs"], a["evidence_refs"] = []any{toObject(ref)}, []any{toObject(ref)}
	a["selection"], a["reason_codes"] = "recommended", []any{"reviewed_for_fixture"}
	mergedManifest := saved.Manifest
	mergedManifest.Files = append([]File{}, saved.Manifest.Files...)
	mergedManifest.SourceRefs = []DraftSourceRef{ref, source}
	// Dropping the old source is rejected before head publication.
	result["source_refs"] = []any{}
	mergedManifest.Files[0].Content = storeDocument(merged)
	if _, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: saved.DraftID, ExpectedRevisionID: saved.RevisionID, CommitID: newID(), Manifest: &mergedManifest}); !errors.Is(err, ErrInvalid) {
		t.Fatal("lost source accepted", err)
	}
	result["source_refs"] = []any{toObject(source)}
	mergedRef := storeDocument(merged)
	mergedManifest.Files[0].Content = mergedRef
	request := DraftRequest{Mode: "commit", DraftID: saved.DraftID, ExpectedRevisionID: saved.RevisionID, CommitID: newID(), Manifest: &mergedManifest}
	port.failRetain = true
	if _, err = svc.WorldDraft(ctx, p, request); !errors.Is(err, ErrDependency) {
		t.Fatal("outage was not recoverable", err)
	}
	head, err := svc.getDraft(ctx, p, saved.DraftID, "")
	if err != nil || head.RevisionID != saved.RevisionID {
		t.Fatal("pending merge changed head", err)
	}
	svc, err = NewService(ctx, db, port, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	port.svc = svc
	resumed, err := svc.WorldDraft(ctx, p, request)
	if err != nil {
		t.Fatal("merge recovery", err)
	}
	newRevision := resumed.(DraftRevision)
	replayed, err := svc.WorldDraft(ctx, p, request)
	if err != nil || replayed.(DraftRevision).RevisionID != newRevision.RevisionID {
		t.Fatal("duplicate merge revision", err)
	}
	old, err := svc.getDraft(ctx, p, saved.DraftID, saved.RevisionID)
	if err != nil || old.Manifest.Files[0].Content != originalRef {
		t.Fatal("old source overwritten", err)
	}
	newContent, err := port.Read(ctx, p, mergedRef, Access{WorldDraftID: saved.DraftID, WorldDraftRevisionID: newRevision.RevisionID}, MaxManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ValidateLearningContent(newContent, newRevision.Manifest.Files)
	if err != nil || str(curationAssessment(parsed.Document)["selection"]) != "recommended" {
		t.Fatal("review lost on recovery", err)
	}
	other := p
	other.SubjectID = newID()
	if _, err = svc.WorldDraft(ctx, other, request); err == nil {
		t.Fatal("foreign merge replay")
	}
}
