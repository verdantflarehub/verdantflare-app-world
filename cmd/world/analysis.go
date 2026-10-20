package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
)

type analysisConfig struct {
	channel  *analysis.GoogleChannel
	dispatch bool
}

func readAnalysisConfig(get func(string) string, existingTokens map[string]bool) (analysisConfig, error) {
	origin, token, ca := get("WORLD_ANALYSIS_URL"), get("GOOGLE_ANALYSIS_TOKEN"), get("WORLD_ANALYSIS_CA_FILE")
	flag := get("WORLD_ANALYSIS_DISPATCH_ENABLED")
	if flag != "" && flag != "false" && flag != "true" {
		return analysisConfig{}, errors.New("invalid analysis dispatch configuration")
	}
	if origin == "" && token == "" && ca == "" && flag != "true" {
		return analysisConfig{}, nil
	}
	if existingTokens[token] {
		return analysisConfig{}, errors.New("independent analysis credential required")
	}
	channel, err := analysis.NewGoogleChannel(origin, token, ca)
	if err != nil {
		return analysisConfig{}, err
	}
	return analysisConfig{channel: channel, dispatch: flag == "true"}, nil
}

func startAnalysisWorker(ctx context.Context, db *pgxpool.Pool, svc *world.Service, cfg analysisConfig) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	worker := analysis.Scheduler{Queue: analysis.PGStore{DB: db}, Dispatch: cfg.dispatch,
		Advance: func(ctx context.Context, w analysis.Work) (string, error) {
			p := world.Principal{OrganizationID: w.OrganizationID, SubjectID: w.OwnerID, RequestID: w.LeaseID}
			if w.StepKey == analysis.SubmissionStepKey {
				return svc.PrepareAnalysisSubmission(ctx, w)
			}
			if w.StepKey == analysis.LearningSaveStepKey {
				r, err := svc.SaveAnalysisLearning(ctx, p, w.TaskID)
				state := r.State
				if state == "conflict" {
					state = "failed"
				}
				return state, err
			}
			if cfg.channel == nil {
				return "", world.ErrDependency
			}
			if w.StepKey == analysis.AssociationStepKey {
				r, err := svc.AdvanceAnalysisAssociation(ctx, p, w.TaskID, cfg.channel, cfg.dispatch)
				return r.State, err
			}
			if w.StepKey == analysis.PromptSetupStepKey {
				return "prompts_planned", svc.PrepareAnalysisPrompts(ctx, p, w.TaskID, cfg.dispatch)
			}
			if analysis.IsPromptBatchKey(w.StepKey) {
				r, err := svc.AdvanceAnalysisPrompt(ctx, p, w.TaskID, w.StepKey, cfg.channel, cfg.dispatch)
				return r.State, err
			}
			if w.StepKey == analysis.ResultSetupStepKey {
				return "results_planned", svc.PrepareAnalysisResults(ctx, p, w.TaskID, cfg.dispatch)
			}
			if analysis.IsResultBatchKey(w.StepKey) {
				r, err := svc.AdvanceAnalysisResult(ctx, p, w.TaskID, w.StepKey, cfg.channel, cfg.dispatch)
				return r.State, err
			}
			r, err := svc.AdvanceAnalysisStep(ctx, p, w.TaskID, w.StepKey, analysis.Preparer{FFprobe: "/usr/bin/ffprobe", FFmpeg: "/usr/bin/ffmpeg"}, cfg.channel)
			return r.State, err
		},
	}
	go func() {
		defer close(done)
		worker.Run(ctx, func(error) {
			fmt.Fprintln(os.Stderr, "World analysis scheduler deferred; inspect owner-scoped task state")
		})
	}()
	return func() { cancel(); <-done }
}
