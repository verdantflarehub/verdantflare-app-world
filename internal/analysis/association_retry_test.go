package analysis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func failedAssociationFixture(t *testing.T) (Bundle, AssociationRecord) {
	b, a := associationFixture(t)
	a.Shots = a.Shots[:1] // A valid native envelope with incomplete member coverage.
	f, call, native := associationNativeFixture(t, b, a)
	raw, _ := json.Marshal(native)
	return b, AssociationRecord{OrganizationID: execOrg, OwnerID: execOwner, Request: f, RequestHash: f.Digest(), Revision: 3, State: "failed", CallID: call, Result: raw, ErrorCode: "association_invalid"}
}
func TestAssociationRetryPreservesFullInputAndRejectsUntrustedNative(t *testing.T) {
	b, old := failedAssociationFixture(t)
	before, _ := json.Marshal(old)
	f, err := RetryAssociationRequest(b, old, execOrg)
	if err != nil {
		t.Fatal(err)
	}
	var a, c associationPayload
	json.Unmarshal(old.Request.Payload, &a)
	json.Unmarshal(f.Payload, &c)
	if a.Text != c.Text || !reflect.DeepEqual(a.Schema, c.Schema) || a.Tokens != c.Tokens || f.BundleHash != old.Request.BundleHash || f.TextHash != old.Request.TextHash || f.BudgetLimit != old.Request.BudgetLimit || f.Retry.PreviousCallID != old.CallID || f.Retry.PreviousRequestHash != old.RequestHash || f.OperationID == old.Request.OperationID || !strings.Contains(c.Prompt, "shot_groups") {
		t.Fatal("retry changed frozen source or lost correction")
	}
	after, _ := json.Marshal(old)
	if string(before) != string(after) {
		t.Fatal("original modified")
	}
	for _, change := range []func(*AssociationRecord){
		func(r *AssociationRecord) { r.Result = []byte(`{}`) },
		func(r *AssociationRecord) { r.State = "running"; r.ErrorCode = ""; r.Result = nil },
		func(r *AssociationRecord) { r.ErrorCode = "native_binding_mismatch" },
		func(r *AssociationRecord) {
			r.Request = f
			r.Request.Retry.Number = 2
			r.RequestHash = r.Request.Digest()
		},
	} {
		b, r := failedAssociationFixture(t)
		change(&r)
		if _, e := RetryAssociationRequest(b, r, execOwner); e == nil {
			t.Fatal("unsafe retry accepted")
		}
	}
}
