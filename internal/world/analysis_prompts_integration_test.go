//go:build integration

package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

func TestAnalysisPromptExecutionRecoveryAndCompleteness(t *testing.T) {
	testAnalysisBundle(t, syntheticPromptTargets())
}

func TestAnalysisPromptFailureStopsUnsentBatches(t *testing.T) {
	testAnalysisBundle(t, syntheticPromptTargets(), true)
}

func syntheticPromptTargets() []analysis.PromptTarget {
	targets := []analysis.PromptTarget{}
	for i := 0; i < 5; i++ {
		targets = append(targets, analysis.PromptTarget{Key: fmt.Sprintf("synthetic-%d", i), RequestedLabel: fmt.Sprintf("Synthetic target %d", i), TaskType: "text", InputMode: "text", Guidance: "Synthetic fixture only; no real model claim"})
	}
	return targets
}

type promptChannelFixture struct {
	associationChannelFixture
	corrupt      bool
	schemaReject bool
}

func (c *promptChannelFixture) SubmitPrompts(_ context.Context, _ string, f analysis.PromptRequest) (analysis.Call, error) {
	c.submits++
	var payload struct {
		Schema map[string]any `json:"response_schema"`
	}
	if json.Unmarshal(f.Payload, &payload) != nil {
		return analysis.Call{}, analysis.ErrInvalid
	}
	raw, _ := json.Marshal(payload.Schema)
	digest := sha256.Sum256(raw)
	result := analysis.PromptCandidates{Version: 1, BatchKey: f.Batch.Key, Items: []analysis.PromptCandidate{}}
	for _, pair := range f.Batch.Pairs {
		evidence := []string{}
		for _, scope := range f.Batch.Scopes {
			if scope.Key == pair.Scope {
				evidence = append(evidence, scope.Evidence...)
			}
		}
		result.Items = append(result.Items, analysis.PromptCandidate{Scope: pair.Scope, Target: pair.Target, Title: "Synthetic candidate", CreativeIntent: "Synthetic prompt recovery test", Text: "A synthetic fixed camera description.", AdaptationNote: "Not an actual model result", Variables: []analysis.PromptVariable{}, Evidence: evidence, MissingInputs: []string{"Real model test pending"}, Uncertainties: []string{}})
	}
	if c.corrupt {
		result.Items[0].Scope = "not-in-the-frozen-batch"
	}
	text, _ := json.Marshal(result)
	c.result, _ = json.Marshal(map[string]any{"schema_version": 1, "call_id": c.call.CallID, "task_id": f.TaskID, "model_id": f.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{
		"text": string(text), "document": result, "model_id": f.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"source_text_sha256": f.TextHash, "response_schema_sha256": hex.EncodeToString(digest[:]), "media": nil, "video_range": nil},
	}})
	if c.schemaReject {
		var n map[string]any
		json.Unmarshal(c.result, &n)
		output := n["analysis"].(map[string]any)
		doc := output["document"].(map[string]any)
		delete(doc, "items")
		encoded, _ := json.Marshal(doc)
		output["text"] = string(encoded)
		output["response_schema_verified"] = false
		n["state"] = "failed"
		n["reason_code"] = "output_schema_mismatch"
		c.result, _ = json.Marshal(n)
		c.call.State = "failed"
	}

	return c.call, c.err
}

