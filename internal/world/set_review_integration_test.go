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

type reviewIntegrationArtifact struct{ integrationArtifact }

func (a *reviewIntegrationArtifact) WriteAssetManifest(ctx context.Context, p Principal, asset, version string, w TextWrite) (ContentVersion, error) {
	v, e := a.integrationArtifact.WriteAssetManifest(ctx, p, asset, version, w)
	if e == nil {
		v.Size = int64(len(a.bytes[v.ContentRef]))
		v.SHA256 = reviewDigest(a.bytes[v.ContentRef])
		a.versions[v.ContentRef] = v
	}
	return v, e
}

// This test requires the explicit remote development test DB. Its synthetic
// bytes exercise transaction/authentication semantics, not visual acceptance.
func TestSetReviewAtomicCASReplayRevocationAndVersionIsolation(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	if e := migrations.ApplyFresh(ctx, db, 1, 1); e != nil {
		t.Fatal(e)
	}
	if e := migrations.ActivateFresh(ctx, db); e != nil {
		t.Fatal(e)
	}
	p := reviewTestPrincipal()
	source := sourceFixture()
	source.Manifest.Files[0].Role = "render"
	content := &reviewIntegrationArtifact{integrationArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}}}
	for _, f := range source.Manifest.Files {
		b := []byte("synthetic fixed published image protocol fixture")
		content.bytes[f.Content] = b
		content.versions[f.Content] = ContentVersion{ContentRef: f.Content, OrganizationID: p.OrganizationID, SHA256: reviewDigest(b), Size: int64(len(b)), MIME: "image/png", Source: Source{Kind: "user_edit", ProjectID: source.ProjectID}}
	}
	svc, e := NewService(ctx, db, content, integrationProject{source}, 1, map[string]coordination.Peer{"project": &integrationPeer{}})
	if e != nil {
		t.Fatal(e)
	}
	publish := registrationFixture()
	publish.AssetType = "scene"
	asset, e := svc.WorldRegister(ctx, p, publish)
	if e != nil {
		t.Fatal(e)
	}
	locator := ReviewLocator{"world", asset.AssetID, asset.VersionID}
	opened, e := svc.ReviewOpen(ctx, p, ReviewOpenRequest{Target: locator})
	if e != nil || opened.TargetHash == "" {
		t.Fatal("open", e)
	}
	request := ReviewAppendRequest{Target: locator, ExpectedHash: opened.TargetHash, CommitID: newID(), Action: "comment", Body: "Protocol fixture only", Scope: ReviewScope{Kind: "evidence", EvidenceIDs: []string{source.Manifest.Files[0].ID}, CameraIDs: []string{}, RegionIDs: []string{}, Claims: []string{"image_reading"}}}
	first, e := svc.ReviewAppend(ctx, p, request)
	if e != nil {
		t.Fatal("append", e)
	}
	retry, e := svc.ReviewAppend(ctx, p, request)
	if e != nil || retry.RecordID != first.RecordID {
		t.Fatal("retry created another record", e)
	}
	changed := request
	changed.Body = "Different intent"
	if _, e = svc.ReviewAppend(ctx, p, changed); !errors.Is(e, ErrIdempotency) {
		t.Fatal("changed retry accepted", e)
	}
	stale := request
	stale.CommitID = newID()
	if _, e = svc.ReviewAppend(ctx, p, stale); !errors.Is(e, ErrReviewConflict) {
		t.Fatal("stale CAS accepted", e)
	}
	status, e := svc.ReviewStatus(ctx, p, ReviewStatusRequest{asset.AssetID, stale.CommitID})
	if e != nil || status.State != "rejected" {
		t.Fatal("conflict not recoverable", e)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			r := request
			r.CommitID = newID()
			r.ExpectedRecordID = &first.RecordID
			<-start
			_, err := svc.ReviewAppend(ctx, p, r)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrReviewConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("CAS fork", success, conflict)
	}
	for _, sql := range []string{`UPDATE studio.world_set_review_records SET record_bytes='x'::bytea`, `DELETE FROM studio.world_set_review_records`, `UPDATE studio.world_set_review_requests SET request_sha256=repeat('b',64)`, `UPDATE studio.world_set_review_heads SET target_sha256=repeat('b',64)`} {
		if _, e = db.Exec(ctx, sql); e == nil {
			t.Fatal("immutable record changed")
		}
	}
	guest := p
	guest.SubjectID = newID()
	if _, e = svc.WorldGrant(ctx, p, WorldGrantRequest{asset.AssetID, guest.SubjectID, "editor"}); e != nil {
		t.Fatal(e)
	}
	current, e := svc.ReviewOpen(ctx, guest, ReviewOpenRequest{Target: locator})
	if e != nil {
		t.Fatal(e)
	}
	g := request
	g.CommitID = newID()
	g.ExpectedRecordID = current.Head
	gr, e := svc.ReviewAppend(ctx, guest, g)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.WorldRevoke(ctx, p, WorldRevokeRequest{asset.AssetID, guest.SubjectID}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.ReviewAppend(ctx, guest, g); !errors.Is(e, ErrForbidden) {
		t.Fatal("revoked retry returned cached result", e)
	}
	if _, e = svc.ReviewStatus(ctx, guest, ReviewStatusRequest{asset.AssetID, gr.CommitID}); !errors.Is(e, ErrForbidden) {
		t.Fatal("revoked status returned cached result", e)
	}
	publish.AssetID = asset.AssetID
	publish.ExpectedVersionID = asset.VersionID
	publish.CommitID = newID()
	next, e := svc.WorldRegister(ctx, p, publish)
	if e != nil {
		t.Fatal(e)
	}
	newer, e := svc.ReviewOpen(ctx, p, ReviewOpenRequest{Target: ReviewLocator{"world", next.AssetID, next.VersionID}})
	if e != nil || newer.State != "unreviewed" || newer.TargetHash == opened.TargetHash || newer.Head != nil {
		t.Fatal("new version inherited review", e)
	}
	old, e := svc.ReviewOpen(ctx, p, ReviewOpenRequest{Target: locator, ExpectedHash: opened.TargetHash})
	if e != nil || old.TargetHash != opened.TargetHash || len(old.Records) != 3 {
		t.Fatal("old deep link changed", e)
	}
}
