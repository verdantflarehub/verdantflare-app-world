package analysis

import (
	"encoding/json"
	"github.com/google/uuid"
	"strings"
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

func TestBatchRetryVerifiedProviderSchemaRejection(t *testing.T) {
	_, b, a, _, results, _ := qualityFixture(t)
	old := results[0]
	old.State = "failed"
	old.ErrorCode = "native_failed"
	var n map[string]any
	json.Unmarshal(old.Result, &n)
	n["state"] = "failed"
	n["accepted"] = true
	n["reason_code"] = "output_schema_mismatch"
	output := n["analysis"].(map[string]any)
	output["response_schema_verified"] = false
	original := output["document"].(map[string]any)
	delete(original, "items")
	output["text"] = string(promptBytes(t, original))
	old.Result = promptBytes(t, n)
	next, err := RetryResultRequest(b, a, old, uuid.NewString())
	if err != nil || len(next.Retry.FailureReceipt) == 0 {
		t.Fatal("confirmed schema rejection", err)
	}
	for _, change := range []string{"identity", "text", "finish", "schema", "reason"} {
		t.Run(change, func(t *testing.T) {
			var x map[string]any
			json.Unmarshal(old.Result, &x)
			o := x["analysis"].(map[string]any)
			switch change {
			case "identity":
				x["call_id"] = uuid.NewString()
			case "text":
				o["text"] = "{}"
			case "finish":
				o["finish_reason"] = "BLOCKED"
			case "schema":
				o["input"].(map[string]any)["response_schema_sha256"] = "bad"
			case "reason":
				x["reason_code"] = "provider_rejected"
			}
			wrong := old
			wrong.Result = promptBytes(t, x)
			if _, err := RetryResultRequest(b, a, wrong, uuid.NewString()); err == nil {
				t.Fatal("unproven failure retried")
			}
		})
	}
	// A false rejection flag on schema-valid native bytes cannot trigger generation.
	var valid map[string]any
	json.Unmarshal(results[0].Result, &valid)
	valid["state"] = "failed"
	valid["reason_code"] = "output_schema_mismatch"
	valid["analysis"].(map[string]any)["response_schema_verified"] = false
	old.Result = promptBytes(t, valid)
	if _, err := RetryResultRequest(b, a, old, uuid.NewString()); err == nil {
		t.Fatal("valid content regenerated")
	}
}

func TestStyleFinalCorrectionRequiresOnlyRepresentationErrors(t *testing.T) {
	_, b, a, _, records, _ := qualityFixture(t)
	var old ResultRecord
	for _, r := range records {
		if r.Request.Batch.Type == "style" {
			old = r
			break
		}
	}
	if old.CallID == "" {
		t.Fatal("no style fixture")
	}
	var n map[string]any
	json.Unmarshal(old.Result, &n)
	output := n["analysis"].(map[string]any)
	doc := output["document"].(map[string]any)
	item := doc["items"].([]any)[0].(map[string]any)
	item["payloads"].([]any)[0].(map[string]any)["palette"] = []any{"#123456 (示例色)"}
	output["text"] = string(promptBytes(t, doc))
	old.Result = promptBytes(t, n)
	old.State = "failed"
	old.ErrorCode = "result_invalid"
	before := string(old.Result)
	for i := 1; i <= 3; i++ {
		f, e := RetryResultRequest(b, a, old, uuid.NewString())
		if e != nil {
			t.Fatal(i, e)
		}
		if i == 3 && f.Retry.Diagnostic != "style-representation-v1" {
			t.Fatal("missing diagnostic")
		}
		old.Request = f
		old.RequestHash = f.Digest()
	}
	if string(old.Result) != before {
		t.Fatal("mutated original")
	}
	if _, e := RetryResultRequest(b, a, old, uuid.NewString()); e == nil {
		t.Fatal("fourth correction")
	}
	for _, bad := range []string{"unknown_evidence", "bad_scope", "invalid_color", "duplicate_rule_evidence"} {
		t.Run(bad, func(t *testing.T) {
			var v map[string]any
			json.Unmarshal(old.Result, &v)
			o := v["analysis"].(map[string]any)
			d := o["document"].(map[string]any)
			it := d["items"].([]any)[0].(map[string]any)
			switch bad {
			case "unknown_evidence":
				it["rule_evidence"].([]any)[0].(map[string]any)["evidence_keys"] = []any{"missing"}
			case "bad_scope":
				it["scope_key"] = "wrong"
			case "invalid_color":
				it["payloads"].([]any)[0].(map[string]any)["palette"] = []any{"#1234567"}
			case "duplicate_rule_evidence":
				rr := it["rule_evidence"].([]any)[0].(map[string]any)
				rr["evidence_keys"] = append(rr["evidence_keys"].([]any), rr["evidence_keys"].([]any)[0])
			}
			o["text"] = string(promptBytes(t, d))
			if styleRepresentationOnly(b, a, old.Request, old.CallID, promptBytes(t, v)) == nil {
				t.Fatal("unexplained defect accepted")
			}
		})
	}
}

func TestMethodDiagnosticPreservesNativeAndBounds(t *testing.T) {
	_, b, a, _, records, _ := qualityFixture(t)
	var old ResultRecord
	for _, r := range records {
		if r.Request.Batch.Type == "method" {
			old = r
		}
	}
	var native map[string]any
	json.Unmarshal(old.Result, &native)
	o := native["analysis"].(map[string]any)
	d := o["document"].(map[string]any)
	item := d["items"].([]any)[0].(map[string]any)
	p := item["payloads"].([]any)[0].(map[string]any)
	p["expected_outputs"].([]any)[0].(map[string]any)["key"] = "unproduced_outline"
	o["text"] = string(promptBytes(t, d))
	old.Result = promptBytes(t, native)
	old.State = "failed"
	old.ErrorCode = "result_invalid"
	before := string(old.Result)
	for i := 1; i <= 3; i++ {
		f, err := RetryResultRequest(b, a, old, uuid.NewString())
		if err != nil {
			t.Fatal(i, err)
		}
		if f.Retry.Diagnostic != "method-links-v1" || !strings.Contains(string(f.Payload), "unproduced_outline") {
			t.Fatal("missing actual error feedback")
		}
		old.Request = f
		old.RequestHash = f.Digest()
	}
	if string(old.Result) != before {
		t.Fatal("native changed")
	}
	if _, err := RetryResultRequest(b, a, old, uuid.NewString()); err == nil {
		t.Fatal("fourth retry")
	}
	item["evidence_keys"] = []any{"unknown"}
	o["text"] = string(promptBytes(t, d))
	old.Result = promptBytes(t, native)
	old.Request.Retry.Number = 2
	old.Request.OperationID = old.Request.TaskID + ":" + old.Request.Batch.Key + ":retry:" + old.Request.Retry.ID
	// ValidateResults must reject unknown evidence before returning method diagnostics.
	if _, err := ValidateResults(b, a, old.Request.Batch, promptBytes(t, d)); err == nil {
		t.Fatal("unknown evidence accepted")
	}
	if methodLinkInstruction([]byte(`{"analysis":{"document":{}}}`)) != "" {
		t.Fatal("malformed native diagnostic")
	}
}

func TestPromptEmptyNegativeDiagnosticStrictAndBounded(t *testing.T) {
	task, b, a, _, _, drafts := qualityFixture(t)
	plan, _, _, err := PlanDerivedPrompts(b, a, drafts, task.PromptTargets)
	if err != nil {
		t.Fatal(err)
	}
	f, call, native := promptNative(t, b, a, plan[0])
	var n map[string]any
	json.Unmarshal(promptBytes(t, native), &n)
	o := n["analysis"].(map[string]any)
	d := o["document"].(map[string]any)
	d["items"].([]any)[0].(map[string]any)["negative_prompt"] = ""
	o["text"] = string(promptBytes(t, d))
	o["response_schema_verified"] = false
	n["state"] = "failed"
	n["reason_code"] = "output_schema_mismatch"
	r := PromptRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: f, RequestHash: f.Digest(), Revision: 4, State: "failed", CallID: call, Result: promptBytes(t, n), ErrorCode: "native_failed"}
	before := string(r.Result)
	for i := 1; i <= 3; i++ {
		next, e := RetryPromptRequest(b, a, r, uuid.NewString())
		if e != nil {
			t.Fatal(i, e)
		}
		if !strings.Contains(string(next.Payload), "negative_prompt") || !strings.Contains(string(next.Payload), "未使用变量") {
			t.Fatal("missing actionable format feedback")
		}
		if i == 3 && next.Retry.Diagnostic != "prompt-empty-negative-v1" {
			t.Fatal("missing bound diagnostic")
		}
		r.Request = next
		r.RequestHash = next.Digest()
	}
	if string(r.Result) != before {
		t.Fatal("native modified")
	}
	if _, e := RetryPromptRequest(b, a, r, uuid.NewString()); e == nil {
		t.Fatal("fourth attempt")
	}
	for _, bad := range []string{"scope", "evidence", "unused_variable", "unknown_field", "finish", "identity", "text", "valid"} {
		t.Run(bad, func(t *testing.T) {
			var x map[string]any
			json.Unmarshal(r.Result, &x)
			out := x["analysis"].(map[string]any)
			doc := out["document"].(map[string]any)
			item := doc["items"].([]any)[0].(map[string]any)
			switch bad {
			case "scope":
				item["scope_key"] = "foreign"
			case "evidence":
				item["evidence_keys"] = []any{"unknown"}
			case "unused_variable":
				item["variables"] = []any{map[string]any{"key": "unused", "description": "unused", "required": true}}
			case "unknown_field":
				item["unknown"] = true
			case "finish":
				out["finish_reason"] = "MAX_TOKENS"
			case "identity":
				x["call_id"] = uuid.NewString()
			case "valid":
				delete(item, "negative_prompt")
			}
			out["text"] = string(promptBytes(t, doc))
			if bad == "text" {
				out["text"] = "{}"
			}
			if promptEmptyNegativeOnly(b, a, f, call, promptBytes(t, x)) == nil {
				t.Fatal("unexplained failure accepted")
			}
		})
	}
}

