package analysis

import (
	"context"
	"encoding/json"
	"errors"
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
	t, err := s.Task(ctx, org, owner, task)
	if err != nil {
		return nil, err
	}
	var plan []PromptBatch
	if t.PipelineVersion == 2 {
		var saved string
		if err = s.DB.QueryRow(ctx, `SELECT plan_sha256 FROM studio.world_analysis_prompt_plans WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task).Scan(&saved); errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMissing
		} else if err != nil {
			return nil, err
		}
		b, a, _, err := s.resultContext(ctx, org, owner, task)
		if err != nil {
			return nil, err
		}
		var hash string
		plan, hash, err = s.PlannedPrompts(ctx, t, b, a)
		if err != nil {
			return nil, err
		}
		if saved != hash || len(keys) != len(plan) {
			return nil, ErrInvalid
		}
	} else if len(keys) == 0 {
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
	if len(records) == 0 {
		return records, nil
	}
	first := records[0].Request
	if t.PipelineVersion != 2 {
		b, a, _, err := s.resultContext(ctx, org, owner, task)
		if err != nil {
			return nil, err
		}
		plan, err = PlanPrompts(b, a, AllPromptScopes(a), t.PromptTargets)
		if err != nil {
			return nil, err
		}
	}
	if len(records) != len(plan) {
		return nil, ErrInvalid
	}
	for i, r := range records {
		if canonicalHash(r.Request.Batch) != canonicalHash(plan[i]) || r.Request.ModelID != first.ModelID || r.Request.AccountID != first.AccountID || r.Request.BudgetLimit != first.BudgetLimit {
			return nil, ErrInvalid
		}
	}
	return records, nil
}

// All requests become visible in one transaction. A failed insert cannot leave
// an apparently complete prefix of a plan for the scheduler to consume.
func (s PGStore) CreatePromptBatches(ctx context.Context, org, owner string, requests []PromptRequest) error {
	if len(requests) == 0 {
		return ErrInvalid
	}
	return s.SavePromptPlan(ctx, org, owner, requests[0].TaskID, requests)
}

// SavePromptPlan includes a zero-call plan receipt; empty applicability must
// never trigger an endless preparation loop or fabricated native completion.
func (s PGStore) SavePromptPlan(ctx context.Context, org, owner, taskID string, requests []PromptRequest) error {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(taskID) {
		return ErrInvalid
	}
	task, err := s.Task(ctx, org, owner, taskID)
	if err != nil {
		return err
	}
	b, a, ar, err := s.resultContext(ctx, org, owner, taskID)
	if err != nil {
		return err
	}
	plan, hash, err := s.PlannedPrompts(ctx, task, b, a)
	if err != nil {
		return err
	}
	if len(plan) != len(requests) || (task.PipelineVersion != 2 && len(plan) == 0) {
		return ErrInvalid
	}
	for i, f := range requests {
		if f.Validate() != nil || f.TaskID != taskID || canonicalHash(f.Batch) != canonicalHash(plan[i]) || f.ModelID != ar.Request.ModelID || f.AccountID != ar.Request.AccountID || f.BudgetLimit != ar.Request.BudgetLimit {
			return ErrInvalid
		}
		text, err := promptTextEncoded(b, a, f.Batch, f.Encoding)
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
	if err = tx.QueryRow(ctx, `SELECT a.state FROM studio.world_analysis_tasks t JOIN studio.world_analysis_current_associations a USING(organization_id,owner_id,task_id) WHERE t.organization_id=$1 AND t.owner_id=$2 AND t.task_id=$3 FOR NO KEY UPDATE OF t`, org, owner, taskID).Scan(&state); err != nil {
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
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_prompt_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, taskID).Scan(&count); err != nil {
		return err
	}
	if count != len(requests) {
		return ErrChanged
	}
	if task.PipelineVersion == 2 {
		_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_prompt_plans(organization_id,owner_id,task_id,plan_sha256,batch_count) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, org, owner, taskID, hash, len(plan))
		if err != nil {
			return err
		}
		var saved string
		var n int
		if err = tx.QueryRow(ctx, `SELECT plan_sha256,batch_count FROM studio.world_analysis_prompt_plans WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, taskID).Scan(&saved, &n); err != nil {
			return err
		}
		if saved != hash || n != len(plan) {
			return ErrChanged
		}
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