func verifyPromptExecution(t *testing.T, svc *Service, p Principal, b analysis.Bundle) {
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	taskForResult, taskErr := store.Task(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if taskErr != nil {
		t.Fatal(taskErr)
	}
	if taskForResult.IncludeTypedResults {
		if e := svc.PrepareAnalysisResults(ctx, p, b.TaskID, true); e == nil {
			t.Fatal("results before prompt prerequisites")
		}
	}

	if err := svc.PrepareAnalysisPrompts(ctx, p, b.TaskID, false); !errors.Is(err, ErrNotReady) {
		t.Fatal("disabled dispatch prepared new batches", err)
	}
	// Inject a failure on the second INSERT, inside this disposable database.
	// The first batch must roll back too; no partial plan may become visible.
	_, err := svc.db.Exec(ctx, `CREATE FUNCTION studio.test_fail_prompt_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.batch_index=1 THEN RAISE EXCEPTION 'synthetic second insert failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER test_fail_prompt_insert BEFORE INSERT ON studio.world_analysis_prompt_batches FOR EACH ROW EXECUTE FUNCTION studio.test_fail_prompt_insert();`)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.PrepareAnalysisPrompts(ctx, p, b.TaskID, true); err == nil || !strings.Contains(err.Error(), "synthetic second insert failure") {
		t.Fatal("injected insert failure missing")
	}
	var count int
	if err = svc.db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_prompt_batches WHERE task_id=$1`, b.TaskID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial prompt plan became visible", count, err)
	}
	if _, err = svc.db.Exec(ctx, `DROP TRIGGER test_fail_prompt_insert ON studio.world_analysis_prompt_batches; DROP FUNCTION studio.test_fail_prompt_insert()`); err != nil {
		t.Fatal(err)
	}
	due := func() {
		t.Helper()
		if _, e := svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); e != nil {
			t.Fatal(e)
		}
	}
	yes := true
	c := &promptChannelFixture{associationChannelFixture: associationChannelFixture{observationChannel: observationChannel{call: analysis.Call{SchemaVersion: 1, Binding: analysis.Binding{CallID: newID(), TaskID: b.TaskID, ModelID: b.Receipts[0].ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}, err: analysis.ErrUnavailable}}}
	dispatch := true
	worker := analysis.Scheduler{Queue: store, Dispatch: true, Advance: func(ctx context.Context, w analysis.Work) (string, error) {
		actor := Principal{OrganizationID: w.OrganizationID, SubjectID: w.OwnerID, RequestID: w.LeaseID}
		if w.StepKey == analysis.PromptSetupStepKey {
			return "prompts_planned", svc.PrepareAnalysisPrompts(ctx, actor, w.TaskID, dispatch)
		}
		if !analysis.IsPromptBatchKey(w.StepKey) {
			t.Fatal("unexpected work", w.StepKey)
		}
		r, e := svc.AdvanceAnalysisPrompt(ctx, actor, w.TaskID, w.StepKey, c, dispatch)
		return r.State, e
	}}
	due()
	if worked, e := worker.Tick(ctx); e != nil || !worked || c.submits != 0 {
		t.Fatal("automatic plan creation", e)
	}
	records, err := store.PromptRecords(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || len(records) != 2 {
		t.Fatal("missing full plan", err)
	}
	requests := []analysis.PromptRequest{records[0].Request, records[1].Request}
	if err = store.CreatePromptBatches(ctx, p.OrganizationID, p.SubjectID, requests); err != nil {
		t.Fatal("same plan replay", err)
	}
	if err = store.CreatePromptBatches(ctx, p.OrganizationID, p.SubjectID, requests[:1]); err == nil {
		t.Fatal("truncated plan accepted")
	}
	changed := requests[1]
	changed.BudgetLimit = "9.00"
	requests[1] = changed
	if err = store.CreatePromptBatches(ctx, p.OrganizationID, p.SubjectID, requests); err == nil {
		t.Fatal("changed frozen plan accepted")
	}
	task, err := store.Task(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	changedTargets := append([]analysis.PromptTarget{}, task.PromptTargets...)
	changedTargets[0].RequestedLabel = "Changed target"
	if _, err = svc.PrepareVideoAnalysis(ctx, p, b.TaskID, AnalysisSource{b.DraftID, b.RevisionID, b.FileID}, AnalysisExecution{ModelID: b.Receipts[0].ModelID, BudgetPerStep: "1.00", PromptTargets: changedTargets}, analysis.Preparer{}); !errors.Is(err, ErrIdempotency) {
		t.Fatal("task target changed", err)
	}
	due()
	if worked, e := worker.Tick(ctx); !worked || !errors.Is(e, analysis.ErrUnavailable) || c.submits != 1 {
		t.Fatal("unknown POST not retained", e)
	}
	first, err := store.LoadPrompt(ctx, p.OrganizationID, p.SubjectID, b.TaskID, records[0].Request.Batch.Key)
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
	if _, err = svc.AnalysisPromptDrafts(ctx, p, b.TaskID); !errors.Is(err, ErrNotReady) {
		t.Fatal("partial candidates appear complete", err)
	}
	progress, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || progress.PromptReady != 1 || progress.PromptPlanned != 1 || progress.PromptTotal != 2 || progress.Stage != "producing_prompts" {
		t.Fatal("partial progress", progress, err)
	}
	due()
	if worked, e := worker.Tick(ctx); worked || e != nil {
		t.Fatal("dispatch disabled but new batch sent", e)
	}
	dispatch = true
	worker.Dispatch = true
	c.err = nil
	c.call.CallID = newID()
	due()
	if worked, e := worker.Tick(ctx); e != nil || !worked || c.submits != 2 {
		t.Fatal("second batch completion", e)
	}
	drafts, err := svc.AnalysisPromptDrafts(ctx, p, b.TaskID)
	if err != nil || len(drafts) != 10 {
		t.Fatal("complete candidates missing", len(drafts), err)
	}
	progress, err = store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	expectedStage := "prompts_ready"
	if task.IncludeTypedResults {
		expectedStage = "preparing_results"
	}
	if err != nil || progress.PromptReady != 2 || progress.Stage != expectedStage {
		t.Fatal("complete candidate progress", progress, err)
	}
	worker.Dispatch = false
	due()
	if worked, e := worker.Tick(ctx); worked || e != nil || c.submits != 2 {
		t.Fatal("completed batch repeated", e)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_prompt_batches SET revision=revision+1 WHERE task_id=$1`, b.TaskID); err == nil {
		t.Fatal("terminal prompt mutable")
	}
}

func verifyPromptFailure(t *testing.T, svc *Service, p Principal, b analysis.Bundle) {
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	if err := svc.PrepareAnalysisPrompts(ctx, p, b.TaskID, true); err != nil {
		t.Fatal(err)
	}
	records, err := store.PromptRecords(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || len(records) != 2 {
		t.Fatal(err)
	}
	c := &promptChannelFixture{associationChannelFixture: associationChannelFixture{observationChannel: observationChannel{err: analysis.ErrRejected}}}
	r, err := svc.AdvanceAnalysisPrompt(ctx, p, b.TaskID, records[0].Request.Batch.Key, c, true)
	if err != nil || r.State != "failed" || c.submits != 1 {
		t.Fatal("explicit rejection not retained", err)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(ctx, true); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("failed task still scheduled new spending", err)
	}
	r, err = svc.AdvanceAnalysisPrompt(ctx, p, b.TaskID, records[1].Request.Batch.Key, c, true)
	if err != nil || r.State != "planned" || c.submits != 1 {
		t.Fatal("direct advance bypassed failure stop", err)
	}
	pv, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || pv.Stage != "needs_attention" || pv.PromptFailed != 1 || pv.PromptPlanned != 1 {
		t.Fatal("failed progress", pv, err)
	}
	if _, err = svc.AnalysisPromptDrafts(ctx, p, b.TaskID); !errors.Is(err, ErrNotReady) {
		t.Fatal("failed task exposes full candidates", err)
	}
}
