package world

import "context"

type PackageReference struct {
	Kind      string         `json:"kind"`
	Reference DraftSourceRef `json:"reference"`
	Content   ContentRef     `json:"content_ref"`
	MIME      string         `json:"mime"`
	Size      int64          `json:"size"`
	SHA256    string         `json:"sha256"`
}

// The caller supplies one explicit fixed business reference, never a content
// identity, owner, URL or capability. No drafts, grants or commits are created.
// WorldDraft validates the closed request schema and principal before this call.
func (s *Service) resolvePackageReference(ctx context.Context, p Principal, r DraftSourceRef) (PackageReference, error) {
	var zero PackageReference
	if !p.Valid() {
		return zero, ErrForbidden
	}
	if s.content == nil {
		return zero, ErrNotReady
	}
	var files []File
	var access Access
	switch r.Kind {
	case "world_draft":
		source, err := s.getDraft(ctx, p, r.DraftID, r.RevisionID)
		if err != nil {
			return zero, err
		}
		if source.DraftID != r.DraftID || source.RevisionID != r.RevisionID {
			return zero, ErrNotReady
		}
		files = source.Manifest.Files
		access = Access{WorldDraftID: r.DraftID, WorldDraftRevisionID: r.RevisionID}
	case "project":
		source, err := s.Open(ctx, p, r.ProjectID, r.RevisionID)
		if err != nil {
			return zero, err
		}
		if source.ProjectID != r.ProjectID || source.RevisionID != r.RevisionID || source.Manifest.ProjectID != r.ProjectID || source.Manifest.Validate() != nil {
			return zero, ErrNotReady
		}
		files = source.Manifest.Files
		access = Access{ProjectID: r.ProjectID, RevisionID: r.RevisionID}
	case "asset":
		source, err := s.WorldGet(ctx, p, WorldGetRequest{AssetID: r.AssetID, VersionID: r.AssetVersionID})
		if err != nil {
			return zero, err
		}
		if source.AssetID != r.AssetID || source.VersionID != r.AssetVersionID {
			return zero, ErrNotReady
		}
		files = source.Manifest.Files
		access = Access{AssetID: r.AssetID, AssetVersionID: r.AssetVersionID}
	default:
		return zero, ErrInvalid
	}
	var selected *File
	for _, file := range files {
		if file.ID == r.FileID {
			if selected != nil || !file.Content.Valid() {
				return zero, ErrNotReady
			}
			copy := file
			selected = &copy
		}
	}
	if selected == nil {
		return zero, ErrNotFound
	}
	v, err := s.content.Metadata(ctx, p, selected.Content, access)
	if err != nil {
		return zero, err
	}
	if v.ContentRef != selected.Content || v.OrganizationID != p.OrganizationID || v.Size < 0 || !validText(v.MIME) || len(v.MIME) > 256 || !producerHashPattern.MatchString(v.SHA256) {
		return zero, ErrNotReady
	}
	if r.ObjectKey != "" {
		if _, err = s.readLearningSourceObject(ctx, p, r, *selected, files, access); err != nil {
			return zero, err
		}
	}
	return PackageReference{Kind: "reference", Reference: r, Content: selected.Content, MIME: v.MIME, Size: v.Size, SHA256: v.SHA256}, nil
}
