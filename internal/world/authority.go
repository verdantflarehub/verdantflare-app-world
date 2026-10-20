package world

import (
	"context"
)

type Permission struct {
	Action  string          `json:"action"`
	Source  *Source         `json:"source,omitempty"`
	Version *ContentVersion `json:"version,omitempty"`
	Owner   *RetentionOwner `json:"owner,omitempty"`
	Access  *Access         `json:"access,omitempty"`
}

func (s *Service) AuthorizeArtifact(ctx context.Context, p Principal, permission Permission) error {
	if !p.Valid() {
		return ErrForbidden
	}
	switch permission.Action {
	case "write":
		if permission.Source == nil || permission.Version != nil || permission.Owner != nil || permission.Access != nil {
			return ErrForbidden
		}
		if permission.Source.WorldDraftID != "" || permission.Source.WorldDraftRevisionID != "" || permission.Source.Kind == "world_draft_manifest" {
			return s.authorizeDraftWrite(ctx, p, *permission.Source)
		}
		return s.authorizeWorldWrite(ctx, p, *permission.Source)
	case "read":
		if permission.Version == nil || permission.Access == nil || permission.Source != nil || permission.Owner != nil {
			return ErrForbidden
		}
		a := *permission.Access
		if a.WorldDraftID != "" || a.WorldDraftRevisionID != "" || (a == (Access{}) && permission.Version.Source.WorldDraftID != "") {
			return s.authorizeDraftRead(ctx, p, *permission.Version, a)
		}
		return s.authorizeAssetRead(ctx, p, *permission.Version, a)
	case "retain", "retain_content", "inspect_retention", "release":
		if permission.Owner != nil && permission.Owner.Kind == "world_draft_revision" {
			return s.authorizeDraftRetention(ctx, p, permission)
		}
		return s.authorizeWorldRetention(ctx, p, permission)
	default:
		return ErrForbidden
	}
}
