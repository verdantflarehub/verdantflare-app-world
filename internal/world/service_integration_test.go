//go:build integration

package world

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

type integrationProject struct{ source OpenResult }

func (p integrationProject) Open(context.Context, Principal, string, string) (OpenResult, error) {
	return p.source, nil
}
func (p integrationProject) Authorize(context.Context, Principal, Permission) error { return nil }

type integrationArtifact struct {
	versions map[ContentRef]ContentVersion
	bytes    map[ContentRef][]byte
}

func (a *integrationArtifact) Write(context.Context, Principal, string, TextWrite) (ContentVersion, error) {
	return ContentVersion{}, ErrForbidden
}
func (a *integrationArtifact) WriteAssetManifest(_ context.Context, p Principal, asset, version string, w TextWrite) (ContentVersion, error) {
	ref := ContentRef{testID(3), newID(), newID()}
	v := ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "asset_manifest", AssetID: asset, AssetVersionID: version}, MIME: "application/json"}
	a.versions[ref] = v
	a.bytes[ref] = []byte(w.Text)
	return v, nil
}
func (a *integrationArtifact) Metadata(_ context.Context, _ Principal, ref ContentRef, _ Access) (ContentVersion, error) {
	v, ok := a.versions[ref]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (a *integrationArtifact) Read(_ context.Context, _ Principal, ref ContentRef, _ Access, _ int64) ([]byte, error) {
	data, ok := a.bytes[ref]
	if !ok {
		return nil, ErrNotFound
	}
	return data, nil
}
func (a *integrationArtifact) Retain(context.Context, Principal, RetentionOwner, []ContentRef) error {
	return nil
}

type integrationPeer struct{ proof coordination.Status }

func (p *integrationPeer) Prepare(context.Context, coordination.Operation, []coordination.Grant) error {
	return nil
}
func (p *integrationPeer) Finalize(context.Context, coordination.Operation) error { return nil }
func (p *integrationPeer) State(context.Context, coordination.Operation) (coordination.Status, error) {
	if p.proof.State == "" {
		return coordination.Status{}, coordination.ErrUnknown
	}
	return p.proof, nil
}

// Run only against an explicitly supplied 5090 development PostgreSQL endpoint.
// This exercises World SQL and durable barriers; separate-service/network crash
// tests remain required before a live data handoff.
func TestWorldIndependentPublicationAndPendingRevocation(t *testing.T) {
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
	retry, err := svc.WorldRegister(ctx, p, request)
	if err != nil || retry.VersionID != published.VersionID || retry.ManifestRef != published.ManifestRef {
		t.Fatal("retry republished", err)
	}
	if _, err = svc.WorldGet(ctx, p, WorldGetRequest{AssetID: published.AssetID, VersionID: published.VersionID}); err != nil {
		t.Fatal("published manifest unavailable", err)
	}
	var recorded []byte
	if err = db.QueryRow(ctx, "SELECT source_manifest_ref FROM studio.world_asset_versions WHERE version_id=$1", published.VersionID).Scan(&recorded); err != nil || !strings.Contains(string(recorded), source.ManifestRef.VersionID) {
		t.Fatal("fixed source proof missing", err)
	}
	guest := testID(60)
	if _, err = svc.WorldGrant(ctx, p, WorldGrantRequest{published.AssetID, guest, "editor"}); err != nil {
		t.Fatal(err)
	}
	op := coordination.Operation{Coordinator: "project", OrganizationID: p.OrganizationID, SubjectID: guest, ResourceID: testID(61), CommitID: testID(62), RequestHash: strings.Repeat("a", 64), Fence: 1}
	if err = svc.coord.Store.Prepare(ctx, op, []coordination.Grant{{ResourceID: published.AssetID, Write: false}}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.WorldRevoke(ctx, p, WorldRevokeRequest{published.AssetID, guest}); !errors.Is(err, coordination.ErrPending) {
		t.Fatal("revoke skipped active reservation", err)
	}
	var role string
	if err = db.QueryRow(ctx, "SELECT role FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3", p.OrganizationID, published.AssetID, guest).Scan(&role); err != nil || role != "editor" {
		t.Fatal("pending reported or applied revocation", err)
	}
	newOperation := op
	newOperation.CommitID = testID(63)
	if err = svc.coord.Store.Prepare(ctx, newOperation, []coordination.Grant{{ResourceID: published.AssetID}}); !errors.Is(err, coordination.ErrDenied) {
		t.Fatal("barrier allowed new prepare", err)
	}
	if err = svc.coord.Finalize(ctx, op); !errors.Is(err, coordination.ErrUnknown) {
		t.Fatal("unknown coordinator released reservation", err)
	}
	peer.proof = coordination.Status{Operation: op, State: "cancelled"}
	if err = svc.coord.Finalize(ctx, op); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.WorldRevoke(ctx, p, WorldRevokeRequest{published.AssetID, guest}); err != nil {
		t.Fatal("authorized retry could not finish", err)
	}
	if _, err = assetRole(ctx, db, Principal{OrganizationID: p.OrganizationID, SubjectID: guest, RequestID: p.RequestID}, published.AssetID); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked grant retained", err)
	}
	if err = svc.coord.Store.Prepare(ctx, op, []coordination.Grant{{ResourceID: published.AssetID}}); err == nil {
		t.Fatal("cancelled operation resurrected")
	}
}
