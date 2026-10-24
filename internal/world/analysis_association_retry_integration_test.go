//go:build integration

package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

func TestAnalysisAssociationRetryHistoryBudgetAndDownstream(t *testing.T) {
	for _, budget := range []string{"1", "0.2", "2"} {
		t.Run(budget, func(t *testing.T) {
			ctx := t.Context()
			svc, p, req, _ := admissionFixture(t)
			req.BudgetLimit = budget
			req.BudgetPerCall = "0.1"
			if _, err := svc.WorldAnalyze(ctx, p, req); err != nil {
				t.Fatal(err)
			}
			store := analysis.PGStore{DB: svc.db}
			w, err := store.Claim(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			task, requests := admissionFrozenTask(t, svc, p, req, 10000)
			if err = store.Create(ctx, task, requests); err != nil {
				t.Fatal(err)
			}
			if err = store.FinishSubmission(ctx, w, ""); err != nil {
				t.Fatal(err)
			}
			r, err := store.Load(ctx, p.OrganizationID, p.SubjectID, req.TaskID, requests[0].Step.Key)
			if err != nil {
				t.Fatal(err)
			}
			sent := r
			sent.Revision++
			sent.State = "dispatching"
			if ok, e := store.CompareAndSwap(ctx, r, sent); e != nil || !ok {
				t.Fatal(e)
			}
			if ok, e := store.CompareAndSwap(ctx, sent, retryNative(t, sent, false, budget == "2")); e != nil || !ok {
				t.Fatal(e)
			}
			b, err := store.Bundle(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			f, err := analysis.FreezeAssociation(b, req.ModelID, "", req.BudgetPerCall)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.CreateAssociation(ctx, p.OrganizationID, p.SubjectID, f); err != nil {
				t.Fatal(err)
			}
			finish := func(valid bool, omit ...bool) analysis.AssociationRecord {
				old, e := store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
				if e != nil {
					t.Fatal(e)
				}
				dispatch := old
				dispatch.State = "dispatching"
				dispatch.Revision++
				ok, e := store.SwapAssociation(ctx, old, dispatch)
				if e != nil || !ok {
					t.Fatal(e)
				}
				var payload map[string]any
				json.Unmarshal(old.Request.Payload, &payload)
				schema, _ := json.Marshal(payload["response_schema"])
				digest := sha256.Sum256(schema)
				a := analysis.Association{Version: 1, BundleHash: b.SHA256, Entities: []analysis.AssociationGroup{}, Voices: []analysis.AssociationGroup{}, Shots: []analysis.ShotGroup{}, Boundaries: []analysis.BoundaryDecision{}, VoiceLinks: []analysis.VoiceLink{}, Uncertainties: []string{}}
				if valid {
					for index, n := range b.Nodes {
						if n.Kind == "entities" && len(omit) == 0 {
							key := n.Key
							if old.Request.Encoding == "short-v1" {
								key = fmt.Sprintf("e%06d", index+1)
							}
							a.Entities = append(a.Entities, analysis.AssociationGroup{Key: "bag", Kind: "prop", Name: "Synthetic bag", Description: "Synthetic fixture", Members: []string{key}, State: "single", Evidence: []string{key}, Reason: "Synthetic fixture"})
						}
						if n.Kind == "shots" {
							if old.Request.Encoding == "short-v1" {
								n.Key = fmt.Sprintf("e%06d", index+1)
							}
							a.Shots = append(a.Shots, analysis.ShotGroup{Key: "one", Members: []string{n.Key}, StartMS: n.Scope.StartMS, EndMS: n.Scope.EndMS, State: "single", Evidence: []string{n.Key}, Reason: "synthetic"})
						}
					}
				}
				text, _ := json.Marshal(a)
				done := dispatch
				done.Revision++
				done.CallID = newID()
				done.State = "association_ready"
				if !valid || len(omit) > 0 {
					done.State = "failed"
					done.ErrorCode = "association_invalid"
				}
				done.Result, _ = json.Marshal(map[string]any{"schema_version": 1, "call_id": done.CallID, "task_id": req.TaskID, "model_id": f.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{"text": string(text), "document": a, "model_id": f.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"source_text_sha256": old.Request.TextHash, "response_schema_sha256": hex.EncodeToString(digest[:])}}})
				if ok, e = store.SwapAssociation(ctx, dispatch, done); e != nil || !ok {
					t.Fatal(e)
				}
				return done
			}
			failed := finish(false)
			var before string
			svc.db.QueryRow(ctx, `SELECT to_jsonb(a)::text FROM studio.world_analysis_associations a WHERE task_id=$1`, req.TaskID).Scan(&before)
			retry := AnalyzeRequest{Mode: "retry", TaskID: req.TaskID, RetryID: newID()}
			if _, e := svc.WorldAnalyze(ctx, Principal{OrganizationID: p.OrganizationID, SubjectID: newID(), RequestID: newID()}, retry); e == nil {
				t.Fatal("foreign retry")
			}
			for range 2 {
				if _, e := svc.WorldAnalyze(ctx, p, retry); e != nil {
					t.Fatal("retry/replay", e)
				}
			}
			next, e := store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
			if e != nil || next.Request.Retry.PreviousCallID != failed.CallID {
				t.Fatal("missing next attempt", e)
			}
			work, e := store.Claim(ctx, true)
			if e != nil || work.StepKey != analysis.AssociationStepKey {
				t.Fatal("wrong scheduling", work, e)
			}
			if e = store.Release(ctx, work, ""); e != nil {
				t.Fatal(e)
			}
			if budget == "0.2" {
				dispatch := next
				dispatch.State = "dispatching"
				dispatch.Revision++
				if _, e = store.SwapAssociation(ctx, next, dispatch); !errors.Is(e, analysis.ErrBudgetExhausted) {
					t.Fatal("budget bypass", e)
				}
			} else {
				if budget == "2" {
					sent := next
					sent.State = "dispatching"
					sent.Revision++
					if ok, e := store.SwapAssociation(ctx, next, sent); e != nil || !ok {
						t.Fatal(e)
					}
					failed := sent
					failed.State = "failed"
					failed.ErrorCode = "native_failed"
					failed.CallID = newID()
					failed.Revision++
					if ok, e := store.SwapAssociation(ctx, sent, failed); e != nil || !ok {
						t.Fatal(e)
					}
					var prior map[string]any
					json.Unmarshal(failed.Request.Payload, &prior)
					schema, _ := json.Marshal(prior["response_schema"])
					sum := sha256.Sum256(schema)
					native, _ := json.Marshal(map[string]any{"schema_version": 1, "call_id": failed.CallID, "task_id": req.TaskID, "model_id": failed.Request.ModelID, "media": "analysis", "operation": "analyze", "state": "failed", "reason_code": "output_incomplete", "analysis": map[string]any{"model_id": failed.Request.ModelID, "finish_reason": "MAX_TOKENS", "text": "{", "document": nil, "response_schema_verified": false, "input": map[string]any{"source_text_sha256": failed.Request.TextHash, "response_schema_sha256": hex.EncodeToString(sum[:])}}})
					reader := associationFailureReader{native}
					retryID := newID()
					for range 2 {
						if e := store.Retry(ctx, p.OrganizationID, p.SubjectID, req.TaskID, retryID, reader); e != nil {
							t.Fatal("truncation retry", e)
						}
					}
					loaded, e := store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
					if e != nil || loaded.Request.Encoding != "short-v1" || loaded.Request.Retry.Number != 2 {
						t.Fatal("encoding not frozen", e)
					}
					var unchanged bool
					svc.db.QueryRow(ctx, `SELECT state='failed' AND native_result IS NULL FROM studio.world_analysis_association_retries WHERE task_id=$1 AND attempt=1`, req.TaskID).Scan(&unchanged)
					if !unchanged {
						t.Fatal("failed terminal rewritten")
					}
				}
				if budget == "2" {
					omitted := finish(true, true)
					rid := newID()
					for range 2 {
						if e := store.Retry(ctx, p.OrganizationID, p.SubjectID, req.TaskID, rid); e != nil {
							t.Fatal("final coverage retry", e)
						}
					}
					current, e := store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
					if e != nil || current.Request.Retry.Number != 3 || current.Request.Retry.PreviousCallID != omitted.CallID {
						t.Fatal("final correction lost binding", e)
					}
					var unchanged bool
					svc.db.QueryRow(ctx, `SELECT native_result=$2::jsonb AND state='failed' FROM studio.world_analysis_association_retries WHERE task_id=$1 AND attempt=2`, req.TaskID, string(omitted.Result)).Scan(&unchanged)
					if !unchanged {
						t.Fatal("entity omission receipt changed")
					}
				}
				finish(true)
				if _, e = svc.AnalysisAssociation(ctx, p, req.TaskID); e != nil {
					t.Fatal("association unreadable", e)
				}
				if e = svc.PrepareAnalysisResults(ctx, p, req.TaskID, true); e != nil {
					t.Fatal("downstream ignored retry", e)
				}
				if _, e = svc.WorldAnalyze(ctx, p, AnalyzeRequest{Mode: "retry", TaskID: req.TaskID, RetryID: newID()}); e == nil {
					t.Fatal("completed association retried")
				}
				cost, e := store.ReservedBudget(ctx, p.OrganizationID, p.SubjectID, req.TaskID)
				expected := "0.3"
				if budget == "2" {
					expected = "0.5"
				}
				if e != nil || cost != expected {
					t.Fatal("lost cost", cost, e)
				}
			}
			var after string
			svc.db.QueryRow(ctx, `SELECT to_jsonb(a)::text FROM studio.world_analysis_associations a WHERE task_id=$1`, req.TaskID).Scan(&after)
			if before != after {
				t.Fatal("terminal history modified")
			}
		})
	}
}

type associationFailureReader struct{ data []byte }

func (r associationFailureReader) Result(context.Context, string, string) ([]byte, error) {
	return r.data, nil
}
