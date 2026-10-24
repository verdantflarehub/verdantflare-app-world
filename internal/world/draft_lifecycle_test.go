package world

import (
	"encoding/json"
	"testing"
)

func TestCollectionLifecycleContractAndCursorIsolation(t *testing.T) {
	version := int64(1)
	for _, mode := range []string{"trash", "restore"} {
		r := DraftRequest{Mode: mode, DraftID: testID(70), OperationID: testID(71), ExpectedLifecycleVersion: &version}
		if _, err := normalizeDraftRequest(r); err != nil {
			t.Fatal(err)
		}
		r.ExpectedLifecycleVersion = nil
		if _, err := normalizeDraftRequest(r); err == nil {
			t.Fatal("missing concurrency version accepted")
		}
		r.ExpectedLifecycleVersion = &version
		r.Purpose = "collection"
		if _, err := normalizeDraftRequest(r); err == nil {
			t.Fatal("extraneous request field accepted")
		}
	}
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	r := DraftRequest{Mode: "list", Purpose: "collection"}
	r.Cursor = encodeDraftCursor(p, r, testID(70))
	r.LifecycleState = "active"
	if _, err := decodeDraftCursor(p, r); err != nil {
		t.Fatal("legacy active cursor rejected", err)
	}
	r.LifecycleState = "trashed"
	if _, err := decodeDraftCursor(p, r); err == nil {
		t.Fatal("active cursor accepted in trash")
	}
	r.Cursor = ""
	r.Purpose = "learning"
	if _, err := normalizeDraftRequest(r); err == nil {
		t.Fatal("non-collection trash filter accepted")
	}
	out := DraftLifecycleResult{Kind: "lifecycle", DraftID: testID(70), OperationID: testID(71), Lifecycle: DraftLifecycle{State: "active", Version: 2, CanTrash: true}}
	data, _ := json.Marshal(out)
	if err := validateLearningJSON(data, "world-draft-response"); err != nil {
		t.Fatal(err)
	}
}
