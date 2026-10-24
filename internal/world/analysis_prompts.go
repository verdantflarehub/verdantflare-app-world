package world

import (
	"context"
	"errors"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

func (s *Service) promptAnalysisContext(ctx context.Context, p Principal, taskID string) (analysis.Bundle, analysis.Association, analysis.AssociationRecord, error) {
	b, err := s.AnalysisBundle(ctx, p, taskID)
	if err != nil {
		return b, analysis.Association{}, analysis.AssociationRecord{}, err
	}
	r, err := (analysis.PGStore{DB: s.db}).LoadAssociation(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return b, analysis.Association{}, r, err
	}
	if r.State != "association_ready" {
		return b, analysis.Association{}, r, ErrNotReady
	}
	a, err := analysis.VerifyAssociationNative(b, r.Request, r.CallID, r.Result)
	return b, a, r, err
}

// PrepareAnalysisPrompts publishes an entire internal plan atomically. It
// performs no inference and never changes the targets of an existing task.
func (s *Service) PrepareAnalysisPrompts(ctx context.Context, p Principal, taskID string, dispatch bool) error {
	b, a, association, err := s.promptAnalysisContext(ctx, p, taskID)
	if err != nil {
		return err
	}
	store := analysis.PGStore{DB: s.db}
	records, err := store.PromptRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err == nil {
		for _, r := range records {
			if r.Request.Batch.BundleHash != b.SHA256 || r.Request.Batch.TaskID != taskID {
				return ErrNotReady
			}
		}
		return nil
	}
	if !errors.Is(err, analysis.ErrMissing) {
		return err
	}
	if !dispatch {
		return ErrNotReady
	}
	task, err := store.Task(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return err
	}
	if len(task.PromptTargets) == 0 {
		return ErrNotReady
	}
	plan, _, err := store.PlannedPrompts(ctx, task, b, a)
	if err != nil {
		return err
	}
	requests := make([]analysis.PromptRequest, 0, len(plan))
	for _, batch := range plan {
		f, err := analysis.FreezePrompts(b, a, batch, association.Request.ModelID, association.Request.AccountID, association.Request.BudgetLimit)
		if err != nil {
			return err
		}
		requests = append(requests, f)
	}
	return store.SavePromptPlan(ctx, p.OrganizationID, p.SubjectID, taskID, requests)
}

func (s *Service) AdvanceAnalysisPrompt(ctx context.Context, p Principal, taskID, key string, channel analysis.PromptChannel, dispatch bool) (analysis.PromptRecord, error) {
	b, a, _, err := s.promptAnalysisContext(ctx, p, taskID)
	if err != nil {
		return analysis.PromptRecord{}, err
	}
	store := analysis.PGStore{DB: s.db}
	records, err := store.PromptRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return analysis.PromptRecord{}, err
	}
	for _, r := range records {
		if r.State == "failed" {
			dispatch = false
		}
	}
	worker := analysis.PromptExecutor{Store: store, Channel: channel}
	return worker.Advance(ctx, p.OrganizationID, p.SubjectID, taskID, key, b, a, dispatch)
}

// AnalysisPromptDrafts returns all requested candidates or not-ready. It never
// reports a completed asset from the subset of batches which happened to finish.
func (s *Service) AnalysisPromptDrafts(ctx context.Context, p Principal, taskID string) ([]analysis.PromptDraft, error) {
	b, a, _, err := s.promptAnalysisContext(ctx, p, taskID)
	if err != nil {
		return nil, err
	}
	store := analysis.PGStore{DB: s.db}
	records, err := store.PromptRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return nil, err
	}
	all := []analysis.PromptDraft{}
	for _, r := range records {
		if r.State != "prompt_ready" {
			return nil, ErrNotReady
		}
		drafts, err := analysis.PromptDraftsFromNative(b, a, r.Request, r.CallID, r.Result)
		if err != nil {
			return nil, err
		}
		all = append(all, drafts...)
	}
	return all, nil
}
