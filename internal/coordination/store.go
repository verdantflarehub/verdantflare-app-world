package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Authorize runs inside the participant's short transaction, after the durable
// authorization barrier has been locked. It may query only this participant's domain.
type Authorize func(context.Context, pgx.Tx, Operation, Grant) error

type Store struct {
	DB        *pgxpool.Pool
	Domain    string
	Fence     int64
	Authorize Authorize
	prefix    string
}

func NewStore(db *pgxpool.Pool, domain string, fence int64, authorize Authorize) (*Store, error) {
	if db == nil || (domain != "project" && domain != "world") || fence < 1 || authorize == nil {
		return nil, ErrInvalid
	}
	return &Store{DB: db, Domain: domain, Fence: fence, Authorize: authorize, prefix: "studio." + domain + "_coord_"}, nil
}

// Schema contains only this application's coordination data. Call from an
// explicit migration, never from a request or automatic service startup.
func Schema(domain string) (string, error) {
	if domain != "project" && domain != "world" {
		return "", ErrInvalid
	}
	p := "studio." + domain + "_coord_"
	return fmt.Sprintf(`
CREATE TABLE %sfences (coordinator text PRIMARY KEY CHECK(coordinator IN ('project','world')), fence bigint NOT NULL CHECK(fence > 0));
CREATE TABLE %soperations (
 organization_id uuid NOT NULL, resource_id uuid NOT NULL, commit_id uuid NOT NULL,
 operation jsonb NOT NULL, plan jsonb NOT NULL, plan_hash text NOT NULL,
 state text NOT NULL CHECK(state IN ('preparing','committed','cancelled')) DEFAULT 'preparing',
 prepared jsonb NOT NULL DEFAULT '{}', updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,resource_id,commit_id));
CREATE TABLE %sparticipants (
 coordinator text NOT NULL, organization_id uuid NOT NULL, resource_id uuid NOT NULL, commit_id uuid NOT NULL,
 operation jsonb NOT NULL, grants_hash text NOT NULL, state text NOT NULL CHECK(state IN ('prepared','committed','cancelled')),
 PRIMARY KEY(coordinator,organization_id,resource_id,commit_id));
CREATE TABLE %sbarriers (
 organization_id uuid NOT NULL, resource_id uuid NOT NULL, subject_id uuid NOT NULL,
 blocked boolean NOT NULL DEFAULT false, epoch bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(organization_id,resource_id,subject_id));
CREATE TABLE %sreservations (
 coordinator text NOT NULL, organization_id uuid NOT NULL, resource_id uuid NOT NULL, commit_id uuid NOT NULL,
 subject_id uuid NOT NULL, grant_resource_id uuid NOT NULL,
 PRIMARY KEY(coordinator,organization_id,resource_id,commit_id,grant_resource_id));
CREATE INDEX %s_pending_grants ON %sreservations(organization_id,grant_resource_id,subject_id);
CREATE TABLE %soutbox (
 organization_id uuid NOT NULL, resource_id uuid NOT NULL, commit_id uuid NOT NULL, participant text NOT NULL,
 delivered boolean NOT NULL DEFAULT false,
 PRIMARY KEY(organization_id,resource_id,commit_id,participant));`, p, p, p, p, p, domain, p, p), nil
}

func (s *Store) checkFence(ctx context.Context, tx pgx.Tx, o Operation) error {
	// Fence admission is a separately provisioned, non-regressing recovery fact.
	// A request cannot silently raise it, including after a database restore.
	var current int64
	// Lock through the fixed SECURITY DEFINER reader: FOR SHARE otherwise needs
	// UPDATE privileges, which runtime roles must never hold on external fences.
	err := tx.QueryRow(ctx, "SELECT * FROM studio."+s.Domain+"_locked_fence($1)", o.Coordinator).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && current != o.Fence {
		return ErrFenced
	}
	return err
}

