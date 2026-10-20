package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

const PromptSetupStepKey = "prompts-prepare-v1"

var promptBatchKey = regexp.MustCompile(`^prompts-v1-[0-9]{4}$`)

func IsPromptBatchKey(key string) bool { return promptBatchKey.MatchString(key) }

// PromptRecords returns only complete, internally consistent frozen plans.
// Missing rows cannot make a partial set appear complete.
func (s PGStore) PromptRecords(ctx context.Context, org, owner, task string) ([]PromptRecord, error) {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(task) {
		return nil, ErrInvalid
	}
	rows, err := s.DB.Query(ctx, `SELECT batch_key FROM studio.world_analysis_prompt_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 ORDER BY batch_index`, org, owner, task)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, ErrMissing
	}
	records := []PromptRecord{}
	for _, key := range keys {
		r, err := s.LoadPrompt(ctx, org, owner, task, key)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	first := records[0].Request
	expected := (len(first.Batch.Scopes)*len(first.Batch.Targets) + 7) / 8
	if len(records) != expected {
		return nil, ErrInvalid
	}
	for i, r := range records {
		if r.Request.Batch.Key != fmt.Sprintf("prompts-v1-%04d", i) || r.Request.Batch.PlanHash != first.Batch.PlanHash || r.Request.ModelID != first.ModelID || r.Request.AccountID != first.AccountID || r.Request.BudgetLimit != first.BudgetLimit {
			return nil, ErrInvalid
		}
	}
	return records, nil
}

// All requests become visible in one transaction. A failed insert cannot leave
// an apparently complete prefix of a plan for the scheduler to consume.
func (s PGStore) CreatePromptBatches(ctx context.Context, org, owner string, requests []PromptRequest) error {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || len(requests) == 0 {
		return ErrInvalid
	}
	first := requests[0]
	if first.Validate() != nil {
		return ErrInvalid
	}
	task, err := s.Task(ctx, org, owner, first.TaskID)
	if err != nil {
		return err
	}
	b, err := s.Bundle(ctx, org, owner, first.TaskID)
	if err != nil {
		return err
	}
	ar, err := s.LoadAssociation(ctx, org, owner, first.TaskID)
	if err != nil {
		return err
	}
	if ar.State != "association_ready" {
		return ErrObservationsIncomplete
	}
	a, err := VerifyAssociationNative(b, ar.Request, ar.CallID, ar.Result)
	if err != nil {
		return err
	}
	plan, err := PlanPrompts(b, a, AllPromptScopes(a), task.PromptTargets)
	if err != nil {
		return err
	}
	if len(plan) != len(requests) {
		return ErrInvalid
	}
	for i, f := range requests {
		if f.Validate() != nil || f.TaskID != first.TaskID || canonicalHash(f.Batch) != canonicalHash(plan[i]) || f.ModelID != ar.Request.ModelID || f.AccountID != ar.Request.AccountID || f.BudgetLimit != ar.Request.BudgetLimit {
			return ErrInvalid
		}
		text, err := promptText(b, a, f.Batch)
		if err != nil {
			return err
		}
		if hashBytes([]byte(text)) != f.TextHash {
			return ErrInvalid
		}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM studio.world_analysis_associations WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 FOR UPDATE`, org, owner, first.TaskID).Scan(&state); err != nil {
		return err
	}
	if state != "association_ready" {
		return ErrObservationsIncomplete
	}
	for i, f := range requests {
		raw, _ := json.Marshal(f)
		_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_prompt_batches(organization_id,owner_id,task_id,batch_key,batch_index,request_sha256,request) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(organization_id,owner_id,task_id,batch_key) DO NOTHING`, org, owner, f.TaskID, f.Batch.Key, i, f.Digest(), raw)
		if err != nil {
			return err
		}
		var saved string
		if err = tx.QueryRow(ctx, `SELECT request_sha256 FROM studio.world_analysis_prompt_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND batch_key=$4`, org, owner, f.TaskID, f.Batch.Key).Scan(&saved); err != nil {
			return err
		}
		if saved != f.Digest() {
			return ErrChanged
		}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_prompt_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, first.TaskID).Scan(&count); err != nil {
		return err
	}
	if count != len(requests) {
		return ErrChanged
	}
	return tx.Commit(ctx)
}

type PromptRecord struct {
	OrganizationID string          `json:"organization_id"`
	OwnerID        string          `json:"owner_id"`
	Request        PromptRequest   `json:"request"`
	RequestHash    string          `json:"request_sha256"`
	Revision       int64           `json:"revision"`
	State          string          `json:"state"`
	CallID         string          `json:"call_id,omitempty"`
	Result         json.RawMessage `json:"native_result,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
}

func (r PromptRecord) Validate() error {
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
	case "prompt_ready":
		if r.CallID == "" || len(r.Result) == 0 || r.ErrorCode != "" {
			return ErrInvalid
		}
	case "failed":
		switch r.ErrorCode {
		case "native_rejected", "native_failed", "native_binding_mismatch", "prompt_invalid":
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if r.State != "failed" && r.State != "prompt_ready" && (len(r.Result) > 0 || r.ErrorCode != "") {
		return ErrInvalid
	}
	return nil
}

func (s PGStore) LoadPrompt(ctx context.Context, org, owner, task, key string) (PromptRecord, error) {
	r := PromptRecord{OrganizationID: org, OwnerID: owner}
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(task) || !promptBatchKey.MatchString(key) {
		return r, ErrInvalid
	}
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT request,request_sha256,revision,state,COALESCE(call_id::text,''),native_result,COALESCE(error_code,'') FROM studio.world_analysis_prompt_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND batch_key=$4`, org, owner, task, key).Scan(&raw, &r.RequestHash, &r.Revision, &r.State, &r.CallID, &r.Result, &r.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrMissing
	}
	if err != nil {
		return r, err
	}
	if json.Unmarshal(raw, &r.Request) != nil || r.Request.TaskID != task || r.Request.Batch.Key != key || r.Validate() != nil {
		return r, ErrInvalid
	}
	return r, nil
}
func (s PGStore) SwapPrompt(ctx context.Context, old, next PromptRecord) (bool, error) {
	if s.DB == nil || old.Validate() != nil || next.Validate() != nil || next.Revision != old.Revision+1 || old.OrganizationID != next.OrganizationID || old.OwnerID != next.OwnerID || old.RequestHash != next.RequestHash || old.State == "prompt_ready" || old.State == "failed" || old.CallID != "" && old.CallID != next.CallID {
		return false, ErrInvalid
	}
	tag, err := s.DB.Exec(ctx, `UPDATE studio.world_analysis_prompt_batches SET revision=$1,state=$2,call_id=NULLIF($3,'')::uuid,native_result=$4,error_code=NULLIF($5,''),updated_at=now() WHERE organization_id=$6 AND owner_id=$7 AND task_id=$8 AND revision=$9 AND request_sha256=$10 AND batch_key=$11`, next.Revision, next.State, next.CallID, []byte(next.Result), next.ErrorCode, old.OrganizationID, old.OwnerID, old.Request.TaskID, old.Revision, old.RequestHash, old.Request.Batch.Key)
	if err != nil {
		return false, budgetError(err)
	}
	return tag.RowsAffected() == 1, nil
}
