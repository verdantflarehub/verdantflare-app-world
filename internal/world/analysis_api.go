package world

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

type AnalysisCapabilityReader interface {
	Capabilities(context.Context, string) (analysis.Capabilities, error)
}
type AnalysisRuntime struct {
	Enabled   bool
	Channel   AnalysisCapabilityReader
	Processor analysis.Preparer
}

// Configure before publishing handlers; settings are immutable during serving.
func (s *Service) ConfigureAnalysis(cfg AnalysisRuntime) error {
	if s.analysis != nil || cfg.Enabled && cfg.Channel == nil {
		return ErrInvalid
	}
	s.analysis = &cfg
	return nil
}

type AnalyzeRequest struct {
	Mode          string                  `json:"mode"`
	TaskID        string                  `json:"task_id,omitempty"`
	Source        *AnalysisSource         `json:"source,omitempty"`
	ModelID       string                  `json:"model_id,omitempty"`
	BudgetLimit   string                  `json:"budget_limit,omitempty"`
	BudgetPerCall string                  `json:"budget_per_call,omitempty"`
	PromptTargets []analysis.PromptTarget `json:"prompt_targets,omitempty"`
	Limit         int                     `json:"limit,omitempty"`
	BeforeTaskID  string                  `json:"before_task_id,omitempty"`
}
type AnalysisBudget struct {
	Limit    string `json:"limit"`
	PerCall  string `json:"per_call"`
	Reserved string `json:"reserved"`
}
type AnalysisTaskView struct {
	TaskID        string                  `json:"task_id"`
	Name          string                  `json:"name"`
	State         string                  `json:"request_state"`
	ErrorCode     string                  `json:"error_code,omitempty"`
	Source        AnalysisSource          `json:"source"`
	ModelID       string                  `json:"model_id"`
	PromptTargets []analysis.PromptTarget `json:"prompt_targets"`
	Budget        AnalysisBudget          `json:"budget"`
	CreatedAt     time.Time               `json:"created_at"`
	Progress      *analysis.Progress      `json:"progress,omitempty"`
	Learning      *AnalysisLearningSave   `json:"learning,omitempty"`
}
type AnalysisAvailability struct {
	Enabled       bool     `json:"enabled"`
	Models        []string `json:"models"`
	InputMIME     []string `json:"input_mime_types"`
	MaxMediaBytes int64    `json:"max_media_bytes"`
}

