package world

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

func draftOwner(ctx context.Context, q queryer, p Principal, id string) (string, string, error) {
	if !p.Valid() || !ValidID(id) {
		return "", "", ErrForbidden
	}
	var head, purpose string
	err := q.QueryRow(ctx, `SELECT COALESCE(head_revision_id::text,''),purpose FROM studio.world_drafts WHERE organization_id=$1 AND draft_id=$2 AND owner_id=$3`, p.OrganizationID, id, p.SubjectID).Scan(&head, &purpose)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrForbidden
	}
	return head, purpose, err
}
func draftRevision(ctx context.Context, q queryer, p Principal, id, revision string) (DraftRevision, error) {
	var out DraftRevision
	head, purpose, err := draftOwner(ctx, q, p, id)
	if err != nil {
		return out, err
	}
	if revision == "" {
		revision = head
	}
	if !ValidID(revision) {
		return out, ErrNotReady
	}
	var manifest, ref []byte
	err = q.QueryRow(ctx, `SELECT manifest_json,manifest_ref,created_at FROM studio.world_draft_revisions WHERE organization_id=$1 AND draft_id=$2 AND revision_id=$3`, p.OrganizationID, id, revision).Scan(&manifest, &ref, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal(manifest, &out.Manifest) != nil || json.Unmarshal(ref, &out.ManifestRef) != nil || out.Manifest.Validate() != nil || !out.ManifestRef.Valid() || out.Manifest.DraftID != id || out.Manifest.Purpose != purpose {
		return out, ErrNotReady
	}
	out.Kind = "revision"
	out.DraftID = id
	out.RevisionID = revision
	out.HeadRevisionID = head
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}
func loadDraftCommit(ctx context.Context, q queryer, p Principal, commit string) (draftCommit, error) {
	var c draftCommit
	var plan, result []byte
	c.CommitID = commit
	c.Actor = p.SubjectID
	err := q.QueryRow(ctx, `SELECT draft_id::text,request_sha256,COALESCE(expected_revision_id::text,''),revision_id::text,state,COALESCE(error_code,''),COALESCE(conflict_revision_id::text,''),plan,result FROM studio.world_draft_commits WHERE organization_id=$1 AND created_by=$2 AND commit_id=$3`, p.OrganizationID, p.SubjectID, commit).Scan(&c.DraftID, &c.Hash, &c.Expected, &c.Revision, &c.State, &c.Error, &c.ConflictHead, &plan, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if json.Unmarshal(plan, &c.Plan) != nil {
		return c, ErrNotReady
	}
	if len(result) > 0 && json.Unmarshal(result, &c.Result) != nil {
		return c, ErrNotReady
	}
	if c.State == "committed" && c.Result == nil {
		return c, ErrNotReady
	}
	return c, nil
}

func (s *Service) WorldDraft(ctx context.Context, p Principal, input DraftRequest) (any, error) {
	if !p.Valid() {
		return nil, ErrForbidden
	}
	r, err := normalizeDraftRequest(input)
	if err != nil {
		return nil, err
	}
	switch r.Mode {
	case "get":
		return s.getDraft(ctx, p, r.DraftID, r.RevisionID)
	case "list":
		return s.listDrafts(ctx, p, r)
	case "status":
		c, err := loadDraftCommit(ctx, s.db, p, r.CommitID)
		if err != nil {
			return nil, err
		}
		if _, _, err = draftOwner(ctx, s.db, p, c.DraftID); err != nil {
			return nil, err
		}
		return DraftStatus{Kind: "status", State: c.State, RequestHash: c.Hash, Result: c.Result, ErrorCode: c.Error, CurrentRevisionID: c.ConflictHead}, nil
	case "create", "commit":
		return s.saveDraft(ctx, p, r)
	default:
		return nil, ErrInvalid
	}
}
func (s *Service) getDraft(ctx context.Context, p Principal, id, revision string) (DraftRevision, error) {
	out, err := draftRevision(ctx, s.db, p, id, revision)
	if err != nil {
		return out, err
	}
	data, err := s.content.Read(ctx, p, out.ManifestRef, Access{WorldDraftID: id, WorldDraftRevisionID: out.RevisionID}, MaxManifestBytes)
	if err != nil {
		return DraftRevision{}, err
	}
	var m DraftManifest
	if validateLearningJSON(data, "world-draft-manifest") != nil || json.Unmarshal(data, &m) != nil || m.Validate() != nil {
		return DraftRevision{}, ErrNotReady
	}
	h1, _ := digest(m)
	h2, _ := digest(out.Manifest)
	if h1 != h2 {
		return DraftRevision{}, ErrNotReady
	}
	return out, nil
}
func (s *Service) listDrafts(ctx context.Context, p Principal, r DraftRequest) (DraftList, error) {
	out := DraftList{Kind: "list", Items: []DraftListItem{}}
	cursor, err := decodeDraftCursor(p, r)
	if err != nil {
		return out, err
	}
	limit := r.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `SELECT draft_id::text,head_revision_id::text,name,purpose,subjects,created_at,updated_at,has_content FROM studio.world_drafts WHERE organization_id=$1 AND owner_id=$2 AND head_revision_id IS NOT NULL AND ($3='' OR purpose=$3) AND ($4='' OR strpos(lower(name),lower($4))>0) AND ($5='' OR subjects ? $5) AND ($6='' OR draft_id>NULLIF($6,'')::uuid) ORDER BY draft_id LIMIT $7`, p.OrganizationID, p.SubjectID, r.Purpose, r.Query, r.Subject, cursor, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v DraftListItem
		var subjects []byte
		if err = rows.Scan(&v.DraftID, &v.HeadRevisionID, &v.Name, &v.Purpose, &subjects, &v.CreatedAt, &v.UpdatedAt, &v.HasContent); err != nil {
			return out, err
		}
		if json.Unmarshal(subjects, &v.Subjects) != nil {
			return out, ErrNotReady
		}
		v.CreatedAt = v.CreatedAt.UTC()
		v.UpdatedAt = v.UpdatedAt.UTC()
		out.Items = append(out.Items, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := encodeDraftCursor(p, r, out.Items[limit-1].DraftID)
		out.NextCursor = &next
	}
	return out, nil
}
func (s *Service) saveDraft(ctx context.Context, p Principal, r DraftRequest) (DraftRevision, error) {
	hash, _ := digest(r)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return DraftRevision{}, err
	}
	defer tx.Rollback(context.Background())
	if err = advisory(ctx, tx, "world-draft-request:"+p.OrganizationID+":"+p.SubjectID+":"+r.CommitID); err != nil {
		return DraftRevision{}, err
	}
	old, err := loadDraftCommit(ctx, tx, p, r.CommitID)
	if err == nil {
		if old.Hash != hash {
			return DraftRevision{}, ErrIdempotency
		}
		if err = tx.Commit(ctx); err != nil {
			return DraftRevision{}, err
		}
		return s.resumeDraft(ctx, p, r.CommitID)
	}
	if !errors.Is(err, ErrNotFound) {
		return DraftRevision{}, err
	}
	// Unsupported validators must not create an endlessly retried job. Existing
	// commits are looked up first so a future capability change cannot alter an
	// already accepted request's idempotency behavior.
	if r.Manifest != nil && draftValidatorsReady(*r.Manifest) != nil {
		return DraftRevision{}, ErrNotReady
	}
	id := r.DraftID
	var m DraftManifest
	if r.Mode == "create" {
		id = newID()
		m = emptyDraft(id, r)
		subjects, _ := json.Marshal(m.Subjects)
		_, err = tx.Exec(ctx, `INSERT INTO studio.world_drafts(draft_id,organization_id,owner_id,purpose,name,subjects) VALUES($1,$2,$3,$4,$5,$6)`, id, p.OrganizationID, p.SubjectID, m.Purpose, m.Name, subjects)
		if err != nil {
			return DraftRevision{}, err
		}
	} else {
		_, purpose, e := draftOwner(ctx, tx, p, id)
		if e != nil {
			return DraftRevision{}, e
		}
		if r.Manifest.Purpose != purpose {
			return DraftRevision{}, ErrInvalid
		}
		m = *r.Manifest
	}
	revision := newID()
	plan := draftPlan{Manifest: m, WriteID: newID(), Refs: []ContentRef{}, Operation: coordination.Operation{Coordinator: "world", OrganizationID: p.OrganizationID, SubjectID: p.SubjectID, ResourceID: id, CommitID: r.CommitID, RequestHash: hash, Fence: s.coord.Store.Fence}}
	data, _ := json.Marshal(plan)
	_, err = tx.Exec(ctx, `INSERT INTO studio.world_draft_commits(organization_id,created_by,commit_id,draft_id,request_sha256,expected_revision_id,revision_id,plan) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8)`, p.OrganizationID, p.SubjectID, r.CommitID, id, hash, r.ExpectedRevisionID, revision, data)
	if err != nil {
		return DraftRevision{}, err
	}
	if err = audit(ctx, tx, p, "world.draft.prepare", revision); err != nil {
		return DraftRevision{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DraftRevision{}, err
	}
	return s.resumeDraft(ctx, p, r.CommitID)
}
func (s *Service) saveDraftPlan(ctx context.Context, p Principal, c draftCommit) error {
	data, err := json.Marshal(c.Plan)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE studio.world_draft_commits SET plan=$1,updated_at=now() WHERE organization_id=$2 AND created_by=$3 AND commit_id=$4 AND state='preparing'`, data, p.OrganizationID, p.SubjectID, c.CommitID)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrIdempotency
	}
	return err
}
func (s *Service) terminalDraft(ctx context.Context, p Principal, c draftCommit, cause error) (DraftRevision, error) {
	state, code, head := "failed", "", ""
	for _, terminal := range []error{ErrInvalid, ErrNotFound, ErrForbidden, ErrNotReady} {
		if errors.Is(cause, terminal) {
			code = terminal.Error()
			break
		}
	}
	var conflict *ConflictError
	if errors.As(cause, &conflict) {
		state = "conflict"
		code = ErrConflict.Error()
		head = conflict.CurrentRevisionID
	}
	if code == "" {
		return DraftRevision{}, cause
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return DraftRevision{}, err
	}
	defer tx.Rollback(context.Background())
	var current string
	if err = tx.QueryRow(ctx, `SELECT state FROM studio.world_draft_commits WHERE organization_id=$1 AND created_by=$2 AND commit_id=$3 FOR UPDATE`, p.OrganizationID, p.SubjectID, c.CommitID).Scan(&current); err != nil {
		return DraftRevision{}, err
	}
	if current != "preparing" {
		return DraftRevision{}, ErrIdempotency
	}
	var registered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_coord_operations WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3)`, p.OrganizationID, c.DraftID, c.CommitID).Scan(&registered); err != nil {
		return DraftRevision{}, err
	}
	if registered {
		if err = s.coord.Store.FinishTx(ctx, tx, c.Plan.Operation, "cancelled"); err != nil {
			return DraftRevision{}, coordinationError(err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE studio.world_draft_commits SET state=$1,error_code=$2,conflict_revision_id=NULLIF($3,'')::uuid,updated_at=now() WHERE organization_id=$4 AND created_by=$5 AND commit_id=$6`, state, code, head, p.OrganizationID, p.SubjectID, c.CommitID)
	if err != nil {
		return DraftRevision{}, err
	}
	if err = audit(ctx, tx, p, "world.draft."+state, c.Revision); err != nil {
		return DraftRevision{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DraftRevision{}, err
	}
	return DraftRevision{}, cause
}
func (s *Service) resumeDraft(ctx context.Context, p Principal, commit string) (DraftRevision, error) {
	select {
	case s.workers <- struct{}{}:
		defer func() { <-s.workers }()
	case <-ctx.Done():
		return DraftRevision{}, ctx.Err()
	}
	lock, err := pgx.ConnectConfig(ctx, s.db.Config().ConnConfig.Copy())
	if err != nil {
		return DraftRevision{}, err
	}
	defer lock.Close(context.Background())
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, "world-draft-execute:"+p.OrganizationID+":"+p.SubjectID+":"+commit); err != nil {
		return DraftRevision{}, err
	}
	c, err := loadDraftCommit(ctx, s.db, p, commit)
	if err != nil {
		return DraftRevision{}, err
	}
	head, _, err := draftOwner(ctx, s.db, p, c.DraftID)
	if err != nil {
		return DraftRevision{}, err
	}
	switch c.State {
	case "committed":
		return *c.Result, nil
	case "conflict":
		return DraftRevision{}, &ConflictError{c.ConflictHead}
	case "failed":
		return DraftRevision{}, errorCode(c.Error)
	}
	op := c.Plan.Operation
	if !op.Valid() || op.Coordinator != "world" || op.Fence != s.coord.Store.Fence || op.SubjectID != p.SubjectID || op.OrganizationID != p.OrganizationID || op.ResourceID != c.DraftID || op.CommitID != commit || op.RequestHash != c.Hash {
		return DraftRevision{}, ErrDependency
	}
	if head != c.Expected {
		return s.terminalDraft(ctx, p, c, &ConflictError{head})
	}
	refs, grants, err := s.checkDraftContents(ctx, p, c)
	if err != nil {
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotReady) {
			return s.terminalDraft(ctx, p, c, err)
		}
		return DraftRevision{}, err
	}
	if c.Plan.Grants == nil {
		c.Plan.Grants = grants
		if err = s.saveDraftPlan(ctx, p, c); err != nil {
			return DraftRevision{}, err
		}
	} else if coordination.Fingerprint(c.Plan.Grants) != coordination.Fingerprint(grants) {
		return DraftRevision{}, ErrNotReady
	}
	if err = s.coord.Store.Register(ctx, op, c.Plan.Grants); err != nil {
		return DraftRevision{}, coordinationError(err)
	}
	if c.Plan.ManifestRef == nil {
		content, ok := s.content.(DraftArtifact)
		if !ok {
			return DraftRevision{}, ErrDependency
		}
		data, err := json.Marshal(c.Plan.Manifest)
		if err != nil || len(data) > MaxManifestBytes {
			return s.terminalDraft(ctx, p, c, ErrInvalid)
		}
		v, err := content.WriteDraftManifest(ctx, p, c.DraftID, c.Revision, TextWrite{WriteID: c.Plan.WriteID, Text: string(data), MIME: "application/json"})
		if err != nil {
			return DraftRevision{}, err
		}
		if !v.ContentRef.Valid() || v.OrganizationID != p.OrganizationID || v.Source.Kind != "world_draft_manifest" || v.Source.WorldDraftID != c.DraftID || v.Source.WorldDraftRevisionID != c.Revision {
			return DraftRevision{}, ErrDependency
		}
		c.Plan.ManifestRef = &v.ContentRef
	}
	c.Plan.Refs = uniqueRefs(append(refs, *c.Plan.ManifestRef))
	if len(c.Plan.Refs) > MaxFiles+1 {
		return s.terminalDraft(ctx, p, c, ErrInvalid)
	}
	if err = s.saveDraftPlan(ctx, p, c); err != nil {
		return DraftRevision{}, err
	}
	if err = s.content.Retain(ctx, p, RetentionOwner{Kind: "world_draft_revision", ID: c.Revision, CommitID: commit}, c.Plan.Refs); err != nil {
		if errors.Is(err, ErrForbidden) {
			return s.terminalDraft(ctx, p, c, ErrForbidden)
		}
		return DraftRevision{}, err
	}
	if err = s.coord.Prepare(ctx, op, c.Plan.Grants); err != nil {
		if errors.Is(err, coordination.ErrDenied) || errors.Is(err, coordination.ErrPending) {
			return s.terminalDraft(ctx, p, c, ErrForbidden)
		}
		return DraftRevision{}, coordinationError(err)
	}
	return s.publishDraft(ctx, p, c)
}
func (s *Service) publishDraft(ctx context.Context, p Principal, c draftCommit) (DraftRevision, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return DraftRevision{}, err
	}
	defer tx.Rollback(context.Background())
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM studio.world_draft_commits WHERE organization_id=$1 AND created_by=$2 AND commit_id=$3 FOR UPDATE`, p.OrganizationID, p.SubjectID, c.CommitID).Scan(&state); err != nil {
		return DraftRevision{}, err
	}
	if state != "preparing" {
		return DraftRevision{}, ErrIdempotency
	}
	if err = s.coord.Store.FinishTx(ctx, tx, c.Plan.Operation, "committed"); err != nil {
		return DraftRevision{}, coordinationError(err)
	}
	var head string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(head_revision_id::text,'') FROM studio.world_drafts WHERE organization_id=$1 AND draft_id=$2 AND owner_id=$3 FOR UPDATE`, p.OrganizationID, c.DraftID, p.SubjectID).Scan(&head); err != nil {
		return DraftRevision{}, err
	}
	if head != c.Expected {
		_ = tx.Rollback(ctx)
		return s.terminalDraft(ctx, p, c, &ConflictError{head})
	}
	out := DraftRevision{Kind: "revision", DraftID: c.DraftID, RevisionID: c.Revision, HeadRevisionID: c.Revision, ManifestRef: *c.Plan.ManifestRef, Manifest: c.Plan.Manifest}
	ref, _ := json.Marshal(out.ManifestRef)
	manifest, _ := json.Marshal(out.Manifest)
	subjects, _ := json.Marshal(out.Manifest.Subjects)
	err = tx.QueryRow(ctx, `INSERT INTO studio.world_draft_revisions(revision_id,organization_id,draft_id,parent_revision_id,manifest_ref,manifest_json,created_by,commit_id) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8) RETURNING created_at`, c.Revision, p.OrganizationID, c.DraftID, c.Expected, ref, manifest, p.SubjectID, c.CommitID).Scan(&out.CreatedAt)
	if err != nil {
		return DraftRevision{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	result, _ := json.Marshal(out)
	_, err = tx.Exec(ctx, `UPDATE studio.world_drafts SET head_revision_id=$1,name=$2,subjects=$3,has_content=$4,updated_at=now() WHERE organization_id=$5 AND draft_id=$6`, c.Revision, out.Manifest.Name, subjects, len(out.Manifest.Files) > 0, p.OrganizationID, c.DraftID)
	if err != nil {
		return DraftRevision{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE studio.world_draft_commits SET state='committed',result=$1,updated_at=now() WHERE organization_id=$2 AND created_by=$3 AND commit_id=$4`, result, p.OrganizationID, p.SubjectID, c.CommitID)
	if err != nil {
		return DraftRevision{}, err
	}
	if err = audit(ctx, tx, p, "world.draft.commit", c.Revision); err != nil {
		return DraftRevision{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DraftRevision{}, err
	}
	return out, nil
}
func (s *Service) recoverDrafts(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT organization_id::text,created_by::text,commit_id::text FROM studio.world_draft_commits WHERE state='preparing' ORDER BY updated_at LIMIT 100`)
	if err != nil {
		return err
	}
	type item struct{ org, actor, commit string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.org, &v.actor, &v.commit); err != nil {
			break
		}
		items = append(items, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	failures := []error{}
	for _, v := range items {
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, err = s.resumeDraft(c, Principal{v.org, v.actor, newID()}, v.commit)
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
