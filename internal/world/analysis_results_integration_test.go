//go:build integration

package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"strings"
	"testing"
)

func TestAnalysisResultExecutionRecoveryAndCompleteness(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{targets: syntheticPromptTargets(), typed: true})
}
func TestAnalysisResultsWithoutPromptTargets(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{typed: true})
}
func TestAnalysisResultFailureStopsUnsentBatches(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{typed: true, failResults: true})
}
func TestAnalysisResultsRequireSuccessfulPrompts(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{targets: syntheticPromptTargets(), typed: true, failPrompts: true})
}

type resultChannelFixture struct{ associationChannelFixture }

func (c *resultChannelFixture) SubmitResults(_ context.Context, _ string, f analysis.ResultRequest) (analysis.Call, error) {
	c.submits++
	var payload struct {
		Schema map[string]any `json:"response_schema"`
	}
	if json.Unmarshal(f.Payload, &payload) != nil {
		return analysis.Call{}, analysis.ErrInvalid
	}
	raw, _ := json.Marshal(payload.Schema)
	digest := sha256.Sum256(raw)
	var request struct {
		Text string `json:"source_text"`
	}
	var textInput struct {
		Observations analysis.Bundle `json:"observations"`
	}
	if json.Unmarshal(f.Payload, &request) != nil || json.Unmarshal([]byte(request.Text), &textInput) != nil || len(textInput.Observations.Nodes) == 0 {
		return analysis.Call{}, analysis.ErrInvalid
	}
	out := analysis.ResultCandidates{Version: 1, BatchKey: f.Batch.Key, Type: f.Batch.Type, Items: []analysis.ResultCandidate{}}
	for _, scope := range f.Batch.Scopes {
		evidence := append([]string{}, scope.Evidence...)
		if len(evidence) == 0 {
			evidence = []string{textInput.Observations.Nodes[0].Key}
		}
		item := analysis.ResultCandidate{Scope: scope.Key, Title: "Synthetic " + f.Batch.Type, State: "ready", Basis: "inferred", BasisNote: "Synthetic fixture; no model validation", Evidence: evidence, MissingInputs: []string{}, Uncertainties: []string{}, Payloads: []map[string]any{}}
		switch f.Batch.Type {
		case "shot":
			timeRange := map[string]any{"kind": "time", "start_tick": scope.Range.StartMS, "end_tick": scope.Range.EndMS, "timebase_num": 1, "timebase_den": 1000}
			item.Payloads = []map[string]any{{"segment_kind": "edit_shot", "scope": timeRange, "composition": "Synthetic wide frame", "camera_motion": "Static", "subject_motion": "Still", "graphic_motion": "None", "action_beats": []any{map[string]any{"scope": timeRange, "action": "Synthetic still frame"}}}}
		case "script_unit":
			item.State = "insufficient_evidence"
			item.MissingInputs = []string{"No narrative evidence in synthetic source"}
		case "style":
			item.State = "not_applicable"
			item.MissingInputs = []string{"No distinctive style in synthetic source"}
		case "method":
			item.Payloads = []map[string]any{{"purpose": "creative_reuse", "objective": "Plan a new scene", "applicability": "Static scenes", "inputs": []any{map[string]any{"key": "brief", "value_type": "string", "required": true, "description": "New scene intent"}}, "steps": []any{map[string]any{"key": "plan", "instruction": "Write a new scene", "input_keys": []string{"brief"}, "output_keys": []string{"outline"}}}, "constraints": []any{map[string]any{"key": "scope", "condition": "New brief", "effect": "Rebuild scene"}}, "expected_outputs": []any{map[string]any{"key": "outline", "type_key": "script_unit", "description": "New outline", "checks": []string{"Matches brief"}}}, "counterexamples": []string{"Copying original dialogue"}}}
		}
		out.Items = append(out.Items, item)
	}
	text, _ := json.Marshal(out)
	c.result, _ = json.Marshal(map[string]any{"schema_version": 1, "call_id": c.call.CallID, "task_id": f.TaskID, "model_id": f.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{"text": string(text), "document": out, "model_id": f.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"source_text_sha256": f.TextHash, "response_schema_sha256": hex.EncodeToString(digest[:]), "media": nil, "video_range": nil}}})
	return c.call, c.err
}

