package analysis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func truncatedAssociationFixture(t *testing.T) (Bundle, AssociationRecord) {
	b, old := failedAssociationFixture(t)
	var doc map[string]any
	json.Unmarshal(old.Result, &doc)
	doc["state"] = "failed"
	doc["reason_code"] = "output_incomplete"
	a := doc["analysis"].(map[string]any)
	a["finish_reason"] = "MAX_TOKENS"
	a["document"] = nil
	a["text"] = `{"shot_groups":[`
	a["response_schema_verified"] = false
	old.Result, _ = json.Marshal(doc)
	old.ErrorCode = "native_failed"
	return b, old
}
func TestAssociationTruncationRetryUsesLosslessShortReferences(t *testing.T) {
	b, old := truncatedAssociationFixture(t)
	next, err := RetryAssociationRequest(b, old, execOrg)
	if err != nil {
		t.Fatal(err)
	}
	if next.Encoding != "short-v1" || next.TextHash == old.Request.TextHash || next.BundleHash != old.Request.BundleHash || next.BudgetLimit != old.Request.BudgetLimit || !reflect.DeepEqual(next.Retry.FailureReceipt, old.Result) {
		t.Fatal("binding lost")
	}
	var original, short associationPayload
	json.Unmarshal(old.Request.Payload, &original)
	json.Unmarshal(next.Payload, &short)
	if len(short.Text) >= len(original.Text) || original.Tokens != short.Tokens {
		t.Fatal("no bounded compression")
	}
	var a, c map[string]any
	json.Unmarshal([]byte(original.Text), &a)
	json.Unmarshal([]byte(short.Text), &c)
	an, cn := a["nodes"].([]any), c["nodes"].([]any)
	if len(an) != len(cn) {
		t.Fatal("node omitted")
	}
	for i := range an {
		if !reflect.DeepEqual(an[i].(map[string]any)["value"], cn[i].(map[string]any)["value"]) {
			t.Fatal("observation rewritten")
		}
	}
	association := associationForBundle(b)
	raw := associationBytes(t, association)
	aliases, _ := associationAliases(b)
	// Build a synthetic native output in the exact provider reference vocabulary.
	for full, alias := range aliases {
		raw = []byte(strings.ReplaceAll(string(raw), `"`+full+`"`, `"`+alias+`"`))
	}
	var output any
	json.Unmarshal(raw, &output)
	schema, _ := associationSchema()
	native, _ := json.Marshal(map[string]any{"schema_version": 1, "call_id": execOwner, "task_id": b.TaskID, "model_id": next.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{"text": string(raw), "document": output, "model_id": next.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"source_text_sha256": next.TextHash, "response_schema_sha256": canonicalHash(schema)}}})
	if got, err := VerifyAssociationNative(b, next, execOwner, native); err != nil || !reflect.DeepEqual(got, association) {
		t.Fatal("native short reference proof", err)
	}
	decoded, err := decodeAssociationReferences(b, next.Encoding, raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ValidateAssociation(b, decoded)
	if err != nil || !reflect.DeepEqual(got, association) {
		t.Fatal("roundtrip", err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "e000001", "e999999"))
	if _, err = decodeAssociationReferences(b, next.Encoding, raw); err == nil {
		t.Fatal("unknown alias accepted")
	}
}
func TestAssociationTruncationRetryRejectsForgedOrOtherFailures(t *testing.T) {
	for _, field := range []string{"call_id", "task_id", "model_id", "reason_code", "state", "finish_reason", "source_text_sha256", "response_schema_sha256", "text", "response_schema_verified"} {
		t.Run(field, func(t *testing.T) {
			b, old := truncatedAssociationFixture(t)
			var n map[string]any
			json.Unmarshal(old.Result, &n)
			a := n["analysis"].(map[string]any)
			switch field {
			case "finish_reason":
				a[field] = "STOP"
			case "source_text_sha256", "response_schema_sha256":
				a["input"].(map[string]any)[field] = strings.Repeat("0", 64)
			case "text":
				a[field] = ""
			case "response_schema_verified":
				a[field] = true
			default:
				n[field] = "invalid"
			}
			old.Result, _ = json.Marshal(n)
			if _, err := RetryAssociationRequest(b, old, execOrg); err == nil {
				t.Fatal("accepted forged truncation")
			}
		})
	}
}
