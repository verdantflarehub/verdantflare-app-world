//go:build integration

package world

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"strings"
	"testing"
)

func TestAnalysisQualityTypedBeforePrompts(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: syntheticPromptTargets()})
}
func TestAnalysisQualityBatchRetriesPreserveFailuresAndSave(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: syntheticPromptTargets(), retryBatches: true})
}
func TestAnalysisQualitySchemaRejectionsRecoverAndSave(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: syntheticPromptTargets(), retryBatches: true, schemaRejections: true})
}
func TestAnalysisQualityStyleDiagnosticCorrectionAndSave(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: syntheticPromptTargets(), styleCorrection: true})
}
func TestAnalysisQualityStyleRevalidationPreservesNativeAndSave(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: syntheticPromptTargets(), styleRevalidate: true})
}
func TestAnalysisQualityMethodDiagnosticsAndSave(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{pipeline: 2, typed: true, saveLearning: true, targets: syntheticPromptTargets(), methodDiagnostic: true})
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
	retryCounts := map[string]int{}
	originals := map[string]string{}
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
			rc.corrupt = options.retryBatches && rc.submits < 2
			rc.schemaReject = options.schemaRejections && rc.corrupt
			rc.methodDiagnostic = options.methodDiagnostic
			rc.styleCorrection = options.styleCorrection || options.styleRevalidate
			rc.call.State = "completed"
			rc.call.CallID = newID()
			if options.styleRevalidate && w.StepKey == "results-v1-style-0000" {
				rc.styleCorrection = false
				rc.independentStyle = true
				ar, err := store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
				if err != nil {
					return "", err
				}
				a, err := analysis.VerifyAssociationNative(b, ar.Request, ar.CallID, ar.Result)
				if err != nil {
					return "", err
				}
				executor := analysis.ResultExecutor{Store: legacyStyleFailureStore{store}, Channel: rc}
				r, e := executor.Advance(ctx, p.OrganizationID, p.SubjectID, b.TaskID, w.StepKey, b, a, true)
				return r.State, e
			}
			r, e := svc.AdvanceAnalysisResult(ctx, p, b.TaskID, w.StepKey, rc, true)
			return r.State, e
		case w.StepKey == analysis.PromptSetupStepKey:
			if !seenResults {
				t.Fatal("prompts before results")
			}
			seenPrompts = true
			return "planned", svc.PrepareAnalysisPrompts(ctx, p, b.TaskID, true)
		case analysis.IsPromptBatchKey(w.StepKey):
			pc.corrupt = options.retryBatches && pc.submits == 0
			pc.schemaReject = options.schemaRejections && pc.corrupt
			pc.call.State = "completed"
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
		if (options.retryBatches || options.styleCorrection || options.styleRevalidate || options.methodDiagnostic) && (progress.ResultFailed > 0 || progress.PromptFailed > 0) {
			kind := "result"
			if progress.PromptFailed > 0 {
				kind = "prompt"
			}
			var before string
			if err = svc.db.QueryRow(ctx, `SELECT to_jsonb(r)::text FROM studio.world_analysis_`+kind+`_batches r WHERE task_id=$1 AND state='failed'`, b.TaskID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if originals[kind] == "" {
				originals[kind] = before
			}
			cost, e := store.ReservedBudget(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
			if e != nil {
				t.Fatal(e)
			}
			rid := newID()
			if e = store.Retry(ctx, p.OrganizationID, newID(), b.TaskID, rid); e == nil {
				t.Fatal("foreign batch retry")
			}
			var reader analysis.AssociationResultReader = rc
			if kind == "prompt" {
				reader = pc
			}
			for range 2 {
				if e = store.Retry(ctx, p.OrganizationID, p.SubjectID, b.TaskID, rid, reader); e != nil {
					t.Fatal("batch retry/replay", kind, e)
				}
			}
			retryCounts[kind]++
			after, e := store.ReservedBudget(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
			if e != nil || cost != after {
				t.Fatal("charged before dispatch", e)
			}
			if e = store.Retry(ctx, p.OrganizationID, p.SubjectID, newID(), rid); e == nil {
				t.Fatal("reused retry id across tasks")
			}
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
	if options.retryBatches || options.styleCorrection || options.styleRevalidate || options.methodDiagnostic {
		wantResult, wantPrompt := 2, 1
		if options.styleCorrection || options.methodDiagnostic {
			wantResult, wantPrompt = 3, 0
		}
		if options.styleRevalidate {
			wantResult, wantPrompt = 1, 0
		}
		if retryCounts["result"] != wantResult || retryCounts["prompt"] != wantPrompt {
			t.Fatal("retry stages not covered", retryCounts)
		}
		for kind, before := range originals {
			var after string
			if e := svc.db.QueryRow(ctx, `SELECT to_jsonb(r)::text FROM studio.world_analysis_`+kind+`_batches r WHERE task_id=$1 AND state='failed'`, b.TaskID).Scan(&after); e != nil || before != after {
				t.Fatal("original failure changed", kind, e)
			}
			var rows []json.RawMessage
			if options.styleRevalidate {
				verifyResultValidation(t, svc, p, b.TaskID)
				continue
			}
			if e := svc.db.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(r)) FROM studio.world_analysis_`+kind+`_retries r WHERE task_id=$1`, b.TaskID).Scan(&rows); e != nil || len(rows) != retryCounts[kind] {
				t.Fatal("retry history lost", kind, e)
			}
			if _, e := svc.db.Exec(ctx, `DELETE FROM studio.world_analysis_`+kind+`_retries WHERE task_id=$1`, b.TaskID); e == nil {
				t.Fatal("retry deleted")
			}
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

// Simulates the previously deployed validator; writes an ordinary immutable
// failure with the valid original native bytes, before any terminal state.
type legacyStyleFailureStore struct{ analysis.PGStore }

func (s legacyStyleFailureStore) SwapResult(ctx context.Context, old, next analysis.ResultRecord) (bool, error) {
	if next.State == "result_ready" {
		next.State = "failed"
		next.ErrorCode = "result_invalid"
	}
	return s.PGStore.SwapResult(ctx, old, next)
}
func verifyResultValidation(t *testing.T, svc *Service, p Principal, task string) {
	t.Helper()
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	r, err := store.LoadResult(ctx, p.OrganizationID, p.SubjectID, task, "results-v1-style-0000")
	if err != nil || r.State != "result_ready" || r.Validation == nil || r.Request.Retry != nil {
		t.Fatal("missing model-free validation", err)
	}
	var count int
	if err = svc.db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_result_retries WHERE task_id=$1`, task).Scan(&count); err != nil || count != 0 {
		t.Fatal("unexpected new generation", err)
	}
	var raw string
	if err = svc.db.QueryRow(ctx, `SELECT native_result::text FROM studio.world_analysis_result_batches WHERE task_id=$1 AND batch_key=$2 AND state='failed'`, task, r.Request.Batch.Key).Scan(&raw); err != nil || raw != string(r.Result) {
		t.Fatal("native changed", err)
	}
	for _, q := range []string{`UPDATE studio.world_analysis_result_validations SET policy='style-evidence-v1' WHERE task_id=$1`, `DELETE FROM studio.world_analysis_result_validations WHERE task_id=$1`} {
		if _, err = svc.db.Exec(ctx, q, task); err == nil {
			t.Fatal("mutable validation")
		}
	}
	rawDoc, err := svc.AnalysisLearningDocument(ctx, p, task)
	if err != nil || !strings.Contains(string(rawDoc), "result_validation") {
		t.Fatal("lost archived receipt", err)
	}
}
