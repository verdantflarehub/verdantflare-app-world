package world

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (s *Service) authorizeDraftWrite(ctx context.Context, p Principal, source Source) error {
	if !draftUserSource(source, source.WorldDraftID) && !draftManifestSource(source, source.WorldDraftID, source.WorldDraftRevisionID) {
		return ErrForbidden
	}
	head, _, err := draftOwner(ctx, s.db, p, source.WorldDraftID)
	if err != nil {
		return err
	}
	if source.Kind != "world_draft_manifest" {
		if head == "" {
			return ErrNotReady
		}
		return nil
	}
	var commit string
	err = s.db.QueryRow(ctx, `SELECT commit_id::text FROM studio.world_draft_commits WHERE organization_id=$1 AND created_by=$2 AND draft_id=$3 AND revision_id=$4 AND state IN ('preparing','committed')`, p.OrganizationID, p.SubjectID, source.WorldDraftID, source.WorldDraftRevisionID).Scan(&commit)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}
func (s *Service) authorizeDraftRead(ctx context.Context, p Principal, v ContentVersion, a Access) error {
	if v.OrganizationID != p.OrganizationID || !v.ContentRef.Valid() {
		return ErrForbidden
	}
	if a == (Access{}) {
		if draftUserSource(v.Source, v.Source.WorldDraftID) {
			_, _, err := draftOwner(ctx, s.db, p, v.Source.WorldDraftID)
			return err
		}
		if draftManifestSource(v.Source, v.Source.WorldDraftID, v.Source.WorldDraftRevisionID) {
			return s.authorizeDraftRead(ctx, p, v, Access{WorldDraftID: v.Source.WorldDraftID, WorldDraftRevisionID: v.Source.WorldDraftRevisionID})
		}
		return ErrForbidden
	}
	if !draftAccess(a) {
		return ErrForbidden
	}
	r, err := draftRevision(ctx, s.db, p, a.WorldDraftID, a.WorldDraftRevisionID)
	if err != nil {
		return err
	}
	if r.ManifestRef == v.ContentRef {
		return nil
	}
	for _, f := range r.Manifest.Files {
		if f.Content == v.ContentRef {
			return nil
		}
	}
	return ErrForbidden
}
func (s *Service) authorizeDraftRetention(ctx context.Context, p Principal, permission Permission) error {
	o := permission.Owner
	if o == nil || o.Kind != "world_draft_revision" || !ValidID(o.ID) || !ValidID(o.CommitID) || permission.Source != nil || permission.Access != nil || (permission.Action == "retain_content") != (permission.Version != nil) {
		return ErrForbidden
	}
	c, err := loadDraftCommit(ctx, s.db, p, o.CommitID)
	if err != nil {
		return err
	}
	if c.Revision != o.ID {
		return ErrForbidden
	}
	if _, _, err = draftOwner(ctx, s.db, p, c.DraftID); err != nil {
		return err
	}
	if permission.Action == "inspect_retention" {
		return nil
	}
	if permission.Action == "release" {
		if c.State != "failed" && c.State != "conflict" {
			return ErrForbidden
		}
		var exists bool
		if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_draft_revisions WHERE organization_id=$1 AND revision_id=$2)`, p.OrganizationID, o.ID).Scan(&exists); err != nil {
			return err
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
	if permission.Action != "retain_content" || permission.Version.OrganizationID != p.OrganizationID || !permission.Version.ContentRef.Valid() {
		return ErrForbidden
	}
	v := *permission.Version
	found := false
	for _, ref := range c.Plan.Refs {
		if ref == v.ContentRef {
			found = true
			break
		}
	}
	if !found {
		return ErrForbidden
	}
	if v.ContentRef == *c.Plan.ManifestRef {
		if !draftManifestSource(v.Source, c.DraftID, c.Revision) {
			return ErrForbidden
		}
		return nil
	}
	// A completed revision is an independent retention owner of its explicit
	// file inventory; subsequent source grant revocation does not erase it.
	if c.State == "committed" {
		for _, f := range c.Plan.Manifest.Files {
			if f.Content == v.ContentRef {
				return nil
			}
		}
	}
	inputs, err := s.resolveDraftInputs(ctx, p, c)
	if err != nil {
		return err
	}
	for _, f := range c.Plan.Manifest.Files {
		if f.Content == v.ContentRef {
			a := inputs.Access[v.ContentRef]
			switch {
			case a == (Access{}):
				if !draftUserSource(v.Source, c.DraftID) {
					return ErrForbidden
				}
				return nil
			case a.WorldDraftID != "":
				return s.authorizeDraftRead(ctx, p, v, a)
			case a.ProjectID != "":
				return s.projects.Authorize(ctx, p, Permission{Action: "read", Version: &v, Access: &a})
			case a.AssetID != "":
				return s.authorizeAssetRead(ctx, p, v, a)
			default:
				return ErrForbidden
			}
		}
	}
	return s.authorizeDependencyContent(ctx, p, c.Plan.Manifest.AssetRefs, v.ContentRef)
}
