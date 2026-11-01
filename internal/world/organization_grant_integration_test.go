//go:build integration

package world

import (
	"context"
	"errors"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
	"strings"
	"testing"
)

func TestWorldOrganizationReadAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	source := sourceFixture()
	content := &integrationArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}}
	for _, file := range source.Manifest.Files {
		content.versions[file.Content] = ContentVersion{ContentRef: file.Content, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_edit", ProjectID: source.ProjectID}, MIME: "text/markdown"}
	}
	peer := &integrationPeer{}
	svc, err := NewService(ctx, db, content, integrationProject{source}, 1, map[string]coordination.Peer{"project": peer})
	if err != nil {
		t.Fatal(err)
	}
	request := registrationFixture()
	published, err := svc.WorldRegister(ctx, p, request)
	if err != nil {
		t.Fatal(err)
	}

	guest := Principal{OrganizationID: p.OrganizationID, SubjectID: testID(70), RequestID: testID(71)}
	foreign := guest
	foreign.OrganizationID = testID(72)
	get := WorldGetRequest{AssetID: published.AssetID, VersionID: published.VersionID}
	check := func(who Principal, n int) {
		t.Helper()
		r, e := svc.WorldList(ctx, who, WorldListRequest{Facets: []string{"system"}})
		if e != nil || len(r.Items) != n || r.Total == nil || *r.Total != n {
			t.Fatalf("list count want %d: %+v %v", n, r, e)
		}
	}
	check(guest, 0)
	if _, e := svc.WorldGet(ctx, guest, get); !errors.Is(e, ErrForbidden) {
		t.Fatal("ungranted read", e)
	}
	grant := WorldGrantRequest{Scope: "organization", AssetID: published.AssetID, Role: "reader"}
	// Publication has committed, but its participant reservation is still held
	// until outbox delivery. Organization changes conservatively wait for it.
	if _, e := svc.WorldGrant(ctx, p, grant); !errors.Is(e, coordination.ErrPending) {
		t.Fatal("initial grant skipped publication reservation", e)
	}
	if e := svc.coord.Deliver(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.WorldGrant(ctx, p, grant); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.WorldGrant(ctx, p, grant); e != nil {
		t.Fatal("idempotent grant", e)
	}
	check(guest, 1)
	check(p, 1)
	check(foreign, 0)
	if _, e := svc.WorldGet(ctx, guest, get); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.WorldGet(ctx, foreign, get); !errors.Is(e, ErrForbidden) {
		t.Fatal("foreign read", e)
	}
	ref := AssetRef{AssetID: published.AssetID, VersionID: published.VersionID, Purpose: "reference"}
	if _, e := svc.ReferencesWithGrants(ctx, guest, ref); e != nil {
		t.Fatal("project reference", e)
	}
	access := Access{AssetID: published.AssetID, AssetVersionID: published.VersionID}
	file := source.Manifest.Files[0].Content
	if e := svc.authorizeAssetRead(ctx, guest, ContentVersion{ContentRef: file, OrganizationID: p.OrganizationID}, access); e != nil {
		t.Fatal("file read", e)
	}
	if e := svc.authorizeAssetRead(ctx, guest, ContentVersion{ContentRef: source.ManifestRef, OrganizationID: p.OrganizationID}, access); e == nil {
		t.Fatal("source manifest leaked")
	}
	if e := assetWritable(ctx, db, guest, published.AssetID); !errors.Is(e, ErrForbidden) {
		t.Fatal("reader writes", e)
	}
	if _, e := svc.WorldGrant(ctx, guest, grant); !errors.Is(e, ErrForbidden) {
		t.Fatal("reader regrants", e)
	}
	revoke := WorldRevokeRequest{Scope: "organization", AssetID: published.AssetID}
	if _, e := svc.WorldRevoke(ctx, guest, revoke); !errors.Is(e, ErrForbidden) {
		t.Fatal("reader revokes", e)
	}
	op := coordination.Operation{Coordinator: "project", OrganizationID: p.OrganizationID, SubjectID: guest.SubjectID, ResourceID: testID(73), CommitID: testID(74), RequestHash: strings.Repeat("b", 64), Fence: 1}
	if e := svc.coord.Store.Prepare(ctx, op, []coordination.Grant{{ResourceID: published.AssetID}}); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.WorldRevoke(ctx, p, revoke); !errors.Is(e, coordination.ErrPending) {
		t.Fatal("organization revoke skipped reservation", e)
	}
	next := op
	next.SubjectID = testID(75)
	next.CommitID = testID(76)
	if e := svc.coord.Store.Prepare(ctx, next, []coordination.Grant{{ResourceID: published.AssetID}}); !errors.Is(e, coordination.ErrDenied) {
		t.Fatal("pending barrier allowed new subject", e)
	}
	peer.proof = coordination.Status{Operation: op, State: "cancelled"}
	if e := svc.coord.Finalize(ctx, op); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.WorldRevoke(ctx, p, revoke); e != nil {
		t.Fatal(e)
	}
	check(guest, 0)
	if _, e := svc.WorldGet(ctx, guest, get); !errors.Is(e, ErrForbidden) {
		t.Fatal("revoked read", e)
	}
	if _, e := svc.ReferencesWithGrants(ctx, guest, ref); !errors.Is(e, ErrForbidden) {
		t.Fatal("revoked reference", e)
	}
	if e := svc.authorizeAssetRead(ctx, guest, ContentVersion{ContentRef: file, OrganizationID: p.OrganizationID}, access); !errors.Is(e, ErrForbidden) {
		t.Fatal("revoked download", e)
	}
	if _, e := svc.WorldGrant(ctx, p, WorldGrantRequest{AssetID: published.AssetID, SubjectID: guest.SubjectID, Role: "editor"}); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.WorldGrant(ctx, p, grant); e != nil {
		t.Fatal(e)
	}
	check(guest, 1)
	if role, e := assetRole(ctx, db, guest, published.AssetID); e != nil || role != "editor" {
		t.Fatal("personal role lost", role, e)
	}
	if _, e := svc.WorldRevoke(ctx, p, revoke); e != nil {
		t.Fatal(e)
	}
	check(guest, 1)
}
