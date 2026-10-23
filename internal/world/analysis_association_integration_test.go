//go:build integration

package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

type associationChannelFixture struct {
	observationChannel
	result []byte
}

func (c *associationChannelFixture) SubmitAssociation(context.Context, string, analysis.AssociationRequest) (analysis.Call, error) {
	c.submits++
	return c.call, c.err
}
func (c *associationChannelFixture) Result(_ context.Context, _ string, id string) ([]byte, error) {
	if id != c.call.CallID {
		return nil, analysis.ErrInvalid
	}
	return c.result, nil
}

// Called by the fixed-source integration test after actual observation records
// are persisted. Channel output is synthetic, not provider/content validation.
func verifyAssociationExecution(t *testing.T, svc *Service, p Principal, b analysis.Bundle) {
	t.Helper()
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	if _, err := store.Claim(ctx, false); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("disabled dispatch created association", err)
	}
	yes := true
	c := &associationChannelFixture{observationChannel: observationChannel{call: analysis.Call{SchemaVersion: 1, Binding: analysis.Binding{CallID: newID(), TaskID: b.TaskID, ModelID: b.Receipts[0].ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}, err: analysis.ErrUnavailable}}
	dispatch := true
	worker := analysis.Scheduler{Queue: store, Dispatch: true, Advance: func(ctx context.Context, w analysis.Work) (string, error) {
		if w.StepKey != analysis.AssociationStepKey || w.TaskID != b.TaskID {
			t.Fatal("wrong automatic phase")
		}
		r, err := svc.AdvanceAnalysisAssociation(ctx, Principal{OrganizationID: w.OrganizationID, SubjectID: w.OwnerID, RequestID: w.LeaseID}, w.TaskID, c, dispatch)
		return r.State, err
	}}
	if worked, err := worker.Tick(ctx); !worked || !errors.Is(err, analysis.ErrUnavailable) || c.submits != 1 {
		t.Fatal("lost POST not retained", err)
	}
	r, err := store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || r.State != "acceptance_unknown" || r.CallID != "" {
		t.Fatal("missing durable unknown", err)
	}
	if _, err = svc.AnalysisAssociation(ctx, p, b.TaskID); !errors.Is(err, ErrNotReady) {
		t.Fatal("unknown appeared ready", err)
	}
	changed := r.Request
	changed.BudgetLimit = "2.00"
	if err = store.CreateAssociation(ctx, p.OrganizationID, p.SubjectID, changed); !errors.Is(err, analysis.ErrChanged) {
		t.Fatal("frozen execution changed", err)
	}
	// New service instance reads persisted state; new submissions remain disabled.
	svc, err = NewService(ctx, svc.db, svc.content, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch = false
	worker.Dispatch = false
	due := func() {
		t.Helper()
		if _, e := svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); e != nil {
			t.Fatal(e)
		}
	}
	due()
	if worked, err := worker.Tick(ctx); err != nil || !worked || c.submits != 1 || c.lookups != 1 {
		t.Fatal("restart resubmitted instead of lookup", err)
	}
	r, err = store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || r.State != "running" || r.CallID != c.call.CallID {
		t.Fatal("original call not bound", err)
	}
	var payload struct {
		Schema map[string]any `json:"response_schema"`
	}
	if json.Unmarshal(r.Request.Payload, &payload) != nil {
		t.Fatal("saved request")
	}
	schemaBytes, err := json.Marshal(payload.Schema)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(schemaBytes)
	a := analysis.Association{Version: 1, BundleHash: b.SHA256, Entities: []analysis.AssociationGroup{}, Shots: []analysis.ShotGroup{}, Voices: []analysis.AssociationGroup{}, VoiceLinks: []analysis.VoiceLink{}, Boundaries: []analysis.BoundaryDecision{}, Uncertainties: []string{"Synthetic fixture only"}}
	for _, n := range b.Nodes {
		if n.Kind == "shots" {
			a.Shots = append(a.Shots, analysis.ShotGroup{Key: "single-shot", Members: []string{n.Key}, StartMS: n.Scope.StartMS, EndMS: n.Scope.EndMS, State: "single", Evidence: []string{n.Key}, Reason: "Synthetic fixed observation"})
		}
	}
	text, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	c.result, err = json.Marshal(map[string]any{"schema_version": 1, "call_id": r.CallID, "task_id": b.TaskID, "model_id": r.Request.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{
		"text": string(text), "document": a, "model_id": r.Request.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"source_text_sha256": r.Request.TextHash, "response_schema_sha256": hex.EncodeToString(digest[:]), "media": nil, "video_range": nil},
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.call.State = "completed"
	due()
	if worked, err := worker.Tick(ctx); err != nil || !worked || c.submits != 1 || c.polls != 1 {
		t.Fatal("original completion not recovered", err)
	}
	got, err := svc.AnalysisAssociation(ctx, p, b.TaskID)
	if err != nil || got.BundleHash != b.SHA256 || len(got.Shots) != 1 {
		t.Fatal("fixed association unreadable", err)
	}
	progress, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	task, taskErr := store.Task(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if taskErr != nil {
		t.Fatal(taskErr)
	}
	expectedStage := "associations_ready"
	if task.IncludeTypedResults {
		expectedStage = "preparing_results"
	}
	if len(task.PromptTargets) > 0 {
		expectedStage = "preparing_prompts"
	}
	if err != nil || progress.Stage != expectedStage || progress.AssociationState != "association_ready" {
		t.Fatal("phase progress", progress, err)
	}
	due()
	if worked, err := worker.Tick(ctx); worked || err != nil || c.submits != 1 {
		t.Fatal("terminal association repeated", err)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_associations SET revision=revision+1 WHERE task_id=$1`, b.TaskID); err == nil {
		t.Fatal("terminal association mutable")
	}
}
