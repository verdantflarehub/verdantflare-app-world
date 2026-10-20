package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

const execOrg = "01900000-0000-7000-8000-000000000001"
const execOwner = "01900000-0000-7000-8000-000000000002"
const execTask = "01900000-0000-7000-8000-000000000003"
const execCall = "01900000-0000-7000-8000-000000000004"

func executorFixture(t *testing.T) (StepRecord, []byte, []byte) {
	t.Helper()
	media := []byte("synthetic-video-fixture")
	p, _ := PlanVideo(Source{SHA256: hashBytes(media), DurationMS: 345002, HasAudio: false})
	step := p.Steps[0]
	f, err := Freeze(execTask, "gemini-3.1-pro-preview", "", "1.00", step, Prepared{SourceHash: step.SourceHash, MediaHash: step.SourceHash, MIME: "video/mp4", Range: step.Input}, int64(len(media)))
	if err != nil {
		t.Fatal(err)
	}
	r := StepRecord{OrganizationID: execOrg, OwnerID: execOwner, Request: f, RequestHash: f.Digest(), Revision: 1, State: "planned"}
	document := visualFixture()
	text, _ := json.Marshal(document)
	schemaHash, _ := step.SchemaHash()
	result, _ := json.Marshal(map[string]any{"schema_version": 1, "call_id": execCall, "task_id": execTask, "model_id": f.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{
		"text": string(text), "document": document, "model_id": f.ModelID, "finish_reason": "STOP", "response_schema_verified": true,
		"input": map[string]any{"media": map[string]any{"sha256": step.SourceHash, "mime_type": "video/mp4"}, "video_range": step.Input, "provider_file_integrity_verified": true, "response_schema_sha256": schemaHash}}})
	return r, media, result
}

type memorySteps struct {
	mu     sync.Mutex
	record StepRecord
}

func (s *memorySteps) Load(_ context.Context, org, owner, task, key string) (StepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.record
	if r.OrganizationID != org || r.OwnerID != owner || r.Request.TaskID != task || r.Request.Step.Key != key {
		return StepRecord{}, ErrMissing
	}
	return r, nil
}
func (s *memorySteps) CompareAndSwap(_ context.Context, old, next StepRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Revision != old.Revision {
		return false, nil
	}
	s.record = next
	return true, nil
}

type fakeChannel struct {
	submits, lookups, polls, results int
	call                             Call
	result                           []byte
	submitErr, lookupErr             error
	onSubmit                         func()
}

func (c *fakeChannel) Submit(context.Context, string, FrozenRequest, []byte) (Call, error) {
	c.submits++
	if c.onSubmit != nil {
		c.onSubmit()
	}
	return c.call, c.submitErr
}
func (c *fakeChannel) Lookup(context.Context, string, string) (Call, error) {
	c.lookups++
	return c.call, c.lookupErr
}
func (c *fakeChannel) Poll(context.Context, string, string) (Call, error) {
	c.polls++
	return c.call, nil
}
func (c *fakeChannel) Result(context.Context, string, string) ([]byte, error) {
	c.results++
	return c.result, nil
}
func completeCall() Call {
	yes := true
	return Call{SchemaVersion: 1, Binding: Binding{CallID: execCall, TaskID: execTask, ModelID: "gemini-3.1-pro-preview"}, Media: "analysis", Operation: "analyze", State: "completed", Accepted: &yes}
}
func advance(t *testing.T, e Executor) (StepRecord, error) {
	t.Helper()
	return e.Advance(t.Context(), execOrg, execOwner, execTask, "visual-0-45000")
}

