package world

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
)

func learningCurationFixture(t *testing.T) contentObject {
	t.Helper()
	var doc contentObject
	if err := json.Unmarshal(learningPromptFixture(t), &doc); err != nil {
		t.Fatal(err)
	}
	doc["schema_version"] = 2
	key := str(objectOf(listOf(doc["results"])[0])["key"])
	doc["curation"] = contentObject{
		"packages":    []any{contentObject{"key": "study", "purpose": "Synthetic study", "result_keys": []any{key}}},
		"assessments": []any{contentObject{"result_key": key, "package_keys": []any{"study"}, "selection": "unassessed", "reason_codes": []any{"pending_review"}, "note": "Synthetic fixture", "evidence_refs": []any{}, "merged_from_refs": []any{}, "previews": []any{}}},
	}
	return doc
}

func curationAssessment(doc contentObject) contentObject {
	return objectOf(listOf(objectOf(doc["curation"])["assessments"])[0])
}

func parseCuration(t *testing.T, doc contentObject) (LearningContent, error) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return ValidateLearningContent(raw, nil)
}

func TestLearningCurationV2StructuralBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(contentObject)
	}{
		{"missing_assessment", func(d contentObject) { objectOf(d["curation"])["assessments"] = []any{} }},
		{"duplicate_assessment", func(d contentObject) {
			objectOf(d["curation"])["assessments"] = []any{curationAssessment(d), curationAssessment(d)}
		}},
		{"membership", func(d contentObject) { curationAssessment(d)["package_keys"] = []any{} }},
		{"missing_result", func(d contentObject) {
			objectOf(listOf(objectOf(d["curation"])["packages"])[0])["result_keys"] = []any{"missing"}
		}},
		{"recommend_without_evidence", func(d contentObject) { curationAssessment(d)["selection"] = "recommended" }},
		{"duplicate_reason", func(d contentObject) { curationAssessment(d)["reason_codes"] = []any{"pending", "pending"} }},
		{"unknown_version", func(d contentObject) { d["schema_version"] = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := learningCurationFixture(t)
			if _, err := parseCuration(t, doc); err != nil {
				t.Fatal("valid v2 rejected", err)
			}
			tc.edit(doc)
			if _, err := parseCuration(t, doc); err == nil {
				t.Fatal("inconsistent curation accepted")
			}
		})
	}
}

func TestDraftLearningCurationV2UsesExistingContentGate(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	m := draftFixture()
	m.Purpose = "learning"
	ref := ContentRef{testID(1), testID(2), testID(3)}
	m.Files = []File{{ID: testID(4), Path: "learning.json", Role: "knowledge", Content: ref}}
	m.DomainDocuments = []DomainDocument{{Type: "reference-learning", FileID: testID(4)}}
	raw, _ := json.Marshal(learningCurationFixture(t))
	a := &learningTestArtifact{version: ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_edit", WorldDraftID: m.DraftID}}, data: raw}
	svc := &Service{content: a}
	c := draftCommit{DraftID: m.DraftID, Plan: draftPlan{Manifest: m}}
	if _, _, err := svc.checkDraftContents(context.Background(), p, c); err != nil {
		t.Fatal(err)
	}
	a.deny = true
	if _, _, err := svc.checkDraftContents(context.Background(), p, c); !errors.Is(err, ErrForbidden) {
		t.Fatal("v2 bypassed content authorization", err)
	}
}

func TestLearningCurationOriginalPreviewChecksAuthorizedMetadata(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	ref := DraftSourceRef{Kind: "project", ProjectID: testID(10), RevisionID: testID(11), FileID: testID(12), Purpose: "Original reference"}
	rawRef, _ := json.Marshal(ref)
	var r contentObject
	_ = json.Unmarshal(rawRef, &r)
	d := learningCurationFixture(t)
	objectOf(listOf(d["results"])[0])["source_refs"] = []any{r}
	preview := contentObject{"key": "original", "state": "ready", "media_kind": "image", "role": "original_reference", "note": "Synthetic metadata", "file_ref": r, "sha256": strings.Repeat("a", 64), "locator": contentObject{"kind": "whole"}}
	curationAssessment(d)["previews"] = []any{preview}
	doc, err := parseCuration(t, d)
	if err != nil {
		t.Fatal(err)
	}
	file := File{ID: ref.FileID, Content: ContentRef{testID(1), testID(2), testID(3)}}
	access := Access{ProjectID: ref.ProjectID, RevisionID: ref.RevisionID}
	a := &learningTestArtifact{access: access, version: ContentVersion{ContentRef: file.Content, OrganizationID: p.OrganizationID, SHA256: strings.Repeat("a", 64), Size: 1024, MIME: "image/png"}}
	svc := &Service{content: a}
	m := draftFixture()
	m.SourceRefs = []DraftSourceRef{ref}
	inputs := draftInputs{Files: map[DraftSourceRef]File{ref: file}, Access: map[ContentRef]Access{file.Content: access}}
	check := func() error { return svc.checkLearningBindings(context.Background(), p, doc, m, &inputs) }
	if err := check(); err != nil {
		t.Fatal(err)
	}
	a.version.SHA256 = strings.Repeat("b", 64)
	if err := check(); err == nil {
		t.Fatal("wrong media digest accepted")
	}
	a.version.SHA256 = strings.Repeat("a", 64)
	a.version.MIME = "video/mp4"
	if err := check(); err == nil {
		t.Fatal("wrong media kind accepted")
	}
	a.version.MIME = "image/png"
	a.deny = true
	if err := check(); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked media accepted", err)
	}
	a.deny = false
	m.SourceRefs = nil
	if err := check(); err == nil {
		t.Fatal("undeclared remote reference accepted")
	}
}

