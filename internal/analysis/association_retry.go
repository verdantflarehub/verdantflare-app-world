package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type AssociationRetry struct {
	ID                  string          `json:"id"`
	Number              int             `json:"number"`
	PreviousRequestHash string          `json:"previous_request_sha256"`
	PreviousCallID      string          `json:"previous_call_id"`
	Issue               string          `json:"issue"`
	FailureReceipt      json.RawMessage `json:"failure_receipt,omitempty"`
}

func (f AssociationRequest) validOperation() bool {
	base := f.TaskID + ":" + AssociationStepKey
	if f.Retry == nil {
		return f.OperationID == base
	}
	r := f.Retry
	return validUUID(r.ID) && validUUID(r.PreviousCallID) && digestPattern.MatchString(r.PreviousRequestHash) && r.Number >= 1 && r.Number <= 2 && len(r.Issue) > 0 && len(r.Issue) <= 512 && f.OperationID == base+":retry:"+r.ID
}

// An attempt preserves the observation bundle and native failure. Truncation
// recovery may change only the reversible reference transport representation.
func RetryAssociationRequest(b Bundle, old AssociationRecord, id string) (AssociationRequest, error) {
	if old.Validate() != nil || !validUUID(id) || old.State != "failed" || (old.ErrorCode != "association_invalid" && old.ErrorCode != "native_failed") {
		return AssociationRequest{}, ErrInvalid
	}
	f := old.Request
	n := 1
	if f.Retry != nil {
		n = f.Retry.Number + 1
	}
	if n > 2 {
		return AssociationRequest{}, ErrInvalid
	}
	schema, err := associationSchema()
	if err != nil {
		return AssociationRequest{}, err
	}
	text, err := associationRequestText(b, f.Encoding)
	if err != nil || b.SHA256 != f.BundleHash || hashBytes([]byte(text)) != f.TextHash {
		return AssociationRequest{}, ErrInvalid
	}
	issue := ""
	var receipt json.RawMessage
	if old.ErrorCode == "native_failed" {
		if verifyTruncatedAssociation(Binding{old.CallID, f.TaskID, f.ModelID}, f.TextHash, schema, old.Result) != nil {
			return AssociationRequest{}, ErrInvalid
		}
		issue = "MAX_TOKENS: use short evidence references and concise complete JSON"
		receipt = append(json.RawMessage(nil), old.Result...)
	} else {
		_, invalid := VerifyAssociationNative(b, f, old.CallID, old.Result)
		if _, envelope := verifyTextNative(Binding{old.CallID, f.TaskID, f.ModelID}, f.TextHash, schema, old.Result); envelope != nil || invalid == nil {
			return AssociationRequest{}, ErrInvalid
		}
		issue = invalid.Error()
	}
	f.Retry = &AssociationRetry{ID: id, Number: n, PreviousRequestHash: old.RequestHash, PreviousCallID: old.CallID, Issue: issue, FailureReceipt: receipt}
	f.OperationID = f.TaskID + ":" + AssociationStepKey + ":retry:" + id
	var payload associationPayload
	if json.Unmarshal(f.Payload, &payload) != nil {
		return AssociationRequest{}, ErrInvalid
	}
	shots, entities, voices := 0, 0, 0
	for _, node := range b.Nodes {
		switch node.Kind {
		case "shots":
			shots++
		case "entities":
			entities++
		case "speakers":
			voices++
		}
	}
	payload.Prompt += fmt.Sprintf("\n上次关联语义校验未通过：%s。重新完整组织同一观察包，不沿用错误分组。必须核对恰好覆盖 %d 个原始镜头节点、%d 个实体节点、%d 个说话者节点，每个成员恰好一次。shot_groups 是连续镜头，不是剧情段落、场景或蒙太奇；同一片段内不同 shots 绝不合并。仅能在输入 shot_boundaries 明确两侧都 continues 的相邻计算边界合并；叙事、人物、道具或地点相同不构成合并依据。保留原始镜头顺序与起止时码。单成员必须 single，多成员必须 inferred。每组 evidence_keys 含其所有 member_keys；每个 boundary 的 evidence_keys 必须包含 left_shot 和 right_shot。不得省略成员、填造证据键或改成摘要。", issue, shots, entities, voices)
	if old.ErrorCode == "native_failed" || f.Encoding == "short-v1" {
		f.Encoding = "short-v1"
		payload.Text, err = associationRequestText(b, f.Encoding)
		if err != nil {
			return AssociationRequest{}, err
		}
		f.TextHash = hashBytes([]byte(payload.Text))
		payload.Prompt += "\n输入节点使用 e000001 格式的短证据编号，所有结构化引用只使用输入的 key；不得自行生成 obs- 键。简洁输出 JSON，理由只保留必要证据，不重复讲述剧情；完整列出所有镜头，不省略任何成员。"
	}
	f.Payload, err = json.Marshal(payload)
	if err != nil {
		return AssociationRequest{}, err
	}
	return f, f.Validate()
}

