package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const sourceHash = "804fc28b37d0628f52ae913e454af31d1d7e3b52ee305690b9011fe11b20f5db"

func plan(t *testing.T) Plan {
	t.Helper()
	p, err := PlanVideo(Source{SHA256: sourceHash, DurationMS: 345002, HasAudio: true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWholeSourceObservationPlanIsStableAndDoesNotCreateProjects(t *testing.T) {
	p := plan(t)
	if len(p.Steps) != 14 || !reflect.DeepEqual(p, plan(t)) {
		t.Fatal("expected eight visual and six audio steps, deterministically")
	}
	for _, kind := range []string{"visual", "audio"} {
		end := int64(0)
		for _, step := range p.Steps {
			if step.Kind != kind {
				continue
			}
			if step.Validate() != nil || step.Core.StartMS != end {
				t.Fatal("core ranges must cover original time without gaps")
			}
			end = step.Core.EndMS
		}
		if end != 345002 {
			t.Fatal("last interval must retain actual final milliseconds")
		}
	}
	if p.Steps[1].Input != (Range{44000, 91000}) {
		t.Fatal("visual step needs boundary context")
	}
	encoded, _ := json.Marshal(p)
	if strings.Contains(string(encoded), "project") {
		t.Fatal("independent observation plan must not require a project")
	}
}

func TestNoAudioNoInventedAudioTasksAndInvalidSourcesRejected(t *testing.T) {
	for _, duration := range []int64{1, 45000, 45001} {
		p, err := PlanVideo(Source{SHA256: sourceHash, DurationMS: duration})
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range p.Steps {
			if step.Kind != "visual" || step.Validate() != nil {
				t.Fatal("silent source has only bounded visual observations")
			}
		}
	}
	for _, source := range []Source{{SHA256: "bad", DurationMS: 1}, {SHA256: sourceHash}, {SHA256: sourceHash, DurationMS: 86400001}} {
		if _, err := PlanVideo(source); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
}

func TestPayloadPinsOriginalRangeAndSchemaWithoutMediaOrProject(t *testing.T) {
	p := plan(t)
	for _, step := range p.Steps {
		payload, err := step.Payload()
		if err != nil {
			t.Fatal(err)
		}
		schema := payload["response_schema"].(map[string]any)
		bounds := schema["properties"].(map[string]any)
		if !reflect.DeepEqual(bounds["start_ms"].(map[string]any)["enum"], []int64{step.Core.StartMS}) {
			t.Fatal("output range not pinned")
		}
		if step.Kind == "visual" && payload["video_range"] != step.Input || step.Kind == "audio" && payload["video_range"] != nil {
			t.Fatal("wrong native modality/range")
		}
		if _, exists := payload["media"]; exists {
			t.Fatal("media must come from authorized reader")
		}
	}
	bad := p.Steps[0]
	bad.Input.EndMS = bad.DurationMS + 1
	if _, err := bad.Payload(); err == nil {
		t.Fatal("out-of-source range accepted")
	}
}

func visualFixture() map[string]any {
	findings := []any{}
	for _, dim := range []string{"narrative", "character", "scene", "prop", "cinematography", "editing", "graphics", "style"} {
		findings = append(findings, map[string]any{"dimension": dim, "description": "合成测试观察，不代表原片事实。", "shot_keys": []any{"s1"}, "basis": "observed"})
	}
	return map[string]any{"start_ms": 0, "end_ms": 45000,
		"shots": []any{map[string]any{"key": "s1", "start_ms": 0, "end_ms": 45000,
			"continues_before": false, "continues_after": true, "composition": "合成测试：全景", "camera_motion": "固定",
			"subject_motion": "合成主体移动", "graphic_motion": "无", "lighting": "合成照明",
			"entity_keys": []any{"c1"}, "beats": []any{map[string]any{"start_ms": 0, "end_ms": 45000, "description": "合成动作"}}, "visible_text": []any{}}},
		"entities": []any{map[string]any{"key": "c1", "kind": "character", "name": "合成人物", "description": "单元测试", "basis": "observed"}},
		"findings": findings, "uncertainties": []any{}}
}

func audioFixture() map[string]any {
	return map[string]any{"start_ms": 0, "end_ms": 60000,
		"intervals": []any{map[string]any{"start_ms": 0, "end_ms": 60000, "kind": "speech", "description": "合成听写测试"}},
		"speech":    []any{map[string]any{"start_ms": 0, "end_ms": 60000, "speaker_key": "voice1", "text": "测试句子", "language": "中文", "intelligibility": "clear"}},
		"speakers":  []any{map[string]any{"key": "voice1", "description": "合成测试说话者"}}, "music": []any{}, "sound_effects": []any{}, "uncertainties": []any{}}
}

func attested(t *testing.T, step Step, doc any) (Prepared, Receipt, []byte) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	prepared := Prepared{SourceHash: sourceHash, MediaHash: sourceHash, MIME: "video/mp4", Range: step.Input}
	if step.Kind == "audio" {
		prepared.MediaHash, prepared.MIME = strings.Repeat("a", 64), "audio/wav"
	}
	schemaHash, err := step.SchemaHash()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	receipt := Receipt{CallID: "10000000-0000-4000-8000-000000000001", State: "completed", Accepted: true,
		Integrity: true, SchemaVerified: true, MediaHash: prepared.MediaHash, MIME: prepared.MIME,
		SchemaHash: schemaHash, OutputHash: hex.EncodeToString(digest[:])}
	if step.Kind == "visual" {
		receipt.VideoRange = &step.Input
	}
	return prepared, receipt, data
}

func hasIssue(report Report, code string) bool {
	for _, issue := range report.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestVisualRequiresNativeEvidenceCompleteTimelineAndResolvableReferences(t *testing.T) {
	step := plan(t).Steps[0]
	p, receipt, data := attested(t, step, visualFixture())
	if report := Verify(step, p, receipt, data); !report.Passed {
		t.Fatalf("valid synthetic observation: %+v", report)
	}
	for _, test := range []struct {
		name, code string
		mutate     func(map[string]any)
	}{
		{"summarized_range", "timeline_gap", func(d map[string]any) { d["shots"].([]any)[0].(map[string]any)["start_ms"] = 1000 }},
		{"end_outside_source", "range_outside_source", func(d map[string]any) { d["shots"].([]any)[0].(map[string]any)["end_ms"] = 350000 }},
		{"partial_end", "timeline_incomplete", func(d map[string]any) { d["shots"].([]any)[0].(map[string]any)["end_ms"] = 30000 }},
		{"dangling_entity", "unresolved_reference", func(d map[string]any) { d["entities"] = []any{} }},
		{"dangling_shot", "unresolved_reference", func(d map[string]any) { d["findings"].([]any)[0].(map[string]any)["shot_keys"] = []any{"missing"} }},
		{"missing_dimensions", "missing_dimension", func(d map[string]any) { d["findings"] = d["findings"].([]any)[:4] }},
		{"empty_composition", "empty_observation", func(d map[string]any) { d["shots"].([]any)[0].(map[string]any)["composition"] = " " }},
		{"fabricated_previous_frame", "invalid_continuation", func(d map[string]any) { d["shots"].([]any)[0].(map[string]any)["continues_before"] = true }},
		{"local_not_original_time", "output_scope_mismatch", func(d map[string]any) { d["start_ms"] = 1000 }},
		{"overlapping_shots", "timeline_overlap", func(d map[string]any) { d["shots"] = append(d["shots"].([]any), d["shots"].([]any)[0]) }},
		{"empty_beats", "invalid_schema", func(d map[string]any) { d["shots"].([]any)[0].(map[string]any)["beats"] = []any{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := visualFixture()
			test.mutate(doc)
			p, receipt, data := attested(t, step, doc)
			original := append([]byte{}, data...)
			report := Verify(step, p, receipt, data)
			if report.Passed || !hasIssue(report, test.code) || !reflect.DeepEqual(data, original) {
				t.Fatalf("must reject without silently repairing source: %+v", report)
			}
		})
	}
}

func TestClientClaimsOrWrongNativeCallCannotAttestOutput(t *testing.T) {
	step := plan(t).Steps[0]
	for _, test := range []struct {
		name, code string
		mutate     func(*Prepared, *Receipt)
	}{
		{"unknown", "native_call_unverified", func(_ *Prepared, r *Receipt) { r.State = "acceptance_unknown" }},
		{"no_integrity", "native_call_unverified", func(_ *Prepared, r *Receipt) { r.Integrity = false }},
		{"missing_call", "native_call_unverified", func(_ *Prepared, r *Receipt) { r.CallID = "client-text" }},
		{"wrong_file", "native_input_mismatch", func(_ *Prepared, r *Receipt) { r.MediaHash = strings.Repeat("b", 64) }},
		{"wrong_source", "source_mismatch", func(p *Prepared, _ *Receipt) { p.SourceHash = strings.Repeat("b", 64) }},
		{"wrong_slice", "native_range_mismatch", func(_ *Prepared, r *Receipt) { r.VideoRange = &Range{0, 30000} }},
		{"no_schema", "native_schema_mismatch", func(_ *Prepared, r *Receipt) { r.SchemaVerified = false }},
		{"changed_output", "native_output_mismatch", func(_ *Prepared, r *Receipt) { r.OutputHash = strings.Repeat("b", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, receipt, data := attested(t, step, visualFixture())
			test.mutate(&p, &receipt)
			report := Verify(step, p, receipt, data)
			if report.Passed || !hasIssue(report, test.code) {
				t.Fatalf("native proof bypass: %+v", report)
			}
		})
	}
}

func TestVisibleTextMaySpanCutsButMustOverlapItsShotWithinCore(t *testing.T) {
	step := plan(t).Steps[0]
	for _, tc := range []struct {
		name       string
		start, end int64
		code       string
	}{
		{"cross_cut", 250, 1500, ""},
		{"within_shot", 1000, 1500, ""},
		{"ends_at_cut_without_overlap", 250, 1000, "text_outside_shot"},
		{"unrelated_earlier_text", 100, 500, "text_outside_shot"},
		{"outside_core", 1000, 46000, "range_outside_source"},
		{"zero_length", 1500, 1500, "range_outside_source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := visualFixture()
			first := doc["shots"].([]any)[0].(map[string]any)
			second := visualFixture()["shots"].([]any)[0].(map[string]any)
			first["end_ms"], first["continues_after"] = 1000, false
			first["beats"].([]any)[0].(map[string]any)["end_ms"] = 1000
			second["key"], second["start_ms"] = "s2", 1000
			second["beats"].([]any)[0].(map[string]any)["start_ms"] = 1000
			second["visible_text"] = []any{map[string]any{"start_ms": tc.start, "end_ms": tc.end, "text": "test subtitle"}}
			doc["shots"] = []any{first, second}
			p, receipt, data := attested(t, step, doc)
			before := append([]byte(nil), data...)
			report := Verify(step, p, receipt, data)
			if report.Passed != (tc.code == "") || tc.code != "" && !hasIssue(report, tc.code) {
				t.Fatalf("unexpected subtitle verification: %+v", report)
			}
			if !reflect.DeepEqual(data, before) {
				t.Fatal("verification changed native subtitle timestamps")
			}
		})
	}
}

func TestAudioRequiresIndependentAudioAndConsistentLayers(t *testing.T) {
	step := plan(t).Steps[8]
	p, receipt, data := attested(t, step, audioFixture())
	if report := Verify(step, p, receipt, data); !report.Passed {
		t.Fatalf("valid synthetic audio observation: %+v", report)
	}
	p.MIME, receipt.MIME = "video/mp4", "video/mp4"
	if report := Verify(step, p, receipt, data); report.Passed || !hasIssue(report, "wrong_modality") {
		t.Fatal("video/subtitle input accepted as independent audio evidence")
	}
	for _, test := range []struct {
		name, code string
		mutate     func(map[string]any)
	}{
		{"no_transcript", "missing_speech", func(d map[string]any) { d["speech"] = []any{} }},
		{"empty_mix", "missing_mixed_layers", func(d map[string]any) { d["intervals"].([]any)[0].(map[string]any)["kind"] = "mixed" }},
		{"silent_speech", "audio_layer_conflict", func(d map[string]any) { d["intervals"].([]any)[0].(map[string]any)["kind"] = "silence" }},
		{"unknown_speaker", "unresolved_reference", func(d map[string]any) { d["speakers"] = []any{} }},
		{"gap", "timeline_gap", func(d map[string]any) { d["intervals"].([]any)[0].(map[string]any)["start_ms"] = 1000 }},
		{"invalid_timestamp", "invalid_schema", func(d map[string]any) { d["speech"].([]any)[0].(map[string]any)["start_ms"] = "2:80" }},
		{"out_of_slice", "range_outside_source", func(d map[string]any) { d["speech"].([]any)[0].(map[string]any)["end_ms"] = 61000 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := audioFixture()
			test.mutate(doc)
			p, receipt, data := attested(t, step, doc)
			report := Verify(step, p, receipt, data)
			if report.Passed || !hasIssue(report, test.code) {
				t.Fatalf("invalid audio accepted: %+v", report)
			}
		})
	}
}

func TestGenuineSilenceAndExplicitUnintelligibilityDoNotFabricateWords(t *testing.T) {
	step := plan(t).Steps[8]
	for _, kind := range []string{"silence", "unintelligible"} {
		doc := audioFixture()
		if kind == "silence" {
			doc["speech"], doc["speakers"] = []any{}, []any{}
			doc["intervals"].([]any)[0].(map[string]any)["kind"] = "silence"
		} else {
			speech := doc["speech"].([]any)[0].(map[string]any)
			speech["text"], speech["intelligibility"] = "", "unintelligible"
		}
		p, receipt, data := attested(t, step, doc)
		if report := Verify(step, p, receipt, data); !report.Passed {
			t.Fatalf("valid qualified observation rejected: %+v", report)
		}
	}
}

func TestNativeResultMustMatchOriginalStepCallTaskModelAndText(t *testing.T) {
	step := plan(t).Steps[0]
	prepared, receipt, text := attested(t, step, visualFixture())
	binding := Binding{CallID: receipt.CallID, TaskID: "synthetic-world-task", ModelID: "synthetic-model"}
	fixture := func() map[string]any {
		return map[string]any{"schema_version": 1, "call_id": binding.CallID, "task_id": binding.TaskID, "model_id": binding.ModelID,
			"media": "analysis", "operation": "analyze", "state": "completed", "accepted": true,
			"analysis": map[string]any{"model_id": binding.ModelID, "finish_reason": "STOP", "text": string(text), "document": json.RawMessage(text), "response_schema_verified": true,
				"input": map[string]any{"media": map[string]any{"mime_type": prepared.MIME, "sha256": prepared.MediaHash}, "video_range": step.Input,
					"provider_file_integrity_verified": true, "response_schema_sha256": receipt.SchemaHash}}}
	}
	data, _ := json.Marshal(fixture())
	if report := VerifyNative(step, prepared, binding, data); !report.Passed {
		t.Fatalf("valid synthetic native envelope: %+v", report)
	}
	for _, field := range []string{"call_id", "task_id", "model_id", "media", "operation"} {
		value := fixture()
		value[field] = "different"
		data, _ := json.Marshal(value)
		if report := VerifyNative(step, prepared, binding, data); report.Passed || !hasIssue(report, "native_binding_mismatch") {
			t.Fatalf("accepted mismatched %s: %+v", field, report)
		}
	}
	value := fixture()
	value["analysis"].(map[string]any)["document"] = map[string]any{"different": "result"}
	data, _ = json.Marshal(value)
	if report := VerifyNative(step, prepared, binding, data); report.Passed || !hasIssue(report, "native_document_mismatch") {
		t.Fatalf("native text/document mismatch accepted: %+v", report)
	}
}
