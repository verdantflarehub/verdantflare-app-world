//go:build integration

package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

// Tests actual SQL/worker orchestration with explicitly synthetic step outputs.
// No provider inference, source understanding or complete learning asset claim.
func TestAnalysisSchedulerLeasesRecoveryAndIsolation(t *testing.T) {
	ctx := t.Context()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	content := &draftTestArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{}}
	svc, err := NewService(ctx, db, content, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	content.svc = svc
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: newID()}
	subjects := []string{"reference"}
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "Synthetic scheduler source", Purpose: "collection", Subjects: &subjects})
	if err != nil {
		t.Fatal(err)
	}
	initial := raw.(DraftRevision)
	media := []byte("synthetic-schedule-source")
	sum := sha256.Sum256(media)
	hash := hex.EncodeToString(sum[:])
	ref := ContentRef{testID(3), newID(), newID()}
	fileID := newID()
	content.versions[ref] = ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_import", WorldDraftID: initial.DraftID}, MIME: "video/mp4", Size: int64(len(media)), SHA256: hash}
	content.bytes[ref] = media
	m := initial.Manifest
	m.Files = []File{{ID: fileID, Path: "reference.mp4", Role: "reference", Content: ref}}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: initial.DraftID, ExpectedRevisionID: initial.RevisionID, CommitID: newID(), Manifest: &m})
	if err != nil {
		t.Fatal(err)
	}
	source := raw.(DraftRevision)
	store := analysis.PGStore{DB: db}
	create := func() analysis.Task {
		plan, _ := analysis.PlanVideo(analysis.Source{SHA256: hash, DurationMS: 90000})
		task := analysis.Task{OrganizationID: p.OrganizationID, OwnerID: p.SubjectID, TaskID: newID(), DraftID: source.DraftID, RevisionID: source.RevisionID, FileID: fileID, Plan: plan}
		requests := []analysis.FrozenRequest{}
		for _, step := range plan.Steps {
			r, e := analysis.Freeze(task.TaskID, "gemini-3.1-pro-preview", "", "1.00", step, analysis.Prepared{SourceHash: hash, MediaHash: hash, MIME: "video/mp4", Range: step.Input}, int64(len(media)))
			if e != nil {
				t.Fatal(e)
			}
			requests = append(requests, r)
		}
		if e := store.Create(ctx, task, requests); e != nil {
			t.Fatal(e)
		}
		return task
	}
	task := create()
	if _, err = store.Claim(ctx, false); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("recovery mode claimed unsent step", err)
	}
	// Two processes must not lease the same task concurrently.
	var wg sync.WaitGroup
	claims := make(chan analysis.Work, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); w, e := store.Claim(ctx, true); claims <- w; errs <- e }()
	}
	wg.Wait()
	close(claims)
	close(errs)
	wins := []analysis.Work{}
	for w := range claims {
		if w.TaskID != "" {
			wins = append(wins, w)
		}
	}
	for e := range errs {
		if e != nil && !errors.Is(e, analysis.ErrMissing) {
			t.Fatal(e)
		}
	}
	if len(wins) != 1 || wins[0].StepKey != task.Plan.Steps[0].Key {
		t.Fatal("duplicate or reordered lease")
	}
	old := wins[0]
	// A process dies after claiming but before entering the executor. Expiry
	// makes the same planned step eligible; no frozen request is modified.
	if _, err = db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET lease_until=now()-interval '1 second' WHERE task_id=$1`, task.TaskID); err != nil {
		t.Fatal(err)
	}
	w, err := store.Claim(ctx, true)
	if err != nil || w.LeaseID == old.LeaseID || w.StepKey != old.StepKey {
		t.Fatal("expired claim not recoverable", err)
	}
	if err = store.Release(ctx, old, ""); !errors.Is(err, analysis.ErrChanged) {
		t.Fatal("late holder overwrote successor", err)
	}
	// Preserve sending intent before simulated death. Resume in recovery-only
	// mode must look up the old operation and never prepare media or submit.
	r, err := store.Load(ctx, p.OrganizationID, p.SubjectID, task.TaskID, w.StepKey)
	if err != nil {
		t.Fatal(err)
	}
	next := r
	next.State = "dispatching"
	next.Revision++
	if ok, e := store.CompareAndSwap(ctx, r, next); e != nil || !ok {
		t.Fatal(e)
	}
	if err = store.Release(ctx, w, ""); err != nil {
		t.Fatal(err)
	}
	due := func(id string) {
		if _, e := db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now()-interval '1 second' WHERE task_id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	due(task.TaskID)
	yes := true
	channel := &observationChannel{call: analysis.Call{SchemaVersion: 1, Binding: analysis.Binding{CallID: newID(), TaskID: task.TaskID, ModelID: r.Request.ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}}
	worker := analysis.Scheduler{Queue: store, Advance: func(ctx context.Context, w analysis.Work) (string, error) {
		r, err := svc.AdvanceAnalysisStep(ctx, Principal{OrganizationID: w.OrganizationID, SubjectID: w.OwnerID, RequestID: w.LeaseID}, w.TaskID, w.StepKey, analysis.Preparer{FFprobe: "must-not-run", FFmpeg: "must-not-run"}, channel)
		return r.State, err
	}}
	if worked, e := worker.Tick(ctx); e != nil || !worked || channel.submits != 0 || channel.lookups != 1 {
		t.Fatal("restart resubmitted", e)
	}
	progress, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, task.TaskID)
	if err != nil || progress.Stage != "observing" || progress.Running != 1 || progress.Planned != 1 {
		t.Fatal("incorrect progress", progress, err)
	}
	for _, identity := range [][2]string{{newID(), p.SubjectID}, {p.OrganizationID, newID()}} {
		if _, e := store.Progress(ctx, identity[0], identity[1], task.TaskID); !errors.Is(e, analysis.ErrMissing) {
			t.Fatal("task visibility leaked", e)
		}
	}
	// Explicit failure stops remaining planned calls while another task runs.
	channel.call.State = "failed"
	due(task.TaskID)
	if worked, e := worker.Tick(ctx); e != nil || !worked || channel.submits != 0 || channel.polls != 1 {
		t.Fatal("terminal recovery", e)
	}
	due(task.TaskID)
	if _, err = store.Claim(ctx, true); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("failed task spent on next step", err)
	}
	progress, err = store.Progress(ctx, p.OrganizationID, p.SubjectID, task.TaskID)
	if err != nil || progress.Stage != "needs_attention" || progress.Failed != 1 || progress.Planned != 1 {
		t.Fatal(progress, err)
	}
	other := create()
	// Ready observations here are synthetic state fixtures, not model evidence.
	completed := 0
	finish := analysis.Scheduler{Queue: store, Dispatch: true, Advance: func(ctx context.Context, w analysis.Work) (string, error) {
		if w.TaskID != other.TaskID || w.StepKey != other.Plan.Steps[completed].Key {
			t.Fatal("fairness or plan order lost")
		}
		r, e := store.Load(ctx, w.OrganizationID, w.OwnerID, w.TaskID, w.StepKey)
		if e != nil {
			return r.State, e
		}
		n := r
		n.State = "dispatching"
		n.Revision++
		if ok, e := store.CompareAndSwap(ctx, r, n); e != nil || !ok {
			return r.State, analysis.ErrChanged
		}
		r = n
		n.State = "observation_ready"
		n.Revision++
		n.CallID = newID()
		n.Result = json.RawMessage(`{"synthetic_scheduler_fixture":true}`)
		n.Report = &analysis.Report{Passed: true, Issues: []analysis.Issue{}}
		if ok, e := store.CompareAndSwap(ctx, r, n); e != nil || !ok {
			return r.State, analysis.ErrChanged
		}
		completed++
		return n.State, nil
	}}
	for range len(other.Plan.Steps) {
		due(other.TaskID)
		if worked, e := finish.Tick(ctx); e != nil || !worked {
			t.Fatal("automatic next step", e)
		}
	}
	progress, err = store.Progress(ctx, p.OrganizationID, p.SubjectID, other.TaskID)
	if err != nil || progress.Stage != "observations_ready" || progress.Ready != 2 {
		t.Fatal("observations not aggregated", progress, err)
	}
	due(other.TaskID)
	if _, e := store.Claim(ctx, false); !errors.Is(e, analysis.ErrMissing) {
		t.Fatal("disabled dispatch created association", e)
	}
	associationWork, e := store.Claim(ctx, true)
	if e != nil || associationWork.TaskID != other.TaskID || associationWork.StepKey != analysis.AssociationStepKey {
		t.Fatal("association not scheduled", e)
	}
	if e = store.Release(ctx, associationWork, ""); e != nil {
		t.Fatal(e)
	}
	// The new queue obeys the same writer ownership gate as business records.
	if _, err = db.Exec(ctx, `UPDATE studio.world_handoff SET state='closed'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now()`); err == nil {
		t.Fatal("schedule bypassed writer gate")
	}
}
