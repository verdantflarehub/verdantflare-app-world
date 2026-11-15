package world

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLearningPreviewProofIdentityAndMediaMultisets(t *testing.T) {
	run := RunRef{"image", "synthetic"}
	a := ProducerMedia{SHA256: strings.Repeat("a", 64), Size: 100, MIME: "image/png"}
	b := ProducerMedia{SHA256: strings.Repeat("b", 64), Size: 200, MIME: "image/png"}
	good := ProducerProof{Version: 1, ServiceID: run.ServiceID, RunID: run.RunID, ProjectID: testID(10), State: "succeeded", Inputs: []ProducerMedia{b, a}, Outputs: []ProducerMedia{a, b}}
	if err := matchCurationPreviewProof(run, good.ProjectID, good, []ProducerMedia{a, b}, b); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "service", "run", "project", "state", "input", "duplicate", "missing", "output", "size", "mime"} {
		t.Run(key, func(t *testing.T) {
			bad := good
			switch key {
			case "version":
				bad.Version = 2
			case "service":
				bad.ServiceID = "other"
			case "run":
				bad.RunID = "other"
			case "project":
				bad.ProjectID = testID(11)
			case "state":
				bad.State = "running"
			case "input":
				bad.Inputs = []ProducerMedia{a}
			case "duplicate":
				bad.Inputs = []ProducerMedia{a, a}
			case "missing":
				bad.Inputs = nil
			case "output":
				bad.Outputs = []ProducerMedia{a}
			case "size":
				changed := b
				changed.Size++
				bad.Outputs = []ProducerMedia{changed}
			case "mime":
				changed := b
				changed.MIME = "video/mp4"
				bad.Outputs = []ProducerMedia{changed}
			}
			if matchCurationPreviewProof(run, good.ProjectID, bad, []ProducerMedia{a, b}, b) == nil {
				t.Fatal("mismatched proof accepted")
			}
		})
	}
	good.Inputs = nil
	if err := matchCurationPreviewProof(run, good.ProjectID, good, nil, b); err != nil {
		t.Fatal("text-only generation requires no invented input", err)
	}
}

func TestLearningGeneratedPreviewRequiresArtifactAndProducerAuthorization(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	ref := DraftSourceRef{Kind: "project", ProjectID: testID(10), RevisionID: testID(11), FileID: testID(12), Purpose: "Synthetic preview"}
	rawRef, _ := json.Marshal(ref)
	var r contentObject
	_ = json.Unmarshal(rawRef, &r)
	d := learningCurationFixture(t)
	result := objectOf(listOf(d["results"])[0])
	result["type_key"], result["domain"] = "scene", "scene"
	result["payload"] = contentObject{"setting": "Synthetic room", "spatial_layout": "Door at left"}
	preview := contentObject{"key": "generated", "state": "ready", "media_kind": "image", "role": "generated_effect", "note": "Synthetic proof", "file_ref": r, "sha256": strings.Repeat("a", 64), "locator": contentObject{"kind": "whole"}, "run_ref": contentObject{"service_id": "image", "run_id": "synthetic"}, "input_refs": []any{}}
	curationAssessment(d)["previews"] = []any{preview}
	doc, err := parseCuration(t, d)
	if err != nil {
		t.Fatal(err)
	}
	file := File{ID: ref.FileID, Content: ContentRef{testID(1), testID(2), testID(3)}}
	access := Access{ProjectID: ref.ProjectID, RevisionID: ref.RevisionID}
	media := ProducerMedia{SHA256: strings.Repeat("a", 64), Size: 100, MIME: "image/png"}
	artifact := &learningTestArtifact{access: access, version: ContentVersion{ContentRef: file.Content, OrganizationID: p.OrganizationID, SHA256: media.SHA256, Size: media.Size, MIME: media.MIME}}
	reader := &producerTestReader{proof: ProducerProof{Version: 1, ServiceID: "image", RunID: "synthetic", ProjectID: ref.ProjectID, State: "succeeded", Inputs: []ProducerMedia{}, Outputs: []ProducerMedia{media}}}
	svc := &Service{content: artifact, producer: reader}
	m := draftFixture()
	m.SourceRefs = []DraftSourceRef{ref}
	m.RunRefs = []RunRef{{"image", "synthetic"}}
	inputs := draftInputs{Files: map[DraftSourceRef]File{ref: file}, Access: map[ContentRef]Access{file.Content: access}}
	check := func() error { return svc.checkLearningBindings(t.Context(), p, doc, m, &inputs) }
	if err := check(); err != nil || !inputs.VerifiedRuns[RunRef{"image", "synthetic"}] {
		t.Fatal("valid generated preview", err)
	}
	artifact.deny = true
	reader.called = false
	if err := check(); !errors.Is(err, ErrForbidden) || reader.called {
		t.Fatal("revoked artifact reached producer", err)
	}
	artifact.deny = false
	reader.deny = true
	if err := check(); !errors.Is(err, ErrForbidden) {
		t.Fatal("producer authorization bypassed", err)
	}
	reader.deny = false
	reader.proof.State = "failed"
	if err := check(); err == nil {
		t.Fatal("old VerifiedRuns bypassed fresh proof")
	}
	reader.proof.State = "succeeded"
	reader.proof.Inputs = []ProducerMedia{media}
	if err := check(); err == nil {
		t.Fatal("undeclared native input accepted")
	}
	reader.proof.Inputs = nil
	delete(inputs.Files, ref)
	if err := check(); err == nil {
		t.Fatal("unresolved output accepted")
	}
}

func TestLearningPreviewCannotBorrowAnotherPromptExecution(t *testing.T) {
	ref := contentObject{"kind": "project", "project_id": testID(1), "revision_id": testID(2), "file_id": testID(3), "purpose": "preview"}
	run := contentObject{"service_id": "image", "run_id": "synthetic"}
	preview := contentObject{"file_ref": ref, "run_ref": run}
	e := contentObject{"run_ref": run, "output_refs": []any{ref}}
	result := contentObject{"payload": contentObject{"variants": []any{contentObject{"execution_evidence": []any{e}}}}}
	if !curationPreviewMatchesPrompt(result, preview) {
		t.Fatal("matching sample rejected")
	}
	e["run_ref"] = contentObject{"service_id": "image", "run_id": "other"}
	if curationPreviewMatchesPrompt(result, preview) {
		t.Fatal("other run accepted")
	}
	e["run_ref"] = run
	e["output_refs"] = []any{}
	if curationPreviewMatchesPrompt(result, preview) {
		t.Fatal("missing output accepted")
	}
}
