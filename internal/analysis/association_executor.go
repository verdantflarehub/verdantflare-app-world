package analysis

import (
	"context"
	"encoding/json"
	"errors"
)

type AssociationStore interface {
	LoadAssociation(context.Context, string, string, string) (AssociationRecord, error)
	SwapAssociation(context.Context, AssociationRecord, AssociationRecord) (bool, error)
}
type AssociationChannel interface {
	SubmitAssociation(context.Context, string, AssociationRequest) (Call, error)
	Lookup(context.Context, string, string) (Call, error)
	Poll(context.Context, string, string) (Call, error)
	Result(context.Context, string, string) ([]byte, error)
}
type AssociationExecutor struct {
	Store   AssociationStore
	Channel AssociationChannel
}

// Advance consumes a freshly authorized immutable bundle. Recovery always uses
// the saved request and original operation/call, never a regenerated prompt.
func (e AssociationExecutor) Advance(ctx context.Context, org, owner, task string, b Bundle, dispatch bool) (AssociationRecord, error) {
	if e.Store == nil || e.Channel == nil || b.TaskID != task {
		return AssociationRecord{}, ErrInvalid
	}
	r, err := e.Store.LoadAssociation(ctx, org, owner, task)
	if err != nil {
		return r, err
	}
	if r.Validate() != nil || r.OrganizationID != org || r.OwnerID != owner || r.Request.TaskID != task || r.Request.BundleHash != b.SHA256 {
		return r, ErrInvalid
	}
	text, err := associationRequestText(b, r.Request.Encoding)
	if err != nil || hashBytes([]byte(text)) != r.Request.TextHash {
		return r, ErrInvalid
	}
	if r.State == "failed" {
		return r, nil
	}
	if r.State == "association_ready" {
		_, err = VerifyAssociationNative(b, r.Request, r.CallID, r.Result)
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
		call, err = e.Channel.SubmitAssociation(ctx, org, r.Request)
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
	if _, err := VerifyAssociationNative(b, r.Request, r.CallID, result); err != nil {
		next.State = "failed"
		next.ErrorCode = "association_invalid"
	} else {
		next.State = "association_ready"
	}
	return e.save(ctx, r, next)
}
func (e AssociationExecutor) save(ctx context.Context, old, next AssociationRecord) (AssociationRecord, error) {
	next.Revision = old.Revision + 1
	if next.Validate() != nil {
		return old, ErrInvalid
	}
	ok, err := e.Store.SwapAssociation(ctx, old, next)
	if err != nil {
		return old, err
	}
	if !ok {
		return old, ErrChanged
	}
	return next, nil
}
