//go:build integration

package world

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

func TestCollectionLifecycleRecoveryIsolationAndCAS(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	a := &draftTestArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{}}
	svc, err := NewService(ctx, db, a, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	a.svc = svc
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	subjects := []string{}
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "回收站验证", Purpose: "collection", Subjects: &subjects})
	if err != nil {
		t.Fatal(err)
	}
	original := raw.(DraftRevision)
	list := func(state string) DraftList {
		t.Helper()
		v, e := svc.WorldDraft(ctx, p, DraftRequest{Mode: "list", Purpose: "collection", LifecycleState: state})
		if e != nil {
			t.Fatal(e)
		}
		return v.(DraftList)
	}
	if v := list(""); len(v.Items) != 1 || v.Items[0].Lifecycle.Version != 1 {
		t.Fatal(v)
	}
	version := int64(1)
	request := DraftRequest{Mode: "trash", DraftID: original.DraftID, OperationID: newID(), ExpectedLifecycleVersion: &version}
	guest := p
	guest.SubjectID = testID(50)
	if _, err = svc.WorldDraft(ctx, guest, request); !errors.Is(err, ErrForbidden) {
		t.Fatal("other user changed collection", err)
	}
	guest = p
	guest.OrganizationID = testID(51)
	if _, err = svc.WorldDraft(ctx, guest, request); !errors.Is(err, ErrForbidden) {
		t.Fatal("other organization changed collection", err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := svc.WorldDraft(ctx, p, request); outcomes <- e }()
	}
	wg.Wait()
	close(outcomes)
	for e := range outcomes {
		if e != nil {
			t.Fatal("duplicate request failed", e)
		}
	}
	if len(list("").Items) != 0 {
		t.Fatal("trash leaked into default directory")
	}
	trashed := list("trashed")
	if len(trashed.Items) != 1 || trashed.Items[0].Lifecycle.Version != 2 || !trashed.Items[0].Lifecycle.CanRestore {
		t.Fatal(trashed)
	}
	// Fixed source remains authorized and byte-identical after trashing.
	got, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "get", DraftID: original.DraftID, RevisionID: original.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	fixed := got.(DraftRevision)
	if fixed.ManifestRef != original.ManifestRef || fixed.HeadRevisionID != original.HeadRevisionID || fixed.Lifecycle.State != "trashed" {
		t.Fatal("fixed source changed")
	}
	stale := request
	stale.OperationID = newID()
	if _, err = svc.WorldDraft(ctx, p, stale); !errors.Is(err, ErrConflict) {
		t.Fatal("stale change succeeded", err)
	}
	v2 := int64(2)
	restore := DraftRequest{Mode: "restore", DraftID: original.DraftID, OperationID: newID(), ExpectedLifecycleVersion: &v2}
	if _, err = svc.WorldDraft(ctx, p, restore); err != nil {
		t.Fatal(err)
	}
	got, err = svc.WorldDraft(ctx, p, request)
	if err != nil || got.(DraftLifecycleResult).Lifecycle.Version != 2 {
		t.Fatal("old receipt was not replayed", err)
	}
	active := list("")
	if len(active.Items) != 1 || active.Items[0].Lifecycle.Version != 3 || active.Items[0].Lifecycle.TrashedAt != nil {
		t.Fatal("replay trashed restored collection", active)
	}
	reused := restore
	reused.OperationID = request.OperationID
	if _, err = svc.WorldDraft(ctx, p, reused); !errors.Is(err, ErrIdempotency) {
		t.Fatal("reused key accepted", err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM studio.world_collection_operations`).Scan(&count); err != nil || count != 2 {
		t.Fatal("duplicate mutation receipts", count, err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_collection_operations SET result='{}'`); err == nil {
		t.Fatal("mutable operation receipt")
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_drafts SET lifecycle_state='trashed',trashed_at=now(),trashed_by=NULL`); err == nil {
		t.Fatal("trash metadata without an actor accepted")
	}
}

func TestCollectionTrashBlocksNewAnalysisButPreservesRecovery(t *testing.T) {
	svc, p, r, a := admissionFixture(t)
	ctx := t.Context()
	if _, err := svc.WorldAnalyze(ctx, p, r); err != nil {
		t.Fatal(err)
	}
	v1 := int64(1)
	trash := DraftRequest{Mode: "trash", DraftID: r.Source.DraftID, OperationID: newID(), ExpectedLifecycleVersion: &v1}
	if _, err := svc.WorldDraft(ctx, p, trash); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WorldAnalyze(ctx, p, r); err != nil {
		t.Fatal("accepted analysis cannot replay", err)
	}
	if _, _, err := svc.analysisSource(ctx, p, *r.Source); err != nil {
		t.Fatal("worker lost fixed original", err)
	}
	next := r
	next.TaskID = newID()
	if _, err := svc.WorldAnalyze(ctx, p, next); !errors.Is(err, ErrConflict) {
		t.Fatal("new analysis admitted from trash", err)
	}
	v2 := int64(2)
	if _, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "restore", DraftID: r.Source.DraftID, OperationID: newID(), ExpectedLifecycleVersion: &v2}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WorldAnalyze(ctx, p, next); err != nil {
		t.Fatal("restored source unavailable", err)
	}
	if a.downloads != 0 {
		t.Fatal("admission unexpectedly downloaded media")
	}
}
