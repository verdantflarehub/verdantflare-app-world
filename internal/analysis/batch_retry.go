package analysis

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

type BatchRetry struct {
	ID                  string          `json:"id"`
	Number              int             `json:"number"`
	PreviousRequestHash string          `json:"previous_request_sha256"`
	PreviousCallID      string          `json:"previous_call_id"`
	FailureReceipt      json.RawMessage `json:"failure_receipt,omitempty"`
}

func validBatchOperation(task, key, operation string, version int, r *BatchRetry) bool {
	base := task + ":" + key
	if r == nil {
		return operation == base
	}
	return version == 2 && validUUID(r.ID) && validUUID(r.PreviousCallID) && digestPattern.MatchString(r.PreviousRequestHash) && r.Number >= 1 && r.Number <= 2 && operation == base+":retry:"+r.ID
}

const batchCorrection = "\n上一批原生结果未通过结构或语义校验。完整重新生成本批全部对象；所有 key、rule_key、变量键及结构标识只允许英文字母、数字、下划线、点或短横线，首字母或数字开头，禁止用中文作标识；title、purpose、description 与正文使用中文。scope_key、target_key、batch_key 及已有证据编号逐字保留，不增删对象，不省略 scope 中的必需证据。逐项核对 Schema、时间范围、输入输出引用和本批用途；简洁完整输出，不截断、不填造证据或可用性。"

func resultRetryInstruction(version int, r *BatchRetry) string {
	s := resultInstructionFor(version)
	if r != nil {
		s += batchCorrection
	}
	return s
}
func nextBatchRetry(id, hash, call string, previous *BatchRetry) (*BatchRetry, error) {
	n := 1
	if previous != nil {
		n = previous.Number + 1
	}
	if !validUUID(id) || !validUUID(call) || !digestPattern.MatchString(hash) || n > 2 {
		return nil, ErrInvalid
	}
	return &BatchRetry{ID: id, Number: n, PreviousRequestHash: hash, PreviousCallID: call}, nil
}
func verifyBatchFailure(kind, code, call, task, model, textHash string, schema map[string]any, native []byte) error {
	if code == "native_failed" {
		return verifyTruncatedAssociation(Binding{call, task, model}, textHash, schema, native)
	}
	if code != kind+"_invalid" {
		return ErrInvalid
	}
	_, err := verifyTextNative(Binding{call, task, model}, textHash, schema, native)
	return err
}
func RetryResultRequest(b Bundle, a Association, old ResultRecord, id string) (ResultRequest, error) {
	if old.Validate() != nil || old.State != "failed" || old.Request.Batch.Version != 2 {
		return ResultRequest{}, ErrInvalid
	}
	f := old.Request
	text, err := resultTextEncoded(b, a, f.Batch, f.Encoding)
	if err != nil || hashBytes([]byte(text)) != f.TextHash {
		return ResultRequest{}, ErrInvalid
	}
	schema, err := resultSchemaVersion(f.Batch.Type, f.Batch.Version)
	if err != nil {
		return ResultRequest{}, err
	}
	if err = verifyBatchFailure("result", old.ErrorCode, old.CallID, f.TaskID, f.ModelID, f.TextHash, schema, old.Result); err != nil {
		return ResultRequest{}, err
	}
	if _, err = VerifyResultsNative(b, a, f, old.CallID, old.Result); err == nil {
		return ResultRequest{}, ErrInvalid
	}
	f.Retry, err = nextBatchRetry(id, old.RequestHash, old.CallID, f.Retry)
	if err != nil {
		return ResultRequest{}, err
	}
	if old.ErrorCode == "native_failed" {
		f.Retry.FailureReceipt = append(json.RawMessage(nil), old.Result...)
	}
	f.OperationID = f.TaskID + ":" + f.Batch.Key + ":retry:" + id
	var p associationPayload
	if json.Unmarshal(f.Payload, &p) != nil {
		return ResultRequest{}, ErrInvalid
	}
	p.Prompt = resultRetryInstruction(f.Batch.Version, f.Retry)
	f.Payload, err = json.Marshal(p)
	if err != nil {
		return ResultRequest{}, err
	}
	return f, f.Validate()
}
func RetryPromptRequest(b Bundle, a Association, old PromptRecord, id string) (PromptRequest, error) {
	if old.Validate() != nil || old.State != "failed" || old.Request.Batch.Version != 2 {
		return PromptRequest{}, ErrInvalid
	}
	f := old.Request
	text, err := promptTextEncoded(b, a, f.Batch, f.Encoding)
	if err != nil || hashBytes([]byte(text)) != f.TextHash {
		return PromptRequest{}, ErrInvalid
	}
	schema, err := promptSchema()
	if err != nil {
		return PromptRequest{}, err
	}
	if err = verifyBatchFailure("prompt", old.ErrorCode, old.CallID, f.TaskID, f.ModelID, f.TextHash, schema, old.Result); err != nil {
		return PromptRequest{}, err
	}
	if _, err = VerifyPromptsNative(b, a, f, old.CallID, old.Result); err == nil {
		return PromptRequest{}, ErrInvalid
	}
	f.Retry, err = nextBatchRetry(id, old.RequestHash, old.CallID, f.Retry)
	if err != nil {
		return PromptRequest{}, err
	}
	if old.ErrorCode == "native_failed" {
		f.Retry.FailureReceipt = append(json.RawMessage(nil), old.Result...)
	}
	f.OperationID = f.TaskID + ":" + f.Batch.Key + ":retry:" + id
	var p associationPayload
	if json.Unmarshal(f.Payload, &p) != nil {
		return PromptRequest{}, ErrInvalid
	}
	if f.Retry.Number == 1 {
		p.Prompt += batchCorrection
	}
	f.Payload, err = json.Marshal(p)
	if err != nil {
		return PromptRequest{}, err
	}
	return f, f.Validate()
}

