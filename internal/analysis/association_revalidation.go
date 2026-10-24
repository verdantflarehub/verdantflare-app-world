package analysis

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

// The native failure is immutable. This stores a separate current-rule verdict,
// with no provider submission or additional budget reservation.
func (s PGStore) revalidateAssociation(ctx context.Context, org, owner, task, id string, old AssociationRecord) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	var pipeline int
	if e = tx.QueryRow(ctx, `SELECT pipeline_version FROM studio.world_analysis_tasks WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 FOR NO KEY UPDATE`, org, owner, task).Scan(&pipeline); e != nil {
		return e
	}
	if pipeline != 2 {
		return ErrInvalid
	}
	var previous string
	e = tx.QueryRow(ctx, `SELECT task_id::text FROM studio.world_analysis_association_validations WHERE organization_id=$1 AND owner_id=$2 AND retry_id=$3`, org, owner, id).Scan(&previous)
	if e == nil {
		if previous != task {
			return ErrChanged
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	var blocked bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_analysis_schedule WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3 AND lease_until>now())`, org, owner, task).Scan(&blocked); e != nil {
		return e
	}
	if blocked {
		return ErrChanged
	}
	if _, e = tx.Exec(ctx, `INSERT INTO studio.world_analysis_association_validations(organization_id,owner_id,task_id,retry_id,request_sha256,call_id,native_result,native_sha256,policy) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'evidence-graph-v2')`, org, owner, task, id, old.RequestHash, old.CallID, old.Result, hashBytes(old.Result)); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now(),last_error_code='',consecutive_errors=0 WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
