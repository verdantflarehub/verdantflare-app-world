package analysis

import (
	"encoding/json"
	"strings"
	"testing"
)

func qualityFixture(t *testing.T) (Task, Bundle, Association, AssociationRecord, []ResultRecord, []ResultDraft) {
	t.Helper()
	task, b, ar, _, _ := learningFixture(t)
	task.PipelineVersion = 2
	a, err := VerifyAssociationNative(b, ar.Request, ar.CallID, ar.Result)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanResultsVersion(b, a, 2)
	if err != nil {
		t.Fatal(err)
	}
	records := []ResultRecord{}
	drafts := []ResultDraft{}
	for _, batch := range plan {
		f, call, native := resultNative(t, b, a, batch)
		raw := promptBytes(t, native)
		ds, err := ResultDraftsFromNative(b, a, f, call, raw)
		if err != nil {
			t.Fatal(batch.Type, err)
		}
		drafts = append(drafts, ds...)
		records = append(records, ResultRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: f, RequestHash: f.Digest(), Revision: 4, State: "result_ready", CallID: call, Result: raw})
	}
	return task, b, a, ar, records, drafts
}

func TestQualityPipelineUsesTypedResultsAndCompatiblePurposes(t *testing.T) {
	task, b, a, ar, results, drafts := qualityFixture(t)
	task.PromptTargets = append(task.PromptTargets, PromptTarget{Key: "text", RequestedLabel: "ChatGPT", TaskType: "text", InputMode: "messages", Guidance: "Adapt the whole story"}, PromptTarget{Key: "image", RequestedLabel: "image", TaskType: "image", InputMode: "text-to-image", Guidance: "Static reference"})
	plan, omissions, hash, err := PlanDerivedPrompts(b, a, drafts, task.PromptTargets)
	if err != nil || len(plan) == 0 || len(omissions) == 0 || !digestPattern.MatchString(hash) {
		t.Fatal(err)
	}
	byKey := map[string]ResultDraft{}
	for _, r := range drafts {
		byKey[r.Key] = r
	}
	count := 0
	records := []PromptRecord{}
	for _, batch := range plan {
		for _, pair := range batch.Pairs {
			count++
			if (pair.Target == "h3" || pair.Target == "sd") && byKey[pair.Scope].Type != "shot" {
				t.Fatal("nonvisual object sent to video")
			}
		}
		f, call, native := promptNative(t, b, a, batch)
		var p associationPayload
		_ = json.Unmarshal(f.Payload, &p)
		if !strings.Contains(p.Text, "typed_results") || !strings.Contains(p.Text, drafts[0].Title) {
			t.Fatal("missing concrete results")
		}
		records = append(records, PromptRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: f, RequestHash: f.Digest(), Revision: 4, State: "prompt_ready", CallID: call, Result: promptBytes(t, native)})
	}
	if count+len(omissions) != len(drafts)*len(task.PromptTargets) {
		t.Fatal("silent omission")
	}
	m, err := AssembleLearningMaterial(task, b, ar, records, results)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.PromptOmissions) != len(omissions) {
		t.Fatal("lost applicability reasons")
	}
	if _, err = AssembleLearningMaterial(task, b, ar, records, results[:len(results)-1]); err == nil {
		t.Fatal("partial results accepted")
	}
	altered := append([]ResultDraft{}, drafts...)
	altered[0].Title = "changed after prompt was frozen"
	changed, _, _, _ := PlanDerivedPrompts(b, a, altered, task.PromptTargets)
	if canonicalHash(changed) == canonicalHash(plan) {
		t.Fatal("typed result changes not pinned")
	}
}