func verifyResultExecution(t *testing.T, svc *Service, p Principal, b analysis.Bundle) {
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	if err := svc.PrepareAnalysisResults(ctx, p, b.TaskID, false); !errors.Is(err, ErrNotReady) {
		t.Fatal("disabled dispatch prepared new batches", err)
	}
	// Inject a failure on the second INSERT, inside this disposable database.
	// The first batch must roll back too; no partial plan may become visible.
	_, err := svc.db.Exec(ctx, `CREATE FUNCTION studio.test_fail_result_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.batch_index=1 THEN RAISE EXCEPTION 'synthetic second insert failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER test_fail_result_insert BEFORE INSERT ON studio.world_analysis_result_batches FOR EACH ROW EXECUTE FUNCTION studio.test_fail_result_insert();`)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.PrepareAnalysisResults(ctx, p, b.TaskID, true); err == nil || !strings.Contains(err.Error(), "synthetic second insert failure") {
		t.Fatal("injected insert failure missing")
	}
	var count int
	if err = svc.db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_result_batches WHERE task_id=$1`, b.TaskID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial result plan became visible", count, err)
	}
	if _, err = svc.db.Exec(ctx, `DROP TRIGGER test_fail_result_insert ON studio.world_analysis_result_batches; DROP FUNCTION studio.test_fail_result_insert()`); err != nil {
		t.Fatal(err)
	}
	due := func() {
		t.Helper()
		if _, e := svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); e != nil {
			t.Fatal(e)
		}
	}
	yes := true
	c := &resultChannelFixture{associationChannelFixture: associationChannelFixture{observationChannel: observationChannel{call: analysis.Call{SchemaVersion: 1, Binding: analysis.Binding{CallID: newID(), TaskID: b.TaskID, ModelID: b.Receipts[0].ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}, err: analysis.ErrUnavailable}}}
	dispatch := true
	worker := analysis.Scheduler{Queue: store, Dispatch: true, Advance: func(ctx context.Context, w analysis.Work) (string, error) {
		actor := Principal{OrganizationID: w.OrganizationID, SubjectID: w.OwnerID, RequestID: w.LeaseID}
		if w.StepKey == analysis.ResultSetupStepKey {
			return "results_planned", svc.PrepareAnalysisResults(ctx, actor, w.TaskID, dispatch)
		}
		if !analysis.IsResultBatchKey(w.StepKey) {
			t.Fatal("unexpected work", w.StepKey)
		}
		r, e := svc.AdvanceAnalysisResult(ctx, actor, w.TaskID, w.StepKey, c, dispatch)
		return r.State, e
	}}
	due()
	if worked, e := worker.Tick(ctx); e != nil || !worked || c.submits != 0 {
		t.Fatal("automatic plan creation", e)
	}
	records, err := store.ResultRecords(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || len(records) != 4 {
		t.Fatal("missing full plan", err)
	}
	requests := []analysis.ResultRequest{}
	for _, r := range records {
		requests = append(requests, r.Request)
	}
	if err = store.CreateResultBatches(ctx, p.OrganizationID, p.SubjectID, requests); err != nil {
		t.Fatal("same plan replay", err)
	}
	if err = store.CreateResultBatches(ctx, p.OrganizationID, p.SubjectID, requests[:1]); err == nil {
		t.Fatal("truncated plan accepted")
	}
	changed := requests[1]
	changed.BudgetLimit = "9.00"
	requests[1] = changed
	if err = store.CreateResultBatches(ctx, p.OrganizationID, p.SubjectID, requests); err == nil {
		t.Fatal("changed frozen plan accepted")
	}
	task, err := store.Task(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.PrepareVideoAnalysis(ctx, p, b.TaskID, AnalysisSource{b.DraftID, b.RevisionID, b.FileID}, AnalysisExecution{ModelID: b.Receipts[0].ModelID, BudgetPerStep: "1.00", PromptTargets: task.PromptTargets, IncludeTypedResults: false}, analysis.Preparer{}); !errors.Is(err, ErrIdempotency) {
		t.Fatal("typed task option changed", err)
	}
	due()
	if worked, e := worker.Tick(ctx); !worked || !errors.Is(e, analysis.ErrUnavailable) || c.submits != 1 {
		t.Fatal("unknown POST not retained", e)
	}
	first, err := store.LoadResult(ctx, p.OrganizationID, p.SubjectID, b.TaskID, records[0].Request.Batch.Key)
	if err != nil || first.State != "acceptance_unknown" || first.CallID != "" {
		t.Fatal("unknown call not durable", err)
	}
	svc, err = NewService(ctx, svc.db, svc.content, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch = false
	worker.Dispatch = false
	due()
	if worked, e := worker.Tick(ctx); e != nil || !worked || c.submits != 1 || c.lookups != 1 {
		t.Fatal("restart did not recover original", e)
	}
	c.call.State = "completed"
	due()
	if worked, e := worker.Tick(ctx); e != nil || !worked || c.submits != 1 || c.polls != 1 {
		t.Fatal("original result not recovered", e)
	}
	if _, err = svc.AnalysisResultDrafts(ctx, p, b.TaskID); !errors.Is(err, ErrNotReady) {
		t.Fatal("partial candidates appear complete", err)
	}
	progress, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || progress.ResultReady != 1 || progress.ResultPlanned != 3 || progress.ResultTotal != 4 || progress.Stage != "producing_results" {
		t.Fatal("partial progress", progress, err)
	}
	due()
	if worked, e := worker.Tick(ctx); worked || e != nil {
		t.Fatal("dispatch disabled but new batch sent", e)
	}
	dispatch = true
	worker.Dispatch = true
	c.err = nil
	for i := 1; i < len(records); i++ {
		c.call.CallID = newID()
		due()
		if worked, e := worker.Tick(ctx); e != nil || !worked || c.submits != i+1 {
			t.Fatal("remaining batch completion", i, e)
		}
	}
	drafts, err := svc.AnalysisResultDrafts(ctx, p, b.TaskID)
	if err != nil || len(drafts) != 4 {
		t.Fatal("complete candidates missing", len(drafts), err)
	}
	progress, err = store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	storedTask, taskErr := store.Task(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if taskErr != nil {
		t.Fatal(taskErr)
	}
	expectedStage := "results_need_evidence"
	if storedTask.SaveLearning {
		expectedStage = "saving_learning"
	}
	if err != nil || progress.ResultReady != 4 || progress.ResultInsufficient != 1 || progress.Stage != expectedStage {
		t.Fatal("missing incomplete disposition", progress, err)
	}
	if drafts[1].State != "insufficient_evidence" || drafts[2].State != "not_applicable" || len(drafts[1].ContextUncertainties) == 0 {
		t.Fatal("dispositions or uncertainty lost")
	}
	worker.Dispatch = false
	due()
	task, e := store.Task(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	if !task.SaveLearning {
		if worked, e := worker.Tick(ctx); worked || e != nil || c.submits != 4 {
			t.Fatal("completed batch repeated", e)
		}
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_result_batches SET revision=revision+1 WHERE task_id=$1`, b.TaskID); err == nil {
		t.Fatal("terminal result mutable")
	}
}

