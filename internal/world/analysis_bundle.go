package world

import (
	"context"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

// AnalysisBundle is an internal synthesis input, not a published asset or an
// externally supplied receipt. Recheck the pinned source's current permission.
func (s *Service) AnalysisBundle(ctx context.Context, p Principal, id string) (analysis.Bundle, error) {
	if !p.Valid() || !ValidID(id) {
		return analysis.Bundle{}, ErrForbidden
	}
	store := analysis.PGStore{DB: s.db}
	task, err := store.Task(ctx, p.OrganizationID, p.SubjectID, id)
	if err != nil {
		return analysis.Bundle{}, err
	}
	_, version, err := s.analysisSource(ctx, p, AnalysisSource{DraftID: task.DraftID, RevisionID: task.RevisionID, FileID: task.FileID})
	if err != nil {
		return analysis.Bundle{}, err
	}
	if version.SHA256 != task.Plan.Source.SHA256 {
		return analysis.Bundle{}, ErrNotReady
	}
	return store.Bundle(ctx, p.OrganizationID, p.SubjectID, id)
}