func TestLearningCurationImageRegionUsesExactBoundedNumbers(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"0e999999999", true}, {"1e-999999999", false}, {"0.9999999999999999999", true},
	} {
		if _, ok := curationUnitRatio(json.Number(tc.value)); ok != tc.valid {
			t.Fatalf("ratio %s valid=%v", tc.value, ok)
		}
	}
	a, _ := curationUnitRatio(json.Number("0.9999999999999999999"))
	b, _ := curationUnitRatio(json.Number("0.0000000000000000002"))
	if a.Add(a, b).Cmp(big.NewRat(1, 1)) <= 0 {
		t.Fatal("fraction rounded down")
	}
}

func TestLearningCurationMergeRequiresResolvedTypeAndPreservedSource(t *testing.T) {
	d := learningCurationFixture(t)
	oldRef := DraftSourceRef{Kind: "project", ProjectID: testID(10), RevisionID: testID(11), FileID: testID(12), ObjectKey: "old", Purpose: "Merge source"}
	sourceRef := oldRef
	sourceRef.ObjectKey = "source"
	toObject := func(ref DraftSourceRef) contentObject {
		b, _ := json.Marshal(ref)
		var o contentObject
		_ = json.Unmarshal(b, &o)
		return o
	}
	curationAssessment(d)["merged_from_refs"] = []any{toObject(oldRef)}
	current := objectOf(listOf(d["results"])[0])
	current["source_refs"] = []any{toObject(sourceRef)}
	doc, err := parseCuration(t, d)
	if err != nil {
		t.Fatal(err)
	}
	old := contentObject{"type_key": current["type_key"], "type_version": json.Number("1"), "domain": current["domain"], "source_refs": []any{contentObject{"kind": "source", "key": "source"}}}
	inputs := draftInputs{Objects: map[DraftSourceRef]contentObject{oldRef: old}}
	svc := &Service{}
	if err := svc.checkLearningCurationBindings(context.Background(), Principal{}, doc, DraftManifest{}, &inputs); err != nil {
		t.Fatal(err)
	}
	old["type_key"] = "method"
	if err := svc.checkLearningCurationBindings(context.Background(), Principal{}, doc, DraftManifest{}, &inputs); err == nil {
		t.Fatal("type mismatch accepted")
	}
	old["type_key"] = current["type_key"]
	doc.Results[str(current["key"])]["source_refs"] = []any{}
	if err := svc.checkLearningCurationBindings(context.Background(), Principal{}, doc, DraftManifest{}, &inputs); err == nil {
		t.Fatal("lost provenance accepted")
	}
}

func TestLearningCurationGeneratedDocumentRequiresTrustedAssembler(t *testing.T) {
	d := learningCurationFixture(t)
	d["origin"] = "generated"
	d["provenance"] = contentObject{"producer": "fixture", "version": "synthetic", "run_refs": []any{contentObject{"service_id": "world", "run_id": "synthetic"}}}
	d["processing_snapshot"] = contentObject{"task_id": testID(1), "pipeline_version": 2, "assembly_version": "1", "adapter_revision": "synthetic", "plan_sha256": strings.Repeat("a", 64)}
	doc, err := parseCuration(t, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Service{}).checkLearningBindings(context.Background(), Principal{}, doc, DraftManifest{}, &draftInputs{}); !errors.Is(err, ErrNotReady) {
		t.Fatal("caller processing claims accepted", err)
	}
}
