package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type memoryAssociationStore struct {
	mu     sync.Mutex
	record AssociationRecord
}

func (s *memoryAssociationStore) LoadAssociation(_ context.Context, org, owner, task string) (AssociationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.OrganizationID != org || s.record.OwnerID != owner || s.record.Request.TaskID != task {
		return AssociationRecord{}, ErrMissing
	}
	return s.record, nil
}
func (s *memoryAssociationStore) SwapAssociation(_ context.Context, old, next AssociationRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Revision != old.Revision || s.record.RequestHash != old.RequestHash {
		return false, nil
	}
	if next.Validate() != nil || next.Revision != old.Revision+1 {
		return false, ErrInvalid
	}
	s.record = next
	return true, nil
}

type memoryAssociationChannel struct {
	mu                      sync.Mutex
	store                   *memoryAssociationStore
	call                    Call
	native                  []byte
	submitErr, resultErr    error
	submits, lookups, polls int
	submitted               AssociationRequest
}

func (c *memoryAssociationChannel) SubmitAssociation(ctx context.Context, org string, f AssociationRequest) (Call, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.store.LoadAssociation(ctx, org, execOwner, f.TaskID)
	if err != nil || r.State != "dispatching" || r.Request.Digest() != f.Digest() {
		return Call{}, ErrInvalid
	}
	c.submits++
	c.submitted = f
	return c.call, c.submitErr
}
func (c *memoryAssociationChannel) Lookup(ctx context.Context, org string, op string) (Call, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lookups++
	r, err := c.store.LoadAssociation(ctx, org, execOwner, c.call.TaskID)
	if err != nil || op != r.Request.OperationID {
		return Call{}, ErrInvalid
	}
	return c.call, nil
}
func (c *memoryAssociationChannel) Poll(_ context.Context, _ string, id string) (Call, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	if id != c.call.CallID {
		return Call{}, ErrInvalid
	}
	return c.call, nil
}
func (c *memoryAssociationChannel) Result(context.Context, string, string) ([]byte, error) {
	return c.native, c.resultErr
}

func associationExecutionFixture(t *testing.T) (Bundle, *memoryAssociationStore, *memoryAssociationChannel) {
	t.Helper()
	b, a := associationFixture(t)
	f, call, native := associationNativeFixture(t, b, a)
	data, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	s := &memoryAssociationStore{record: AssociationRecord{OrganizationID: execOrg, OwnerID: execOwner, Request: f, RequestHash: f.Digest(), Revision: 1, State: "planned"}}
	yes := true
	c := &memoryAssociationChannel{store: s, call: Call{SchemaVersion: 1, Binding: Binding{call, b.TaskID, f.ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}, native: data}
	return b, s, c
}

func TestAssociationExecutionPersistsIntentAndResumesOriginalAfterLostPost(t *testing.T) {
	b, s, c := associationExecutionFixture(t)
	worker := AssociationExecutor{s, c}
	if r, err := worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, false); err != nil || r.State != "planned" || c.submits != 0 {
		t.Fatal("disabled dispatch spent", err)
	}
	c.submitErr = ErrUnavailable
	r, err := worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, true)
	if !errors.Is(err, ErrUnavailable) || r.State != "acceptance_unknown" || c.submits != 1 || c.submitted.Digest() != s.record.RequestHash {
		t.Fatal("intent or unknown lost", r.State, err)
	}
	// New executor instance, dispatch disabled: recovery is still mandatory.
	worker = AssociationExecutor{s, c}
	r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, false)
	if err != nil || r.State != "running" || c.lookups != 1 || c.submits != 1 {
		t.Fatal("restart resubmitted", err)
	}
	c.call.State = "completed"
	c.resultErr = ErrUnavailable
	r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, false)
	if !errors.Is(err, ErrUnavailable) || r.State != "running" || r.CallID != c.call.CallID {
		t.Fatal("result outage lost identity", err)
	}
	c.resultErr = nil
	r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, false)
	if err != nil || r.State != "association_ready" || c.submits != 1 || c.polls != 2 {
		t.Fatal("original result not recovered", err)
	}
	before := r.Revision
	if r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, true); err != nil || r.Revision != before || c.submits != 1 || c.polls != 2 {
		t.Fatal("terminal association repeated", err)
	}
}

func TestAssociationExecutionRejectsBadCallsAndResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*memoryAssociationChannel)
		code   string
	}{
		{"rejection", func(c *memoryAssociationChannel) { c.submitErr = ErrRejected }, "native_rejected"},
		{"binding", func(c *memoryAssociationChannel) { c.call.ModelID = "another-model" }, "native_binding_mismatch"},
		{"invalid result", func(c *memoryAssociationChannel) { c.call.State = "completed"; c.native = []byte(`{"forged":true}`) }, "association_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, s, c := associationExecutionFixture(t)
			tc.change(c)
			w := AssociationExecutor{s, c}
			r, err := w.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, true)
			if err != nil || r.State != "failed" || r.ErrorCode != tc.code {
				t.Fatal(r.State, r.ErrorCode, err)
			}
			if tc.code == "association_invalid" && string(r.Result) != string(c.native) {
				t.Fatal("failure evidence dropped")
			}
			if _, err = w.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, true); err != nil || c.submits != 1 {
				t.Fatal("failed call resubmitted")
			}
		})
	}
}

func TestAssociationExecutionConcurrentAdvanceSubmitsOnce(t *testing.T) {
	b, s, c := associationExecutionFixture(t)
	w := AssociationExecutor{s, c}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.Advance(t.Context(), execOrg, execOwner, b.TaskID, b, true)
			if err != nil && !errors.Is(err, ErrChanged) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.submits != 1 {
		t.Fatal("duplicate submission", c.submits)
	}
	if _, err := w.Advance(t.Context(), execOrg, "10000000-0000-4000-8000-000000000099", b.TaskID, b, true); !errors.Is(err, ErrMissing) || c.submits != 1 {
		t.Fatal("foreign caller advanced")
	}
}
