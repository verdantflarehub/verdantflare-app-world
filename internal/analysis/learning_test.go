package analysis

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func learningFixture(t *testing.T) (Task, Bundle, AssociationRecord, []PromptRecord, []ResultRecord) {
	t.Helper()
	b, a, resultPlan := resultFixture(t)
	plan, err := PlanVideo(b.Source)
	if err != nil {
		t.Fatal(err)
	}
	task := Task{OrganizationID: uuid.NewString(), OwnerID: uuid.NewString(), TaskID: b.TaskID, DraftID: b.DraftID, RevisionID: b.RevisionID, FileID: b.FileID, Plan: plan, IncludeTypedResults: true,
		PromptTargets: []PromptTarget{{Key: "h3", RequestedLabel: "h3", TaskType: "video", InputMode: "text", Guidance: "Separate camera and subject motion"}, {Key: "sd", RequestedLabel: "sd2.5", TaskType: "video", InputMode: "reference", Guidance: "Retain missing reference requirements"}}}
	af, call, native := associationNativeFixture(t, b, a)
	ar := AssociationRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: af, RequestHash: af.Digest(), Revision: 4, State: "association_ready", CallID: call, Result: promptBytes(t, native)}
	pp, err := PlanPrompts(b, a, AllPromptScopes(a), task.PromptTargets)
	if err != nil {
		t.Fatal(err)
	}
	prompts := []PromptRecord{}
	for _, batch := range pp {
		f, call, native := promptNative(t, b, a, batch)
		prompts = append(prompts, PromptRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: f, RequestHash: f.Digest(), Revision: 4, State: "prompt_ready", CallID: call, Result: promptBytes(t, native)})
	}
	results := []ResultRecord{}
	for _, batch := range resultPlan {
		f, call, native := resultNative(t, b, a, batch)
		results = append(results, ResultRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: f, RequestHash: f.Digest(), Revision: 4, State: "result_ready", CallID: call, Result: promptBytes(t, native)})
	}
	return task, b, ar, prompts, results
}

func TestLearningMaterialCompletePlanAndNativeBindings(t *testing.T) {
	task, b, ar, prompts, results := learningFixture(t)
	m, err := AssembleLearningMaterial(task, b, ar, prompts, results)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, r := range m.Results {
		kinds[r.Type] = true
	}
	if len(kinds) != 9 || len(m.Prompts) == 0 || len(m.Calls) != 1+len(prompts)+len(results) {
		t.Fatal("missing types, prompts or original calls")
	}
	again, err := AssembleLearningMaterial(task, b, ar, prompts, results)
	if err != nil || canonicalHash(m) != canonicalHash(again) {
		t.Fatal("unstable snapshot", err)
	}
	for _, r := range m.Prompts {
		v := r.Payload["variants"].([]any)[0].(map[string]any)
		if v["compatibility"] != "unresolved" || len(v["execution_evidence"].([]any)) != 0 {
			t.Fatal("fabricated generation validation")
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Task, *Bundle, *AssociationRecord, *[]PromptRecord, *[]ResultRecord)
	}{
		{"missing_prompt", func(_ *Task, _ *Bundle, _ *AssociationRecord, p *[]PromptRecord, _ *[]ResultRecord) {
			*p = (*p)[:len(*p)-1]
		}},
		{"missing_result", func(_ *Task, _ *Bundle, _ *AssociationRecord, _ *[]PromptRecord, r *[]ResultRecord) {
			*r = (*r)[:len(*r)-1]
		}},
		{"duplicated_batch", func(_ *Task, _ *Bundle, _ *AssociationRecord, _ *[]PromptRecord, r *[]ResultRecord) {
			(*r)[1] = (*r)[0]
		}},
		{"foreign_owner", func(_ *Task, _ *Bundle, a *AssociationRecord, _ *[]PromptRecord, _ *[]ResultRecord) {
			a.OwnerID = uuid.NewString()
		}},
		{"foreign_source", func(_ *Task, b *Bundle, _ *AssociationRecord, _ *[]PromptRecord, _ *[]ResultRecord) {
			b.RevisionID = uuid.NewString()
		}},
		{"wrong_call", func(_ *Task, _ *Bundle, _ *AssociationRecord, _ *[]PromptRecord, r *[]ResultRecord) {
			(*r)[0].CallID = uuid.NewString()
		}},
		{"forged_native_text", func(_ *Task, _ *Bundle, _ *AssociationRecord, _ *[]PromptRecord, r *[]ResultRecord) {
			var n map[string]any
			_ = json.Unmarshal((*r)[0].Result, &n)
			n["analysis"].(map[string]any)["text"] = "{}"
			(*r)[0].Result = promptBytes(t, n)
		}},
		{"not_ready", func(_ *Task, _ *Bundle, _ *AssociationRecord, _ *[]PromptRecord, r *[]ResultRecord) {
			(*r)[0].State = "running"
			(*r)[0].Result = nil
		}},
		{"legacy_no_results", func(t *Task, _ *Bundle, _ *AssociationRecord, _ *[]PromptRecord, _ *[]ResultRecord) {
			t.IncludeTypedResults = false
		}},
		{"changed_targets", func(t *Task, _ *Bundle, _ *AssociationRecord, _ *[]PromptRecord, _ *[]ResultRecord) {
			t.PromptTargets[0].RequestedLabel = "changed"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task, b, ar, p, r := learningFixture(t)
			tc.mutate(&task, &b, &ar, &p, &r)
			if _, err := AssembleLearningMaterial(task, b, ar, p, r); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}

func TestLearningPreservesRevalidationReceipt(t *testing.T) {
	task, b, ar, prompts, results := learningFixture(t)
	ar.Validation = &AssociationValidation{ID: uuid.NewString(), Policy: "evidence-graph-v2", NativeHash: hashBytes(ar.Result)}
	m, err := AssembleLearningMaterial(task, b, ar, prompts, results)
	if err != nil || m.AssociationValidation == nil || *m.AssociationValidation != *ar.Validation {
		t.Fatal("lost validation receipt", err)
	}
	ar.Validation.NativeHash = "incorrect"
	if _, err = AssembleLearningMaterial(task, b, ar, prompts, results); err == nil {
		t.Fatal("accepted mismatching native proof")
	}
}