func TestPromptEvidenceDiagnosticPreservesNativeAndBounds(t *testing.T) {
	task, b, a, _, _, drafts := qualityFixture(t)
	for i := range drafts {
		if len(drafts[i].Evidence) == 1 {
			for _, n := range b.Nodes {
				if n.Key != drafts[i].Evidence[0] && n.Kind != "uncertainties" {
					drafts[i].Evidence = append(drafts[i].Evidence, n.Key)
					break
				}
			}
		}
	}

	plan, _, _, err := PlanDerivedPrompts(b, a, drafts, task.PromptTargets)
	if err != nil {
		t.Fatal(err)
	}
	f, call, native := promptNative(t, b, a, plan[0])
	var n map[string]any
	json.Unmarshal(promptBytes(t, native), &n)
	o := n["analysis"].(map[string]any)
	d := o["document"].(map[string]any)
	item := d["items"].([]any)[0].(map[string]any)
	evidence := item["evidence_keys"].([]any)
	if len(evidence) < 2 {
		t.Fatal("fixture needs multiple evidence nodes")
	}
	item["evidence_keys"] = evidence[:1]
	o["text"] = string(promptBytes(t, d))
	r := PromptRecord{OrganizationID: task.OrganizationID, OwnerID: task.OwnerID, Request: f, RequestHash: f.Digest(), Revision: 4, State: "failed", CallID: call, Result: promptBytes(t, n), ErrorCode: "prompt_invalid"}
	before := string(r.Result)
	for i := 1; i <= 3; i++ {
		next, e := RetryPromptRequest(b, a, r, uuid.NewString())
		if e != nil {
			t.Fatal(i, e)
		}
		if next.Retry.Diagnostic != "prompt-evidence-v1" || next.TextHash != f.TextHash || next.BudgetLimit != f.BudgetLimit || !strings.Contains(string(next.Payload), "missing_evidence_keys") {
			t.Fatal("missing bounded diagnostic")
		}
		r.Request = next
		r.RequestHash = next.Digest()
	}
	if string(r.Result) != before {
		t.Fatal("native rewritten")
	}
	if _, e := RetryPromptRequest(b, a, r, uuid.NewString()); e == nil {
		t.Fatal("fourth attempt")
	}
	for _, bad := range []string{"unknown", "duplicate", "variable", "field", "scope", "finish", "identity", "text"} {
		t.Run(bad, func(t *testing.T) {
			var x map[string]any
			json.Unmarshal(r.Result, &x)
			out := x["analysis"].(map[string]any)
			doc := out["document"].(map[string]any)
			it := doc["items"].([]any)[0].(map[string]any)
			switch bad {
			case "unknown":
				it["evidence_keys"] = []any{"unknown"}
			case "duplicate":
				it["evidence_keys"] = []any{evidence[0], evidence[0]}
			case "variable":
				it["variables"] = []any{map[string]any{"key": "unused", "description": "unused", "required": true}}
			case "field":
				it["unknown"] = true
			case "scope":
				it["scope_key"] = "foreign"
			case "finish":
				out["finish_reason"] = "MAX_TOKENS"
			case "identity":
				x["call_id"] = uuid.NewString()
			}
			out["text"] = string(promptBytes(t, doc))
			if bad == "text" {
				out["text"] = "{}"
			}
			if promptEvidenceOnly(b, a, f, call, promptBytes(t, x)) == nil {
				t.Fatal("non-omission accepted")
			}
		})
	}
}