// Check fails closed until operators have provisioned the externally fenced
// recovery epoch for both domain identities. Request handling never sets it.
func (s *Store) Check(ctx context.Context, expected map[string]int64) error {
	if expected[s.Domain] != s.Fence || len(expected) != 2 {
		return ErrFenced
	}
	for _, domain := range []string{"project", "world"} {
		var current int64
		if expected[domain] < 1 {
			return ErrFenced
		}
		err := s.DB.QueryRow(ctx, "SELECT fence FROM "+s.prefix+"fences WHERE coordinator=$1", domain).Scan(&current)
		if err != nil || current != expected[domain] {
			return ErrFenced
		}
	}
	return nil
}

// Register fixes the participant set before any remote request is sent.
func (s *Store) Register(ctx context.Context, o Operation, plan Plan) error {
	if !o.Valid() || o.Coordinator != s.Domain || o.Fence != s.Fence {
		return ErrInvalid
	}
	plan, err := Normalize(plan)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = s.checkFence(ctx, tx, o); err != nil {
		return err
	}
	data, _ := json.Marshal(o)
	planned, _ := json.Marshal(plan)
	_, err = tx.Exec(ctx, "INSERT INTO "+s.prefix+"operations(organization_id,resource_id,commit_id,operation,plan,plan_hash) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING", o.OrganizationID, o.ResourceID, o.CommitID, data, planned, Fingerprint(plan))
	if err != nil {
		return err
	}
	var saved []byte
	var hash, state string
	err = tx.QueryRow(ctx, "SELECT operation,plan_hash,state FROM "+s.prefix+"operations WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3 FOR UPDATE", o.OrganizationID, o.ResourceID, o.CommitID).Scan(&saved, &hash, &state)
	if err != nil {
		return err
	}
	var original Operation
	if json.Unmarshal(saved, &original) != nil || original != o || hash != Fingerprint(plan) {
		return ErrConflict
	}
	if state == "cancelled" {
		return ErrDenied
	}
	return tx.Commit(ctx)
}

