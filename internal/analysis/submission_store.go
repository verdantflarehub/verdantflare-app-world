package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const SubmissionStepKey = "source-prepare-v1"

// A delayed worker must not start media preparation using an expired claim.
func (s PGStore) CheckSubmissionLease(ctx context.Context, w Work) error {
	if s.DB == nil || w.StepKey != SubmissionStepKey || !validUUID(w.OrganizationID) || !validUUID(w.OwnerID) || !validUUID(w.TaskID) || !validUUID(w.LeaseID) {
		return ErrInvalid
	}
	var valid bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_analysis_requests WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state='preparing' AND lease_id=$4 AND lease_until>now())`, w.OrganizationID, w.OwnerID, w.TaskID, w.LeaseID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrChanged
	}
	return nil
}

type Submission struct {
	PipelineVersion                                                                                        int
	OrganizationID, OwnerID, TaskID, DraftID, RevisionID, FileID, SourceHash, Hash, State, ErrorCode, Name string
	Request                                                                                                json.RawMessage
	CreatedAt                                                                                              time.Time
}

var ErrSourceTrashed = errors.New("collection is in trash")

func (s PGStore) CreateSubmission(ctx context.Context, r Submission) error {
	if (r.PipelineVersion != 0 && r.PipelineVersion != 2) || s.DB == nil || !validUUID(r.OrganizationID) || !validUUID(r.OwnerID) || !validUUID(r.TaskID) || !validUUID(r.DraftID) || !validUUID(r.RevisionID) || !validUUID(r.FileID) || !digestPattern.MatchString(r.SourceHash) || !digestPattern.MatchString(r.Hash) || !json.Valid(r.Request) {
		return ErrInvalid
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var state string
	err = tx.QueryRow(ctx, `SELECT lifecycle_state FROM studio.world_drafts WHERE organization_id=$1 AND owner_id=$2 AND draft_id=$3 AND purpose='collection' FOR UPDATE`, r.OrganizationID, r.OwnerID, r.DraftID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMissing
	}
	if err != nil {
		return err
	}
	// Accepted work survives trashing; only admission of new work is denied.
	var oldHash, oldSource string
	err = tx.QueryRow(ctx, `SELECT request_sha256,source_sha256 FROM studio.world_analysis_requests WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, r.OrganizationID, r.OwnerID, r.TaskID).Scan(&oldHash, &oldSource)
	if err == nil {
		if oldHash != r.Hash || oldSource != r.SourceHash {
			return ErrChanged
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if state != "active" {
		return ErrSourceTrashed
	}
	_, err = tx.Exec(ctx, `INSERT INTO studio.world_analysis_requests(organization_id,owner_id,task_id,draft_id,revision_id,file_id,source_sha256,request_sha256,request,pipeline_version)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10 WHERE EXISTS(SELECT 1 FROM studio.world_drafts d JOIN studio.world_draft_revisions v USING(organization_id,draft_id) WHERE d.organization_id=$1 AND d.owner_id=$2 AND d.draft_id=$4 AND v.revision_id=$5 AND d.purpose='collection' AND EXISTS(SELECT 1 FROM jsonb_array_elements(v.manifest_json->'files') f WHERE f->>'file_id'=$6::uuid::text))
 ON CONFLICT(organization_id,owner_id,task_id) DO NOTHING`, r.OrganizationID, r.OwnerID, r.TaskID, r.DraftID, r.RevisionID, r.FileID, r.SourceHash, r.Hash, []byte(r.Request), r.PipelineVersion)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	saved, err := s.Submission(ctx, r.OrganizationID, r.OwnerID, r.TaskID)
	if err != nil {
		return err
	}
	if saved.Hash != r.Hash || saved.SourceHash != r.SourceHash {
		return ErrChanged
	}
	return nil
}
func (s PGStore) Submission(ctx context.Context, org, owner, id string) (Submission, error) {
	r := Submission{OrganizationID: org, OwnerID: owner, TaskID: id}
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(id) {
		return r, ErrInvalid
	}
	err := s.DB.QueryRow(ctx, `SELECT q.draft_id::text,q.revision_id::text,q.file_id::text,q.source_sha256,q.request_sha256,q.request,q.state,q.error_code,q.created_at,COALESCE(v.manifest_json->>'name',''),q.pipeline_version FROM studio.world_analysis_requests q JOIN studio.world_draft_revisions v ON v.organization_id=q.organization_id AND v.draft_id=q.draft_id AND v.revision_id=q.revision_id WHERE q.organization_id=$1 AND q.owner_id=$2 AND q.task_id=$3`, org, owner, id).Scan(&r.DraftID, &r.RevisionID, &r.FileID, &r.SourceHash, &r.Hash, &r.Request, &r.State, &r.ErrorCode, &r.CreatedAt, &r.Name, &r.PipelineVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrMissing
	}
	return r, err
}
func (s PGStore) SubmissionIDs(ctx context.Context, org, owner, before string, limit int) ([]string, error) {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || before != "" && !validUUID(before) || limit < 1 || limit > 51 {
		return nil, ErrInvalid
	}
	rows, err := s.DB.Query(ctx, `SELECT q.task_id::text FROM studio.world_analysis_requests q JOIN studio.world_drafts d ON d.organization_id=q.organization_id AND d.draft_id=q.draft_id AND d.owner_id=q.owner_id WHERE q.organization_id=$1 AND q.owner_id=$2 AND ($3='' OR q.task_id<NULLIF($3,'')::uuid) ORDER BY q.task_id DESC LIMIT $4`, org, owner, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s PGStore) claimSubmission(ctx context.Context) (Work, error) {
	w := Work{StepKey: SubmissionStepKey}
	id, err := newLeaseID()
	if err != nil {
		return w, err
	}
	w.LeaseID = id
	err = s.DB.QueryRow(ctx, `WITH picked AS(SELECT organization_id,owner_id,task_id FROM studio.world_analysis_requests WHERE state IN ('pending','preparing') AND next_attempt_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY next_attempt_at,task_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE studio.world_analysis_requests q SET state='preparing',lease_id=$1,lease_until=now()+interval '25 minutes',updated_at=now() FROM picked p WHERE q.organization_id=p.organization_id AND q.owner_id=p.owner_id AND q.task_id=p.task_id RETURNING q.organization_id::text,q.owner_id::text,q.task_id::text,q.consecutive_errors`, id).Scan(&w.OrganizationID, &w.OwnerID, &w.TaskID, &w.ConsecutiveErrors)
	if errors.Is(err, pgx.ErrNoRows) {
		return Work{}, ErrMissing
	}
	return w, err
}
func (s PGStore) releaseSubmission(ctx context.Context, w Work, code string) error {
	switch code {
	case "", "dependency_unavailable", "source_unavailable", "invalid_observation", "step_failed", "interrupted":
	default:
		return ErrInvalid
	}
	n := 0
	if code != "" {
		n = min(w.ConsecutiveErrors+1, 16)
	}
	tag, err := s.DB.Exec(ctx, `UPDATE studio.world_analysis_requests SET lease_id=NULL,lease_until=NULL,next_attempt_at=now()+make_interval(secs=>$1),consecutive_errors=$2,error_code=CASE WHEN state='failed' THEN error_code ELSE $3 END,updated_at=now() WHERE organization_id=$4 AND owner_id=$5 AND task_id=$6 AND lease_id=$7 AND lease_until>now()`, scheduleDelay(code, n).Seconds(), n, code, w.OrganizationID, w.OwnerID, w.TaskID, w.LeaseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrChanged
	}
	return nil
}
func (s PGStore) FinishSubmission(ctx context.Context, w Work, code string) error {
	state := "queued"
	if code != "" {
		state = "failed"
	}
	tag, err := s.DB.Exec(ctx, `UPDATE studio.world_analysis_requests SET state=$1,error_code=$2,updated_at=now() WHERE organization_id=$3 AND owner_id=$4 AND task_id=$5 AND state='preparing' AND lease_id=$6 AND lease_until>now()`, state, code, w.OrganizationID, w.OwnerID, w.TaskID, w.LeaseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrChanged
	}
	return nil
}
func (s PGStore) ReservedBudget(ctx context.Context, org, owner, id string) (string, error) {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(id) {
		return "", ErrInvalid
	}
	var total string
	err := s.DB.QueryRow(ctx, `SELECT COALESCE(sum((r.request->>'budget_limit')::numeric),0)::text FROM (
 SELECT request FROM studio.world_analysis_steps WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state<>'planned'
 UNION ALL SELECT request FROM studio.world_analysis_associations WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state<>'planned'
 UNION ALL SELECT request FROM studio.world_analysis_prompt_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state<>'planned'
 UNION ALL SELECT request FROM studio.world_analysis_result_batches WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND state<>'planned') r`, org, owner, id).Scan(&total)
	return total, err
}
