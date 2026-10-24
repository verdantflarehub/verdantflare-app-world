package analysis

import (
	"encoding/json"
	"reflect"
	"testing"
)

func retryFixture(t *testing.T) StepRecord {
	t.Helper()
	r, _, raw := executorFixture(t)
	var native map[string]any
	if err := json.Unmarshal(raw, &native); err != nil {
		t.Fatal(err)
	}
	a := native["analysis"].(map[string]any)
	doc := a["document"].(map[string]any)
	doc["findings"].([]any)[0].(map[string]any)["shot_keys"] = []string{"missing_shot"}
	text, _ := json.Marshal(doc)
	a["text"] = string(text)
	r.Result, _ = json.Marshal(native)
	report := VerifyNative(r.Request.Step, r.Request.Prepared, Binding{CallID: execCall, TaskID: execTask, ModelID: r.Request.ModelID}, r.Result)
	if report.Passed || !hasIssue(report, "unresolved_reference") {
		t.Fatal(report)
	}
	r.State, r.CallID, r.Revision, r.ErrorCode, r.Report = "failed", execCall, 3, "observation_invalid", &report
	return r
}
func TestRetryPinsFailedNativeAndPreservesSourceAndBudget(t *testing.T) {
	old := retryFixture(t)
	before, _ := json.Marshal(old)
	next, err := RetryRequest(old, execOrg)
	if err != nil {
		t.Fatal(err)
	}
	if next.Step != old.Request.Step || next.Prepared != old.Request.Prepared || next.BudgetLimit != old.Request.BudgetLimit || next.ModelID != old.Request.ModelID || next.Retry.PreviousRequestHash != old.RequestHash || next.Retry.PreviousCallID != old.CallID || next.OperationID == old.Request.OperationID || next.Digest() == old.RequestHash {
		t.Fatal("retry lost fixed provenance")
	}
	after, _ := json.Marshal(old)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("mutated prior record")
	}
	replay, err := RetryRequest(old, execOrg)
	if err != nil || replay.Digest() != next.Digest() {
		t.Fatal("unstable retry")
	}
	payload := map[string]any{}
	json.Unmarshal(next.Payload, &payload)
	if len(payload["prompt"].(string)) <= len(old.Request.Payload)/10 {
		t.Fatal("missing correction instruction")
	}
	if (Call{SchemaVersion: 1, Binding: Binding{CallID: old.CallID, TaskID: next.TaskID, ModelID: next.ModelID}, Media: "analysis", Operation: "analyze", State: "completed", Accepted: ptrTrue()}).valid(next, "") {
		t.Fatal("old call reused for a new retry")
	}
}
func ptrTrue() *bool { v := true; return &v }
func TestRetryRejectsUnknownIdentityChangesAndAttemptOverflow(t *testing.T) {
	for _, change := range []func(*StepRecord){
		func(r *StepRecord) { r.State = "acceptance_unknown"; r.Report = nil; r.ErrorCode = ""; r.Result = nil },
		func(r *StepRecord) { r.ErrorCode = "native_rejected" },
		func(r *StepRecord) { r.Result = []byte(`{}`) },
		func(r *StepRecord) { r.Report = &Report{Issues: []Issue{{"native_input_mismatch", "input"}}} },
		func(r *StepRecord) {
			next, e := RetryRequest(*r, execOrg)
			if e != nil {
				t.Fatal(e)
			}
			next.Retry.Number = 2
			r.Request = next
			r.RequestHash = next.Digest()
		},
	} {
		old := retryFixture(t)
		change(&old)
		if _, err := RetryRequest(old, execOwner); err == nil {
			t.Fatal("invalid retry accepted")
		}
	}
}