func DecodeAnalyzeRequest(data []byte) (AnalyzeRequest, error) {
	var r AnalyzeRequest
	if validateLearningJSON(data, "world-analyze-request") != nil || json.Unmarshal(data, &r) != nil {
		return r, ErrInvalid
	}
	return normalizeAnalyzeRequest(r)
}
func normalizeAnalyzeRequest(r AnalyzeRequest) (AnalyzeRequest, error) {
	data, err := json.Marshal(r)
	if err != nil || validateLearningJSON(data, "world-analyze-request") != nil {
		return r, ErrInvalid
	}
	if r.Mode == "submit" && (len(r.PromptTargets) == 0 || analysis.ValidatePromptTargets(r.PromptTargets) != nil || analysis.ValidateBudget(r.BudgetLimit, r.BudgetPerCall) != nil) {
		return r, ErrInvalid
	}
	return r, nil
}
func analysisAPIError(err error) error {
	switch {
	case errors.Is(err, analysis.ErrSourceTrashed):
		return ErrConflict
	case errors.Is(err, analysis.ErrMissing):
		return ErrNotFound
	case errors.Is(err, analysis.ErrChanged):
		return ErrIdempotency
	case errors.Is(err, analysis.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, analysis.ErrUnavailable):
		return ErrDependency
	}
	return err
}
func (s *Service) analysisAvailability(ctx context.Context, p Principal) (AnalysisAvailability, error) {
	v := AnalysisAvailability{Models: []string{}, InputMIME: []string{"video/mp4"}, MaxMediaBytes: analysis.MaxMediaBytes}
	if s.analysis == nil || !s.analysis.Enabled || s.analysis.Channel == nil {
		return v, nil
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	remote, err := s.analysis.Channel.Capabilities(discoveryCtx, p.OrganizationID)
	if err != nil {
		return v, analysisAPIError(err)
	}
	if remote.State != "enabled" || !slices.Contains(remote.MIME, "video/mp4") || !slices.Contains(remote.MIME, "audio/wav") || remote.MaxMediaBytes < analysis.MaxMediaBytes {
		return v, nil
	}
	v.Enabled = len(remote.Models) > 0
	v.Models = append([]string{}, remote.Models...)
	return v, nil
}
func (s *Service) WorldAnalyze(ctx context.Context, p Principal, input AnalyzeRequest) (any, error) {
	if !p.Valid() {
		return nil, ErrForbidden
	}
	r, err := normalizeAnalyzeRequest(input)
	if err != nil {
		return nil, err
	}
	store := analysis.PGStore{DB: s.db}
	switch r.Mode {
	case "capabilities":
		return s.analysisAvailability(ctx, p)
	case "get":
		return s.analysisTaskView(ctx, p, r.TaskID, true)
	case "list":
		limit := r.Limit
		if limit == 0 {
			limit = 20
		}
		ids, e := store.SubmissionIDs(ctx, p.OrganizationID, p.SubjectID, r.BeforeTaskID, limit+1)
		if e != nil {
			return nil, analysisAPIError(e)
		}
		next := ""
		if len(ids) > limit {
			ids = ids[:limit]
			next = ids[len(ids)-1]
		}
		items := []AnalysisTaskView{}
		for _, id := range ids {
			v, e := s.analysisTaskView(ctx, p, id, false)
			if e != nil {
				return nil, e
			}
			items = append(items, v)
		}
		return struct {
			Items []AnalysisTaskView `json:"items"`
			Next  string             `json:"next_before_task_id,omitempty"`
		}{items, next}, nil
	case "submit":
		data, _ := json.Marshal(r)
		hash := analysisDocumentHash(data)
		saved, e := store.Submission(ctx, p.OrganizationID, p.SubjectID, r.TaskID)
		if e == nil {
			if saved.Hash != hash {
				return nil, ErrIdempotency
			}
			return s.analysisTaskView(ctx, p, r.TaskID, true)
		}
		if !errors.Is(e, analysis.ErrMissing) {
			return nil, analysisAPIError(e)
		}
		if _, e = store.Task(ctx, p.OrganizationID, p.SubjectID, r.TaskID); e == nil {
			return nil, ErrIdempotency
		} else if !errors.Is(e, analysis.ErrMissing) {
			return nil, analysisAPIError(e)
		}
		available, e := s.analysisAvailability(ctx, p)
		if e != nil {
			return nil, e
		}
		if !available.Enabled {
			return nil, ErrDependency
		}
		if !slices.Contains(available.Models, r.ModelID) {
			return nil, ErrInvalid
		}
		_, version, e := s.analysisSource(ctx, p, *r.Source)
		if e != nil {
			return nil, e
		}
		e = store.CreateSubmission(ctx, analysis.Submission{PipelineVersion: 2, OrganizationID: p.OrganizationID, OwnerID: p.SubjectID, TaskID: r.TaskID, DraftID: r.Source.DraftID, RevisionID: r.Source.RevisionID, FileID: r.Source.FileID, SourceHash: version.SHA256, Hash: hash, Request: data})
		if e != nil {
			return nil, analysisAPIError(e)
		}
		return s.analysisTaskView(ctx, p, r.TaskID, true)
	}
	return nil, ErrInvalid
}
func (s *Service) analysisTaskView(ctx context.Context, p Principal, id string, full bool) (AnalysisTaskView, error) {
	var out AnalysisTaskView
	store := analysis.PGStore{DB: s.db}
	row, err := store.Submission(ctx, p.OrganizationID, p.SubjectID, id)
	if err != nil {
		return out, analysisAPIError(err)
	}
	r, err := DecodeAnalyzeRequest(row.Request)
	if err != nil {
		return out, ErrNotReady
	}
	if !analysisSubmissionMatches(row, r) {
		return out, ErrNotReady
	}
	if _, _, err = draftOwner(ctx, s.db, p, row.DraftID); err != nil {
		return out, err
	}
	if full {
		_, v, e := s.analysisSource(ctx, p, *r.Source)
		if e != nil {
			return out, e
		}
		if v.SHA256 != row.SourceHash {
			return out, ErrNotReady
		}
	}
	reserved, err := store.ReservedBudget(ctx, p.OrganizationID, p.SubjectID, id)
	if err != nil {
		return out, err
	}
	out = AnalysisTaskView{TaskID: id, Name: row.Name, State: row.State, ErrorCode: row.ErrorCode, Source: *r.Source, ModelID: r.ModelID, PromptTargets: r.PromptTargets, Budget: AnalysisBudget{r.BudgetLimit, r.BudgetPerCall, reserved}, CreatedAt: row.CreatedAt}
	if _, e := store.Task(ctx, p.OrganizationID, p.SubjectID, id); e == nil {
		progress, e := store.Progress(ctx, p.OrganizationID, p.SubjectID, id)
		if e != nil {
			return out, analysisAPIError(e)
		}
		out.Progress = &progress
		if full {
			saved, e := s.loadAnalysisLearningSave(ctx, p, id)
			if e != nil && !errors.Is(e, ErrNotFound) {
				return out, e
			}
			if e == nil {
				if saved.State == "saved" {
					data, e := s.AnalysisLearningDocument(ctx, p, id)
					if e != nil {
						return out, analysisAPIError(e)
					}
					if _, e = s.readSavedAnalysisLearning(ctx, p, saved, data); e != nil {
						return out, e
					}
				}
				v := saved.AnalysisLearningSave
				out.Learning = &v
			}
		}
	} else if !errors.Is(e, analysis.ErrMissing) {
		return out, analysisAPIError(e)
	}
	return out, nil
}
func (s *Service) PrepareAnalysisSubmission(ctx context.Context, w analysis.Work) (string, error) {
	store := analysis.PGStore{DB: s.db}
	if err := store.CheckSubmissionLease(ctx, w); err != nil {
		return "", err
	}
	p := Principal{OrganizationID: w.OrganizationID, SubjectID: w.OwnerID, RequestID: w.LeaseID}
	row, err := store.Submission(ctx, p.OrganizationID, p.SubjectID, w.TaskID)
	if err != nil {
		return "", err
	}
	r, err := DecodeAnalyzeRequest(row.Request)
	if err == nil && !analysisSubmissionMatches(row, r) {
		err = ErrNotReady
	}
	if err == nil {
		var v ContentVersion
		_, v, err = s.analysisSource(ctx, p, *r.Source)
		if err == nil && v.SHA256 != row.SourceHash {
			err = ErrNotReady
		}
	}
	if err == nil {
		processor := analysis.Preparer{FFprobe: "/usr/bin/ffprobe", FFmpeg: "/usr/bin/ffmpeg"}
		if s.analysis != nil {
			processor = s.analysis.Processor
		}
		_, err = s.PrepareVideoAnalysis(ctx, p, w.TaskID, *r.Source, AnalysisExecution{PipelineVersion: row.PipelineVersion, ModelID: r.ModelID, BudgetPerStep: r.BudgetPerCall, PromptTargets: r.PromptTargets, IncludeTypedResults: true, SaveLearning: true, TotalBudget: r.BudgetLimit}, processor)
	}
	if err != nil {
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotReady) || errors.Is(err, analysis.ErrInvalid) || errors.Is(err, analysis.ErrMediaInvalid) || errors.Is(err, analysis.ErrMediaUnsupported) {
			if e := store.FinishSubmission(ctx, w, "SOURCE_UNAVAILABLE"); e != nil {
				return "", e
			}
			return "failed", nil
		}
		return "", err
	}
	if err = store.FinishSubmission(ctx, w, ""); err != nil {
		return "", err
	}
	return "queued", nil
}

func analysisSubmissionMatches(row analysis.Submission, r AnalyzeRequest) bool {
	data, err := json.Marshal(r)
	return err == nil && r.Mode == "submit" && r.TaskID == row.TaskID && r.Source != nil &&
		r.Source.DraftID == row.DraftID && r.Source.RevisionID == row.RevisionID && r.Source.FileID == row.FileID &&
		analysisDocumentHash(data) == row.Hash
}
