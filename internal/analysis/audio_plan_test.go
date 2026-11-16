package analysis

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAudioPlanCoversOnlyAudioAndRetainsLegacyVideoBytes(t *testing.T) {
	for _, duration := range []int64{1, 60000, 60001, 345002, 86400000} {
		p, err := PlanSource(Source{Kind: "audio", SHA256: sourceHash, DurationMS: duration, HasAudio: true})
		if err != nil || p.Version != 2 {
			t.Fatal(p, err)
		}
		end := int64(0)
		for _, s := range p.Steps {
			if s.Kind != "audio" || s.Validate() != nil || s.Core.StartMS != end || s.Input != s.Core {
				t.Fatal("audio coverage or modality changed", s)
			}
			end = s.Core.EndMS
		}
		if end != duration {
			t.Fatal("incomplete audio coverage")
		}
	}
	legacy, err := PlanSource(Source{SHA256: sourceHash, DurationMS: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(legacy)
	want := fmt.Sprintf(`{"version":1,"source":{"sha256":"%s","duration_ms":1,"has_audio":false},"steps":[{"key":"visual-0-1","kind":"visual","source_sha256":"%s","source_duration_ms":1,"core":{"start_ms":0,"end_ms":1},"input":{"start_ms":0,"end_ms":1}}]}`, sourceHash, sourceHash)
	if string(raw) != want {
		t.Fatalf("legacy frozen bytes changed: %s", raw)
	}
	for _, s := range []Source{
		{Kind: "audio", SHA256: sourceHash, DurationMS: 1},
		{Kind: "unknown", SHA256: sourceHash, DurationMS: 1, HasAudio: true},
	} {
		if _, err := PlanSource(s); err == nil {
			t.Fatal("invalid source accepted", s)
		}
	}
	if _, err := PlanVideo(Source{Kind: "audio", SHA256: sourceHash, DurationMS: 1, HasAudio: true}); err == nil {
		t.Fatal("video planner accepted audio source")
	}
}

func audioBundleFixture(t *testing.T) (Task, []StepRecord) {
	// Synthetic observation receipts exercise recovery, not real media acceptance.
	task, mixed := bundleFixture(t)
	source := task.Plan.Source
	source.Kind = "audio"
	var err error
	task.Plan, err = PlanSource(source)
	if err != nil {
		t.Fatal(err)
	}
	rows := []StepRecord{}
	for _, row := range mixed {
		if row.Request.Step.Kind == "audio" {
			rows = append(rows, row)
		}
	}
	return task, rows
}

func TestAudioBundleRestoresWithoutVisualStepsAndRejectsTampering(t *testing.T) {
	task, rows := audioBundleFixture(t)
	b, err := Assemble(task, rows)
	if err != nil || checkBundle(b) != nil || len(b.Boundaries) != 0 || len(b.Receipts) != 2 {
		t.Fatal("audio bundle recovery failed", err)
	}
	bad := b
	bad.Source.Kind = ""
	if checkBundle(bad) == nil {
		t.Fatal("source tampering retained valid digest")
	}
	bad.SHA256 = ""
	bad.SHA256 = canonicalHash(bad)
	if checkBundle(bad) == nil {
		t.Fatal("video source accepted without visual receipts")
	}
	task.Plan.Version = 1
	if _, err := Assemble(task, rows); err == nil {
		t.Fatal("audio plan accepted with legacy version")
	}
	task.Plan.Version = 2
	task.Plan.Steps = append(task.Plan.Steps, Step{Kind: "visual"})
	if _, err := Assemble(task, rows); err == nil {
		t.Fatal("audio plan accepted extra visual receipts")
	}
}

func TestAudioAssociationAndResultPlanRejectVisualFabrication(t *testing.T) {
	task, rows := audioBundleFixture(t)
	b, err := Assemble(task, rows)
	if err != nil {
		t.Fatal(err)
	}
	a := associationForBundle(b)
	raw, _ := json.Marshal(a)
	if _, err := ValidateAssociation(b, raw); err != nil {
		t.Fatal(err)
	}
	f, err := FreezeAssociation(b, "gemini-3.1-pro-preview", "", "1.00")
	if err != nil || f.Validate() != nil {
		t.Fatal("audio request rejected", err)
	}
	nativeRequest, call, native := associationNativeFixture(t, b, a)
	nativeBytes, _ := json.Marshal(native)
	if _, err := VerifyAssociationNative(b, nativeRequest, call, nativeBytes); err != nil {
		t.Fatal("audio native observation association rejected", err)
	}
	var payload associationPayload
	json.Unmarshal(f.Payload, &payload)
	payload.Schema, _ = associationSchema()
	bad := f
	bad.Payload, _ = json.Marshal(payload)
	if bad.Validate() == nil {
		t.Fatal("audio source accepted video schema")
	}
	batches, err := PlanResultsVersion(b, a, 2)
	if err != nil || len(batches) == 0 {
		t.Fatal("audio result planning failed", err)
	}
	for _, batch := range batches {
		switch batch.Type {
		case "character", "scene", "prop", "shot":
			t.Fatal("fabricated visual result", batch.Type)
		}
	}
	a.Shots = []ShotGroup{{Key: "fake", Members: []string{b.Nodes[0].Key}, StartMS: 0, EndMS: 1, State: "single", Evidence: []string{b.Nodes[0].Key}, Reason: "invented"}}
	raw, _ = json.Marshal(a)
	if _, err := ValidateAssociation(b, raw); err == nil {
		t.Fatal("audio accepted fabricated shot")
	}
	video, va := associationFixture(t)
	va.Shots = []ShotGroup{}
	raw, _ = json.Marshal(va)
	if _, err := ValidateAssociation(video, raw); err == nil {
		t.Fatal("video lost mandatory shot coverage")
	}
}