// Retry routes a stable id across the two supported phases. Admission takes
// the same task lock as dispatch, and terminal/downstream rows remain intact.
func (s PGStore) Retry(ctx context.Context, org, owner, task, id string, readers ...AssociationResultReader) error {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(task) || !validUUID(id) {
		return ErrInvalid
	}
	var existingTask string
	err := s.DB.QueryRow(ctx, `SELECT task_id::text FROM studio.world_analysis_association_retries WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3`, org, owner, id).Scan(&existingTask)
	if err == nil {
		if existingTask != task {
			return ErrChanged
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	err = s.DB.QueryRow(ctx, `SELECT task_id::text FROM studio.world_analysis_step_retries WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3`, org, owner, id).Scan(&existingTask)
	if err == nil {
		if existingTask != task {
			return ErrChanged
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	r, err := s.LoadAssociation(ctx, org, owner, task)
	if errors.Is(err, ErrMissing) {
		return s.RetryObservation(ctx, org, owner, task, id)
	}
	if err != nil {
		return err
	}
	if r.State == "failed" && r.ErrorCode == "native_failed" && len(r.Result) == 0 {
		if len(readers) != 1 || readers[0] == nil {
			return ErrUnavailable
		}
		r.Result, err = readers[0].Result(ctx, org, r.CallID)
		if err != nil {
			return err
		}
	}
	b, err := s.Bundle(ctx, org, owner, task)
	if err != nil {
		return err
	}
	next, err := RetryAssociationRequest(b, r, id)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var pipeline int
	if err = tx.QueryRow(ctx, `SELECT pipeline_version FROM studio.world_analysis_tasks WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 FOR NO KEY UPDATE`, org, owner, task).Scan(&pipeline); err != nil {
		return err
	}
	if pipeline != 2 {
		return ErrInvalid
	}
	// Recheck idempotence after acquiring the dispatch/admission lock.
	err = tx.QueryRow(ctx, `SELECT task_id::text FROM studio.world_analysis_association_retries WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3 UNION ALL SELECT task_id::text FROM studio.world_analysis_step_retries WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3`, org, owner, id).Scan(&existingTask)
	if err == nil {
		if existingTask != task {
			return ErrChanged
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_analysis_result_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3) OR EXISTS(SELECT 1 FROM studio.world_analysis_prompt_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3) OR EXISTS(SELECT 1 FROM studio.world_analysis_schedule WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND lease_until>now())`, org, owner, task).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return ErrChanged
	}
	var hash, state string
	err = tx.QueryRow(ctx, `SELECT request_sha256,state FROM studio.world_analysis_current_associations WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task).Scan(&hash, &state)
	if err != nil {
		return err
	}
	if hash != r.RequestHash || state != "failed" {
		return ErrChanged
	}
	raw, _ := json.Marshal(next)
	_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_association_retries(organization_id,owner_id,task_id,request_sha256,request,attempt,retry_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, org, owner, task, next.Digest(), raw, next.Retry.Number, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now(),last_error_code=CASE WHEN last_error_code='budget_exhausted' THEN last_error_code ELSE '' END,consecutive_errors=0 WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
