package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

var (
	ErrUnavailable = errors.New("analysis dependency unavailable")
	ErrMissing     = errors.New("analysis record not found")
	ErrChanged     = errors.New("analysis record changed")
)

// FrozenRequest contains no credentials, source bytes or externally supplied
// receipt. It is built by the authorized worker after media preparation.
type FrozenRequest struct {
	TaskID      string          `json:"task_id"`
	OperationID string          `json:"idempotency_key"`
	ModelID     string          `json:"model_id"`
	AccountID   string          `json:"account_id,omitempty"`
	BudgetLimit string          `json:"budget_limit"`
	Step        Step            `json:"step"`
	Prepared    Prepared        `json:"prepared"`
	MediaSize   int64           `json:"media_size"`
	Payload     json.RawMessage `json:"payload"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,199}$`)
var positiveAmount = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,8})?$`)

func validUUID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id.String() == value && id != uuid.Nil
}

func Freeze(taskID, model, account, budget string, step Step, prepared Prepared, size int64) (FrozenRequest, error) {
	payload, err := step.Payload()
	if err != nil {
		return FrozenRequest{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return FrozenRequest{}, err
	}
	f := FrozenRequest{TaskID: taskID, OperationID: taskID + ":" + step.Key, ModelID: model, AccountID: account, BudgetLimit: budget, Step: step, Prepared: prepared, MediaSize: size, Payload: raw}
	return f, f.Validate()
}

func (f FrozenRequest) Validate() error {
	if !validUUID(f.TaskID) || f.OperationID != f.TaskID+":"+f.Step.Key || !identifier.MatchString(f.ModelID) ||
		f.AccountID != "" && !validUUID(f.AccountID) || !positiveAmount.MatchString(f.BudgetLimit) || strings.Trim(f.BudgetLimit, "0.") == "" ||
		f.Step.Validate() != nil || f.Prepared.SourceHash != f.Step.SourceHash || !digestPattern.MatchString(f.Prepared.MediaHash) ||
		f.Prepared.Range != f.Step.Input || f.MediaSize < 1 || f.MediaSize > 48<<20 || len(f.Payload) > 64<<10 {
		return ErrInvalid
	}
	if f.Step.Kind == "visual" && (f.Prepared.MIME != "video/mp4" || f.Prepared.MediaHash != f.Step.SourceHash) ||
		f.Step.Kind == "audio" && f.Prepared.MIME != "audio/wav" && f.Prepared.MIME != "audio/mpeg" {
		return ErrInvalid
	}
	var p struct {
		Prompt string         `json:"prompt"`
		Schema map[string]any `json:"response_schema"`
		Tokens int            `json:"max_output_tokens"`
		FPS    *float64       `json:"video_fps"`
		Range  *Range         `json:"video_range"`
	}
	clean, err := strictjson.ReadValue(bytes.NewReader(f.Payload), 64<<10)
	if err != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(clean))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || strings.TrimSpace(p.Prompt) == "" || p.Tokens < 1024 || p.Tokens > 32768 {
		return ErrInvalid
	}
	if f.Step.Kind == "visual" && (p.FPS == nil || *p.FPS < 0.1 || *p.FPS > 8 || p.Range == nil || *p.Range != f.Step.Input) ||
		f.Step.Kind == "audio" && (p.FPS != nil || p.Range != nil) {
		return ErrInvalid
	}
	data, e := json.Marshal(p.Schema)
	expected, e2 := f.Step.SchemaHash()
	if e != nil || e2 != nil || hashBytes(data) != expected {
		return ErrInvalid
	}
	return nil
}
func hashBytes(data []byte) string     { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func (f FrozenRequest) Digest() string { return canonicalHash(f) }
func canonicalHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&canonical) != nil {
		return ""
	}
	data, err = json.Marshal(canonical)
	if err != nil {
		return ""
	}
	return hashBytes(data)
}

// StepRecord is private task state, never an asset completion or validation badge.
type StepRecord struct {
	OrganizationID string          `json:"organization_id"`
	OwnerID        string          `json:"owner_id"`
	Request        FrozenRequest   `json:"request"`
	RequestHash    string          `json:"request_sha256"`
	Revision       int64           `json:"revision"`
	State          string          `json:"state"`
	CallID         string          `json:"call_id,omitempty"`
	Result         json.RawMessage `json:"native_result,omitempty"`
	Report         *Report         `json:"report,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
}

func (r StepRecord) Validate() error {
	if !validUUID(r.OrganizationID) || !validUUID(r.OwnerID) || r.Request.Validate() != nil || r.RequestHash != r.Request.Digest() || r.Revision < 1 || r.CallID != "" && !validUUID(r.CallID) {
		return ErrInvalid
	}
	switch r.State {
	case "planned":
		if r.CallID != "" {
			return ErrInvalid
		}
	case "dispatching", "acceptance_unknown":
	case "running":
		if r.CallID == "" {
			return ErrInvalid
		}
	case "observation_ready":
		if r.CallID == "" || len(r.Result) == 0 || r.Report == nil || !r.Report.Passed || len(r.Report.Issues) != 0 || r.ErrorCode != "" {
			return ErrInvalid
		}
	case "failed":
		if r.ErrorCode == "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if r.State != "failed" && r.State != "observation_ready" && (len(r.Result) > 0 || r.Report != nil || r.ErrorCode != "") {
		return ErrInvalid
	}
	return nil
}

