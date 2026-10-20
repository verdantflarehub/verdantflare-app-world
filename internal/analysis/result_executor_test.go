package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type memoryResultStore struct {
	mu     sync.Mutex
	record ResultRecord
}

func (s *memoryResultStore) LoadResult(_ context.Context, org, owner, task, key string) (ResultRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.OrganizationID != org || s.record.OwnerID != owner || s.record.Request.TaskID != task || s.record.Request.Batch.Key != key {
		return ResultRecord{}, ErrMissing
	}
	return s.record, nil
}
func (s *memoryResultStore) SwapResult(_ context.Context, old, next ResultRecord) (bool, error) {
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

type memoryResultChannel struct {
	mu                      sync.Mutex
	store                   *memoryResultStore
	call                    Call
	native                  []byte
	submitErr, resultErr    error
	submits, lookups, polls int
	submitted               ResultRequest
	batchKey                string
}

func (c *memoryResultChannel) SubmitResults(ctx context.Context, org string, f ResultRequest) (Call, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.store.LoadResult(ctx, org, execOwner, f.TaskID, f.Batch.Key)
	if err != nil || r.State != "dispatching" || r.Request.Digest() != f.Digest() {
		return Call{}, ErrInvalid
	}
	c.submits++
	c.submitted = f
	return c.call, c.submitErr
}
func (c *memoryResultChannel) Lookup(ctx context.Context, org string, op string) (Call, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lookups++
	r, err := c.store.LoadResult(ctx, org, execOwner, c.call.TaskID, c.batchKey)
	if err != nil || op != r.Request.OperationID {
		return Call{}, ErrInvalid
	}
	return c.call, nil
}
func (c *memoryResultChannel) Poll(_ context.Context, _ string, id string) (Call, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	if id != c.call.CallID {
		return Call{}, ErrInvalid
	}
	return c.call, nil
}
func (c *memoryResultChannel) Result(context.Context, string, string) ([]byte, error) {
	return c.native, c.resultErr
}

func resultExecutionFixture(t *testing.T) (Bundle, Association, *memoryResultStore, *memoryResultChannel) {
	t.Helper()
	b, a, batches := resultFixture(t)
	f, call, native := resultNative(t, b, a, batches[0])
	data, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	s := &memoryResultStore{record: ResultRecord{OrganizationID: execOrg, OwnerID: execOwner, Request: f, RequestHash: f.Digest(), Revision: 1, State: "planned"}}
	yes := true
	c := &memoryResultChannel{store: s, call: Call{SchemaVersion: 1, Binding: Binding{call, b.TaskID, f.ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}, native: data, batchKey: f.Batch.Key}
	return b, a, s, c
}

func TestResultExecutionPersistsIntentAndResumesOriginalAfterLostPost(t *testing.T) {
	b, a, s, c := resultExecutionFixture(t)
	worker := ResultExecutor{s, c}
	if r, err := worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, false); err != nil || r.State != "planned" || c.submits != 0 {
		t.Fatal("disabled dispatch spent", err)
	}
	c.submitErr = ErrUnavailable
	r, err := worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, true)
	if !errors.Is(err, ErrUnavailable) || r.State != "acceptance_unknown" || c.submits != 1 || c.submitted.Digest() != s.record.RequestHash {
		t.Fatal("intent or unknown lost", r.State, err)
	}
	// New executor instance, dispatch disabled: recovery is still mandatory.
	worker = ResultExecutor{s, c}
	r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, false)
	if err != nil || r.State != "running" || c.lookups != 1 || c.submits != 1 {
		t.Fatal("restart resubmitted", err)
	}
	c.call.State = "completed"
	c.resultErr = ErrUnavailable
	r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, false)
	if !errors.Is(err, ErrUnavailable) || r.State != "running" || r.CallID != c.call.CallID {
		t.Fatal("result outage lost identity", err)
	}
	c.resultErr = nil
	r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, false)
	if err != nil || r.State != "result_ready" || c.submits != 1 || c.polls != 2 {
		t.Fatal("original result not recovered", err)
	}
	before := r.Revision
	if r, err = worker.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, true); err != nil || r.Revision != before || c.submits != 1 || c.polls != 2 {
		t.Fatal("terminal result repeated", err)
	}
}

func TestResultExecutionRejectsBadCallsAndResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*memoryResultChannel)
		code   string
	}{
		{"rejection", func(c *memoryResultChannel) { c.submitErr = ErrRejected }, "native_rejected"},
		{"binding", func(c *memoryResultChannel) { c.call.ModelID = "another-model" }, "native_binding_mismatch"},
		{"invalid result", func(c *memoryResultChannel) { c.call.State = "completed"; c.native = []byte(`{"forged":true}`) }, "result_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, a, s, c := resultExecutionFixture(t)
			tc.change(c)
			w := ResultExecutor{s, c}
			r, err := w.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, true)
			if err != nil || r.State != "failed" || r.ErrorCode != tc.code {
				t.Fatal(r.State, r.ErrorCode, err)
			}
			if tc.code == "result_invalid" && string(r.Result) != string(c.native) {
				t.Fatal("failure evidence dropped")
			}
			if _, err = w.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, true); err != nil || c.submits != 1 {
				t.Fatal("failed call resubmitted")
			}
		})
	}
}

func TestResultExecutionConcurrentAdvanceSubmitsOnce(t *testing.T) {
	b, a, s, c := resultExecutionFixture(t)
	w := ResultExecutor{s, c}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.Advance(t.Context(), execOrg, execOwner, b.TaskID, c.batchKey, b, a, true)
			if err != nil && !errors.Is(err, ErrChanged) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.submits != 1 {
		t.Fatal("duplicate submission", c.submits)
	}
	if _, err := w.Advance(t.Context(), execOrg, "10000000-0000-4000-8000-000000000099", b.TaskID, c.batchKey, b, a, true); !errors.Is(err, ErrMissing) || c.submits != 1 {
		t.Fatal("foreign caller advanced")
	}
}
