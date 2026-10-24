package world

import "context"

type ProjectSampleReference struct {
	DocumentFileID string `json:"document_file_id"`
	ProjectID      string `json:"project_id"`
	RevisionID     string `json:"revision_id"`
	FileID         string `json:"file_id"`
}
type ResolvedSampleFile struct {
	ProjectSampleReference
	Content ContentRef `json:"content_ref"`
}

// Called only after WorldGet has verified the exact asset and current grant.
// A shared asset does not confer access to its source project.
func (s *Service) resolveSampleFile(ctx context.Context, p Principal, asset WorldResult, r ProjectSampleReference) (*ResolvedSampleFile, error) {
	if !p.Valid() {
		return nil, ErrForbidden
	}
	if !ValidID(r.DocumentFileID) || !ValidID(r.ProjectID) || !ValidID(r.RevisionID) || !ValidID(r.FileID) {
		return nil, ErrInvalid
	}
	declared := false
	if asset.Manifest.DomainDocuments != nil {
		for _, d := range *asset.Manifest.DomainDocuments {
			if d.Type == "reference-learning" && d.FileID == r.DocumentFileID {
				declared = true
			}
		}
	}
	if !declared {
		return nil, ErrInvalid
	}
	var document File
	for _, f := range asset.Manifest.Files {
		if f.ID == r.DocumentFileID {
			document = f
		}
	}
	if !document.Content.Valid() {
		return nil, ErrNotReady
	}
	data, err := s.content.Read(ctx, p, document.Content, Access{AssetID: asset.AssetID, AssetVersionID: asset.VersionID}, MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	doc, err := ValidateLearningContent(data, asset.Manifest.Files)
	if err != nil {
		return nil, err
	}
	found := false
	for _, ref := range doc.RemoteRefs {
		if ref.Kind == "project" && ref.ProjectID == r.ProjectID && ref.RevisionID == r.RevisionID && ref.FileID == r.FileID && ref.ObjectKey == "" {
			found = true
		}
	}
	if !found {
		return nil, ErrInvalid
	}
	project, err := s.Open(ctx, p, r.ProjectID, r.RevisionID)
	if err != nil {
		return nil, err
	}
	if project.ProjectID != r.ProjectID || project.RevisionID != r.RevisionID || project.Manifest.ProjectID != r.ProjectID {
		return nil, ErrDependency
	}
	for _, file := range project.Manifest.Files {
		if file.ID == r.FileID && file.Content.Valid() {
			return &ResolvedSampleFile{r, file.Content}, nil
		}
	}
	return nil, ErrNotFound
}