func verifyResultFailure(t *testing.T, svc *Service, p Principal, b analysis.Bundle) {
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	if err := svc.PrepareAnalysisResults(ctx, p, b.TaskID, true); err != nil {
		t.Fatal(err)
	}
	records, err := store.ResultRecords(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || len(records) != 4 {
		t.Fatal(err)
	}
	c := &resultChannelFixture{associationChannelFixture: associationChannelFixture{observationChannel: observationChannel{err: analysis.ErrRejected}}}
	r, err := svc.AdvanceAnalysisResult(ctx, p, b.TaskID, records[0].Request.Batch.Key, c, true)
	if err != nil || r.State != "failed" || c.submits != 1 {
		t.Fatal("explicit rejection not retained", err)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(ctx, true); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("failed task still scheduled new spending", err)
	}
	r, err = svc.AdvanceAnalysisResult(ctx, p, b.TaskID, records[1].Request.Batch.Key, c, true)
	if err != nil || r.State != "planned" || c.submits != 1 {
		t.Fatal("direct advance bypassed failure stop", err)
	}
	pv, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || pv.Stage != "needs_attention" || pv.ResultFailed != 1 || pv.ResultPlanned != 3 {
		t.Fatal("failed progress", pv, err)
	}
	if _, err = svc.AnalysisResultDrafts(ctx, p, b.TaskID); !errors.Is(err, ErrNotReady) {
		t.Fatal("failed task exposes full candidates", err)
	}
}
