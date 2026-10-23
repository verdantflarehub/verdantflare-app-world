//go:build integration

package world

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"sync"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

type admissionArtifact struct {
	*draftTestArtifact
	downloads int
}

func (a *admissionArtifact) Download(context.Context, Principal, ContentRef, Access, io.Writer, int64) error {
	a.downloads++
	return errors.New("unexpected download during admission or replay")
}
func admissionFixture(t *testing.T) (*Service, Principal, AnalyzeRequest, *admissionArtifact) {
	t.Helper()
	ctx := t.Context()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	a := &admissionArtifact{draftTestArtifact: &draftTestArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{}}}
	svc, err := NewService(ctx, db, a, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	a.svc = svc
	p := Principal{OrganizationID: newID(), SubjectID: newID(), RequestID: newID()}
	subjects := []string{"reference"}
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "Synthetic admission source", Purpose: "collection", Subjects: &subjects})
	if err != nil {
		t.Fatal(err)
	}
	d := raw.(DraftRevision)
	ref := ContentRef{testID(3), newID(), newID()}
	file := newID()
	media := []byte("synthetic-video-input")
	a.versions[ref] = ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_import", WorldDraftID: d.DraftID}, MIME: "video/mp4", Size: int64(len(media)), SHA256: mediaHash(media)}
	a.bytes[ref] = media
	m := d.Manifest
	m.Files = []File{{ID: file, Path: "reference.mp4", Role: "reference", Content: ref}}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: d.DraftID, ExpectedRevisionID: d.RevisionID, CommitID: newID(), Manifest: &m})
	if err != nil {
		t.Fatal(err)
	}
	d = raw.(DraftRevision)
	r := analyzeFixture()
	r.Source = &AnalysisSource{d.DraftID, d.RevisionID, file}
	c := &capabilityReader{value: analysis.Capabilities{State: "enabled", Models: []string{r.ModelID}, MIME: []string{"video/mp4", "audio/wav"}, MaxMediaBytes: analysis.MaxMediaBytes}}
	if err = svc.ConfigureAnalysis(AnalysisRuntime{Enabled: true, Channel: c}); err != nil {
		t.Fatal(err)
	}
	return svc, p, r, a
}
func admissionFrozenTask(t *testing.T, svc *Service, p Principal, r AnalyzeRequest, duration int64) (analysis.Task, []analysis.FrozenRequest) {
	t.Helper()
	_, v, err := svc.analysisSource(t.Context(), p, *r.Source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := analysis.PlanVideo(analysis.Source{SHA256: v.SHA256, DurationMS: duration})
	if err != nil {
		t.Fatal(err)
	}
	task := analysis.Task{OrganizationID: p.OrganizationID, OwnerID: p.SubjectID, TaskID: r.TaskID, DraftID: r.Source.DraftID, RevisionID: r.Source.RevisionID, FileID: r.Source.FileID, Plan: plan, PromptTargets: r.PromptTargets, IncludeTypedResults: true, SaveLearning: true, TotalBudget: r.BudgetLimit}
	requests := []analysis.FrozenRequest{}
	for _, s := range plan.Steps {
		f, err := analysis.Freeze(task.TaskID, r.ModelID, "", r.BudgetPerCall, s, analysis.Prepared{SourceHash: v.SHA256, MediaHash: v.SHA256, MIME: "video/mp4", Range: s.Input}, v.Size)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, f)
	}
	return task, requests
}
func TestAnalysisAdmissionDurableReplayOwnershipAndLease(t *testing.T) {
	svc, p, r, a := admissionFixture(t)
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	raw, err := svc.WorldAnalyze(ctx, p, r)
	if err != nil {
		t.Fatal(err)
	}
	v := raw.(AnalysisTaskView)
	if v.State != "pending" || v.Progress != nil || a.downloads != 0 {
		t.Fatal("admission blocked on preparation", v)
	}
	if _, err = store.Task(ctx, p.OrganizationID, p.SubjectID, r.TaskID); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("admission created work before preparation", err)
	}
	// Reconstruct service after the submit response is lost; disabled admission
	// still permits exact replay, inspection and recovery of an accepted request.
	restarted, err := NewService(ctx, svc.db, a, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.ConfigureAnalysis(AnalysisRuntime{}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = restarted.WorldAnalyze(ctx, p, r); err != nil {
			t.Fatal("replay after restart", err)
		}
	}
	changed := r
	changed.BudgetLimit = "12"
	if _, err = restarted.WorldAnalyze(ctx, p, changed); !errors.Is(err, ErrIdempotency) {
		t.Fatal("frozen request replaced", err)
	}
	fresh := r
	fresh.TaskID = newID()
	if _, err = restarted.WorldAnalyze(ctx, p, fresh); !errors.Is(err, ErrDependency) {
		t.Fatal("disabled admission accepted new request", err)
	}
	for _, other := range []Principal{{OrganizationID: newID(), SubjectID: p.SubjectID, RequestID: newID()}, {OrganizationID: p.OrganizationID, SubjectID: newID(), RequestID: newID()}} {
		if _, err = svc.WorldAnalyze(ctx, other, AnalyzeRequest{Mode: "get", TaskID: r.TaskID}); !errors.Is(err, ErrNotFound) {
			t.Fatal("private task leaked", err)
		}
		ids, err := store.SubmissionIDs(ctx, other.OrganizationID, other.SubjectID, "", 20)
		if err != nil || len(ids) != 0 {
			t.Fatal("private list leaked", err)
		}
	}
	ids, err := store.SubmissionIDs(ctx, p.OrganizationID, p.SubjectID, "", 20)
	if err != nil || len(ids) != 1 {
		t.Fatal("replay duplicated task", err)
	}
	old, err := store.Claim(ctx, true)
	if err != nil || old.StepKey != analysis.SubmissionStepKey {
		t.Fatal("no preparation claim", err)
	}
	if _, err = store.Claim(ctx, true); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("duplicate preparation lease", err)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_requests SET lease_until=now()-interval '1 second' WHERE task_id=$1`, r.TaskID); err != nil {
		t.Fatal(err)
	}
	next, err := store.Claim(ctx, true)
	if err != nil || next.LeaseID == old.LeaseID {
		t.Fatal("dead preparation not resumed", err)
	}
	if _, err = svc.PrepareAnalysisSubmission(ctx, old); !errors.Is(err, analysis.ErrChanged) || a.downloads != 0 {
		t.Fatal("stale holder prepared media", err)
	}
	// Model preparation completed before death, but queued marker was lost.
	task, requests := admissionFrozenTask(t, svc, p, r, 45000)
	if err = store.Create(ctx, task, requests); err != nil {
		t.Fatal(err)
	}
	state, err := restarted.PrepareAnalysisSubmission(ctx, next)
	if err != nil || state != "queued" || a.downloads != 0 {
		t.Fatal("existing task did not recover without download", state, err)
	}
	if err = store.Release(ctx, next, ""); err != nil {
		t.Fatal(err)
	}
	raw, err = restarted.WorldAnalyze(ctx, p, AnalyzeRequest{Mode: "get", TaskID: r.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	v = raw.(AnalysisTaskView)
	if v.State != "queued" || v.Progress == nil || v.Progress.Stage != "planned" || v.Learning != nil {
		t.Fatal("wrong restored progress", v)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_requests SET request=jsonb_set(request,'{budget_limit}','"99"') WHERE task_id=$1`, r.TaskID); err == nil {
		t.Fatal("immutable request changed")
	}
	if _, err = svc.db.Exec(ctx, `ALTER TABLE studio.world_analysis_requests DISABLE TRIGGER world_handoff_gate`); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Check(ctx, svc.db); err == nil {
		t.Fatal("admission writer gate omission")
	}
}
func TestAnalysisTotalBudgetConcurrentDispatchAndRecovery(t *testing.T) {
	svc, p, r, _ := admissionFixture(t)
	ctx := t.Context()
	r.BudgetLimit = "0.10"
	r.BudgetPerCall = "0.10"
	task, requests := admissionFrozenTask(t, svc, p, r, 90000)
	store := analysis.PGStore{DB: svc.db}
	if err := store.Create(ctx, task, requests); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, len(requests))
	won := make(chan string, len(requests))
	for _, f := range requests {
		wg.Add(1)
		go func(f analysis.FrozenRequest) {
			defer wg.Done()
			old, err := store.Load(ctx, p.OrganizationID, p.SubjectID, r.TaskID, f.Step.Key)
			if err != nil {
				results <- err
				return
			}
			next := old
			next.State = "dispatching"
			next.Revision++
			ok, err := store.CompareAndSwap(ctx, old, next)
			if ok {
				won <- f.Step.Key
			}
			results <- err
		}(f)
	}
	wg.Wait()
	close(results)
	close(won)
	success, blocked := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, analysis.ErrBudgetExhausted) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || blocked != len(requests)-1 {
		t.Fatal("concurrent dispatch exceeded cap", success, blocked)
	}
	winner := <-won
	checkReserved := func() {
		t.Helper()
		raw, err := store.ReservedBudget(ctx, p.OrganizationID, p.SubjectID, r.TaskID)
		v, ok := new(big.Rat).SetString(raw)
		if err != nil || !ok || v.Cmp(big.NewRat(1, 10)) != 0 {
			t.Fatal("budget counted twice or lost", raw, err)
		}
	}
	checkReserved()
	w, err := store.Claim(ctx, true)
	if err != nil || w.StepKey != winner {
		t.Fatal("accepted operation not recovered", err)
	}
	if err = store.Release(ctx, w, "budget_exhausted"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, r.TaskID); err != nil {
		t.Fatal(err)
	}
	w, err = store.Claim(ctx, false)
	if err != nil || w.StepKey != winner {
		t.Fatal("cap blocked recovery", err)
	}
	old, err := store.Load(ctx, p.OrganizationID, p.SubjectID, r.TaskID, winner)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.Revision++
	next.State = "running"
	next.CallID = newID()
	if ok, err := store.CompareAndSwap(ctx, old, next); err != nil || !ok {
		t.Fatal("inflight recovery charged again", err)
	}
	if err = store.Release(ctx, w, ""); err != nil {
		t.Fatal(err)
	}
	checkReserved()
	var code string
	if err = svc.db.QueryRow(ctx, `SELECT last_error_code FROM studio.world_analysis_schedule WHERE task_id=$1`, r.TaskID).Scan(&code); err != nil || code != "budget_exhausted" {
		t.Fatal("recovery erased budget stop", code, err)
	}
	old = next
	next.Revision++
	next.State = "failed"
	next.ErrorCode = "SYNTHETIC_FAILURE"
	if ok, err := store.CompareAndSwap(ctx, old, next); err != nil || !ok {
		t.Fatal(err)
	}
	checkReserved()
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, r.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(ctx, true); !errors.Is(err, analysis.ErrMissing) {
		t.Fatal("task spent after frozen cap", err)
	}
	before, _ := json.Marshal(task)
	task.TotalBudget = "1"
	if err = store.Create(ctx, task, requests); !errors.Is(err, analysis.ErrChanged) {
		t.Fatal("budget changed after creation", err)
	}
	after, err := store.Task(ctx, p.OrganizationID, p.SubjectID, r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(after)
	if string(before) != string(data) {
		t.Fatal("frozen task changed")
	}
}

