package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const MaxObservationRetries = 2

type ObservationRetry struct {
	ID                  string  `json:"id"`
	Number              int     `json:"number"`
	PreviousRequestHash string  `json:"previous_request_sha256"`
	PreviousCallID      string  `json:"previous_call_id"`
	Issues              []Issue `json:"issues"`
}

func (f FrozenRequest) validOperation() bool {
	base := f.TaskID + ":" + f.Step.Key
	if f.Retry == nil {
		return f.OperationID == base
	}
	r := f.Retry
	if !validUUID(r.ID) || !validUUID(r.PreviousCallID) || !digestPattern.MatchString(r.PreviousRequestHash) || r.Number < 1 || r.Number > MaxObservationRetries || len(r.Issues) == 0 || len(r.Issues) > 64 {
		return false
	}
	for _, issue := range r.Issues {
		if !repairableIssue(issue.Code) || len(issue.Path) > 512 {
			return false
		}
	}
	return f.OperationID == base+":retry:"+r.ID
}
func repairableIssue(code string) bool {
	switch code {
	case "invalid_json", "invalid_schema", "invalid_time", "output_scope_mismatch", "empty_observation", "range_outside_source", "text_outside_shot", "timeline_gap", "timeline_overlap", "timeline_incomplete", "duplicate_or_empty_key", "unresolved_reference", "invalid_continuation", "missing_dimension", "missing_speech", "missing_music", "missing_sound_effect", "missing_mixed_layers", "audio_layer_conflict":
		return true
	}
	return false
}

// RetryRequest creates a new paid operation. It never changes the original
// native output, terminal report, source, model, schema or per-call budget.
func RetryRequest(old StepRecord, retryID string) (FrozenRequest, error) {
	if old.Validate() != nil || !validUUID(retryID) || old.State != "failed" || old.ErrorCode != "observation_invalid" || old.Report == nil || old.Report.Passed || len(old.Result) == 0 {
		return FrozenRequest{}, ErrInvalid
	}
	n := 1
	if old.Request.Retry != nil {
		n = old.Request.Retry.Number + 1
	}
	if n > MaxObservationRetries {
		return FrozenRequest{}, ErrInvalid
	}
	// Integrity and identity failures are not semantic model repair candidates.
	verified := VerifyNative(old.Request.Step, old.Request.Prepared, Binding{CallID: old.CallID, TaskID: old.Request.TaskID, ModelID: old.Request.ModelID}, old.Result)
	for _, issue := range verified.Issues {
		if !repairableIssue(issue.Code) {
			return FrozenRequest{}, ErrInvalid
		}
	}
	next := old.Request
	next.Retry = &ObservationRetry{ID: retryID, Number: n, PreviousRequestHash: old.RequestHash, PreviousCallID: old.CallID, Issues: append([]Issue(nil), old.Report.Issues...)}
	next.OperationID = next.TaskID + ":" + next.Step.Key + ":retry:" + retryID
	var payload map[string]json.RawMessage
	if json.Unmarshal(old.Request.Payload, &payload) != nil {
		return FrozenRequest{}, ErrInvalid
	}
	var prompt string
	if json.Unmarshal(payload["prompt"], &prompt) != nil {
		return FrozenRequest{}, ErrInvalid
	}
	issues, _ := json.Marshal(next.Retry.Issues)
	prompt += fmt.Sprintf("\n原生分析语义校验未通过（第 %d 次重试）：%s。重新读取同一原片范围并输出完整 JSON。修正这些问题，不能省略画面或声音。所有引用键必须来自本次实际输出的实体、镜头或说话者列表；不能用时间点凭空构造键。时间覆盖与音频层须相互一致，不得补造未知事实。", n, string(issues))
	payload["prompt"], _ = json.Marshal(prompt)
	next.Payload, _ = json.Marshal(payload)
	return next, next.Validate()
}

func readStep(row pgx.Row, org, owner string) (StepRecord, error) {
	r := StepRecord{OrganizationID: org, OwnerID: owner}
	var request, report []byte
	err := row.Scan(&request, &r.RequestHash, &r.Revision, &r.State, &r.CallID, &r.Result, &report, &r.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrMissing
	}
	if err != nil {
		return r, err
	}
	if json.Unmarshal(request, &r.Request) != nil || len(report) > 0 && json.Unmarshal(report, &r.Report) != nil || r.Validate() != nil {
		return r, ErrInvalid
	}
	return r, nil
}

// RetryObservation is owner-scoped, idempotent and serialized against both
// retry admission and the task budget. It cannot resurrect an unknown call.
func (s PGStore) RetryObservation(ctx context.Context, org, owner, task, retryID string) error {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(task) || !validUUID(retryID) {
		return ErrInvalid
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var pipeline int
	if err = tx.QueryRow(ctx, `SELECT pipeline_version FROM studio.world_analysis_tasks WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 FOR NO KEY UPDATE`, org, owner, task).Scan(&pipeline); errors.Is(err, pgx.ErrNoRows) {
		return ErrMissing
	} else if err != nil {
		return err
	}
	var priorTask string
	err = tx.QueryRow(ctx, `SELECT task_id::text FROM studio.world_analysis_step_retries WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3`, org, owner, retryID).Scan(&priorTask)
	if err == nil {
		if priorTask != task {
			return ErrChanged
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if pipeline != 2 {
		return ErrInvalid
	}
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_analysis_current_steps WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state IN ('dispatching','running','acceptance_unknown')) OR EXISTS(SELECT 1 FROM studio.world_analysis_associations WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3) OR EXISTS(SELECT 1 FROM studio.world_analysis_schedule WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND lease_until>now())`, org, owner, task).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return ErrChanged
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_current_steps WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state='failed'`, org, owner, task).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ErrInvalid
	}
	old, err := readStep(tx.QueryRow(ctx, `SELECT request,request_sha256,revision,state,COALESCE(call_id::text,''),native_result,report,COALESCE(error_code,'') FROM studio.world_analysis_current_steps WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state='failed'`, org, owner, task), org, owner)
	if err != nil {
		return err
	}
	next, err := RetryRequest(old, retryID)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(next)
	_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_step_retries(organization_id,owner_id,task_id,step_key,request_sha256,request,attempt,retry_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, org, owner, task, next.Step.Key, next.Digest(), raw, next.Retry.Number, retryID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now(),last_error_code=CASE WHEN last_error_code='budget_exhausted' THEN last_error_code ELSE '' END,consecutive_errors=0 WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
