//go:build integration

package world

import (
	"context"
	"errors"
	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"testing"
)

func TestAnalysisQualityTypedBeforePrompts(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: syntheticPromptTargets()})
}
func TestAnalysisQualityEmptyPlanPersistsAndSaves(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: []analysis.PromptTarget{{Key: "unavailable", RequestedLabel: "Unknown", TaskType: "unsupported", InputMode: "unknown", Guidance: "Keep limitations"}}})
}
func verifyQualityPipeline(t *testing.T, svc *Service, p Principal, b analysis.Bundle, options bundleOptions) {
	t.Helper()
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	if err := svc.PrepareAnalysisPrompts(ctx, p, b.TaskID, true); err == nil {
		t.Fatal("prompt admission preceded typed results")
	}
	if err := svc.PrepareAnalysisPrompts(ctx, Principal{OrganizationID: p.OrganizationID, SubjectID: newID(), RequestID: newID()}, b.TaskID, true); err == nil {
		t.Fatal("foreign task accessed")
	}
	yes := true
	channel := func() associationChannelFixture {
		return associationChannelFixture{observationChannel: observationChannel{call: analysis.Call{SchemaVersion: 1, Binding: analysis.Binding{CallID: newID(), TaskID: b.TaskID, ModelID: b.Receipts[0].ModelID}, Media: "analysis", Operation: "analyze", State: "completed", Accepted: &yes}}}
	}
	rc := &resultChannelFixture{associationChannelFixture: channel()}
	pc := &promptChannelFixture{associationChannelFixture: channel()}
	seenResults := false
	seenPrompts := false
	advance := func(ctx context.Context, w analysis.Work) (string, error) {
		switch {
		case w.StepKey == analysis.ResultSetupStepKey:
			if seenPrompts {
				t.Fatal("results after prompts")
			}
			seenResults = true
			return "planned", svc.PrepareAnalysisResults(ctx, p, b.TaskID, true)
		case analysis.IsResultBatchKey(w.StepKey):
			rc.call.CallID = newID()
			r, e := svc.AdvanceAnalysisResult(ctx, p, b.TaskID, w.StepKey, rc, true)
			return r.State, e
		case w.StepKey == analysis.PromptSetupStepKey:
			if !seenResults {
				t.Fatal("prompts before results")
			}
			seenPrompts = true
			return "planned", svc.PrepareAnalysisPrompts(ctx, p, b.TaskID, true)
		case analysis.IsPromptBatchKey(w.StepKey):
			pc.call.CallID = newID()
			r, e := svc.AdvanceAnalysisPrompt(ctx, p, b.TaskID, w.StepKey, pc, true)
			return r.State, e
		case w.StepKey == analysis.LearningSaveStepKey:
			if !seenPrompts {
				t.Fatal("learning saved before applicability")
			}
			_, e := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
			return "saved", e
		}
		t.Fatalf("unexpected work %s", w.StepKey)
		return "", nil
	}
	worker := analysis.Scheduler{Queue: store, Dispatch: true, Advance: advance}
	for i := 0; i < 60; i++ {
		if _, err := svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); err != nil {
			t.Fatal(err)
		}
		worked, err := worker.Tick(ctx)
		if err != nil {
			t.Fatal(i, err)
		}
		progress, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if progress.LearningState == "saved" {
			break
		}
		if !worked {
			t.Fatal("pipeline stalled", progress)
		}
		if i == 59 {
			t.Fatal("pipeline did not finish")
		}
	}
	records, err := store.PromptRecords(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if options.targets[0].TaskType == "unsupported" && (len(records) != 0 || pc.submits != 0) {
		t.Fatal("empty applicability generated calls")
	}
	if err = svc.PrepareAnalysisPrompts(ctx, p, b.TaskID, true); err != nil {
		t.Fatal("lost plan response replay", err)
	}
	raw, err := svc.AnalysisLearningDocument(ctx, p, b.TaskID)
	if err != nil || len(raw) == 0 {
		t.Fatal("fixed learning unavailable", err)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_prompt_plans SET plan_sha256=repeat('f',64)`); err == nil {
		t.Fatal("plan receipt mutable")
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_tasks SET pipeline_version=0`); err == nil {
		t.Fatal("task pipeline mutable")
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(ctx, true); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("finished task claimed again", err)
	}
}
