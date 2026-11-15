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
