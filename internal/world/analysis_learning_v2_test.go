package world

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

func learningTaskFixture(m analysis.LearningMaterial) analysis.Task {
	return analysis.Task{PipelineVersion: 2, IncludeTypedResults: true, SaveLearning: true,
		TaskID: m.Bundle.TaskID, DraftID: m.Bundle.DraftID, RevisionID: m.Bundle.RevisionID, FileID: m.Bundle.FileID,
		Plan: analysis.Plan{Source: m.Bundle.Source}}
}

func TestAnalysisLearningVersionOnePreservesExactBytes(t *testing.T) {
	m := learningMaterialFixture(t)
	legacy, err := assembleAnalysisLearning(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := assembleAnalysisLearningVersion(m, learningTaskFixture(m), "1")
	if err != nil || !bytes.Equal(raw, legacy) || analysisDocumentHash(raw) != analysisDocumentHash(legacy) {
		t.Fatal("v1 bytes changed", err)
	}
}

func TestAnalysisLearningVersionTwoPreservesBodyAndPinsProcessing(t *testing.T) {
	m := learningMaterialFixture(t)
	task := learningTaskFixture(m)
	before, _ := json.Marshal(m)
	legacy, err := assembleAnalysisLearning(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := assembleAnalysisLearningVersion(m, task, "2")
	if err != nil {
		t.Fatal(err)
	}
	again, err := assembleAnalysisLearningVersion(m, task, "2")
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("v2 nondeterministic", err)
	}
	after, _ := json.Marshal(m)
	if !bytes.Equal(before, after) {
		t.Fatal("native material mutated")
	}
	old, _ := ValidateLearningContent(legacy, nil)
	doc, err := ValidateLearningContent(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"sources", "results", "relations", "coverage", "uses"} {
		if learningJSONKey(old.Document[field]) != learningJSONKey(doc.Document[field]) {
			t.Fatalf("%s changed", field)
		}
	}
	snapshot := objectOf(doc.Document["processing_snapshot"])
	hash, _ := digest(task.Plan)
	if str(snapshot["task_id"]) != task.TaskID || str(snapshot["plan_sha256"]) != hash || str(snapshot["assembly_version"]) != "2" || str(snapshot["adapter_revision"]) != learningCurationAdapterRevision {
		t.Fatal("processing snapshot not pinned")
	}
	curation := objectOf(doc.Document["curation"])
	if len(listOf(curation["assessments"])) != len(doc.Results) {
		t.Fatal("results omitted")
	}
	previews := 0
	for _, item := range listOf(curation["assessments"]) {
		a := objectOf(item)
		if str(a["selection"]) != "unassessed" {
			t.Fatal("analysis marked as human recommendation")
		}
		for _, item := range listOf(a["previews"]) {
			p := objectOf(item)
			previews++
			if str(p["role"]) != "original_reference" || str(p["sha256"]) != m.Bundle.Source.SHA256 || p["run_ref"] != nil {
				t.Fatal("fake generated preview")
			}
		}
	}
	if previews == 0 {
		t.Fatal("missing original references")
	}
	if len(doc.RunRefs) != 1 || doc.RunRefs[0] != old.RunRefs[0] {
		t.Fatal("native task identity changed")
	}
}

func TestAnalysisLearningVersionTwoRejectsTaskOrSourceSubstitution(t *testing.T) {
	m := learningMaterialFixture(t)
	for _, field := range []string{"task", "draft", "revision", "file", "source", "pipeline", "typed"} {
		t.Run(field, func(t *testing.T) {
			task := learningTaskFixture(m)
			switch field {
			case "task":
				task.TaskID = testID(90)
			case "draft":
				task.DraftID = testID(90)
			case "revision":
				task.RevisionID = testID(90)
			case "file":
				task.FileID = testID(90)
			case "source":
				task.Plan.Source.DurationMS++
			case "pipeline":
				task.PipelineVersion = 0
			case "typed":
				task.IncludeTypedResults = false
			}
			if _, err := assembleAnalysisLearningVersion(m, task, "2"); err == nil {
				t.Fatal("substituted task accepted")
			}
		})
	}
	if _, err := assembleAnalysisLearningVersion(m, learningTaskFixture(m), "3"); err == nil {
		t.Fatal("unknown assembler version accepted")
	}
}

func TestAudioLearningUsesAudioOriginalAndNoVisualCoverage(t *testing.T) {
	// Synthetic formatter input; actual source access and media decoding are
	// independently checked by the admission and media integration suites.
	m := learningMaterialFixture(t)
	m.Bundle.Source.Kind = "audio"
	m.Bundle.Receipts = m.Bundle.Receipts[1:]
	m.Prompts = nil
	var results []analysis.ResultDraft
	for _, r := range m.Results {
		if r.Type == "sound_cue" || r.Type == "music_structure" {
			results = append(results, r)
		}
	}
	m.Results = results
	for i := range m.Bundle.Nodes {
		m.Bundle.Nodes[i].Kind = "music"
	}
	raw, err := assembleAnalysisLearningVersion(m, learningTaskFixture(m), "2")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ValidateLearningContent(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if str(objectOf(doc.Sources["original"])["media_kind"]) != "audio" {
		t.Fatal("audio source mislabeled as video")
	}
	found := false
	for _, item := range listOf(doc.Document["coverage"]) {
		c := objectOf(item)
		if str(c["dimension"]) == "visual_observations" {
			found = true
			if str(c["state"]) != "not_applicable" || len(listOf(c["covered_scopes"])) != 0 {
				t.Fatal("fabricated visual coverage")
			}
		}
	}
	if !found {
		t.Fatal("missing explicit visual applicability")
	}
	count := 0
	for _, item := range listOf(objectOf(doc.Document["curation"])["assessments"]) {
		for _, preview := range listOf(objectOf(item)["previews"]) {
			count++
			if str(objectOf(preview)["media_kind"]) != "audio" {
				t.Fatal("audio preview mislabeled")
			}
		}
	}
	if count == 0 {
		t.Fatal("no audio reference previews")
	}
}
