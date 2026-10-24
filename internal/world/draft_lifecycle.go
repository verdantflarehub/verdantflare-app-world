package world

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Directory state is mutable; never embed it in an immutable content manifest.
type DraftLifecycle struct {
	State      string     `json:"state"`
	Version    int64      `json:"version"`
	TrashedAt  *time.Time `json:"trashed_at"`
	TrashedBy  *string    `json:"trashed_by"`
	CanTrash   bool       `json:"can_trash"`
	CanRestore bool       `json:"can_restore"`
}
type DraftLifecycleResult struct {
	Kind        string         `json:"kind"`
	DraftID     string         `json:"draft_id"`
	OperationID string         `json:"operation_id"`
	Lifecycle   DraftLifecycle `json:"lifecycle"`
}

func (v *DraftLifecycle) permissions(purpose string) {
	v.CanTrash = purpose == "collection" && v.State == "active"
	v.CanRestore = purpose == "collection" && v.State == "trashed"
	if v.TrashedAt != nil {
		t := v.TrashedAt.UTC()
		v.TrashedAt = &t
	}
}
func readDraftLifecycle(ctx context.Context, q queryer, p Principal, id string, lock bool) (DraftLifecycle, string, error) {
	var v DraftLifecycle
	var purpose string
	sql := `SELECT lifecycle_state,lifecycle_version,trashed_at,trashed_by::text,purpose FROM studio.world_drafts WHERE organization_id=$1 AND owner_id=$2 AND draft_id=$3`
	if lock {
		sql += ` FOR UPDATE`
	}
	err := q.QueryRow(ctx, sql, p.OrganizationID, p.SubjectID, id).Scan(&v.State, &v.Version, &v.TrashedAt, &v.TrashedBy, &purpose)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	v.permissions(purpose)
	return v, purpose, err
}
func (s *Service) changeDraftLifecycle(ctx context.Context, p Principal, r DraftRequest) (DraftLifecycleResult, error) {
	out := DraftLifecycleResult{Kind: "lifecycle", DraftID: r.DraftID, OperationID: r.OperationID}
	hash, _ := digest(r)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err = advisory(ctx, tx, "world-collection-operation:"+p.OrganizationID+":"+p.SubjectID+":"+r.OperationID); err != nil {
		return out, err
	}
	// Recheck ownership even for a replay; an old receipt grants no new access.
	v, purpose, err := readDraftLifecycle(ctx, tx, p, r.DraftID, true)
	if err != nil {
		return out, err
	}
	if purpose != "collection" {
		return out, ErrInvalid
	}
	var oldHash string
	var old []byte
	err = tx.QueryRow(ctx, `SELECT request_sha256,result FROM studio.world_collection_operations WHERE organization_id=$1 AND owner_id=$2 AND operation_id=$3`, p.OrganizationID, p.SubjectID, r.OperationID).Scan(&oldHash, &old)
	if err == nil {
		if hash != oldHash {
			return out, ErrIdempotency
		}
		if json.Unmarshal(old, &out) != nil {
			return out, ErrNotReady
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if r.ExpectedLifecycleVersion == nil || v.Version != *r.ExpectedLifecycleVersion || (r.Mode == "trash" && !v.CanTrash) || (r.Mode == "restore" && !v.CanRestore) {
		return out, ErrConflict
	}
	if v.Version >= 9007199254740991 {
		return out, ErrConflict
	}
	state := "active"
	if r.Mode == "trash" {
		state = "trashed"
	}
	err = tx.QueryRow(ctx, `UPDATE studio.world_drafts SET lifecycle_state=$4,lifecycle_version=lifecycle_version+1,trashed_at=CASE WHEN $4='trashed' THEN now() ELSE NULL END,trashed_by=CASE WHEN $4='trashed' THEN owner_id ELSE NULL END,updated_at=now() WHERE organization_id=$1 AND owner_id=$2 AND draft_id=$3 RETURNING lifecycle_state,lifecycle_version,trashed_at,trashed_by::text`, p.OrganizationID, p.SubjectID, r.DraftID, state).Scan(&v.State, &v.Version, &v.TrashedAt, &v.TrashedBy)
	if err != nil {
		return out, err
	}
	v.permissions(purpose)
	out.Lifecycle = v
	data, _ := json.Marshal(out)
	_, err = tx.Exec(ctx, `INSERT INTO studio.world_collection_operations(organization_id,owner_id,operation_id,draft_id,request_sha256,result) VALUES($1,$2,$3,$4,$5,$6)`, p.OrganizationID, p.SubjectID, r.OperationID, r.DraftID, hash, data)
	if err != nil {
		return out, err
	}
	if err = audit(ctx, tx, p, "world.draft."+r.Mode, r.DraftID); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
