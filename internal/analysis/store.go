package analysis

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Task is created only by the World worker after source authorization, byte
// verification and media probing. It cannot be created by untrusted MCP JSON.
type Task struct {
	OrganizationID string `json:"organization_id"`
	OwnerID        string `json:"owner_id"`
	TaskID         string `json:"task_id"`
	DraftID        string `json:"draft_id"`
	RevisionID     string `json:"revision_id"`
	FileID         string `json:"file_id"`
	Plan           Plan   `json:"plan"`
}
type PGStore struct{ DB *pgxpool.Pool }

func (s PGStore) Create(ctx context.Context, t Task, requests []FrozenRequest) error {
	if s.DB == nil {
		return ErrInvalid
	}
	for _, id := range []string{t.OrganizationID, t.OwnerID, t.TaskID, t.DraftID, t.RevisionID, t.FileID} {
		if !validUUID(id) {
			return ErrInvalid
		}
	}
	expected, err := PlanVideo(t.Plan.Source)
	if err != nil {
		return err
	}
	plan, _ := json.Marshal(t.Plan)
	correct, _ := json.Marshal(expected)
	if string(plan) != string(correct) || len(requests) != len(t.Plan.Steps) {
		return ErrInvalid
	}
	for i, f := range requests {
		if f.Validate() != nil || f.TaskID != t.TaskID || f.Step != t.Plan.Steps[i] {
			return ErrInvalid
		}
	}
	digest := canonicalHash(struct {
		Task     Task
		Requests []FrozenRequest
	}{t, requests})
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// Recheck owner, collection and file membership transactionally. Artifact
	// source digest/probe verification is performed before entering this store.
	var authorized bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_drafts d JOIN studio.world_draft_revisions r USING(organization_id,draft_id) WHERE d.organization_id=$1 AND d.owner_id=$2 AND d.draft_id=$3 AND d.purpose='collection' AND r.revision_id=$4 AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.manifest_json->'files') f WHERE f->>'file_id'=$5))`, t.OrganizationID, t.OwnerID, t.DraftID, t.RevisionID, t.FileID).Scan(&authorized)
	if err != nil {
		return err
	}
	if !authorized {
		return ErrMissing
	}
	_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_tasks(organization_id,owner_id,task_id,draft_id,revision_id,file_id,request_sha256,plan) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(organization_id,owner_id,task_id) DO NOTHING`, t.OrganizationID, t.OwnerID, t.TaskID, t.DraftID, t.RevisionID, t.FileID, digest, plan)
	if err != nil {
		return err
	}
	var saved string
	if err = tx.QueryRow(ctx, `SELECT request_sha256 FROM studio.world_analysis_tasks WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, t.OrganizationID, t.OwnerID, t.TaskID).Scan(&saved); err != nil {
		return err
	}
	if saved != digest {
		return ErrChanged
	}
	for _, f := range requests {
		raw, _ := json.Marshal(f)
		_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_steps(organization_id,owner_id,task_id,step_key,request_sha256,request) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(organization_id,owner_id,task_id,step_key) DO NOTHING`, t.OrganizationID, t.OwnerID, t.TaskID, f.Step.Key, f.Digest(), raw)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s PGStore) Load(ctx context.Context, org, owner, task, key string) (StepRecord, error) {
	r := StepRecord{OrganizationID: org, OwnerID: owner}
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(task) || !identifier.MatchString(key) {
		return r, ErrInvalid
	}
	var request, report []byte
	err := s.DB.QueryRow(ctx, `SELECT request,request_sha256,revision,state,COALESCE(call_id::text,''),native_result,report,COALESCE(error_code,'') FROM studio.world_analysis_steps WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND step_key=$4`, org, owner, task, key).Scan(&request, &r.RequestHash, &r.Revision, &r.State, &r.CallID, &r.Result, &report, &r.ErrorCode)
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
func (s PGStore) CompareAndSwap(ctx context.Context, old, next StepRecord) (bool, error) {
	if s.DB == nil || old.Validate() != nil || next.Validate() != nil || next.Revision != old.Revision+1 || old.OrganizationID != next.OrganizationID || old.OwnerID != next.OwnerID || old.RequestHash != next.RequestHash || old.State == "failed" || old.State == "observation_ready" || old.CallID != "" && old.CallID != next.CallID {
		return false, ErrInvalid
	}
	var report []byte
	if next.Report != nil {
		report, _ = json.Marshal(next.Report)
	}
	tag, err := s.DB.Exec(ctx, `UPDATE studio.world_analysis_steps SET revision=$1,state=$2,call_id=NULLIF($3,'')::uuid,native_result=$4,report=$5,error_code=NULLIF($6,''),updated_at=now() WHERE organization_id=$7 AND owner_id=$8 AND task_id=$9 AND step_key=$10 AND revision=$11 AND request_sha256=$12`, next.Revision, next.State, next.CallID, []byte(next.Result), report, next.ErrorCode, old.OrganizationID, old.OwnerID, old.Request.TaskID, old.Request.Step.Key, old.Revision, old.RequestHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s PGStore) Task(ctx context.Context, org, owner, id string) (Task, error) {
	t := Task{OrganizationID: org, OwnerID: owner, TaskID: id}
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(id) {
		return t, ErrInvalid
	}
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT draft_id::text,revision_id::text,file_id::text,plan FROM studio.world_analysis_tasks WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, id).Scan(&t.DraftID, &t.RevisionID, &t.FileID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrMissing
	}
	if err != nil {
		return t, err
	}
	if json.Unmarshal(raw, &t.Plan) != nil {
		return t, ErrInvalid
	}
	expected, err := PlanVideo(t.Plan.Source)
	if err != nil || canonicalHash(t.Plan) != canonicalHash(expected) {
		return t, ErrInvalid
	}
	return t, nil
}
