package analysis

import (
	"context"
	"encoding/json"
	"errors"
)

type PromptStore interface {
	LoadPrompt(context.Context, string, string, string, string) (PromptRecord, error)
	SwapPrompt(context.Context, PromptRecord, PromptRecord) (bool, error)
}
type PromptChannel interface {
	SubmitPrompts(context.Context, string, PromptRequest) (Call, error)
	Lookup(context.Context, string, string) (Call, error)
	Poll(context.Context, string, string) (Call, error)
	Result(context.Context, string, string) ([]byte, error)
}
type PromptExecutor struct {
	Store   PromptStore
	Channel PromptChannel
}

// Advance consumes a freshly authorized immutable bundle. Recovery always uses
// the saved request and original operation/call, never a regenerated prompt.
func (e PromptExecutor) Advance(ctx context.Context, org, owner, task, key string, b Bundle, a Association, dispatch bool) (PromptRecord, error) {
	if e.Store == nil || e.Channel == nil || b.TaskID != task || !IsPromptBatchKey(key) {
		return PromptRecord{}, ErrInvalid
	}
	r, err := e.Store.LoadPrompt(ctx, org, owner, task, key)
	if err != nil {
		return r, err
	}
	if r.Validate() != nil || r.OrganizationID != org || r.OwnerID != owner || r.Request.TaskID != task || r.Request.Batch.Key != key || r.Request.Batch.BundleHash != b.SHA256 {
		return r, ErrInvalid
	}
	text, err := promptText(b, a, r.Request.Batch)
	if err != nil || hashBytes([]byte(text)) != r.Request.TextHash {
		return r, ErrInvalid
	}
	if r.State == "failed" {
		return r, nil
	}
	if r.State == "prompt_ready" {
		_, err = VerifyPromptsNative(b, a, r.Request, r.CallID, r.Result)
		return r, err
	}
	var call Call
	if r.State == "planned" {
		if !dispatch {
			return r, nil
		}
		next := r
		next.State = "dispatching"
		r, err = e.save(ctx, r, next)
		if err != nil {
			return r, err
		}
		call, err = e.Channel.SubmitPrompts(ctx, org, r.Request)
	} else if r.CallID == "" {
		call, err = e.Channel.Lookup(ctx, org, r.Request.OperationID)
	} else {
		call, err = e.Channel.Poll(ctx, org, r.CallID)
	}
	if errors.Is(err, ErrRejected) {
		next := r
		next.State = "failed"
		next.ErrorCode = "native_rejected"
		return e.save(ctx, r, next)
	}
	if err != nil {
		next := r
		next.State = "acceptance_unknown"
		saved, saveErr := e.save(ctx, r, next)
		if saveErr != nil {
			return saved, saveErr
		}
		return saved, err
	}
	if !call.matches(r.Request.TaskID, r.Request.ModelID, r.CallID) {
		next := r
		next.State = "failed"
		next.ErrorCode = "native_binding_mismatch"
		return e.save(ctx, r, next)
	}
	next := r
	next.CallID = call.CallID
	switch call.State {
	case "queued", "in_queue", "in_progress", "completed":
		next.State = "running"
	case "acceptance_unknown":
		next.State = "acceptance_unknown"
	case "failed", "rejected":
		next.State = "failed"
		next.ErrorCode = "native_" + call.State
	}
	r, err = e.save(ctx, r, next)
	if err != nil || call.State != "completed" {
		return r, err
	}
	result, err := e.Channel.Result(ctx, org, r.CallID)
	if err != nil {
		return r, err
	}
	next = r
	next.Result = json.RawMessage(result)
	if _, err := VerifyPromptsNative(b, a, r.Request, r.CallID, result); err != nil {
		next.State = "failed"
		next.ErrorCode = "prompt_invalid"
	} else {
		next.State = "prompt_ready"
	}
	return e.save(ctx, r, next)
}
func (e PromptExecutor) save(ctx context.Context, old, next PromptRecord) (PromptRecord, error) {
	next.Revision = old.Revision + 1
	if next.Validate() != nil {
		return old, ErrInvalid
	}
	ok, err := e.Store.SwapPrompt(ctx, old, next)
	if err != nil {
		return old, err
	}
	if !ok {
		return old, ErrChanged
	}
	return next, nil
}