func (s PGStore) retryBatch(ctx context.Context, org, owner, task, id string, readers ...AssociationResultReader) error {
	b, a, _, err := s.resultContext(ctx, org, owner, task)
	if err != nil {
		return err
	}
	var kind, key, state, code, call, oldHash string
	var raw json.RawMessage
	// Results precede prompts in v2. Never skip an unfinished earlier batch.
	err = s.DB.QueryRow(ctx, `SELECT kind,batch_key,state FROM (
 SELECT 'result'::text kind,batch_key,state,0 phase,batch_index FROM studio.world_analysis_current_results WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state<>'result_ready'
 UNION ALL SELECT 'prompt'::text kind,batch_key,state,1 phase,batch_index FROM studio.world_analysis_current_prompts WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state<>'prompt_ready'
 ) x ORDER BY phase,batch_index LIMIT 1`, org, owner, task).Scan(&kind, &key, &state)
	if err != nil {
		return err
	}
	if state != "failed" {
		return ErrInvalid
	}
	readNative := func(data json.RawMessage) (json.RawMessage, error) {
		if code != "native_failed" || len(data) > 0 {
			return data, nil
		}
		if len(readers) != 1 || readers[0] == nil {
			return nil, ErrUnavailable
		}
		return readers[0].Result(ctx, org, call)
	}
	var retry *BatchRetry
	var hash string
	if kind == "result" {
		old, e := s.LoadResult(ctx, org, owner, task, key)
		if e != nil {
			return e
		}
		code, call, oldHash = old.ErrorCode, old.CallID, old.RequestHash
		old.Result, e = readNative(old.Result)
		if e != nil {
			return e
		}
		next, e := RetryResultRequest(b, a, old, id)
		if e != nil {
			return e
		}
		retry = next.Retry
		hash = next.Digest()
		raw, e = json.Marshal(next)
		if e != nil {
			return e
		}
	} else if kind == "prompt" {
		old, e := s.LoadPrompt(ctx, org, owner, task, key)
		if e != nil {
			return e
		}
		code, call, oldHash = old.ErrorCode, old.CallID, old.RequestHash
		old.Result, e = readNative(old.Result)
		if e != nil {
			return e
		}
		next, e := RetryPromptRequest(b, a, old, id)
		if e != nil {
			return e
		}
		retry = next.Retry
		hash = next.Digest()
		raw, e = json.Marshal(next)
		if e != nil {
			return e
		}
	} else {
		return ErrInvalid
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
	var existing string
	err = tx.QueryRow(ctx, `SELECT task_id::text FROM studio.world_analysis_result_retries WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3 UNION ALL SELECT task_id::text FROM studio.world_analysis_prompt_retries WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3`, org, owner, id).Scan(&existing)
	if err == nil {
		if existing != task {
			return ErrChanged
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_analysis_schedule WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND lease_until>now())`, org, owner, task).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return ErrChanged
	}
	// kind is selected from constant SQL literals, never supplied by a caller.
	var current string
	var index int
	err = tx.QueryRow(ctx, `SELECT request_sha256,batch_index,state FROM studio.world_analysis_current_`+kind+`s WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND batch_key=$4`, org, owner, task, key).Scan(&current, &index, &state)
	if err != nil {
		return err
	}
	if current != oldHash || state != "failed" {
		return ErrChanged
	}
	_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_`+kind+`_retries(organization_id,owner_id,task_id,batch_key,batch_index,request_sha256,request,attempt,retry_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, org, owner, task, key, index, hash, raw, retry.Number, id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now(),last_error_code='',consecutive_errors=0 WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
