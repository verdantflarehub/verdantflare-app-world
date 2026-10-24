//go:build integration

package world

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

// Synthetic native fixture verifies actual PostgreSQL constraints and recovery;
// this is not evidence of model/media quality.
func retryNative(t *testing.T, r analysis.StepRecord, invalid bool, entity ...bool) analysis.StepRecord {
	t.Helper()
	step := r.Request.Step
	key := "shot"
	if invalid {
		key = "missing"
	}
	findings := []any{}
	for _, d := range []string{"narrative", "character", "scene", "prop", "cinematography", "editing", "graphics", "style"} {
		findings = append(findings, map[string]any{"dimension": d, "description": "Synthetic observation", "shot_keys": []string{key}, "basis": "observed"})
	}
	doc := map[string]any{"start_ms": step.Core.StartMS, "end_ms": step.Core.EndMS, "entities": []any{}, "findings": findings, "uncertainties": []string{}, "shots": []any{map[string]any{"key": "shot", "start_ms": step.Core.StartMS, "end_ms": step.Core.EndMS, "continues_before": false, "continues_after": false, "composition": "Synthetic", "camera_motion": "Static", "subject_motion": "Still", "graphic_motion": "None", "lighting": "Synthetic", "entity_keys": []string{}, "beats": []any{map[string]any{"start_ms": step.Core.StartMS, "end_ms": step.Core.EndMS, "description": "Synthetic"}}, "visible_text": []any{}}}}
	if len(entity) > 0 && entity[0] {
		doc["entities"] = []any{map[string]any{"key": "bag", "kind": "prop", "name": "Synthetic bag", "description": "Synthetic fixture", "basis": "observed"}}
		doc["shots"].([]any)[0].(map[string]any)["entity_keys"] = []string{"bag"}
	}
	text, _ := json.Marshal(doc)
	sh, _ := step.SchemaHash()
	r.CallID = newID()
	r.Result, _ = json.Marshal(map[string]any{"schema_version": 1, "call_id": r.CallID, "task_id": r.Request.TaskID, "model_id": r.Request.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{"text": string(text), "document": doc, "model_id": r.Request.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"media": map[string]any{"mime_type": r.Request.Prepared.MIME, "sha256": step.SourceHash}, "provider_file_integrity_verified": true, "response_schema_sha256": sh, "video_range": step.Input}}})
	report := analysis.VerifyNative(step, r.Request.Prepared, analysis.Binding{CallID: r.CallID, TaskID: r.Request.TaskID, ModelID: r.Request.ModelID}, r.Result)
	if report.Passed == invalid {
		t.Fatal(report)
	}
	r.State = "observation_ready"
	r.ErrorCode = ""
	if invalid {
		r.State = "failed"
		r.ErrorCode = "observation_invalid"
	}
	r.Report = &report
	r.Revision++
	return r
}
func TestAnalysisRetryPreservesHistoryBudgetOwnershipAndContinues(t *testing.T) {
	for _, budget := range []string{"1", "0.2"} {
		t.Run(budget, func(t *testing.T) {
			ctx := t.Context()
			svc, p, req, _ := admissionFixture(t)
			req.BudgetLimit = budget
			req.BudgetPerCall = "0.1"
			if _, e := svc.WorldAnalyze(ctx, p, req); e != nil {
				t.Fatal(e)
			}
			store := analysis.PGStore{DB: svc.db}
			w, e := store.Claim(ctx, true)
			if e != nil {
				t.Fatal(e)
			}
			task, requests := admissionFrozenTask(t, svc, p, req, 90000)
			if e = store.Create(ctx, task, requests); e != nil {
				t.Fatal(e)
			}
			if e = store.FinishSubmission(ctx, w, ""); e != nil {
				t.Fatal(e)
			}
			finish := func(key string, invalid bool, entity ...bool) analysis.StepRecord {
				r, e := store.Load(ctx, p.OrganizationID, p.SubjectID, req.TaskID, key)
				if e != nil {
					t.Fatal(e)
				}
				sent := r
				sent.Revision++
				sent.State = "dispatching"
				ok, e := store.CompareAndSwap(ctx, r, sent)
				if e != nil || !ok {
					t.Fatal(e)
				}
				done := retryNative(t, sent, invalid)
				ok, e = store.CompareAndSwap(ctx, sent, done)
				if e != nil || !ok {
					t.Fatal(e)
				}
				return done
			}
			first := finish(requests[0].Step.Key, false)
			bad := finish(requests[1].Step.Key, true)
			var snapshot string
			if e = svc.db.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(s) ORDER BY step_key)::text FROM studio.world_analysis_steps s WHERE task_id=$1`, req.TaskID).Scan(&snapshot); e != nil {
				t.Fatal(e)
			}
			retry := AnalyzeRequest{Mode: "retry", TaskID: req.TaskID, RetryID: newID()}
			if _, e = svc.WorldAnalyze(ctx, Principal{OrganizationID: p.OrganizationID, SubjectID: newID(), RequestID: newID()}, retry); e == nil {
				t.Fatal("foreign retry authorized")
			}
			for range 2 {
				if _, e = svc.WorldAnalyze(ctx, p, retry); e != nil {
					t.Fatal("retry/replay", e)
				}
			}
			var count int
			svc.db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_step_retries WHERE task_id=$1`, req.TaskID).Scan(&count)
			if count != 1 {
				t.Fatal("duplicate attempt", count)
			}
			current, e := store.Load(ctx, p.OrganizationID, p.SubjectID, req.TaskID, bad.Request.Step.Key)
			if e != nil || current.State != "planned" || current.Request.Retry.PreviousRequestHash != bad.RequestHash {
				t.Fatal("wrong retry", e)
			}
			got, e := store.Load(ctx, p.OrganizationID, p.SubjectID, req.TaskID, first.Request.Step.Key)
			if e != nil || got.RequestHash != first.RequestHash || got.CallID != first.CallID {
				t.Fatal("replayed ready step")
			}
			claimed, e := store.Claim(ctx, true)
			if e != nil || claimed.StepKey != bad.Request.Step.Key {
				t.Fatal("scheduler did not select retry", claimed, e)
			}
			if e = store.Release(ctx, claimed, ""); e != nil {
				t.Fatal(e)
			}
			if budget == "0.2" {
				sent := current
				sent.State = "dispatching"
				sent.Revision++
				if _, e = store.CompareAndSwap(ctx, current, sent); !errors.Is(e, analysis.ErrBudgetExhausted) {
					t.Fatal("retry bypassed total budget", e)
				}
			} else {
				done := finish(bad.Request.Step.Key, false)
				if done.CallID == bad.CallID {
					t.Fatal("old native call reused")
				}
				b, e := store.Bundle(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
				if e != nil || len(b.Receipts) != 2 {
					t.Fatal("bundle failed", e)
				}
				f, e := analysis.FreezeAssociation(b, req.ModelID, "", req.BudgetPerCall)
				if e != nil {
					t.Fatal(e)
				}
				if e = store.CreateAssociation(ctx, p.OrganizationID, p.SubjectID, f); e != nil {
					t.Fatal("association guard ignored successful retry", e)
				}
				reserved, e := store.ReservedBudget(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
				if e != nil || reserved != "0.3" {
					t.Fatal("lost failed-call cost", reserved, e)
				}
			}
			var after string
			svc.db.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(s) ORDER BY step_key)::text FROM studio.world_analysis_steps s WHERE task_id=$1`, req.TaskID).Scan(&after)
			if after != snapshot {
				t.Fatal("original records mutated")
			}
			if _, e = svc.db.Exec(ctx, `UPDATE studio.world_analysis_steps SET revision=revision+1 WHERE task_id=$1`, req.TaskID); e == nil {
				t.Fatal("terminal history became mutable")
			}
		})
	}
}