func TestAnalysisBudgetIncludesAllFourPhases(t *testing.T) {
	svc, p, r, _ := admissionFixture(t)
	ctx := t.Context()
	r.BudgetLimit = "0.4"
	r.BudgetPerCall = "0.1"
	task, requests := admissionFrozenTask(t, svc, p, r, 45000)
	store := analysis.PGStore{DB: svc.db}
	if err := store.Create(ctx, task, requests); err != nil {
		t.Fatal(err)
	}
	// Database transition fixtures only: deliberately no model-content claim.
	// Each phase reserves 0.1; a fifth operation must not evade the total cap.
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := svc.db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE studio.world_analysis_steps SET state='dispatching',revision=2`)
	exec(`UPDATE studio.world_analysis_steps SET state='observation_ready',revision=3,call_id=$1,native_result='{}',report='{"passed":true,"issues":[]}'`, newID())
	exec(`INSERT INTO studio.world_analysis_associations(organization_id,owner_id,task_id,request_sha256,request) VALUES($1,$2,$3,repeat('a',64),'{"budget_limit":"0.1","synthetic_fixture":true}')`, p.OrganizationID, p.SubjectID, r.TaskID)
	exec(`UPDATE studio.world_analysis_associations SET state='dispatching',revision=2`)
	exec(`UPDATE studio.world_analysis_associations SET state='association_ready',revision=3,call_id=$1,native_result='{}'`, newID())
	targets, _ := json.Marshal(r.PromptTargets)
	exec(`INSERT INTO studio.world_analysis_prompt_batches(organization_id,owner_id,task_id,batch_key,batch_index,request_sha256,request) VALUES($1,$2,$3,'prompts-v1-0000',0,repeat('b',64),jsonb_build_object('task_id',$3::uuid::text,'budget_limit','0.1','batch',jsonb_build_object('batch_key','prompts-v1-0000','targets',$4::jsonb)))`, p.OrganizationID, p.SubjectID, r.TaskID, targets)
	exec(`UPDATE studio.world_analysis_prompt_batches SET state='dispatching',revision=2`)
	exec(`UPDATE studio.world_analysis_prompt_batches SET state='prompt_ready',revision=3,call_id=$1,native_result='{}'`, newID())
	for i, key := range []string{"results-v1-shot-0000", "results-v1-scene-0001"} {
		exec(`INSERT INTO studio.world_analysis_result_batches(organization_id,owner_id,task_id,batch_key,batch_index,request_sha256,request) VALUES($1,$2,$3,$4,$5,repeat('c',64),jsonb_build_object('task_id',$3::uuid::text,'budget_limit','0.1','batch',jsonb_build_object('batch_key',$4::text)))`, p.OrganizationID, p.SubjectID, r.TaskID, key, i)
	}
	exec(`UPDATE studio.world_analysis_result_batches SET state='dispatching',revision=2 WHERE batch_index=0`)
	if _, err := svc.db.Exec(ctx, `UPDATE studio.world_analysis_result_batches SET state='dispatching',revision=2 WHERE batch_index=1`); err == nil {
		t.Fatal("cap ignored previous phases")
	}
	raw, err := store.ReservedBudget(ctx, p.OrganizationID, p.SubjectID, r.TaskID)
	v, ok := new(big.Rat).SetString(raw)
	if err != nil || !ok || v.Cmp(big.NewRat(4, 10)) != 0 {
		t.Fatal("wrong total reservation", raw, err)
	}
	exec(`UPDATE studio.world_analysis_result_batches SET state='failed',revision=3,error_code='SYNTHETIC_FAILURE' WHERE batch_index=0`)
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_result_batches SET state='dispatching',revision=2 WHERE batch_index=1`); err == nil {
		t.Fatal("failed call refunded without settlement")
	}
}
