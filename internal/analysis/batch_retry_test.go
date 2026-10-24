package analysis

import (
	"encoding/json"
	"github.com/google/uuid"
	"testing"
)

func invalidBatchNative(t *testing.T, raw []byte) []byte {
	t.Helper()
	var n map[string]any
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}
	a := n["analysis"].(map[string]any)
	d := a["document"].(map[string]any)
	d["items"].([]any)[0].(map[string]any)["scope_key"] = "outside-frozen-batch"
	a["text"] = string(promptBytes(t, d))
	return promptBytes(t, n)
}
func TestBatchRetryBoundsAndExactFrozenInputs(t *testing.T) {
	task, b, a, _, results, drafts := qualityFixture(t)
	old := results[0]
	old.State = "failed"
	old.ErrorCode = "result_invalid"
	old.Result = invalidBatchNative(t, old.Result)
	original := old
	for i := 1; i <= 2; i++ {
		next, err := RetryResultRequest(b, a, old, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if next.Retry.Number != i || next.TextHash != original.Request.TextHash || canonicalHash(next.Batch) != canonicalHash(original.Request.Batch) || next.BudgetLimit != original.Request.BudgetLimit || next.Retry.PreviousRequestHash != old.RequestHash {
			t.Fatal("changed frozen input")
		}
		old.Request = next
		old.RequestHash = next.Digest()
	}
	if _, err := RetryResultRequest(b, a, old, uuid.NewString()); err == nil {
		t.Fatal("third batch retry")
	}
	bad := original
	bad.Result = []byte(`{"forged":true}`)
	if _, err := RetryResultRequest(b, a, bad, uuid.NewString()); err == nil {
		t.Fatal("forged native retry")
	}
	bad = original
	bad.ErrorCode = "native_binding_mismatch"
	if _, err := RetryResultRequest(b, a, bad, uuid.NewString()); err == nil {
		t.Fatal("identity failure retried")
	}
	bad = results[0]
	bad.State = "failed"
	bad.ErrorCode = "result_invalid"
	if _, err := RetryResultRequest(b, a, bad, uuid.NewString()); err == nil {
		t.Fatal("valid result regenerated")
	}
	plan, _, _, err := PlanDerivedPrompts(b, a, drafts, task.PromptTargets)
	if err != nil || len(plan) == 0 {
		t.Fatal(err)
	}
	f, call, native := promptNative(t, b, a, plan[0])
	r := PromptRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: f, RequestHash: f.Digest(), Revision: 4, State: "failed", CallID: call, Result: invalidBatchNative(t, promptBytes(t, native)), ErrorCode: "prompt_invalid"}
	for i := 1; i <= 2; i++ {
		next, err := RetryPromptRequest(b, a, r, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if next.Retry.Number != i || next.TextHash != f.TextHash || canonicalHash(next.Batch) != canonicalHash(f.Batch) {
			t.Fatal("prompt inputs changed")
		}
		r.Request = next
		r.RequestHash = next.Digest()
	}
	if _, err := RetryPromptRequest(b, a, r, uuid.NewString()); err == nil {
		t.Fatal("third prompt retry")
	}
}
