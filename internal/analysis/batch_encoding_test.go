package analysis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func expandBatchSource(t *testing.T, b Bundle, a Association, margin int) (Bundle, Association) {
	text, err := AssociationText(b)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	json.Unmarshal(b.Nodes[0].Value, &v)
	v["synthetic_context"] = strings.Repeat("x", MaxAssociationCharacters-margin-len(text))
	b.Nodes[0].Value = promptBytes(t, v)
	b.SHA256 = ""
	b.SHA256 = canonicalHash(b)
	a.BundleHash = b.SHA256
	return b, a
}
func encodedNativeOutput(t *testing.T, b Bundle, native map[string]any) {
	a := native["analysis"].(map[string]any)
	var value any
	json.Unmarshal([]byte(a["text"].(string)), &value)
	aliases, _ := associationAliases(b)
	if err := translateEvidence(value, aliases, false); err != nil {
		t.Fatal(err)
	}
	a["document"] = value
	a["text"] = string(promptBytes(t, value))
}
func TestLargeResultBatchKeepsCompleteEvidenceAndNativeBinding(t *testing.T) {
	b, a, _ := resultFixture(t)
	b, a = expandBatchSource(t, b, a, 500)
	plan, err := PlanResultsVersion(b, a, 2)
	if err != nil {
		t.Fatal(err)
	}
	f, call, native := resultNative(t, b, a, plan[0])
	if f.Encoding != "short-v1" {
		t.Fatal("large request not encoded")
	}
	encodedNativeOutput(t, b, native)
	out, err := VerifyResultsNative(b, a, f, call, promptBytes(t, native))
	if err != nil || !reflect.DeepEqual(out, resultOutput(b, plan[0])) {
		t.Fatal("fixed results changed", err)
	}
	var p associationPayload
	json.Unmarshal(f.Payload, &p)
	var input struct {
		Observations struct {
			Nodes []any `json:"nodes"`
		} `json:"observations"`
	}
	json.Unmarshal([]byte(p.Text), &input)
	if len(input.Observations.Nodes) != len(b.Nodes) {
		t.Fatal("observations omitted")
	}
	p.Text = strings.Replace(p.Text, "xxxxxxxx", "yyyyyyyy", 1)
	f.Payload = promptBytes(t, p)
	f.TextHash = hashBytes([]byte(p.Text))
	if _, err := VerifyResultsNative(b, a, f, call, promptBytes(t, native)); err == nil {
		t.Fatal("mutated context accepted")
	}
}
func TestLargePromptBatchPinsFullPlanAndUsesOnlyItsTypedResults(t *testing.T) {
	task, b, a, _, _, drafts := qualityFixture(t)
	b, a = expandBatchSource(t, b, a, 8000)
	task.PromptTargets = append(task.PromptTargets, PromptTarget{Key: "text", RequestedLabel: "text", TaskType: "text", InputMode: "chat", Guidance: "whole story"})
	targets := append(task.PromptTargets, PromptTarget{Key: "image", RequestedLabel: "image", TaskType: "image", InputMode: "text", Guidance: "static image"})
	plan, _, _, err := PlanDerivedPrompts(b, a, drafts, targets)
	if err != nil || len(plan) < 2 {
		t.Fatal("plan", err)
	}
	for _, batch := range plan {
		f, call, native := promptNative(t, b, a, batch)
		if f.Encoding != "short-v1" {
			t.Fatal("large prompt not encoded")
		}
		if !reflect.DeepEqual(f.Batch.Results, drafts) {
			t.Fatal("frozen complete result plan changed")
		}
		var p associationPayload
		json.Unmarshal(f.Payload, &p)
		var input struct {
			Batch        PromptBatch `json:"batch"`
			Observations struct {
				Nodes []any `json:"nodes"`
			} `json:"observations"`
		}
		json.Unmarshal([]byte(p.Text), &input)
		wanted := map[string]bool{}
		for _, pair := range batch.Pairs {
			wanted[pair.Scope] = true
		}
		if len(input.Observations.Nodes) != len(b.Nodes) || len(input.Batch.Results) != len(wanted) || len(input.Batch.Scopes) != len(wanted) {
			t.Fatal("lost scope or observations")
		}
		for _, r := range input.Batch.Results {
			if !wanted[r.Key] {
				t.Fatal("unrelated result included")
			}
		}
		encodedNativeOutput(t, b, native)
		out, err := VerifyPromptsNative(b, a, f, call, promptBytes(t, native))
		if err != nil || !reflect.DeepEqual(out, promptOutput(batch)) {
			t.Fatal("prompt evidence changed", err)
		}
	}
}
