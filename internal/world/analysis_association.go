package world

import (
	"context"
	"errors"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

// AdvanceAnalysisAssociation belongs to the internal full-analysis worker. It
// never creates a project, publishes an asset, or replaces the source revision.
func (s *Service) AdvanceAnalysisAssociation(ctx context.Context, p Principal, taskID string, channel analysis.AssociationChannel, dispatch bool) (analysis.AssociationRecord, error) {
	b, err := s.AnalysisBundle(ctx, p, taskID)
	if err != nil {
		return analysis.AssociationRecord{}, err
	}
	store := analysis.PGStore{DB: s.db}
	_, err = store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, taskID)
	if errors.Is(err, analysis.ErrMissing) {
		if !dispatch {
			return analysis.AssociationRecord{}, analysis.ErrMissing
		}
		// Use the immutable execution selection already made for this task.
		var model, account, budget string
		for i, receipt := range b.Receipts {
			r, e := store.Load(ctx, p.OrganizationID, p.SubjectID, taskID, receipt.Step.Key)
			if e != nil {
				return analysis.AssociationRecord{}, e
			}
			if i == 0 {
				model, account, budget = r.Request.ModelID, r.Request.AccountID, r.Request.BudgetLimit
			} else if model != r.Request.ModelID || account != r.Request.AccountID || budget != r.Request.BudgetLimit {
				return analysis.AssociationRecord{}, ErrNotReady
			}
		}
		f, e := analysis.FreezeAssociation(b, model, account, budget)
		if e != nil {
			return analysis.AssociationRecord{}, e
		}
		if e = store.CreateAssociation(ctx, p.OrganizationID, p.SubjectID, f); e != nil {
			return analysis.AssociationRecord{}, e
		}
	} else if err != nil {
		return analysis.AssociationRecord{}, err
	}
	worker := analysis.AssociationExecutor{Store: store, Channel: channel}
	return worker.Advance(ctx, p.OrganizationID, p.SubjectID, taskID, b, dispatch)
}

func (s *Service) AnalysisAssociation(ctx context.Context, p Principal, taskID string) (analysis.Association, error) {
	b, err := s.AnalysisBundle(ctx, p, taskID)
	if err != nil {
		return analysis.Association{}, err
	}
	r, err := (analysis.PGStore{DB: s.db}).LoadAssociation(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return analysis.Association{}, err
	}
	if r.State != "association_ready" {
		return analysis.Association{}, ErrNotReady
	}
	return analysis.VerifyAssociationNative(b, r.Request, r.CallID, r.Result)
}
