package world

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

// References resolves the exact dependency closure without reading or granting
// access to source-project manifests. Retention verifies physical bytes later.
func (s *Service) References(ctx context.Context, p Principal, a AssetRef) ([]ContentRef, error) {
	return assetReferences(ctx, s.db, p, a, false, nil)
}

type ReferencesResult struct {
	Refs   []ContentRef         `json:"refs"`
	Grants []coordination.Grant `json:"grants"`
}

func (s *Service) ReferencesWithGrants(ctx context.Context, p Principal, a AssetRef) (ReferencesResult, error) {
	grants := map[string]bool{}
	refs, err := assetReferences(ctx, s.db, p, a, false, grants)
	if err != nil {
		return ReferencesResult{}, err
	}
	plan := coordination.Plan{"world": {}}
	for resource := range grants {
		plan["world"] = append(plan["world"], coordination.Grant{ResourceID: resource, Write: false})
	}
	plan, err = coordination.Normalize(plan)
	if err != nil {
		return ReferencesResult{}, ErrInvalid
	}
	return ReferencesResult{Refs: refs, Grants: plan["world"]}, nil
}

func assetReferences(ctx context.Context, q queryer, p Principal, a AssetRef, lock bool, grants map[string]bool) ([]ContentRef, error) {
	if !p.Valid() || !ValidID(a.AssetID) || !ValidID(a.VersionID) || !validText(a.Purpose) {
		return nil, ErrInvalid
	}
	type node struct {
		ref   AssetRef
		depth int
	}
	queue := []node{{a, 0}}
	seen := map[WorldGetRequest]bool{}
	refs := []ContentRef{}
	for len(queue) > 0 {
		n := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		key := WorldGetRequest{AssetID: n.ref.AssetID, VersionID: n.ref.VersionID}
		if n.depth > 32 {
			return nil, ErrInvalid
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > 10000 {
			return nil, ErrInvalid
		}
		if lock {
			var role string
			e := q.QueryRow(ctx, "SELECT role FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3 FOR SHARE", p.OrganizationID, key.AssetID, p.SubjectID).Scan(&role)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil, ErrForbidden
			}
			if e != nil {
				return nil, e
			}
		} else if _, e := assetRole(ctx, q, p, key.AssetID); e != nil {
			return nil, e
		}
		if grants != nil {
			grants[key.AssetID] = true
		}
		m, ref, e := assetVersion(ctx, q, p.OrganizationID, key.AssetID, key.VersionID)
		if e != nil {
			return nil, e
		}
		if m.Validate() != nil || !ref.Valid() {
			return nil, ErrNotReady
		}
		refs = append(refs, ref)
		for _, f := range m.Files {
			refs = append(refs, f.Content)
		}
		refs = uniqueRefs(refs)
		if len(refs) > MaxFiles {
			return nil, ErrInvalid
		}
		for _, d := range m.DependsOn {
			queue = append(queue, node{d, n.depth + 1})
		}
	}
	return refs, nil
}
func (s *Service) authorizeAssetRead(ctx context.Context, p Principal, v ContentVersion, a Access) error {
	if a.WorldDraftID != "" || a.WorldDraftRevisionID != "" || a.ProjectID != "" || a.RevisionID != "" || !ValidID(a.AssetID) || !ValidID(a.AssetVersionID) || v.OrganizationID != p.OrganizationID || !v.ContentRef.Valid() {
		return ErrForbidden
	}
	if _, e := assetRole(ctx, s.db, p, a.AssetID); e != nil {
		return e
	}
	m, ref, e := assetVersion(ctx, s.db, p.OrganizationID, a.AssetID, a.AssetVersionID)
	if e != nil {
		return e
	}
	if ref == v.ContentRef {
		return nil
	}
	for _, f := range m.Files {
		if f.Content == v.ContentRef {
			return nil
		}
	}
	return ErrForbidden
}
func (s *Service) authorizeWorldWrite(ctx context.Context, p Principal, source Source) error {
	if source.WorldDraftID != "" || source.WorldDraftRevisionID != "" || source.Kind != "asset_manifest" || !ValidID(source.AssetID) || !ValidID(source.AssetVersionID) || source.ProjectID != "" || source.ServiceID != "" || source.RunID != "" || source.TaskID != "" || source.Original != nil {
		return ErrForbidden
	}
	if e := assetWritable(ctx, s.db, p, source.AssetID); e != nil {
		return e
	}
	var commit string
	e := s.db.QueryRow(ctx, "SELECT commit_id::text FROM studio.world_commits WHERE organization_id=$1 AND asset_id=$2 AND version_id=$3", p.OrganizationID, source.AssetID, source.AssetVersionID).Scan(&commit)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if e != nil {
		return e
	}
	c, e := loadWorldCommit(ctx, s.db, p.OrganizationID, source.AssetID, commit)
	if e != nil {
		return e
	}
	if c.Actor != p.SubjectID || (c.State != "preparing" && c.State != "committed") {
		return ErrForbidden
	}
	if c.Plan.Manifest.SchemaVersion == 2 {
		_, _, e = draftOwner(ctx, s.db, p, c.Plan.Manifest.Source.DraftID)
		return e
	}
	_, e = s.Open(ctx, p, c.Plan.Manifest.Source.ProjectID, c.Plan.Manifest.Source.RevisionID)
	return e
}
func (s *Service) authorizeWorldRetention(ctx context.Context, p Principal, permission Permission) error {
	o := permission.Owner
	if o == nil || o.Kind != "asset_version" || !ValidID(o.ID) || !ValidID(o.CommitID) || permission.Source != nil || permission.Access != nil || (permission.Action == "retain_content") != (permission.Version != nil) {
		return ErrForbidden
	}
	var asset string
	e := s.db.QueryRow(ctx, "SELECT asset_id::text FROM studio.world_commits WHERE organization_id=$1 AND version_id=$2 AND commit_id=$3", p.OrganizationID, o.ID, o.CommitID).Scan(&asset)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if e != nil {
		return e
	}
	member, e := assetRole(ctx, s.db, p, asset)
	if e != nil {
		return e
	}
	c, e := loadWorldCommit(ctx, s.db, p.OrganizationID, asset, o.CommitID)
	if e != nil {
		return e
	}
	if c.Actor != p.SubjectID && member != "owner" {
		return ErrForbidden
	}
	if permission.Action == "inspect_retention" {
		return nil
	}
	if member != "owner" && member != "editor" {
		return ErrForbidden
	}
	if permission.Action == "release" {
		if c.State != "failed" && c.State != "conflict" {
			return ErrForbidden
		}
		var exists bool
		e = s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM studio.world_asset_versions WHERE version_id=$1)", o.ID).Scan(&exists)
		if e != nil {
			return e
		}
		if exists {
			return ErrForbidden
		}
		return nil
	}
	if (c.State != "preparing" && c.State != "committed") || c.Plan.ManifestRef == nil || len(c.Plan.Refs) == 0 {
		return ErrForbidden
	}
	if permission.Action == "retain" {
		return nil
	}
	if permission.Action != "retain_content" || permission.Version.OrganizationID != p.OrganizationID {
		return ErrForbidden
	}
	ref := permission.Version.ContentRef
	found := false
	for _, r := range c.Plan.Refs {
		if r == ref {
			found = true
			break
		}
	}
	if !found {
		return ErrForbidden
	}
	if ref == *c.Plan.ManifestRef {
		source := permission.Version.Source
		if source.WorldDraftID != "" || source.WorldDraftRevisionID != "" || source.Kind != "asset_manifest" || source.AssetID != asset || source.AssetVersionID != c.Version {
			return ErrForbidden
		}
		return nil
	}
	isSource := ref == c.Plan.SourceRef
	for _, f := range c.Plan.Manifest.Files {
		if f.Content == ref {
			isSource = true
		}
	}
	if isSource {
		a := c.Plan.Manifest.Source.access()
		if c.Plan.Manifest.SchemaVersion == 2 {
			return s.authorizeDraftRead(ctx, p, *permission.Version, a)
		}
		return s.projects.Authorize(ctx, p, Permission{Action: "read", Version: permission.Version, Access: &a})
	}
	return s.authorizeDependencyContent(ctx, p, c.Plan.Manifest.DependsOn, ref)
}
func (s *Service) authorizeDependencyContent(ctx context.Context, p Principal, assets []AssetRef, ref ContentRef) error {
	found := false
	for _, a := range assets {
		refs, e := s.References(ctx, p, a)
		if e != nil {
			return e
		}
		for _, r := range refs {
			if r == ref {
				found = true
			}
		}
	}
	if found {
		return nil
	}
	return ErrForbidden
}
func (s *Service) WorldGrant(ctx context.Context, p Principal, r WorldGrantRequest) (WorldGrantResult, error) {
	if r.Role != "reader" && r.Role != "editor" {
		return WorldGrantResult{}, ErrInvalid
	}
	return s.setWorldGrant(ctx, p, r.AssetID, r.SubjectID, r.Role)
}
func (s *Service) WorldRevoke(ctx context.Context, p Principal, r WorldRevokeRequest) (WorldGrantResult, error) {
	return s.setWorldGrant(ctx, p, r.AssetID, r.SubjectID, "none")
}
func (s *Service) setWorldGrant(ctx context.Context, p Principal, asset, subject, wanted string) (WorldGrantResult, error) {
	result := WorldGrantResult{AssetID: asset, SubjectID: subject, Role: wanted}
	if !p.Valid() || !ValidID(asset) || !ValidID(subject) {
		return result, ErrInvalid
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return result, e
	}
	defer tx.Rollback(context.Background())
	var actor string
	e = tx.QueryRow(ctx, "SELECT role FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3 FOR SHARE", p.OrganizationID, asset, p.SubjectID).Scan(&actor)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && actor != "owner" {
		return result, ErrForbidden
	}
	if e != nil {
		return result, e
	}
	var owner, head string
	e = tx.QueryRow(ctx, "SELECT owner_id::text,COALESCE(head_version_id::text,'') FROM studio.world_assets WHERE organization_id=$1 AND asset_id=$2", p.OrganizationID, asset).Scan(&owner, &head)
	if e != nil {
		return result, e
	}
	if owner == subject {
		return result, ErrInvalid
	}
	if head == "" {
		return result, ErrNotReady
	}
	if e = advisory(ctx, tx, "asset-grant:"+p.OrganizationID+":"+asset+":"+subject); e != nil {
		return result, e
	}
	var previous string
	e = tx.QueryRow(ctx, "SELECT role FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3", p.OrganizationID, asset, subject).Scan(&previous)
	if errors.Is(e, pgx.ErrNoRows) {
		previous = "none"
	} else if e != nil {
		return result, e
	}
	var pendingRole, pendingActor, pendingState string
	e = tx.QueryRow(ctx, "SELECT wanted_role,requested_by::text,state FROM studio.world_grant_changes WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3 FOR UPDATE", p.OrganizationID, asset, subject).Scan(&pendingRole, &pendingActor, &pendingState)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return result, e
	}
	if e == nil && pendingState == "pending" && (pendingRole != wanted || pendingActor != p.SubjectID) {
		return result, coordination.ErrPending
	}
	if previous == wanted && pendingState != "pending" {
		return result, tx.Commit(ctx)
	}
	if pendingState != "pending" {
		if _, e = tx.Exec(ctx, "INSERT INTO studio.world_grant_changes(organization_id,asset_id,subject_id,requested_by,request_id,wanted_role,state) VALUES($1,$2,$3,$4,$5,$6,'pending') ON CONFLICT(organization_id,asset_id,subject_id) DO UPDATE SET requested_by=EXCLUDED.requested_by,request_id=EXCLUDED.request_id,wanted_role=EXCLUDED.wanted_role,state='pending',updated_at=now()", p.OrganizationID, asset, subject, p.SubjectID, p.RequestID, wanted); e != nil {
			return result, e
		}
		if e = audit(ctx, tx, p, "world.grant.pending."+wanted, asset); e != nil {
			return result, e
		}
	}
	// Persist a revocation/downgrade barrier before changing a grant. Pending
	// reservations retain the old role; only an authorized retry can complete it.
	pending, err := s.coord.Store.BlockTx(ctx, tx, p.OrganizationID, asset, subject)
	if err != nil {
		return result, err
	}
	if pending {
		if err = tx.Commit(ctx); err != nil {
			return result, err
		}
		return result, coordination.ErrPending
	}
	if wanted == "none" {
		_, e = tx.Exec(ctx, "DELETE FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3", p.OrganizationID, asset, subject)
	} else {
		_, e = tx.Exec(ctx, "INSERT INTO studio.world_asset_grants(organization_id,asset_id,subject_id,role) VALUES($1,$2,$3,$4) ON CONFLICT(organization_id,asset_id,subject_id) DO UPDATE SET role=EXCLUDED.role", p.OrganizationID, asset, subject, wanted)
	}
	if e != nil {
		return result, e
	}
	if e = s.coord.Store.ChangedTx(ctx, tx, p.OrganizationID, asset, subject); e != nil {
		return result, e
	}
	if _, e = tx.Exec(ctx, "UPDATE studio.world_grant_changes SET state='completed',updated_at=now() WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3", p.OrganizationID, asset, subject); e != nil {
		return result, e
	}
	if e = audit(ctx, tx, p, "world.grant."+wanted, asset); e != nil {
		return result, e
	}
	return result, tx.Commit(ctx)
}
