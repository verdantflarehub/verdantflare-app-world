package world

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

type ProjectReader interface {
	Open(context.Context, Principal, string, string) (OpenResult, error)
	Authorize(context.Context, Principal, Permission) error
}
type Service struct {
	db       *pgxpool.Pool
	content  Artifact
	projects ProjectReader
	coord    *coordination.Engine
	workers  chan struct{}
	analysis *AnalysisRuntime
	producer ProducerReader
}

func NewService(ctx context.Context, db *pgxpool.Pool, content Artifact, projects ProjectReader, fence int64, peers map[string]coordination.Peer) (*Service, error) {
	if db == nil || content == nil || projects == nil || fence < 1 {
		return nil, ErrInvalid
	}
	if err := migrations.Check(ctx, db); err != nil {
		return nil, err
	}
	s := &Service{db: db, content: content, projects: projects, workers: make(chan struct{}, 8)}
	store, err := coordination.NewStore(db, "world", fence, func(ctx context.Context, tx pgx.Tx, o coordination.Operation, g coordination.Grant) error {
		p := Principal{OrganizationID: o.OrganizationID, SubjectID: o.SubjectID, RequestID: o.CommitID}
		var owner string
		err := tx.QueryRow(ctx, "SELECT owner_id::text FROM studio.world_drafts WHERE organization_id=$1 AND draft_id=$2 FOR SHARE", p.OrganizationID, g.ResourceID).Scan(&owner)
		if err == nil {
			if owner != p.SubjectID {
				return coordination.ErrDenied
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var role string
		err = tx.QueryRow(ctx, "SELECT role FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3 FOR SHARE", p.OrganizationID, g.ResourceID, p.SubjectID).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (g.Write && role != "owner" && role != "editor") {
			return coordination.ErrDenied
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.coord = &coordination.Engine{Store: store, Peers: peers}
	return s, nil
}
func (s *Service) Coordination() *coordination.Engine { return s.coord }

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func advisory(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", key)
	return err
}
func audit(ctx context.Context, tx pgx.Tx, p Principal, action, id string) error {
	_, err := tx.Exec(ctx, "INSERT INTO studio.world_events(event_id,organization_id,subject_id,request_id,action,resource_id) VALUES($1,$2,$3,$4,$5,$6)", newID(), p.OrganizationID, p.SubjectID, p.RequestID, action, id)
	return err
}
func errorCode(code string) error {
	for _, err := range []error{ErrInvalid, ErrNotFound, ErrForbidden, ErrConflict, ErrNotReady, ErrIdempotency, ErrEmptySetHasActors} {
		if code == err.Error() {
			return err
		}
	}
	return ErrDependency
}
func uniqueRefs(refs []ContentRef) []ContentRef {
	seen := map[ContentRef]bool{}
	out := []ContentRef{}
	for _, ref := range refs {
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.StoreID != b.StoreID {
			return a.StoreID < b.StoreID
		}
		if a.ArtifactID != b.ArtifactID {
			return a.ArtifactID < b.ArtifactID
		}
		return a.VersionID < b.VersionID
	})
	return out
}
func (s *Service) Open(ctx context.Context, p Principal, projectID, revisionID string) (OpenResult, error) {
	if !p.Valid() || !ValidID(projectID) || !ValidID(revisionID) || s.projects == nil {
		return OpenResult{}, ErrForbidden
	}
	return s.projects.Open(ctx, p, projectID, revisionID)
}
func (s *Service) operation(_ Principal, c worldCommit) coordination.Operation {
	if c.Plan.Operation == nil {
		return coordination.Operation{}
	}
	return *c.Plan.Operation
}

func coordinationError(err error) error {
	if errors.Is(err, coordination.ErrDenied) {
		return ErrForbidden
	}
	if errors.Is(err, coordination.ErrInvalid) {
		return ErrInvalid
	}
	if errors.Is(err, coordination.ErrConflict) {
		return ErrIdempotency
	}
	return ErrDependency
}
func (s *Service) authorizationPlan(ctx context.Context, p Principal, c worldCommit) (coordination.Plan, error) {
	if c.Plan.Manifest.SchemaVersion == 2 {
		_, plan, err := s.draftPublicationContents(ctx, p, c.Plan)
		if err != nil {
			return nil, err
		}
		plan["world"] = append(plan["world"], coordination.Grant{ResourceID: c.AssetID, Write: true})
		return coordination.Normalize(plan)
	}
	grants := map[string]bool{c.AssetID: true}
	for _, asset := range c.Plan.Manifest.DependsOn {
		closure, err := s.ReferencesWithGrants(ctx, p, asset)
		if err != nil {
			return nil, err
		}
		for _, grant := range closure.Grants {
			if _, exists := grants[grant.ResourceID]; !exists {
				grants[grant.ResourceID] = false
			}
		}
	}
	plan := coordination.Plan{"project": {{ResourceID: c.Plan.Manifest.Source.ProjectID, Write: false}}, "world": {}}
	for resource, write := range grants {
		plan["world"] = append(plan["world"], coordination.Grant{ResourceID: resource, Write: write})
	}
	return coordination.Normalize(plan)
}

// Recovery uses durable business records, including a crash before registration
// in the coordination ledger. Unknown dependencies stay preparing without TTL.
func (s *Service) Recover(ctx context.Context) error {
	rows, err := s.db.Query(ctx, "SELECT organization_id::text,asset_id::text,commit_id::text,created_by::text FROM studio.world_commits WHERE state='preparing' ORDER BY updated_at LIMIT 100")
	if err != nil {
		return err
	}
	type pending struct{ org, asset, commit, actor string }
	items := []pending{}
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.org, &item.asset, &item.commit, &item.actor); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	if err = s.coord.Deliver(ctx); err != nil {
		failures = append(failures, err)
	}
	for _, item := range items {
		p := Principal{OrganizationID: item.org, SubjectID: item.actor, RequestID: newID()}
		operationCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, err = s.resumeWorld(operationCtx, p, item.asset, item.commit)
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
	}
	if err = s.coord.Deliver(ctx); err != nil {
		failures = append(failures, err)
	}
	if err = s.recoverDrafts(ctx); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
func (s *Service) RunRecovery(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Recover(ctx)
		}
	}
}
