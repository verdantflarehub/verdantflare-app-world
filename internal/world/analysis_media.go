package world

import (
	"context"
	"errors"
	"io"
	"slices"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

type AnalysisSource struct {
	DraftID    string `json:"draft_id"`
	RevisionID string `json:"revision_id"`
	FileID     string `json:"file_id"`
}
type AnalysisExecution struct {
	ModelID, AccountID, BudgetPerStep string
	PromptTargets                     []analysis.PromptTarget
	IncludeTypedResults               bool
	SaveLearning                      bool
}
type analysisDownloader interface {
	Download(context.Context, Principal, ContentRef, Access, io.Writer, int64) error
}

// PrepareVideoAnalysis is an internal worker entry, not an HTTP/MCP accepting
// endpoint. Complete learning orchestration must be available before admission.
func (s *Service) PrepareVideoAnalysis(ctx context.Context, p Principal, taskID string, source AnalysisSource, execution AnalysisExecution, processor analysis.Preparer) (analysis.Task, error) {
	if !p.Valid() || !ValidID(taskID) || !ValidID(source.DraftID) || !ValidID(source.RevisionID) || !ValidID(source.FileID) || analysis.ValidatePromptTargets(execution.PromptTargets) != nil || (execution.SaveLearning && !execution.IncludeTypedResults) {
		return analysis.Task{}, ErrInvalid
	}
	store := analysis.PGStore{DB: s.db}
	old, err := store.Task(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err == nil {
		if old.DraftID != source.DraftID || old.RevisionID != source.RevisionID || old.FileID != source.FileID || !slices.Equal(old.PromptTargets, execution.PromptTargets) || old.IncludeTypedResults != execution.IncludeTypedResults || old.SaveLearning != execution.SaveLearning {
			return analysis.Task{}, ErrIdempotency
		}
		// Even a replay must still have access to the pinned collection.
		if _, _, err = s.analysisSource(ctx, p, source); err != nil {
			return analysis.Task{}, err
		}
		for _, step := range old.Plan.Steps {
			r, e := store.Load(ctx, p.OrganizationID, p.SubjectID, taskID, step.Key)
			if e != nil {
				return analysis.Task{}, e
			}
			if r.Request.ModelID != execution.ModelID || r.Request.AccountID != execution.AccountID || r.Request.BudgetLimit != execution.BudgetPerStep {
				return analysis.Task{}, ErrIdempotency
			}
		}
		return old, nil
	}
	if !errors.Is(err, analysis.ErrMissing) {
		return analysis.Task{}, err
	}
	material, err := s.stageAnalysisSource(ctx, p, source, processor)
	if err != nil {
		return analysis.Task{}, err
	}
	defer material.Close()
	plan, err := analysis.PlanVideo(material.Info().Source)
	if err != nil {
		return analysis.Task{}, err
	}
	task := analysis.Task{OrganizationID: p.OrganizationID, OwnerID: p.SubjectID, TaskID: taskID, DraftID: source.DraftID, RevisionID: source.RevisionID, FileID: source.FileID, Plan: plan, PromptTargets: append([]analysis.PromptTarget(nil), execution.PromptTargets...), IncludeTypedResults: execution.IncludeTypedResults, SaveLearning: execution.SaveLearning}
	requests := make([]analysis.FrozenRequest, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		prepared, media, e := material.Prepare(ctx, step)
		if e != nil {
			return analysis.Task{}, e
		}
		request, e := analysis.Freeze(taskID, execution.ModelID, execution.AccountID, execution.BudgetPerStep, step, prepared, int64(len(media)))
		if e != nil {
			return analysis.Task{}, e
		}
		requests = append(requests, request)
	}
	if err = store.Create(ctx, task, requests); err != nil {
		return analysis.Task{}, err
	}
	return task, nil
}

// AdvanceAnalysisStep reuses frozen requests. Recovery of dispatched calls does
// not require re-downloading or decoding media, or the current channel admission.
func (s *Service) AdvanceAnalysisStep(ctx context.Context, p Principal, taskID, stepKey string, processor analysis.Preparer, channel analysis.Channel) (analysis.StepRecord, error) {
	if !p.Valid() {
		return analysis.StepRecord{}, ErrForbidden
	}
	store := analysis.PGStore{DB: s.db}
	task, err := store.Task(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return analysis.StepRecord{}, err
	}
	if _, _, err = draftOwner(ctx, s.db, p, task.DraftID); err != nil {
		return analysis.StepRecord{}, err
	}
	source := AnalysisSource{DraftID: task.DraftID, RevisionID: task.RevisionID, FileID: task.FileID}
	var material *analysis.Material
	defer func() {
		if material != nil {
			material.Close()
		}
	}()
	worker := analysis.Executor{Store: store, Channel: channel, Media: func(ctx context.Context, r analysis.StepRecord) ([]byte, error) {
		var e error
		material, e = s.stageAnalysisSource(ctx, p, source, processor)
		if e != nil {
			return nil, e
		}
		info := material.Info().Source
		if info != task.Plan.Source {
			return nil, ErrNotReady
		}
		prepared, data, e := material.Prepare(ctx, r.Request.Step)
		if e != nil {
			return nil, e
		}
		if prepared != r.Request.Prepared {
			return nil, ErrNotReady
		}
		return data, nil
	}}
	return worker.Advance(ctx, p.OrganizationID, p.SubjectID, taskID, stepKey)
}
func (s *Service) analysisSource(ctx context.Context, p Principal, source AnalysisSource) (File, ContentVersion, error) {
	raw, err := s.WorldDraft(ctx, p, DraftRequest{Mode: "get", DraftID: source.DraftID, RevisionID: source.RevisionID})
	if err != nil {
		return File{}, ContentVersion{}, err
	}
	revision, ok := raw.(DraftRevision)
	if !ok || revision.Manifest.Purpose != "collection" || revision.RevisionID != source.RevisionID {
		return File{}, ContentVersion{}, ErrInvalid
	}
	for _, file := range revision.Manifest.Files {
		if file.ID == source.FileID {
			v, e := s.content.Metadata(ctx, p, file.Content, Access{WorldDraftID: source.DraftID, WorldDraftRevisionID: source.RevisionID})
			if e != nil {
				return File{}, v, e
			}
			if v.ContentRef != file.Content || v.OrganizationID != p.OrganizationID || v.MIME != "video/mp4" || v.Size < 1 || v.Size > analysis.MaxMediaBytes {
				return File{}, v, ErrInvalid
			}
			return file, v, nil
		}
	}
	return File{}, ContentVersion{}, ErrNotFound
}
func (s *Service) stageAnalysisSource(ctx context.Context, p Principal, source AnalysisSource, processor analysis.Preparer) (*analysis.Material, error) {
	file, version, err := s.analysisSource(ctx, p, source)
	if err != nil {
		return nil, err
	}
	downloader, ok := s.content.(analysisDownloader)
	if !ok {
		return nil, ErrDependency
	}
	return processor.Stage(ctx, version.SHA256, version.Size, func(dst io.Writer) error {
		return downloader.Download(ctx, p, file.Content, Access{WorldDraftID: source.DraftID, WorldDraftRevisionID: source.RevisionID}, dst, analysis.MaxMediaBytes)
	})
}
