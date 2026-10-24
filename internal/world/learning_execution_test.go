package world

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLearningExecutionProofBindsNativeIdentity(t *testing.T) {
	run := RunRef{"image", "task1"}
	project := "project1"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("portrait")))
	output := ProducerMedia{SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 100, MIME: "image/png"}
	proof := ProducerProof{Version: 1, ServiceID: "image", RunID: "task1", ProjectID: project, State: "succeeded", ModelID: "gpt-image-2.5-sunburst", Route: "codex", PromptHash: hash, Parameters: map[string]any{"size": "1024x1024"}, Inputs: []ProducerMedia{}, Outputs: []ProducerMedia{output}}
	variant := contentObject{"prompt_text": "portrait", "model_id": proof.ModelID, "route": "codex", "parameters": contentObject{"size": "1024x1024"}, "reference_bindings": []any{}}
	execution := contentObject{"state": "succeeded", "model_id": proof.ModelID, "route": "codex", "prompt_sha256": hash, "input_refs": []any{}}
	if matchLearningExecution(variant, execution, run, project, proof, nil, []ProducerMedia{output}) != nil {
		t.Fatal("valid proof rejected")
	}
	for _, kind := range []string{"run", "project", "model", "prompt", "route", "state", "output", "parameter", "input"} {
		t.Run(kind, func(t *testing.T) {
			p := proof
			switch kind {
			case "run":
				p.RunID = "another"
			case "project":
				p.ProjectID = "another"
			case "model":
				p.ModelID = "another"
			case "prompt":
				p.PromptHash = "bad"
			case "route":
				p.Route = "other"
			case "state":
				p.State = "failed"
			case "output":
				p.Outputs = []ProducerMedia{{SHA256: output.SHA256, Size: 101, MIME: output.MIME}}
			case "parameter":
				p.Parameters = map[string]any{"size": "other"}
			case "input":
				p.Inputs = []ProducerMedia{output}
			}
			if matchLearningExecution(variant, execution, run, project, p, nil, []ProducerMedia{output}) == nil {
				t.Fatal("mismatch accepted")
			}
		})
	}
}

type producerTestReader struct {
	proof  ProducerProof
	called bool
	deny   bool
}

func (r *producerTestReader) Read(_ context.Context, p Principal, run RunRef, project string) (ProducerProof, error) {
	r.called = true
	if r.deny || !p.Valid() || run.RunID != r.proof.RunID || project != r.proof.ProjectID {
		return ProducerProof{}, ErrForbidden
	}
	return r.proof, nil
}
func TestLearningExecutionRequiresAuthorizedFixedProjectFiles(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	project, revision := testID(10), testID(11)
	content := ContentRef{testID(1), testID(2), testID(3)}
	file := File{ID: testID(4), Content: content}
	data := learningPromptFixture(t)
	var raw contentObject
	json.Unmarshal(data, &raw)
	variant := objectOf(listOf(objectOf(objectOf(listOf(raw["results"])[0])["payload"])["variants"])[0])
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("portrait")))
	media := ProducerMedia{SHA256: strings.Repeat("a", 64), Size: 100, MIME: "image/png"}
	run := RunRef{"image", "task1"}
	reference := contentObject{"kind": "project", "project_id": project, "revision_id": revision, "file_id": file.ID, "purpose": "sample"}
	variant["model_id"] = "gpt-image-2.5-sunburst"
	variant["route"] = "codex"
	variant["prompt_text"] = "portrait"
	variant["parameters"] = contentObject{}
	variant["reference_bindings"] = []any{}
	variant["execution_evidence"] = []any{contentObject{"run_ref": contentObject{"service_id": "image", "run_id": "task1"}, "model_id": "gpt-image-2.5-sunburst", "route": "codex", "state": "succeeded", "prompt_sha256": hash, "input_refs": []any{}, "output_refs": []any{reference}, "note": "native verified sample"}}
	data, _ = json.Marshal(raw)
	doc, err := ValidateLearningContent(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := learningSourceRef(reference)
	inputs := draftInputs{Files: map[DraftSourceRef]File{source: file}, Access: map[ContentRef]Access{content: {ProjectID: project, RevisionID: revision}}}
	artifact := &learningTestArtifact{version: ContentVersion{ContentRef: content, OrganizationID: p.OrganizationID, SHA256: media.SHA256, Size: media.Size, MIME: media.MIME}, access: Access{ProjectID: project, RevisionID: revision}}
	reader := &producerTestReader{proof: ProducerProof{Version: 1, ServiceID: "image", RunID: "task1", ProjectID: project, State: "succeeded", ModelID: "gpt-image-2.5-sunburst", Route: "codex", PromptHash: hash, Parameters: map[string]any{}, Inputs: []ProducerMedia{}, Outputs: []ProducerMedia{media}}}
	svc := &Service{content: artifact, producer: reader}
	manifest := DraftManifest{SourceRefs: []DraftSourceRef{source}, RunRefs: []RunRef{run}}
	if err = svc.checkLearningBindings(t.Context(), p, doc, manifest, &inputs); err != nil || !reader.called || !inputs.VerifiedRuns[run] {
		t.Fatal("authorized native sample", err)
	}
	artifact.deny = true
	reader.called = false
	if err = svc.checkLearningBindings(t.Context(), p, doc, manifest, &inputs); err == nil || reader.called {
		t.Fatal("revoked file reached producer")
	}
	artifact.deny = false
	reader.deny = true
	if err = svc.checkLearningBindings(t.Context(), p, doc, manifest, &inputs); err == nil {
		t.Fatal("revoked native access")
	}
	reader.deny = false
	doc.Document["provenance"] = contentObject{"producer": "made-up"}
	if err = svc.checkLearningExecutions(t.Context(), p, doc, manifest, &inputs); err == nil {
		t.Fatal("forged producer")
	}
}