func TestExecutorLostPOSTRecoversOriginalCallAfterRestart(t *testing.T) {
	r, media, result := executorFixture(t)
	store := &memorySteps{record: r}
	channel := &fakeChannel{call: completeCall(), result: result, submitErr: ErrUnavailable}
	reader := func(context.Context, StepRecord) ([]byte, error) { return media, nil }
	e := Executor{store, channel, reader}
	r, err := advance(t, e)
	if !errors.Is(err, ErrUnavailable) || r.State != "acceptance_unknown" || channel.submits != 1 {
		t.Fatal("lost submission not retained", r.State, err)
	}
	// Simulate a new worker process and a still-not-visible original operation.
	channel.lookupErr = ErrMissing
	e = Executor{store, channel, reader}
	r, err = advance(t, e)
	if !errors.Is(err, ErrMissing) || r.State != "acceptance_unknown" || channel.submits != 1 {
		t.Fatal("unknown acceptance resubmitted")
	}
	channel.lookupErr = nil
	r, err = advance(t, e)
	if err != nil || r.State != "observation_ready" || r.CallID != execCall || channel.submits != 1 || channel.lookups != 2 {
		t.Fatal("did not recover original result", r.State, err)
	}
	// Read-only terminal recovery cannot re-fetch or regenerate anything.
	_, err = advance(t, e)
	if err != nil || channel.results != 1 || channel.submits != 1 {
		t.Fatal("terminal recovery issued work")
	}
}
func TestExecutorClaimPrecedesNetworkAndSecondWorkerNeverSubmits(t *testing.T) {
	r, media, result := executorFixture(t)
	store := &memorySteps{record: r}
	c := &fakeChannel{call: completeCall(), result: result}
	reader := func(context.Context, StepRecord) ([]byte, error) { return media, nil }
	e := Executor{store, c, reader}
	c.onSubmit = func() {
		if store.record.State != "dispatching" {
			t.Fatal("POST before durable intent")
		}
		other := &fakeChannel{call: completeCall(), result: result}
		_, err := advance(t, Executor{store, other, reader})
		if err != nil || other.submits != 0 || other.lookups != 1 {
			t.Fatal("second worker submitted", err)
		}
	}
	_, err := advance(t, e)
	if !errors.Is(err, ErrChanged) || store.record.State != "observation_ready" || c.submits != 1 {
		t.Fatal("stale response replaced newer state", err)
	}
}
func TestExecutorRejectsWrongBindingsAndPreservesInvalidOutput(t *testing.T) {
	for _, mode := range []string{"task", "model", "call", "shape", "rejected", "media"} {
		t.Run(mode, func(t *testing.T) {
			r, media, result := executorFixture(t)
			store := &memorySteps{record: r}
			c := &fakeChannel{call: completeCall(), result: result}
			switch mode {
			case "task":
				c.call.TaskID = execOwner
			case "model":
				c.call.ModelID = "other"
			case "call":
				store.record.State = "running"
				store.record.CallID = execOwner
			case "shape":
				c.result = []byte(`{"schema_version":1}`)
			case "rejected":
				c.submitErr = ErrRejected
			case "media":
				media = []byte("changed")
			}
			got, err := advance(t, Executor{store, c, func(context.Context, StepRecord) ([]byte, error) { return media, nil }})
			if mode == "media" {
				if !errors.Is(err, ErrInvalid) || c.submits != 0 || got.State != "planned" {
					t.Fatal("changed bytes submitted")
				}
				return
			}
			if err != nil || got.State != "failed" {
				t.Fatal("unsafe observation accepted", got.State, err)
			}
			if mode == "shape" && (len(got.Result) == 0 || got.Report == nil || got.Report.Passed) {
				t.Fatal("invalid original output discarded")
			}
		})
	}
}
func TestFrozenRequestSurvivesJSONBKeyReorderingAndPreservesOldPrompt(t *testing.T) {
	r, _, _ := executorFixture(t)
	original := r.Request.Digest()
	var p map[string]any
	json.Unmarshal(r.Request.Payload, &p)
	p["prompt"] = "fixed earlier prompt, no regeneration on resume"
	r.Request.Payload, _ = json.Marshal(p)
	changed := r.Request.Digest()
	if changed == original {
		t.Fatal("prompt excluded from digest")
	}
	var v any
	data, _ := json.Marshal(r.Request)
	json.Unmarshal(data, &v)
	data, _ = json.MarshalIndent(v, "", " ")
	var f FrozenRequest
	json.Unmarshal(data, &f)
	// JSONB can reorder object keys, including the embedded payload.
	var payload map[string]json.RawMessage
	json.Unmarshal(f.Payload, &payload)
	var reordered bytes.Buffer
	reordered.WriteByte('{')
	for i, k := range []string{"video_range", "video_fps", "response_schema", "prompt", "max_output_tokens"} {
		if i > 0 {
			reordered.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		reordered.Write(key)
		reordered.WriteByte(':')
		reordered.Write(payload[k])
	}
	reordered.WriteByte('}')
	f.Payload = reordered.Bytes()
	if f.Validate() != nil || f.Digest() != changed {
		t.Fatal("jsonb changed frozen identity")
	}
	p["media"] = map[string]string{"data": "forbidden"}
	f.Payload, _ = json.Marshal(p)
	if f.Validate() == nil {
		t.Fatal("media bytes entered frozen database record")
	}
}