func (s *Store) Prepare(ctx context.Context, o Operation, grants []Grant) error {
	if !o.Valid() {
		return ErrInvalid
	}
	normalized, err := Normalize(Plan{s.Domain: grants})
	if err != nil {
		return err
	}
	grants = normalized[s.Domain]
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = s.checkFence(ctx, tx, o); err != nil {
		return err
	}
	// The operation advisory lock also serializes a cancellation tombstone arriving
	// before a delayed prepare. It is always taken before any resource barrier.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", s.prefix+"participant:"+o.Coordinator+":"+o.OrganizationID+":"+o.ResourceID+":"+o.CommitID); err != nil {
		return err
	}
	var original []byte
	var savedHash, state string
	err = tx.QueryRow(ctx, "SELECT operation,grants_hash,state FROM "+s.prefix+"participants WHERE coordinator=$1 AND organization_id=$2 AND resource_id=$3 AND commit_id=$4 FOR UPDATE", o.Coordinator, o.OrganizationID, o.ResourceID, o.CommitID).Scan(&original, &savedHash, &state)
	if err == nil {
		var saved Operation
		if json.Unmarshal(original, &saved) != nil || saved != o || savedHash != Fingerprint(grants) {
			return ErrConflict
		}
		if state != "prepared" {
			return ErrDenied
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	for _, grant := range grants {
		if _, err = tx.Exec(ctx, "INSERT INTO "+s.prefix+"barriers(organization_id,resource_id,subject_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", o.OrganizationID, grant.ResourceID, o.SubjectID); err != nil {
			return err
		}
		var blocked bool
		if err = tx.QueryRow(ctx, "SELECT blocked FROM "+s.prefix+"barriers WHERE organization_id=$1 AND resource_id=$2 AND subject_id=$3 FOR UPDATE", o.OrganizationID, grant.ResourceID, o.SubjectID).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return ErrDenied
		}
		if err = s.Authorize(ctx, tx, o, grant); err != nil {
			return err
		}
	}
	data, _ := json.Marshal(o)
	if _, err = tx.Exec(ctx, "INSERT INTO "+s.prefix+"participants(coordinator,organization_id,resource_id,commit_id,operation,grants_hash,state) VALUES($1,$2,$3,$4,$5,$6,'prepared')", o.Coordinator, o.OrganizationID, o.ResourceID, o.CommitID, data, Fingerprint(grants)); err != nil {
		return err
	}
	for _, grant := range grants {
		if _, err = tx.Exec(ctx, "INSERT INTO "+s.prefix+"reservations(coordinator,organization_id,resource_id,commit_id,subject_id,grant_resource_id) VALUES($1,$2,$3,$4,$5,$6)", o.Coordinator, o.OrganizationID, o.ResourceID, o.CommitID, o.SubjectID, grant.ResourceID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordPrepared(ctx context.Context, o Operation, domain string) error {
	if domain != "project" && domain != "world" {
		return ErrInvalid
	}
	data, _ := json.Marshal(o)
	tag, err := s.DB.Exec(ctx, "UPDATE "+s.prefix+"operations SET prepared=prepared || jsonb_build_object($4::text,true),updated_at=now() WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3 AND operation=$5::jsonb AND state='preparing' AND plan ? $4", o.OrganizationID, o.ResourceID, o.CommitID, domain, data)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrUnknown
	}
	return err
}

// FinishTx must be in the very transaction that writes the business version,
// CAS head and final business result; cancellation uses the same business lock.
func (s *Store) FinishTx(ctx context.Context, tx pgx.Tx, o Operation, state string) error {
	if !o.Valid() || !terminal(state) || o.Coordinator != s.Domain {
		return ErrInvalid
	}
	if err := s.checkFence(ctx, tx, o); err != nil {
		return err
	}
	var data, rawPlan, rawPrepared []byte
	var previous string
	err := tx.QueryRow(ctx, "SELECT operation,plan,prepared,state FROM "+s.prefix+"operations WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3 FOR UPDATE", o.OrganizationID, o.ResourceID, o.CommitID).Scan(&data, &rawPlan, &rawPrepared, &previous)
	if err != nil {
		return err
	}
	var saved Operation
	var plan Plan
	var prepared map[string]bool
	if json.Unmarshal(data, &saved) != nil || saved != o || json.Unmarshal(rawPlan, &plan) != nil || json.Unmarshal(rawPrepared, &prepared) != nil {
		return ErrConflict
	}
	if previous != "preparing" && previous != state {
		return ErrConflict
	}
	if state == "committed" {
		for domain := range plan {
			if !prepared[domain] {
				return ErrUnknown
			}
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE "+s.prefix+"operations SET state=$4,updated_at=now() WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3", o.OrganizationID, o.ResourceID, o.CommitID, state); err != nil {
		return err
	}
	for domain := range plan {
		if _, err = tx.Exec(ctx, "INSERT INTO "+s.prefix+"outbox(organization_id,resource_id,commit_id,participant) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", o.OrganizationID, o.ResourceID, o.CommitID, domain); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Status(ctx context.Context, o Operation) (Status, error) {
	if !o.Valid() || o.Coordinator != s.Domain {
		return Status{}, ErrInvalid
	}
	if o.Fence != s.Fence {
		return Status{}, ErrFenced
	}
	var data []byte
	var state string
	err := s.DB.QueryRow(ctx, "SELECT operation,state FROM "+s.prefix+"operations WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3", o.OrganizationID, o.ResourceID, o.CommitID).Scan(&data, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, ErrUnknown
	}
	if err != nil {
		return Status{}, err
	}
	var saved Operation
	if json.Unmarshal(data, &saved) != nil || saved != o {
		return Status{}, ErrConflict
	}
	return Status{saved, state}, nil
}

// Finalize accepts only an exact terminal status obtained by trusted coordinator
// lookup. The HTTP handler never deserializes a caller-provided status here.
func (s *Store) Finalize(ctx context.Context, o Operation, proof Status) error {
	if err := VerifyTerminal(o, proof); err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = s.checkFence(ctx, tx, o); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", s.prefix+"participant:"+o.Coordinator+":"+o.OrganizationID+":"+o.ResourceID+":"+o.CommitID); err != nil {
		return err
	}
	var original []byte
	var state string
	err = tx.QueryRow(ctx, "SELECT operation,state FROM "+s.prefix+"participants WHERE coordinator=$1 AND organization_id=$2 AND resource_id=$3 AND commit_id=$4 FOR UPDATE", o.Coordinator, o.OrganizationID, o.ResourceID, o.CommitID).Scan(&original, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		if proof.State != "cancelled" {
			return ErrUnknown
		}
		data, _ := json.Marshal(o)
		_, err = tx.Exec(ctx, "INSERT INTO "+s.prefix+"participants(coordinator,organization_id,resource_id,commit_id,operation,grants_hash,state) VALUES($1,$2,$3,$4,$5,'','cancelled')", o.Coordinator, o.OrganizationID, o.ResourceID, o.CommitID, data)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	var saved Operation
	if json.Unmarshal(original, &saved) != nil || saved != o {
		return ErrConflict
	}
	if terminal(state) && state != proof.State {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE "+s.prefix+"participants SET state=$5 WHERE coordinator=$1 AND organization_id=$2 AND resource_id=$3 AND commit_id=$4", o.Coordinator, o.OrganizationID, o.ResourceID, o.CommitID, proof.State); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM "+s.prefix+"reservations WHERE coordinator=$1 AND organization_id=$2 AND resource_id=$3 AND commit_id=$4", o.Coordinator, o.OrganizationID, o.ResourceID, o.CommitID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BlockTx installs the revocation barrier in the caller's domain transaction.
// If pending is true the caller MUST COMMIT the barrier and report pending,
// without changing the grant. A later authorized retry drains it after finalize.
func (s *Store) BlockTx(ctx context.Context, tx pgx.Tx, org, resource, subject string) (bool, error) {
	if !idPattern.MatchString(org) || !idPattern.MatchString(resource) || !idPattern.MatchString(subject) {
		return false, ErrInvalid
	}
	_, err := tx.Exec(ctx, "INSERT INTO "+s.prefix+"barriers(organization_id,resource_id,subject_id,blocked) VALUES($1,$2,$3,true) ON CONFLICT(organization_id,resource_id,subject_id) DO UPDATE SET blocked=true", org, resource, subject)
	if err != nil {
		return false, err
	}
	var pending bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.prefix+"reservations WHERE organization_id=$1 AND grant_resource_id=$2 AND subject_id=$3)", org, resource, subject).Scan(&pending)
	return pending, err
}

// ChangedTx is called only after a grant mutation succeeds in that same tx.
func (s *Store) ChangedTx(ctx context.Context, tx pgx.Tx, org, resource, subject string) error {
	_, err := tx.Exec(ctx, "UPDATE "+s.prefix+"barriers SET blocked=false,epoch=epoch+1 WHERE organization_id=$1 AND resource_id=$2 AND subject_id=$3", org, resource, subject)
	return err
}

type OutboxEntry struct {
	Operation   Operation
	Participant string
}

func (s *Store) PendingOutbox(ctx context.Context) ([]OutboxEntry, error) {
	rows, err := s.DB.Query(ctx, "SELECT o.operation,b.participant FROM "+s.prefix+"outbox b JOIN "+s.prefix+"operations o USING(organization_id,resource_id,commit_id) WHERE NOT b.delivered ORDER BY o.updated_at LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboxEntry{}
	for rows.Next() {
		var data []byte
		var row OutboxEntry
		if err = rows.Scan(&data, &row.Participant); err != nil {
			return nil, err
		}
		if json.Unmarshal(data, &row.Operation) != nil || !row.Operation.Valid() {
			return nil, ErrInvalid
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Store) Delivered(ctx context.Context, entry OutboxEntry) error {
	o := entry.Operation
	_, err := s.DB.Exec(ctx, "UPDATE "+s.prefix+"outbox SET delivered=true WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3 AND participant=$4", o.OrganizationID, o.ResourceID, o.CommitID, entry.Participant)
	return err
}

func (s *Store) PendingOperations(ctx context.Context) ([]Operation, error) {
	rows, err := s.DB.Query(ctx, "SELECT operation FROM "+s.prefix+"operations WHERE state='preparing' ORDER BY updated_at LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		var data []byte
		var operation Operation
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if json.Unmarshal(data, &operation) != nil || !operation.Valid() || operation.Fence != s.Fence {
			return nil, ErrFenced
		}
		out = append(out, operation)
	}
	return out, rows.Err()
}
