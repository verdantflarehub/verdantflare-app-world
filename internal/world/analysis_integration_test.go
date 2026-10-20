//go:build integration

package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

type observationChannel struct {
	submits, lookups, polls int
	call                    analysis.Call
	err                     error
}

func (c *observationChannel) Submit(context.Context, string, analysis.FrozenRequest, []byte) (analysis.Call, error) {
	c.submits++
	return c.call, c.err
}
func (c *observationChannel) Lookup(context.Context, string, string) (analysis.Call, error) {
	c.lookups++
	return c.call, nil
}
func (c *observationChannel) Poll(context.Context, string, string) (analysis.Call, error) {
	c.polls++
	return c.call, nil
}
func (c *observationChannel) Result(context.Context, string, string) ([]byte, error) {
	return nil, analysis.ErrUnavailable
}

// Uses a disposable real PostgreSQL database; channel responses are synthetic.
// This proves durability/ownership, not analysis content or provider availability.
func TestAnalysisPersistenceRestartIsolationAndConcurrentClaim(t *testing.T) {
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
	p := Principal{testID(30), testID(31), testID(32)}
	subjects := []string{"reference"}
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "Synthetic analysis source", Purpose: "collection", Subjects: &subjects})
	if err != nil {
		t.Fatal(err)
	}
	initial := raw.(DraftRevision)
	media := []byte("synthetic-video-input")
	h := sha256.Sum256(media)
	hash := hex.EncodeToString(h[:])
	ref := ContentRef{testID(3), newID(), newID()}
	fileID := newID()
	content.versions[ref] = ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_import", WorldDraftID: initial.DraftID}, MIME: "video/mp4"}
	content.bytes[ref] = media
	manifest := initial.Manifest
	manifest.Files = []File{{ID: fileID, Path: "reference.mp4", Role: "reference", Content: ref}}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: initial.DraftID, ExpectedRevisionID: initial.RevisionID, CommitID: newID(), Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	saved := raw.(DraftRevision)
	plan, _ := analysis.PlanVideo(analysis.Source{SHA256: hash, DurationMS: 45000})
	task := analysis.Task{OrganizationID: p.OrganizationID, OwnerID: p.SubjectID, TaskID: newID(), DraftID: saved.DraftID, RevisionID: saved.RevisionID, FileID: fileID, Plan: plan}
	f, err := analysis.Freeze(task.TaskID, "gemini-3.1-pro-preview", "", "1.00", plan.Steps[0], analysis.Prepared{SourceHash: hash, MediaHash: hash, MIME: "video/mp4", Range: plan.Steps[0].Input}, int64(len(media)))
	if err != nil {
		t.Fatal(err)
	}
	store := analysis.PGStore{DB: db}
	for range 2 {
		if err = store.Create(ctx, task, []analysis.FrozenRequest{f}); err != nil {
			t.Fatal("create/reentry", err)
		}
	}
	r, err := store.Load(ctx, p.OrganizationID, p.SubjectID, task.TaskID, f.Step.Key)
	if err != nil {
		t.Fatal("jsonb roundtrip", err)
	}
	bad := f
	bad.BudgetLimit = "2.00"
	if err = store.Create(ctx, task, []analysis.FrozenRequest{bad}); !errors.Is(err, analysis.ErrChanged) {
		t.Fatal("replaced fixed request", err)
	}
	for _, identity := range [][2]string{{newID(), p.SubjectID}, {p.OrganizationID, newID()}} {
		if _, err = store.Load(ctx, identity[0], identity[1], task.TaskID, f.Step.Key); !errors.Is(err, analysis.ErrMissing) {
			t.Fatal("leaked owner state", err)
		}
	}
	// Competing workers use the same snapshot. Exactly one can reserve POST.
	next := r
	next.State = "dispatching"
	next.Revision++
	var wg sync.WaitGroup
	wins := make(chan bool, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); ok, e := store.CompareAndSwap(ctx, r, next); wins <- ok; failures <- e }()
	}
	wg.Wait()
	close(wins)
	close(failures)
	count := 0
	for ok := range wins {
		if ok {
			count++
		}
	}
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	if count != 1 {
		t.Fatal("duplicate claimant", count)
	}
	// Restart after intent but before receiving response. Only original lookup.
	yes := true
	channel := &observationChannel{call: analysis.Call{SchemaVersion: 1, Binding: analysis.Binding{CallID: newID(), TaskID: task.TaskID, ModelID: f.ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}}
	worker := analysis.Executor{Store: analysis.PGStore{DB: db}, Channel: channel, Media: func(context.Context, analysis.StepRecord) ([]byte, error) {
		t.Fatal("restart attempted media/submit")
		return nil, nil
	}}
	r, err = worker.Advance(ctx, p.OrganizationID, p.SubjectID, task.TaskID, f.Step.Key)
	if err != nil || r.State != "running" || channel.submits != 0 || channel.lookups != 1 {
		t.Fatal("restart did not bind original call", r.State, err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_analysis_steps SET call_id=$1,revision=revision+1 WHERE task_id=$2`, newID(), task.TaskID); err == nil {
		t.Fatal("call rebind bypassed guard")
	}
	channel.call.State = "failed"
	r, err = worker.Advance(ctx, p.OrganizationID, p.SubjectID, task.TaskID, f.Step.Key)
	if err != nil || r.State != "failed" || channel.polls != 1 {
		t.Fatal("terminal call not preserved", err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_analysis_steps SET revision=revision+1 WHERE task_id=$1`, task.TaskID); err == nil {
		t.Fatal("terminal record mutable")
	}
	if err = migrations.Check(ctx, db); err != nil {
		t.Fatal(err)
	}
	// Migration idempotence leaves source revisions and task records intact.
	if err = migrations.UpgradeGates(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "get", DraftID: saved.DraftID, RevisionID: saved.RevisionID}); err != nil {
		t.Fatal("source changed", err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_handoff SET state='closed'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO studio.world_analysis_tasks(organization_id,owner_id,task_id,draft_id,revision_id,file_id,request_sha256,plan) SELECT organization_id,owner_id,$1,draft_id,revision_id,file_id,request_sha256,plan FROM studio.world_analysis_tasks LIMIT 1`, newID()); err == nil {
		t.Fatal("analysis bypassed writer gate")
	}
}