func TestQualityEmptyAdaptationAndUnresolvedModelsStayHonest(t *testing.T) {
	task, b, a, ar, results, drafts := qualityFixture(t)
	task.PromptTargets = []PromptTarget{{Key: "unsupported", RequestedLabel: "Unknown", TaskType: "unsupported", InputMode: "unknown", Guidance: "Unresolved input"}}
	plan, omissions, hash, err := PlanDerivedPrompts(b, a, drafts, task.PromptTargets)
	if err != nil || len(plan) != 0 || len(omissions) != len(drafts) || hash == "" {
		t.Fatal(err)
	}
	m, err := AssembleLearningMaterial(task, b, ar, nil, results)
	if err != nil || len(m.Prompts) != 0 || len(m.PromptOmissions) == 0 {
		t.Fatal(err)
	}
	task.PromptTargets[0].TaskType = "video"
	plan, _, _, err = PlanDerivedPrompts(b, a, drafts, task.PromptTargets)
	if err != nil || len(plan) == 0 {
		t.Fatal(err)
	}
	f, call, n := promptNative(t, b, a, plan[0])
	ds, err := PromptDraftsFromNative(b, a, f, call, promptBytes(t, n))
	if err != nil {
		t.Fatal(err)
	}
	v := ds[0].Payload["variants"].([]any)[0].(map[string]any)
	if v["compatibility"] != "unresolved" || len(v["execution_evidence"].([]any)) != 0 {
		t.Fatal("fabricated model/sample")
	}
}

func TestQualityRejectsPlaceholdersAndUnboundStyleRules(t *testing.T) {
	_, b, a, _, _, _ := qualityFixture(t)
	plan, _ := PlanResultsVersion(b, a, 2)
	for _, batch := range plan {
		out := resultOutput(b, batch)
		if batch.Type == "character" {
			out.Items[0].Payloads[0]["costume"] = "原片镜头内可见的服装款式、色彩与配饰特征"
			if _, err := ValidateResults(b, a, batch, promptBytes(t, out)); err == nil {
				t.Fatal("placeholder passed")
			}
		}
		if batch.Type == "style" {
			out.Items[0].RuleEvidence = []RuleEvidence{}
			if _, err := ValidateResults(b, a, batch, promptBytes(t, out)); err == nil {
				t.Fatal("missing per-rule evidence")
			}
			out.Items[0].RuleEvidence = []RuleEvidence{{RuleKey: "pause", Evidence: []string{"forged"}}}
			if _, err := ValidateResults(b, a, batch, promptBytes(t, out)); err == nil {
				t.Fatal("foreign evidence passed")
			}
		}
	}
	legacy, _ := PlanResults(b, a)
	for _, batch := range legacy {
		f, call, n := resultNative(t, b, a, batch)
		if _, err := VerifyResultsNative(b, a, f, call, promptBytes(t, n)); err != nil {
			t.Fatal("legacy broke", err)
		}
	}
}

func TestStyleRuleEvidenceIsIndependentAndStrict(t *testing.T) {
	_, b, a, _, _, _ := qualityFixture(t)
	plan, _ := PlanResultsVersion(b, a, 2)
	for _, batch := range plan {
		if batch.Type != "style" {
			continue
		}
		out := resultOutput(b, batch)
		root := out.Items[0].Evidence
		var independent, uncertainty string
		for _, n := range b.Nodes {
			if n.Kind == "uncertainties" {
				uncertainty = n.Key
				continue
			}
			present := false
			for _, k := range root {
				if k == n.Key {
					present = true
				}
			}
			if !present {
				independent = n.Key
			}
		}
		if independent == "" {
			t.Fatal("fixture needs independent evidence")
		}
		out.Items[0].RuleEvidence = []RuleEvidence{{RuleKey: "pause", Evidence: []string{independent}}}
		if _, err := ValidateResults(b, a, batch, promptBytes(t, out)); err != nil {
			t.Fatal("valid nested evidence rejected", err)
		}
		for _, bad := range [][]string{{"unknown"}, {independent, independent}, {uncertainty}} {
			out.Items[0].RuleEvidence[0].Evidence = bad
			if _, err := ValidateResults(b, a, batch, promptBytes(t, out)); err == nil {
				t.Fatal("invalid evidence accepted", bad)
			}
		}
	}
}
func TestResultValidationReceiptBoundToNative(t *testing.T) {
	_, _, _, _, records, _ := qualityFixture(t)
	for _, r := range records {
		if r.Request.Batch.Type != "style" {
			continue
		}
		r.Validation = &ResultValidation{ID: execOwner, Policy: "style-evidence-v1", NativeHash: hashBytes(r.Result), BatchKey: r.Request.Batch.Key}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		r.Validation.NativeHash = "incorrect"
		if r.Validate() == nil {
			t.Fatal("mismatched native hash accepted")
		}
	}
}
