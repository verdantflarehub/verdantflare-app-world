package world

import (
	"context"
	"errors"
	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

// PrepareAnalysisResults publishes an entire internal plan atomically. It
// performs no inference and never changes the targets of an existing task.
func (s *Service) PrepareAnalysisResults(ctx context.Context, p Principal, taskID string, dispatch bool) error {
	b, a, association, err := s.promptAnalysisContext(ctx, p, taskID)
	if err != nil {
		return err
	}
	store := analysis.PGStore{DB: s.db}
	records, err := store.ResultRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
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
	if !task.IncludeTypedResults {
		return ErrNotReady
	}
	if err = store.ResultPrerequisites(ctx, p.OrganizationID, p.SubjectID, taskID, b, a); err != nil {
		return err
	}
	plan, err := analysis.PlanResultsVersion(b, a, task.PipelineVersion)
	if err != nil {
		return err
	}
	requests := make([]analysis.ResultRequest, 0, len(plan))
	for _, batch := range plan {
		f, err := analysis.FreezeResults(b, a, batch, association.Request.ModelID, association.Request.AccountID, association.Request.BudgetLimit)
		if err != nil {
			return err
		}
		requests = append(requests, f)
	}
	return store.CreateResultBatches(ctx, p.OrganizationID, p.SubjectID, requests)
}

func (s *Service) AdvanceAnalysisResult(ctx context.Context, p Principal, taskID, key string, channel analysis.ResultChannel, dispatch bool) (analysis.ResultRecord, error) {
	b, a, _, err := s.promptAnalysisContext(ctx, p, taskID)
	if err != nil {
		return analysis.ResultRecord{}, err
	}
	store := analysis.PGStore{DB: s.db}
	records, err := store.ResultRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return analysis.ResultRecord{}, err
	}
	for _, r := range records {
		if r.State == "failed" {
			dispatch = false
		}
	}
	worker := analysis.ResultExecutor{Store: store, Channel: channel}
	return worker.Advance(ctx, p.OrganizationID, p.SubjectID, taskID, key, b, a, dispatch)
}

// AnalysisResultDrafts returns all requested candidates or not-ready. It never
// reports a completed asset from the subset of batches which happened to finish.
func (s *Service) AnalysisResultDrafts(ctx context.Context, p Principal, taskID string) ([]analysis.ResultDraft, error) {
	b, a, _, err := s.promptAnalysisContext(ctx, p, taskID)
	if err != nil {
		return nil, err
	}
	store := analysis.PGStore{DB: s.db}
	records, err := store.ResultRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return nil, err
	}
	all := []analysis.ResultDraft{}
	for _, r := range records {
		if r.State != "result_ready" {
			return nil, ErrNotReady
		}
		drafts, err := analysis.ResultDraftsFromNative(b, a, r.Request, r.CallID, r.Result)
		if err != nil {
			return nil, err
		}
		all = append(all, drafts...)
	}
	return all, nil
}
