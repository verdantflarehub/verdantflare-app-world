package analysis

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

const AssociationStepKey = "association-v1"

type AssociationRecord struct {
	OrganizationID string             `json:"organization_id"`
	OwnerID        string             `json:"owner_id"`
	Request        AssociationRequest `json:"request"`
	RequestHash    string             `json:"request_sha256"`
	Revision       int64              `json:"revision"`
	State          string             `json:"state"`
	CallID         string             `json:"call_id,omitempty"`
	Result         json.RawMessage    `json:"native_result,omitempty"`
	ErrorCode      string             `json:"error_code,omitempty"`
}

func (r AssociationRecord) Validate() error {
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
	case "association_ready":
		if r.CallID == "" || len(r.Result) == 0 || r.ErrorCode != "" {
			return ErrInvalid
		}
	case "failed":
		switch r.ErrorCode {
		case "native_rejected", "native_failed", "native_binding_mismatch", "association_invalid":
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if r.State != "failed" && r.State != "association_ready" && (len(r.Result) > 0 || r.ErrorCode != "") {
		return ErrInvalid
	}
	return nil
}

func (s PGStore) CreateAssociation(ctx context.Context, org, owner string, f AssociationRequest) error {
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || f.Validate() != nil {
		return ErrInvalid
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return ErrInvalid
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO studio.world_analysis_associations(organization_id,owner_id,task_id,request_sha256,request) VALUES($1,$2,$3,$4,$5) ON CONFLICT(organization_id,owner_id,task_id) DO NOTHING`, org, owner, f.TaskID, f.Digest(), raw)
	if err != nil {
		return err
	}
	r, err := s.LoadAssociation(ctx, org, owner, f.TaskID)
	if err != nil {
		return err
	}
	if r.RequestHash != f.Digest() {
		return ErrChanged
	}
	return nil
}
func (s PGStore) LoadAssociation(ctx context.Context, org, owner, task string) (AssociationRecord, error) {
	r := AssociationRecord{OrganizationID: org, OwnerID: owner}
	if s.DB == nil || !validUUID(org) || !validUUID(owner) || !validUUID(task) {
		return r, ErrInvalid
	}
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT request,request_sha256,revision,state,COALESCE(call_id::text,''),native_result,COALESCE(error_code,'') FROM studio.world_analysis_associations WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task).Scan(&raw, &r.RequestHash, &r.Revision, &r.State, &r.CallID, &r.Result, &r.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrMissing
	}
	if err != nil {
		return r, err
	}
	if json.Unmarshal(raw, &r.Request) != nil || r.Request.TaskID != task || r.Validate() != nil {
		return r, ErrInvalid
	}
	return r, nil
}
func (s PGStore) SwapAssociation(ctx context.Context, old, next AssociationRecord) (bool, error) {
	if s.DB == nil || old.Validate() != nil || next.Validate() != nil || next.Revision != old.Revision+1 || old.OrganizationID != next.OrganizationID || old.OwnerID != next.OwnerID || old.RequestHash != next.RequestHash || old.State == "association_ready" || old.State == "failed" || old.CallID != "" && old.CallID != next.CallID {
		return false, ErrInvalid
	}
	tag, err := s.DB.Exec(ctx, `UPDATE studio.world_analysis_associations SET revision=$1,state=$2,call_id=NULLIF($3,'')::uuid,native_result=$4,error_code=NULLIF($5,''),updated_at=now() WHERE organization_id=$6 AND owner_id=$7 AND task_id=$8 AND revision=$9 AND request_sha256=$10`, next.Revision, next.State, next.CallID, []byte(next.Result), next.ErrorCode, old.OrganizationID, old.OwnerID, old.Request.TaskID, old.Revision, old.RequestHash)
	if err != nil {
		return false, budgetError(err)
	}
	return tag.RowsAffected() == 1, nil
}
