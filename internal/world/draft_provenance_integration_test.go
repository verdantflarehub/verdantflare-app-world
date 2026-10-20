//go:build integration

package world

import (
	"context"
	"errors"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

// Uses actual World SQL/authorization with explicit Project and Artifact
// adapters. It does not run a producer engine or a separate Artifact service.
type provenanceArtifact struct {
	*draftTestArtifact
	project *provenanceProject
}

func (a *provenanceArtifact) Metadata(ctx context.Context, p Principal, ref ContentRef, access Access) (ContentVersion, error) {
	if access.ProjectID == "" {
		return a.draftTestArtifact.Metadata(ctx, p, ref, access)
	}
	v, ok := a.versions[ref]
	if !ok || v.OrganizationID != p.OrganizationID {
		return ContentVersion{}, ErrForbidden
	}
	return v, a.project.Authorize(ctx, p, Permission{Action: "read", Version: &v, Access: &access})
}

func TestDraftNativeOriginPersistsWithoutProjectBinding(t *testing.T) {
	ctx := t.Context()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	p := Principal{testID(30), testID(31), testID(32)}
	file := File{ID: testID(4), Path: "native-output.txt", Role: "reference", Content: ContentRef{testID(1), testID(2), testID(3)}}
	project := &provenanceProject{source: OpenResult{Result: Result{ProjectID: testID(40), RevisionID: testID(41), Manifest: Manifest{Files: []File{file}}}}}
	run := RunRef{ServiceID: "speech", RunID: "producer-runtime/opaque:1"}
	origin := Source{Kind: "task_output", ServiceID: run.ServiceID, RunID: run.RunID, TaskID: testID(42), ProjectID: project.source.ProjectID}
	content := &provenanceArtifact{draftTestArtifact: &draftTestArtifact{
		versions: map[ContentRef]ContentVersion{file.Content: {ContentRef: file.Content, OrganizationID: p.OrganizationID, Source: origin}},
		bytes:    map[ContentRef][]byte{file.Content: []byte("synthetic archived output; not a real media generation")}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{},
	}, project: project}
	newService := func() *Service {
		svc, err := NewService(ctx, db, content, project, 1, map[string]coordination.Peer{"project": &integrationPeer{}})
		if err != nil {
			t.Fatal(err)
		}
		content.svc = svc
		return svc
	}
	svc := newService()
	create := func() DraftRevision {
		subjects := []string{}
		v, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "Independent creative material", Purpose: "creation", Subjects: &subjects})
		if err != nil {
			t.Fatal(err)
		}
		return v.(DraftRevision)
	}
	created := create()
	m := created.Manifest
	m.Files = []File{file}
	m.RunRefs = []RunRef{run}
	m.SourceRefs = []DraftSourceRef{{Kind: "project", ProjectID: project.source.ProjectID, RevisionID: project.source.RevisionID, FileID: file.ID, Purpose: "reference"}}
	commit := DraftRequest{Mode: "commit", CommitID: newID(), DraftID: created.DraftID, ExpectedRevisionID: created.RevisionID, Manifest: &m}
	content.failRetain = true
	if _, err := svc.WorldDraft(ctx, p, commit); !errors.Is(err, ErrDependency) {
		t.Fatal("failed retain did not remain recoverable", err)
	}
	svc = newService()
	v, err := svc.WorldDraft(ctx, p, commit)
	if err != nil {
		t.Fatal("native origin failed after restart", err)
	}
	saved := v.(DraftRevision)
	if len(saved.Manifest.RunRefs) != 1 || saved.Manifest.RunRefs[0] != run || saved.Manifest.Purpose != "creation" {
		t.Fatal("original run lost or generation context replaced")
	}
	for _, wrong := range []RunRef{{ServiceID: run.ServiceID, RunID: origin.TaskID}, {ServiceID: "video", RunID: run.RunID}} {
		bad := m
		bad.RunRefs = []RunRef{wrong}
		request := DraftRequest{Mode: "commit", CommitID: newID(), DraftID: saved.DraftID, ExpectedRevisionID: saved.RevisionID, Manifest: &bad}
		if _, err = svc.WorldDraft(ctx, p, request); !errors.Is(err, ErrNotReady) {
			t.Fatal("wrong task identity accepted", err)
		}
		status, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "status", CommitID: request.CommitID})
		if err != nil || status.(DraftStatus).State != "failed" || status.(DraftStatus).ErrorCode != ErrNotReady.Error() {
			t.Fatal("unattested origin not recorded as a terminal error", err)
		}
	}
	head, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "get", DraftID: saved.DraftID})
	if err != nil || head.(DraftRevision).RevisionID != saved.RevisionID {
		t.Fatal("invalid provenance moved head", err)
	}
	// Retained bytes and source are independent of the original project grant.
	project.deny = true
	access := Access{WorldDraftID: saved.DraftID, WorldDraftRevisionID: saved.RevisionID}
	metadata, err := content.Metadata(ctx, p, file.Content, access)
	if err != nil || metadata.Source != origin {
		t.Fatal("fixed output lost original provenance", err)
	}
	// A new revision may use its retained file without keeping the old source
	// project in its authorization graph. No hidden project is created.
	m.SourceRefs = []DraftSourceRef{}
	commit = DraftRequest{Mode: "commit", CommitID: newID(), DraftID: saved.DraftID, ExpectedRevisionID: saved.RevisionID, Manifest: &m}
	v, err = svc.WorldDraft(ctx, p, commit)
	if err != nil {
		t.Fatal("retained creative output still bound to source project", err)
	}
	saved = v.(DraftRevision)
	asset, err := svc.WorldRegister(ctx, p, WorldRegisterRequest{CommitID: newID(), SourceDraftID: saved.DraftID, SourceRevisionID: saved.RevisionID, FileIDs: []string{file.ID}, Name: "Archived creation", AssetType: "file", Subjects: []string{}, Relation: "curated_in"})
	if err != nil {
		t.Fatal("native origin prevented independent asset publication", err)
	}
	second := create()
	m2 := second.Manifest
	m2.Files = []File{file}
	m2.RunRefs = []RunRef{run}
	m2.SourceRefs = []DraftSourceRef{{Kind: "asset", AssetID: asset.AssetID, AssetVersionID: asset.VersionID, FileID: file.ID, Purpose: "reference"}}
	if _, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", CommitID: newID(), DraftID: second.DraftID, ExpectedRevisionID: second.RevisionID, Manifest: &m2}); err != nil {
		t.Fatal("asset could not supply native origin without source project", err)
	}
}