type StepStore interface {
	Load(context.Context, string, string, string, string) (StepRecord, error)
	CompareAndSwap(context.Context, StepRecord, StepRecord) (bool, error)
}
type Call struct {
	SchemaVersion int `json:"schema_version"`
	Binding
	Media     string `json:"media"`
	Operation string `json:"operation"`
	State     string `json:"state"`
	Accepted  *bool  `json:"accepted"`
}

func (c Call) valid(f FrozenRequest, callID string) bool {
	return c.matches(f.TaskID, f.ModelID, callID)
}
func (c Call) matches(task, model, callID string) bool {
	if c.SchemaVersion != 1 || !validUUID(c.CallID) || c.TaskID != task || c.ModelID != model || c.Media != "analysis" || c.Operation != "analyze" || callID != "" && c.CallID != callID {
		return false
	}
	switch c.State {
	case "completed", "queued", "in_queue", "in_progress":
		return c.Accepted != nil && *c.Accepted
	case "rejected":
		return c.Accepted != nil && !*c.Accepted
	case "failed", "acceptance_unknown":
		return true
	}
	return false
}

type Channel interface {
	Submit(context.Context, string, FrozenRequest, []byte) (Call, error)
	Lookup(context.Context, string, string) (Call, error)
	Poll(context.Context, string, string) (Call, error)
	Result(context.Context, string, string) ([]byte, error)
}
type MediaReader func(context.Context, StepRecord) ([]byte, error)
type Executor struct {
	Store   StepStore
	Channel Channel
	Media   MediaReader
}

// Advance makes at most one paid submission, only after durable CAS intent.
// Recovery never submits a dispatching/unknown step, including a lost POST response.
func (e Executor) Advance(ctx context.Context, org, owner, task, key string) (StepRecord, error) {
	if e.Store == nil || e.Channel == nil || e.Media == nil {
		return StepRecord{}, ErrInvalid
	}
	r, err := e.Store.Load(ctx, org, owner, task, key)
	if err != nil {
		return r, err
	}
	if r.Validate() != nil || r.OrganizationID != org || r.OwnerID != owner || r.Request.TaskID != task || r.Request.Step.Key != key {
		return r, ErrInvalid
	}
	if r.State == "observation_ready" || r.State == "failed" {
		return r, nil
	}
	var call Call
	if r.State == "planned" {
		media, readErr := e.Media(ctx, r)
		if readErr != nil {
			return r, readErr
		}
		if int64(len(media)) != r.Request.MediaSize || hashBytes(media) != r.Request.Prepared.MediaHash {
			return r, ErrInvalid
		}
		next := r
		next.State = "dispatching"
		next.Revision++
		ok, saveErr := e.Store.CompareAndSwap(ctx, r, next)
		if saveErr != nil {
			return r, saveErr
		}
		if !ok {
			return r, ErrChanged
		}
		r = next
		call, err = e.Channel.Submit(ctx, org, r.Request, media)
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
		// A 404 lookup is not proof that an in-flight request was never accepted.
		next := r
		next.State = "acceptance_unknown"
		saved, saveErr := e.save(ctx, r, next)
		if saveErr != nil {
			return saved, saveErr
		}
		return saved, err
	}
	if !call.valid(r.Request, r.CallID) {
		next := r
		next.State = "failed"
		next.ErrorCode = "native_binding_mismatch"
		return e.save(ctx, r, next)
	}
	// Pin call identity before fetching a potentially large result. A restart
	// now continues by this call, not the operation lookup or a new submission.
	next := r
	next.CallID = call.CallID
	switch call.State {
	case "completed", "queued", "in_queue", "in_progress":
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
	report := VerifyNative(r.Request.Step, r.Request.Prepared, Binding{CallID: r.CallID, TaskID: task, ModelID: r.Request.ModelID}, result)
	next = r
	next.Result = json.RawMessage(result)
	next.Report = &report
	if report.Passed {
		next.State = "observation_ready"
	} else {
		next.State = "failed"
		next.ErrorCode = "observation_invalid"
	}
	return e.save(ctx, r, next)
}
func (e Executor) save(ctx context.Context, old, next StepRecord) (StepRecord, error) {
	next.Revision = old.Revision + 1
	if next.Validate() != nil {
		return old, ErrInvalid
	}
	ok, err := e.Store.CompareAndSwap(ctx, old, next)
	if err != nil {
		return old, err
	}
	if !ok {
		return old, ErrChanged
	}
	return next, nil
}
