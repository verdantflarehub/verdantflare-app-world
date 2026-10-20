package world

import (
	"context"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

type publicationSource struct {
	Project *OpenResult
	Draft   *DraftRevision
}

func (s *Service) openPublicationSource(ctx context.Context, p Principal, r WorldRegisterRequest) (publicationSource, error) {
	if r.SourceDraftID != "" {
		v, err := s.getDraft(ctx, p, r.SourceDraftID, r.SourceRevisionID)
		return publicationSource{Draft: &v}, err
	}
	v, err := s.Open(ctx, p, r.SourceProjectID, r.SourceRevisionID)
	return publicationSource{Project: &v}, err
}
func preparePublication(r WorldRegisterRequest, asset string, source publicationSource) (worldPlan, error) {
	if r.SourceDraftID != "" {
		if source.Draft == nil || source.Project != nil {
			return worldPlan{}, ErrDependency
		}
		return prepareDraftAsset(r, asset, *source.Draft)
	}
	if source.Project == nil || source.Draft != nil {
		return worldPlan{}, ErrDependency
	}
	return prepareWorld(r, asset, *source.Project)
}
func prepareDraftAsset(r WorldRegisterRequest, asset string, opened DraftRevision) (worldPlan, error) {
	source := opened.Manifest
	if opened.DraftID != r.SourceDraftID || opened.RevisionID != r.SourceRevisionID || source.DraftID != r.SourceDraftID || source.Validate() != nil || !opened.ManifestRef.Valid() {
		return worldPlan{}, ErrDependency
	}
	byID := map[string]File{}
	for _, f := range source.Files {
		byID[f.ID] = f
	}
	files := []File{}
	selected := map[string]bool{}
	for _, id := range r.FileIDs {
		f, ok := byID[id]
		if !ok || selected[id] {
			return worldPlan{}, ErrInvalid
		}
		files = append(files, f)
		selected[id] = true
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
	if r.DependsOn == nil {
		return worldPlan{}, ErrInvalid
	}
	for _, a := range *r.DependsOn {
		if !sourceDeps[a] {
			return worldPlan{}, ErrInvalid
		}
	}
	documents := []DomainDocument{}
	for _, d := range source.DomainDocuments {
		if selected[d.FileID] {
			documents = append(documents, d)
		}
	}
	m := AssetManifest{SchemaVersion: 2, Kind: "asset-version", AssetID: asset, Name: r.Name, AssetType: r.AssetType, Subjects: r.Subjects, Files: files, Source: AssetSource{Kind: "world_draft", DraftID: r.SourceDraftID, DraftRevisionID: r.SourceRevisionID, Relation: r.Relation, ReviewFileID: r.ReviewFileID}, DependsOn: *r.DependsOn, DomainDocuments: &documents}
	if m.Validate() != nil {
		return worldPlan{}, ErrInvalid
	}
	return worldPlan{Manifest: m, SourceRef: opened.ManifestRef, WriteID: newID(), Refs: []ContentRef{}}, nil
}
func (source AssetSource) access() Access {
	if source.Kind == "world_draft" {
		return Access{WorldDraftID: source.DraftID, WorldDraftRevisionID: source.DraftRevisionID}
	}
	return Access{ProjectID: source.ProjectID, RevisionID: source.RevisionID}
}

// Publication validates only selected payloads and their explicit dependencies.
// The source manifest is retained as provenance, never exposed through asset read.
func (s *Service) draftPublicationContents(ctx context.Context, p Principal, plan worldPlan) ([]ContentRef, coordination.Plan, error) {
	m := plan.Manifest
	if m.Validate() != nil || m.SchemaVersion != 2 {
		return nil, nil, ErrInvalid
	}
	source, err := s.getDraft(ctx, p, m.Source.DraftID, m.Source.DraftRevisionID)
	if err != nil {
		return nil, nil, err
	}
	if source.ManifestRef != plan.SourceRef {
		return nil, nil, ErrNotReady
	}
	ids := []string{}
	for _, f := range m.Files {
		ids = append(ids, f.ID)
	}
	deps := append([]AssetRef{}, m.DependsOn...)
	expected, err := prepareDraftAsset(WorldRegisterRequest{SourceDraftID: m.Source.DraftID, SourceRevisionID: m.Source.DraftRevisionID, FileIDs: ids, Name: m.Name, AssetType: m.AssetType, Subjects: m.Subjects, Relation: m.Source.Relation, ReviewFileID: m.Source.ReviewFileID, DependsOn: &deps}, m.AssetID, source)
	if err != nil {
		return nil, nil, err
	}
	h1, _ := digest(expected.Manifest)
	h2, _ := digest(m)
	if h1 != h2 {
		return nil, nil, ErrNotReady
	}
	selected := source.Manifest
	selected.Files = m.Files
	selected.DomainDocuments = *m.DomainDocuments
	selected.AssetRefs = m.DependsOn
	selected.RunRefs = []RunRef{}
	selected.SourceRefs = []DraftSourceRef{}
	declared := map[DraftSourceRef]bool{}
	for _, r := range source.Manifest.SourceRefs {
		declared[r] = true
	}
	needed := map[DraftSourceRef]bool{}
	for _, entry := range selected.DomainDocuments {
		if entry.Type != "reference-learning" {
			return nil, nil, ErrNotReady
		}
		for _, f := range selected.Files {
			if f.ID == entry.FileID {
				data, err := s.content.Read(ctx, p, f.Content, m.Source.access(), MaxManifestBytes)
				if err != nil {
					return nil, nil, err
				}
				doc, err := ValidateLearningContent(data, selected.Files)
				if err != nil {
					return nil, nil, err
				}
				for _, r := range doc.RemoteRefs {
					if !declared[r] {
						return nil, nil, ErrInvalid
					}
					needed[r] = true
				}
			}
		}
	}
	// Preserve the source order for stable normalization and authorization plans.
	for _, r := range source.Manifest.SourceRefs {
		if needed[r] {
			selected.SourceRefs = append(selected.SourceRefs, r)
		}
	}
	refs, grants, err := s.checkDraftContents(ctx, p, draftCommit{DraftID: m.Source.DraftID, Expected: m.Source.DraftRevisionID, Plan: draftPlan{Manifest: selected}})
	if err != nil {
		return nil, nil, err
	}
	if m.Source.Relation == "produced_in" {
		for _, f := range selected.Files {
			v, err := s.content.Metadata(ctx, p, f.Content, m.Source.access())
			if err != nil {
				return nil, nil, err
			}
			if v.Source.WorldDraftID != m.Source.DraftID {
				return nil, nil, ErrInvalid
			}
		}
	}
	for i, g := range grants["world"] {
		if g.ResourceID == m.Source.DraftID {
			grants["world"][i].Write = false
		}
	}
	return uniqueRefs(append(refs, plan.SourceRef)), grants, nil
}

func validPublicationSource(r WorldRegisterRequest) bool {
	return (ValidID(r.SourceProjectID) && r.SourceDraftID == "") || (ValidID(r.SourceDraftID) && r.SourceProjectID == "")
}
func publicationRevision(source AssetSource) string {
	if source.Kind == "world_draft" {
		return source.DraftRevisionID
	}
	return source.RevisionID
}
