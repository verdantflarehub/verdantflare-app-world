package world

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

type worldCommit struct {
	AssetID, CommitID, Actor, Hash, Expected, Version, State, Error string
	Plan                                                            worldPlan
	Result                                                          *WorldResult
}

func assetRole(ctx context.Context, q queryer, p Principal, id string) (string, error) {
	if !p.Valid() || !ValidID(id) {
		return "", ErrForbidden
	}
	var value string
	e := q.QueryRow(ctx, "SELECT role FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3", p.OrganizationID, id, p.SubjectID).Scan(&value)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	return value, e
}
func assetWritable(ctx context.Context, q queryer, p Principal, id string) error {
	v, e := assetRole(ctx, q, p, id)
	if e != nil {
		return e
	}
	if v != "owner" && v != "editor" {
		return ErrForbidden
	}
	return nil
}
func assetVersion(ctx context.Context, q queryer, org, asset, version string) (AssetManifest, ContentRef, error) {
	var m AssetManifest
	var ref ContentRef
	var a, b []byte
	e := q.QueryRow(ctx, "SELECT manifest_json,manifest_ref FROM studio.world_asset_versions WHERE organization_id=$1 AND asset_id=$2 AND version_id=$3", org, asset, version).Scan(&a, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return m, ref, ErrNotFound
	}
	if e != nil {
		return m, ref, e
	}
	if e = json.Unmarshal(a, &m); e != nil {
		return m, ref, e
	}
	e = json.Unmarshal(b, &ref)
	return m, ref, e
}
func loadWorldCommit(ctx context.Context, q queryer, org, asset, commit string) (worldCommit, error) {
	c := worldCommit{AssetID: asset, CommitID: commit}
	var plan, result []byte
	e := q.QueryRow(ctx, `SELECT created_by::text,request_sha256,COALESCE(expected_version_id::text,''),version_id::text,state,plan,result,COALESCE(error_code,'') FROM studio.world_commits WHERE organization_id=$1 AND asset_id=$2 AND commit_id=$3`, org, asset, commit).Scan(&c.Actor, &c.Hash, &c.Expected, &c.Version, &c.State, &plan, &result, &c.Error)
	if errors.Is(e, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(plan, &c.Plan); e != nil {
		return c, e
	}
	if result != nil {
		e = json.Unmarshal(result, &c.Result)
	}
	return c, e
}
func normalizeWorld(r WorldRegisterRequest) (WorldRegisterRequest, error) {
	if !ValidID(r.CommitID) || !validPublicationSource(r) || !ValidID(r.SourceRevisionID) || !validText(r.Name) || !tokenPattern.MatchString(r.AssetType) || r.Subjects == nil || len(r.Subjects) > 256 || len(r.FileIDs) == 0 || len(r.FileIDs) > MaxFiles || (r.Relation != "produced_in" && r.Relation != "curated_in") {
		return r, ErrInvalid
	}
	if (r.AssetID == "") != (r.ExpectedVersionID == "") || (r.AssetID != "" && (!ValidID(r.AssetID) || !ValidID(r.ExpectedVersionID))) || (r.ReviewFileID != "" && !ValidID(r.ReviewFileID)) {
		return r, ErrInvalid
	}
	r.FileIDs = append([]string{}, r.FileIDs...)
	sort.Strings(r.FileIDs)
	for i, v := range r.FileIDs {
		if !ValidID(v) || (i > 0 && r.FileIDs[i-1] == v) {
			return r, ErrInvalid
		}
	}
	r.Subjects = append([]string{}, r.Subjects...)
	sort.Strings(r.Subjects)
	for i, v := range r.Subjects {
		if !validText(v) || (i > 0 && r.Subjects[i-1] == v) {
			return r, ErrInvalid
		}
	}
	deps := []AssetRef{}
	if r.DependsOn != nil {
		deps = append(deps, (*r.DependsOn)...)
	}
	sort.Slice(deps, func(i, j int) bool {
		a, b := deps[i], deps[j]
		if a.AssetID != b.AssetID {
			return a.AssetID < b.AssetID
		}
		if a.VersionID != b.VersionID {
			return a.VersionID < b.VersionID
		}
		return a.Purpose < b.Purpose
	})
	r.DependsOn = &deps
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > MaxManifestBytes {
		return r, ErrInvalid
	}
	return r, nil
}
func prepareWorld(r WorldRegisterRequest, asset string, opened OpenResult) (worldPlan, error) {
	source, sourceRef := opened.Manifest, opened.ManifestRef
	if opened.ProjectID != r.SourceProjectID || opened.RevisionID != r.SourceRevisionID || source.ProjectID != r.SourceProjectID || source.Validate() != nil || !sourceRef.Valid() {
		return worldPlan{}, ErrDependency
	}
	byID := map[string]File{}
	for _, f := range source.Files {
		byID[f.ID] = f
	}
	files := []File{}
	for _, id := range r.FileIDs {
		f, ok := byID[id]
		if !ok {
			return worldPlan{}, ErrInvalid
		}
		files = append(files, f)
	}
	if r.ReviewFileID != "" {
		if _, ok := byID[r.ReviewFileID]; !ok {
			return worldPlan{}, ErrInvalid
		}
	}
	sourceDeps := map[AssetRef]bool{}
	for _, a := range source.AssetRefs {
		sourceDeps[a] = true
	}
	for _, a := range *r.DependsOn {
		if !sourceDeps[a] {
			return worldPlan{}, ErrInvalid
		}
	}
	m := AssetManifest{SchemaVersion: 1, Kind: "asset-version", AssetID: asset, Name: r.Name, AssetType: r.AssetType, Subjects: r.Subjects, Files: files, Source: AssetSource{ProjectID: r.SourceProjectID, RevisionID: r.SourceRevisionID, Relation: r.Relation, ReviewFileID: r.ReviewFileID}, DependsOn: *r.DependsOn}
	if m.Validate() != nil {
		return worldPlan{}, ErrInvalid
	}
	return worldPlan{Manifest: m, SourceRef: sourceRef, WriteID: newID(), Refs: []ContentRef{}}, nil
}
func (s *Service) WorldRegister(ctx context.Context, p Principal, input WorldRegisterRequest) (WorldResult, error) {
	if !p.Valid() {
		return WorldResult{}, ErrForbidden
	}
	r, e := normalizeWorld(input)
	if e != nil {
		return WorldResult{}, e
	}
	hash, _ := digest(r)
	// Resolve a fixed source outside all SQL transactions. Existing operations
	// resume their saved content plan even when the remote Project is unavailable.
	if r.AssetID == "" {
		var priorAsset, priorHash string
		err := s.db.QueryRow(ctx, "SELECT asset_id::text,request_sha256 FROM studio.world_create_requests WHERE organization_id=$1 AND subject_id=$2 AND commit_id=$3", p.OrganizationID, p.SubjectID, r.CommitID).Scan(&priorAsset, &priorHash)
		if err == nil {
			if priorHash != hash {
				return WorldResult{}, ErrIdempotency
			}
			return s.resumeWorld(ctx, p, priorAsset, r.CommitID)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return WorldResult{}, err
		}
	} else {
		if err := assetWritable(ctx, s.db, p, r.AssetID); err != nil {
			return WorldResult{}, err
		}
		prior, err := loadWorldCommit(ctx, s.db, p.OrganizationID, r.AssetID, r.CommitID)
		if err == nil {
			if prior.Actor != p.SubjectID {
				return WorldResult{}, ErrForbidden
			}
			if prior.Hash != hash {
				return WorldResult{}, ErrIdempotency
			}
			return s.resumeWorld(ctx, p, r.AssetID, r.CommitID)
		}
		if !errors.Is(err, ErrNotFound) {
			return WorldResult{}, err
		}
	}
	opened, e := s.openPublicationSource(ctx, p, r)
	if e != nil {
		return WorldResult{}, e
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return WorldResult{}, e
	}
	defer tx.Rollback(context.Background())
	asset := r.AssetID
	if asset == "" {
		if e = advisory(ctx, tx, "world-create:"+p.OrganizationID+":"+p.SubjectID+":"+r.CommitID); e != nil {
			return WorldResult{}, e
		}
		var prior string
		e = tx.QueryRow(ctx, "SELECT asset_id::text,request_sha256 FROM studio.world_create_requests WHERE organization_id=$1 AND subject_id=$2 AND commit_id=$3", p.OrganizationID, p.SubjectID, r.CommitID).Scan(&asset, &prior)
		if e == nil {
			if prior != hash {
				return WorldResult{}, ErrIdempotency
			}
			if e = tx.Commit(ctx); e != nil {
				return WorldResult{}, e
			}
			return s.resumeWorld(ctx, p, asset, r.CommitID)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return WorldResult{}, e
		}
		asset = newID()
		if _, e = tx.Exec(ctx, "INSERT INTO studio.world_assets(asset_id,organization_id,owner_id,name,asset_type) VALUES($1,$2,$3,$4,$5)", asset, p.OrganizationID, p.SubjectID, r.Name, r.AssetType); e != nil {
			return WorldResult{}, e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO studio.world_asset_grants(organization_id,asset_id,subject_id,role) VALUES($1,$2,$3,'owner')", p.OrganizationID, asset, p.SubjectID); e != nil {
			return WorldResult{}, e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO studio.world_create_requests(organization_id,subject_id,commit_id,asset_id,request_sha256) VALUES($1,$2,$3,$4,$5)", p.OrganizationID, p.SubjectID, r.CommitID, asset, hash); e != nil {
			return WorldResult{}, e
		}
	} else {
		if e = assetWritable(ctx, tx, p, asset); e != nil {
			return WorldResult{}, e
		}
		if e = advisory(ctx, tx, "world-commit:"+p.OrganizationID+":"+asset+":"+r.CommitID); e != nil {
			return WorldResult{}, e
		}
		c, err := loadWorldCommit(ctx, tx, p.OrganizationID, asset, r.CommitID)
		if err == nil {
			if c.Actor != p.SubjectID {
				return WorldResult{}, ErrForbidden
			}
			if c.Hash != hash {
				return WorldResult{}, ErrIdempotency
			}
			if e = tx.Commit(ctx); e != nil {
				return WorldResult{}, e
			}
			return s.resumeWorld(ctx, p, asset, r.CommitID)
		}
		if !errors.Is(err, ErrNotFound) {
			return WorldResult{}, err
		}
		var kind string
		if e = tx.QueryRow(ctx, "SELECT asset_type FROM studio.world_assets WHERE organization_id=$1 AND asset_id=$2", p.OrganizationID, asset).Scan(&kind); e != nil {
			return WorldResult{}, e
		}
		if kind != r.AssetType {
			return WorldResult{}, ErrInvalid
		}
		if _, _, e = assetVersion(ctx, tx, p.OrganizationID, asset, r.ExpectedVersionID); e != nil {
			return WorldResult{}, e
		}
	}
	plan, e := preparePublication(r, asset, opened)
	if e != nil {
		return WorldResult{}, e
	}
	operation := coordination.Operation{Coordinator: "world", OrganizationID: p.OrganizationID, SubjectID: p.SubjectID, ResourceID: asset, CommitID: r.CommitID, RequestHash: hash, Fence: s.coord.Store.Fence}
	plan.Operation = &operation
	data, _ := json.Marshal(plan)
	_, e = tx.Exec(ctx, `INSERT INTO studio.world_commits(organization_id,asset_id,commit_id,created_by,request_sha256,expected_version_id,version_id,plan) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8)`, p.OrganizationID, asset, r.CommitID, p.SubjectID, hash, r.ExpectedVersionID, newID(), data)
	if e != nil {
		return WorldResult{}, e
	}
	if e = audit(ctx, tx, p, "world.prepare", asset); e != nil {
		return WorldResult{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return WorldResult{}, e
	}
	return s.resumeWorld(ctx, p, asset, r.CommitID)
}
func (s *Service) saveWorldPlan(ctx context.Context, p Principal, c worldCommit) error {
	data, e := json.Marshal(c.Plan)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, "UPDATE studio.world_commits SET plan=$1,updated_at=now() WHERE organization_id=$2 AND asset_id=$3 AND commit_id=$4 AND state='preparing'", data, p.OrganizationID, c.AssetID, c.CommitID)
	return e
}
func (s *Service) worldTerminal(ctx context.Context, p Principal, c worldCommit, state string, cause error) (WorldResult, error) {
	code := cause.Error()
	for _, known := range []error{ErrInvalid, ErrNotFound, ErrForbidden, ErrNotReady, ErrConflict} {
		if errors.Is(cause, known) {
			code = known.Error()
			break
		}
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return WorldResult{}, e
	}
	defer tx.Rollback(context.Background())
	var current string
	if e = tx.QueryRow(ctx, "SELECT state FROM studio.world_commits WHERE organization_id=$1 AND asset_id=$2 AND commit_id=$3 FOR UPDATE", p.OrganizationID, c.AssetID, c.CommitID).Scan(&current); e != nil {
		return WorldResult{}, e
	}
	if current != "preparing" {
		return WorldResult{}, ErrIdempotency
	}
	var registered bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM studio.world_coord_operations WHERE organization_id=$1 AND resource_id=$2 AND commit_id=$3)", p.OrganizationID, c.AssetID, c.CommitID).Scan(&registered); e != nil {
		return WorldResult{}, e
	}
	if registered {
		if e = s.coord.Store.FinishTx(ctx, tx, s.operation(p, c), "cancelled"); e != nil {
			return WorldResult{}, e
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE studio.world_commits SET state=$1,error_code=$2,updated_at=now() WHERE organization_id=$3 AND asset_id=$4 AND commit_id=$5 AND state='preparing'", state, code, p.OrganizationID, c.AssetID, c.CommitID); e != nil {
		return WorldResult{}, e
	}
	if e = audit(ctx, tx, p, "world."+state, c.Version); e != nil {
		return WorldResult{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return WorldResult{}, e
	}
	return WorldResult{}, cause
}
func (s *Service) resumeWorld(ctx context.Context, p Principal, asset, commit string) (WorldResult, error) {
	select {
	case s.workers <- struct{}{}:
		defer func() { <-s.workers }()
	case <-ctx.Done():
		return WorldResult{}, ctx.Err()
	}
	lock, e := pgx.ConnectConfig(ctx, s.db.Config().ConnConfig.Copy())
	if e != nil {
		return WorldResult{}, e
	}
	defer lock.Close(context.Background())
	if _, e = lock.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1,0))", "world-execute:"+p.OrganizationID+":"+asset+":"+commit); e != nil {
		return WorldResult{}, e
	}
	c, e := loadWorldCommit(ctx, s.db, p.OrganizationID, asset, commit)
	if e != nil {
		return WorldResult{}, e
	}
	if c.Actor != p.SubjectID {
		return WorldResult{}, ErrForbidden
	}
	operation := s.operation(p, c)
	if c.State == "preparing" && (!operation.Valid() || operation.Fence != s.coord.Store.Fence || operation.Coordinator != "world" || operation.OrganizationID != p.OrganizationID || operation.SubjectID != c.Actor || operation.ResourceID != c.AssetID || operation.CommitID != c.CommitID || operation.RequestHash != c.Hash) {
		return WorldResult{}, ErrDependency
	}
	if e = assetWritable(ctx, s.db, p, asset); e != nil {
		if c.State == "preparing" && errors.Is(e, ErrForbidden) {
			return s.worldTerminal(ctx, p, c, "failed", ErrForbidden)
		}
		return WorldResult{}, e
	}
	var head string
	if e = s.db.QueryRow(ctx, "SELECT COALESCE(head_version_id::text,'') FROM studio.world_assets WHERE organization_id=$1 AND asset_id=$2", p.OrganizationID, asset).Scan(&head); e != nil {
		return WorldResult{}, e
	}
	switch c.State {
	case "committed":
		return *c.Result, nil
	case "conflict":
		return WorldResult{}, &WorldConflictError{head}
	case "failed":
		return WorldResult{}, errorCode(c.Error)
	}
	if head != c.Expected {
		return s.worldTerminal(ctx, p, c, "conflict", &WorldConflictError{head})
	}
	if c.Plan.Grants == nil {
		c.Plan.Grants, e = s.authorizationPlan(ctx, p, c)
		if e != nil {
			if errors.Is(e, ErrInvalid) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrForbidden) {
				return s.worldTerminal(ctx, p, c, "failed", e)
			}
			return WorldResult{}, e
		}
		if e = s.saveWorldPlan(ctx, p, c); e != nil {
			return WorldResult{}, e
		}
	}
	if e = s.coord.Store.Register(ctx, s.operation(p, c), c.Plan.Grants); e != nil {
		return WorldResult{}, coordinationError(e)
	}
	refs, e := s.checkWorldContents(ctx, p, c.Plan)
	if e != nil {
		if errors.Is(e, ErrInvalid) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrForbidden) {
			return s.worldTerminal(ctx, p, c, "failed", e)
		}
		return WorldResult{}, e
	}
	if c.Plan.ManifestRef == nil {
		data, e := json.Marshal(c.Plan.Manifest)
		if e != nil || len(data) > MaxManifestBytes {
			return s.worldTerminal(ctx, p, c, "failed", ErrInvalid)
		}
		v, e := s.content.WriteAssetManifest(ctx, p, asset, c.Version, TextWrite{WriteID: c.Plan.WriteID, Text: string(data), MIME: "application/json"})
		if e != nil {
			return WorldResult{}, e
		}
		if !v.ContentRef.Valid() || v.OrganizationID != p.OrganizationID || v.Source.Kind != "asset_manifest" || v.Source.AssetID != asset || v.Source.AssetVersionID != c.Version {
			return WorldResult{}, ErrDependency
		}
		c.Plan.ManifestRef = &v.ContentRef
	}
	c.Plan.Refs = uniqueRefs(append(refs, *c.Plan.ManifestRef))
	if len(c.Plan.Refs) > 10001 {
		return s.worldTerminal(ctx, p, c, "failed", ErrInvalid)
	}
	if e = s.saveWorldPlan(ctx, p, c); e != nil {
		return WorldResult{}, e
	}
	if e = s.content.Retain(ctx, p, RetentionOwner{Kind: "asset_version", ID: c.Version, CommitID: commit}, c.Plan.Refs); e != nil {
		if errors.Is(e, ErrForbidden) {
			return s.worldTerminal(ctx, p, c, "failed", ErrForbidden)
		}
		return WorldResult{}, e
	}
	if e = s.coord.Prepare(ctx, s.operation(p, c), c.Plan.Grants); e != nil {
		if errors.Is(e, coordination.ErrDenied) || errors.Is(e, coordination.ErrPending) || errors.Is(e, coordination.ErrConflict) {
			return s.worldTerminal(ctx, p, c, "failed", coordinationError(e))
		}
		return WorldResult{}, coordinationError(e)
	}
	return s.publishWorld(ctx, p, c)
}
func (s *Service) checkWorldContents(ctx context.Context, p Principal, plan worldPlan) ([]ContentRef, error) {
	if plan.Manifest.SchemaVersion == 2 {
		refs, _, err := s.draftPublicationContents(ctx, p, plan)
		return refs, err
	}
	m := plan.Manifest
	if m.Validate() != nil {
		return nil, ErrInvalid
	}
	source, e := s.Open(ctx, p, m.Source.ProjectID, m.Source.RevisionID)
	if e != nil {
		return nil, e
	}
	if source.ManifestRef != plan.SourceRef {
		return nil, ErrNotReady
	}
	request := WorldRegisterRequest{SourceProjectID: m.Source.ProjectID, SourceRevisionID: m.Source.RevisionID, ReviewFileID: m.Source.ReviewFileID}
	for _, file := range m.Files {
		request.FileIDs = append(request.FileIDs, file.ID)
	}
	// Artifact authorization callbacks need free DB connections: validate after
	// preparation commits, alongside the existing content checks, outside a tx.
	if e = s.checkWorldDramaPublication(ctx, p, request); e != nil {
		return nil, e
	}
	refs := []ContentRef{plan.SourceRef}
	for _, f := range m.Files {
		v, e := s.content.Metadata(ctx, p, f.Content, Access{ProjectID: m.Source.ProjectID, RevisionID: m.Source.RevisionID})
		if e != nil {
			return nil, e
		}
		if v.ContentRef != f.Content || v.OrganizationID != p.OrganizationID {
			return nil, ErrDependency
		}
		if m.Source.Relation == "produced_in" && v.Source.ProjectID != m.Source.ProjectID {
			return nil, ErrInvalid
		}
		refs = append(refs, f.Content)
	}
	for _, a := range m.DependsOn {
		more, e := s.References(ctx, p, a)
		if e != nil {
			return nil, e
		}
		refs = append(refs, more...)
	}
	return uniqueRefs(refs), nil
}
func (s *Service) publishWorld(ctx context.Context, p Principal, c worldCommit) (WorldResult, error) {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return WorldResult{}, e
	}
	defer tx.Rollback(context.Background())
	var state string
	if e = tx.QueryRow(ctx, "SELECT state FROM studio.world_commits WHERE organization_id=$1 AND asset_id=$2 AND commit_id=$3 FOR UPDATE", p.OrganizationID, c.AssetID, c.CommitID).Scan(&state); e != nil {
		return WorldResult{}, e
	}
	if state != "preparing" {
		return WorldResult{}, ErrIdempotency
	}
	if e = s.coord.Store.FinishTx(ctx, tx, s.operation(p, c), "committed"); e != nil {
		return WorldResult{}, coordinationError(e)
	}
	var head string
	e = tx.QueryRow(ctx, "SELECT COALESCE(head_version_id::text,'') FROM studio.world_assets WHERE organization_id=$1 AND asset_id=$2 FOR UPDATE", p.OrganizationID, c.AssetID).Scan(&head)
	if e != nil {
		return WorldResult{}, e
	}
	if head != c.Expected {
		_ = tx.Rollback(ctx)
		return s.worldTerminal(ctx, p, c, "conflict", &WorldConflictError{head})
	}
	result := WorldResult{AssetID: c.AssetID, VersionID: c.Version, ManifestRef: *c.Plan.ManifestRef, Manifest: c.Plan.Manifest}
	ref, _ := json.Marshal(result.ManifestRef)
	manifest, _ := json.Marshal(result.Manifest)
	data, _ := json.Marshal(result)
	subjects, _ := json.Marshal(result.Manifest.Subjects)
	sourceRef, _ := json.Marshal(c.Plan.SourceRef)
	_, e = tx.Exec(ctx, `INSERT INTO studio.world_asset_versions(version_id,organization_id,asset_id,parent_version_id,source_project_id,source_revision_id,manifest_ref,manifest_json,created_by,commit_id,source_manifest_ref,source_draft_id) VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid)`, c.Version, p.OrganizationID, c.AssetID, c.Expected, c.Plan.Manifest.Source.ProjectID, publicationRevision(c.Plan.Manifest.Source), ref, manifest, p.SubjectID, c.CommitID, sourceRef, c.Plan.Manifest.Source.DraftID)
	if e != nil {
		return WorldResult{}, e
	}
	for _, a := range c.Plan.Manifest.DependsOn {
		if _, e = tx.Exec(ctx, "INSERT INTO studio.world_version_dependencies(organization_id,asset_id,version_id,dependency_asset_id,dependency_version_id,purpose) VALUES($1,$2,$3,$4,$5,$6)", p.OrganizationID, c.AssetID, c.Version, a.AssetID, a.VersionID, a.Purpose); e != nil {
			return WorldResult{}, e
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE studio.world_assets SET head_version_id=$1,name=$2,subjects=$3 WHERE organization_id=$4 AND asset_id=$5", c.Version, c.Plan.Manifest.Name, subjects, p.OrganizationID, c.AssetID); e != nil {
		return WorldResult{}, e
	}
	if _, e = tx.Exec(ctx, "UPDATE studio.world_commits SET state='committed',result=$1,updated_at=now() WHERE organization_id=$2 AND asset_id=$3 AND commit_id=$4", data, p.OrganizationID, c.AssetID, c.CommitID); e != nil {
		return WorldResult{}, e
	}
	if e = audit(ctx, tx, p, "world.publish", c.Version); e != nil {
		return WorldResult{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return WorldResult{}, e
	}
	return result, nil
}

func (s *Service) WorldGet(ctx context.Context, p Principal, r WorldGetRequest) (WorldResult, error) {
	if !ValidID(r.VersionID) {
		return WorldResult{}, ErrInvalid
	}
	if _, e := assetRole(ctx, s.db, p, r.AssetID); e != nil {
		return WorldResult{}, e
	}
	projection, ref, e := assetVersion(ctx, s.db, p.OrganizationID, r.AssetID, r.VersionID)
	if e != nil {
		return WorldResult{}, e
	}
	data, e := s.content.Read(ctx, p, ref, Access{AssetID: r.AssetID, AssetVersionID: r.VersionID}, MaxManifestBytes)
	if e != nil {
		return WorldResult{}, e
	}
	m, e := decodeAsset(data)
	if e != nil || m.AssetID != r.AssetID {
		return WorldResult{}, ErrNotReady
	}
	a, _ := digest(m)
	b, _ := digest(projection)
	if a != b {
		return WorldResult{}, ErrNotReady
	}
	return WorldResult{AssetID: r.AssetID, VersionID: r.VersionID, ManifestRef: ref, Manifest: m}, nil
}
func (s *Service) WorldList(ctx context.Context, p Principal, r WorldListRequest) (WorldListResult, error) {
	out := WorldListResult{Items: []WorldListItem{}}
	if !p.Valid() {
		return out, ErrForbidden
	}
	if r.Limit == 0 {
		r.Limit = 50
	}
	if r.Limit < 1 || r.Limit > 100 || (r.Cursor != "" && !ValidID(r.Cursor)) || (r.AssetType != "" && !tokenPattern.MatchString(r.AssetType)) || (r.Subject != "" && !validText(r.Subject)) {
		return out, ErrInvalid
	}
	rows, e := s.db.Query(ctx, `SELECT a.asset_id::text,a.head_version_id::text,a.name,a.asset_type,a.subjects,a.created_at FROM studio.world_assets a JOIN studio.world_asset_grants g USING(organization_id,asset_id) WHERE a.organization_id=$1 AND g.subject_id=$2 AND a.head_version_id IS NOT NULL AND ($3='' OR a.asset_type=$3) AND ($4='' OR a.subjects ? $4) AND ($5='' OR a.asset_id>NULLIF($5,'')::uuid) ORDER BY a.asset_id LIMIT $6`, p.OrganizationID, p.SubjectID, r.AssetType, r.Subject, r.Cursor, r.Limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var v WorldListItem
		var subjects []byte
		if e = rows.Scan(&v.AssetID, &v.HeadVersionID, &v.Name, &v.AssetType, &subjects, &v.CreatedAt); e != nil {
			return out, e
		}
		if e = json.Unmarshal(subjects, &v.Subjects); e != nil {
			return out, e
		}
		v.CreatedAt = v.CreatedAt.UTC()
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > r.Limit {
		out.Items = out.Items[:r.Limit]
		out.NextCursor = out.Items[len(out.Items)-1].AssetID
	}
	return out, nil
}
func (s *Service) WorldCommitStatus(ctx context.Context, p Principal, r WorldStatusRequest) (WorldStatus, error) {
	if !p.Valid() {
		return WorldStatus{}, ErrForbidden
	}
	if !ValidID(r.CommitID) {
		return WorldStatus{}, ErrInvalid
	}
	if r.AssetID == "" {
		err := s.db.QueryRow(ctx, "SELECT asset_id::text FROM studio.world_create_requests WHERE organization_id=$1 AND subject_id=$2 AND commit_id=$3", p.OrganizationID, p.SubjectID, r.CommitID).Scan(&r.AssetID)
		if errors.Is(err, pgx.ErrNoRows) {
			return WorldStatus{}, ErrNotFound
		}
		if err != nil {
			return WorldStatus{}, err
		}
	}
	member, e := assetRole(ctx, s.db, p, r.AssetID)
	if e != nil {
		return WorldStatus{}, e
	}
	c, e := loadWorldCommit(ctx, s.db, p.OrganizationID, r.AssetID, r.CommitID)
	if e != nil {
		return WorldStatus{}, e
	}
	if c.Actor != p.SubjectID && member != "owner" {
		return WorldStatus{}, ErrForbidden
	}
	result := WorldStatus{State: c.State, RequestHash: c.Hash, Result: c.Result, ErrorCode: c.Error}
	if c.State == "conflict" {
		e = s.db.QueryRow(ctx, "SELECT head_version_id::text FROM studio.world_assets WHERE organization_id=$1 AND asset_id=$2", p.OrganizationID, r.AssetID).Scan(&result.CurrentVersionID)
	}
	return result, e
}
