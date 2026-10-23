//go:build integration

package world

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

// Exercises durable PostgreSQL state and actual World authority callbacks. This
// stand-in does not replace separate-service Artifact/network verification.
type draftTestArtifact struct {
	mu         sync.Mutex
	svc        *Service
	versions   map[ContentRef]ContentVersion
	bytes      map[ContentRef][]byte
	writes     map[string]ContentRef
	retained   map[string][]ContentRef
	failRetain bool
}

func (a *draftTestArtifact) Write(context.Context, Principal, string, TextWrite) (ContentVersion, error) {
	return ContentVersion{}, ErrForbidden
}
func (a *draftTestArtifact) WriteAssetManifest(ctx context.Context, p Principal, id, rev string, w TextWrite) (ContentVersion, error) {
	return a.writeManifest(ctx, p, Source{Kind: "asset_manifest", AssetID: id, AssetVersionID: rev}, w)
}
func (a *draftTestArtifact) WriteDraftManifest(ctx context.Context, p Principal, id, rev string, w TextWrite) (ContentVersion, error) {
	return a.writeManifest(ctx, p, Source{Kind: "world_draft_manifest", WorldDraftID: id, WorldDraftRevisionID: rev}, w)
}
func (a *draftTestArtifact) writeManifest(ctx context.Context, p Principal, source Source, w TextWrite) (ContentVersion, error) {
	if err := a.svc.AuthorizeArtifact(ctx, p, Permission{Action: "write", Source: &source}); err != nil {
		return ContentVersion{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ref, ok := a.writes[w.WriteID]; ok {
		return a.versions[ref], nil
	}
	ref := ContentRef{testID(3), newID(), newID()}
	v := ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: source, MIME: w.MIME}
	a.versions[ref] = v
	a.bytes[ref] = []byte(w.Text)
	a.writes[w.WriteID] = ref
	return v, nil
}
func (a *draftTestArtifact) Metadata(ctx context.Context, p Principal, ref ContentRef, access Access) (ContentVersion, error) {
	a.mu.Lock()
	v, ok := a.versions[ref]
	a.mu.Unlock()
	if !ok {
		return v, ErrNotFound
	}
	return v, a.svc.AuthorizeArtifact(ctx, p, Permission{Action: "read", Version: &v, Access: &access})
}
func (a *draftTestArtifact) Read(ctx context.Context, p Principal, ref ContentRef, access Access, _ int64) ([]byte, error) {
	if _, err := a.Metadata(ctx, p, ref, access); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte{}, a.bytes[ref]...), nil
}
func (a *draftTestArtifact) Retain(ctx context.Context, p Principal, o RetentionOwner, refs []ContentRef) error {
	if err := a.svc.AuthorizeArtifact(ctx, p, Permission{Action: "retain", Owner: &o}); err != nil {
		return err
	}
	for _, ref := range refs {
		a.mu.Lock()
		v, ok := a.versions[ref]
		a.mu.Unlock()
		if !ok {
			return ErrNotFound
		}
		if err := a.svc.AuthorizeArtifact(ctx, p, Permission{Action: "retain_content", Owner: &o, Version: &v}); err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failRetain {
		a.failRetain = false
		return ErrDependency
	}
	a.retained[o.ID] = append([]ContentRef{}, refs...)
	return nil
}
func TestDraftPersistenceRecoveryIsolationAndCAS(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	content := &draftTestArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{}, failRetain: true}
	svc, err := NewService(ctx, db, content, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	content.svc = svc
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	subjects := []string{"reference"}
	req := DraftRequest{Mode: "create", CommitID: testID(72), Name: "仅收录", Purpose: "collection", Subjects: &subjects}
	if _, err = svc.WorldDraft(ctx, p, req); !errors.Is(err, ErrDependency) {
		t.Fatal("retention outage did not suspend", err)
	}
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "status", CommitID: req.CommitID})
	if err != nil || raw.(DraftStatus).State != "preparing" {
		t.Fatal("lost create cannot be found without draft id", err)
	}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "list"})
	if err != nil || len(raw.(DraftList).Items) != 0 {
		t.Fatal("half saved draft became visible", err)
	}
	// Restart service with the same durable database and retained Artifact bytes.
	svc, err = NewService(ctx, db, content, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	content.svc = svc
	if err = svc.recoverDrafts(ctx); err != nil {
		t.Fatal("recovery failed", err)
	}
	raw, err = svc.WorldDraft(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	created := raw.(DraftRevision)
	if len(content.writes) != 1 || len(created.Manifest.Files) != 0 {
		t.Fatal("retry duplicated immutable write or fabricated content")
	}
	changed := req
	changed.Name = "other"
	if _, err = svc.WorldDraft(ctx, p, changed); !errors.Is(err, ErrIdempotency) {
		t.Fatal("changed create reused id", err)
	}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "list", Purpose: "collection"})
	if err != nil || len(raw.(DraftList).Items) != 1 || raw.(DraftList).Items[0].HasContent {
		t.Fatal("empty draft not labeled", err)
	}
	for _, guest := range []Principal{{OrganizationID: p.OrganizationID, SubjectID: testID(90), RequestID: p.RequestID}, {OrganizationID: testID(91), SubjectID: p.SubjectID, RequestID: p.RequestID}} {
		if _, err = svc.WorldDraft(ctx, guest, DraftRequest{Mode: "get", DraftID: created.DraftID}); !errors.Is(err, ErrForbidden) {
			t.Fatal("private draft leaked", err)
		}
		raw, err = svc.WorldDraft(ctx, guest, DraftRequest{Mode: "list"})
		if err != nil || len(raw.(DraftList).Items) != 0 {
			t.Fatal("private draft leaked into list", err)
		}
		if _, err = svc.WorldDraft(ctx, guest, DraftRequest{Mode: "status", CommitID: req.CommitID}); !errors.Is(err, ErrNotFound) {
			t.Fatal("private commit leaked", err)
		}
	}
	ref := ContentRef{testID(3), testID(80), testID(81)}
	content.versions[ref] = ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_import", WorldDraftID: created.DraftID}, MIME: "text/plain"}
	content.bytes[ref] = []byte("collected reference")
	m := created.Manifest
	m.Files = []File{{ID: testID(82), Path: "reference.txt", Role: "reference", Content: ref}}
	commit := DraftRequest{Mode: "commit", DraftID: created.DraftID, ExpectedRevisionID: created.RevisionID, CommitID: testID(83), Manifest: &m}
	if _, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: created.DraftID, ExpectedRevisionID: created.RevisionID, CommitID: req.CommitID, Manifest: &m}); !errors.Is(err, ErrIdempotency) {
		t.Fatal("create/commit domains diverged", err)
	}
	raw, err = svc.WorldDraft(ctx, p, commit)
	if err != nil {
		t.Fatal(err)
	}
	saved := raw.(DraftRevision)
	if len(content.retained[saved.RevisionID]) != 2 {
		t.Fatal("manifest/file not both retained")
	}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "get", DraftID: created.DraftID})
	if err != nil || raw.(DraftRevision).RevisionID != saved.RevisionID {
		t.Fatal("head unreadable", err)
	}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "get", DraftID: created.DraftID, RevisionID: created.RevisionID})
	if err != nil || len(raw.(DraftRevision).Manifest.Files) != 0 || raw.(DraftRevision).HeadRevisionID != saved.RevisionID {
		t.Fatal("old revision changed", err)
	}
	assetList, err := svc.WorldList(ctx, p, WorldListRequest{})
	if err != nil || len(assetList.Items) != 0 {
		t.Fatal("collected content mixed with assets", err)
	}
	// Domain validation reads the real candidate bytes before publishing a head.
	domainRef := ContentRef{testID(3), testID(100), testID(101)}
	var invalidDoc contentObject
	if json.Unmarshal(learningPromptFixture(t), &invalidDoc) != nil {
		t.Fatal("fixture")
	}
	item := objectOf(listOf(invalidDoc["results"])[0])
	item["parent_key"] = item["key"]
	invalidBytes, _ := json.Marshal(invalidDoc)
	content.versions[domainRef] = ContentVersion{ContentRef: domainRef, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_edit", WorldDraftID: created.DraftID}, MIME: "application/json"}
	content.bytes[domainRef] = invalidBytes
	withDomain := m
	withDomain.Files = append(append([]File{}, m.Files...), File{ID: testID(102), Path: "learning.json", Role: "knowledge", Content: domainRef})
	withDomain.DomainDocuments = []DomainDocument{{Type: "reference-learning", FileID: testID(102)}}
	invalidCommit := DraftRequest{Mode: "commit", DraftID: created.DraftID, ExpectedRevisionID: saved.RevisionID, CommitID: testID(103), Manifest: &withDomain}
	if _, err = svc.WorldDraft(ctx, p, invalidCommit); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid domain published", err)
	}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "status", CommitID: invalidCommit.CommitID})
	if err != nil || raw.(DraftStatus).State != "failed" || raw.(DraftStatus).ErrorCode != ErrInvalid.Error() {
		t.Fatal("semantic failure not recoverable as a typed status", err)
	}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "get", DraftID: created.DraftID})
	if err != nil || raw.(DraftRevision).RevisionID != saved.RevisionID {
		t.Fatal("invalid body changed head", err)
	}
	// Corrected immutable bytes require a new content version and a new commit.
	validRef := ContentRef{testID(3), testID(100), testID(104)}
	content.versions[validRef] = ContentVersion{ContentRef: validRef, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_edit", WorldDraftID: created.DraftID}, MIME: "application/json"}
	content.bytes[validRef] = learningPromptFixture(t)
	withDomain.Files[1].Content = validRef
	validCommit := invalidCommit
	validCommit.CommitID = testID(105)
	raw, err = svc.WorldDraft(ctx, p, validCommit)
	if err != nil {
		t.Fatal("corrected domain did not save", err)
	}
	saved = raw.(DraftRevision)
	m = withDomain
	// Publish only the learning document; the collected reference remains private.
	content.failRetain = true
	registration := WorldRegisterRequest{CommitID: testID(110), SourceDraftID: saved.DraftID, SourceRevisionID: saved.RevisionID, FileIDs: []string{testID(102)}, Name: "Independent prompt", AssetType: "prompt", Subjects: []string{}, Relation: "produced_in"}
	if _, err = svc.WorldRegister(ctx, p, registration); !errors.Is(err, ErrDependency) {
		t.Fatal("publication outage not recoverable", err)
	}
	status, err := svc.WorldCommitStatus(ctx, p, WorldStatusRequest{CommitID: registration.CommitID})
	if err != nil || status.State != "preparing" {
		t.Fatal("initial publication cannot recover without asset ID", err)
	}
	asset, err := svc.WorldRegister(ctx, p, registration)
	if err != nil {
		t.Fatal("independent registration failed", err)
	}
	status, err = svc.WorldCommitStatus(ctx, p, WorldStatusRequest{CommitID: registration.CommitID})
	if err != nil || status.State != "committed" || status.Result == nil || status.Result.AssetID != asset.AssetID {
		t.Fatal("publication status lost fixed asset", err)
	}
	if asset.Manifest.SchemaVersion != 2 || len(asset.Manifest.Files) != 1 || len(*asset.Manifest.DomainDocuments) != 1 {
		t.Fatal("draft asset returned wrong manifest")
	}
	if len(content.retained[asset.VersionID]) != 3 {
		t.Fatal("publication retention must contain selected file and two manifests only")
	}
	for _, r := range content.retained[asset.VersionID] {
		if r == ref {
			t.Fatal("private sibling retained as asset")
		}
	}
	guest := Principal{OrganizationID: p.OrganizationID, SubjectID: testID(120), RequestID: p.RequestID}
	if _, err = svc.WorldCommitStatus(ctx, guest, WorldStatusRequest{CommitID: registration.CommitID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("creation lookup leaked to another user", err)
	}
	foreign := p
	foreign.OrganizationID = testID(121)
	if _, err = svc.WorldCommitStatus(ctx, foreign, WorldStatusRequest{CommitID: registration.CommitID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("creation lookup leaked across organizations", err)
	}
	if _, err = svc.WorldGrant(ctx, p, WorldGrantRequest{AssetID: asset.AssetID, SubjectID: guest.SubjectID, Role: "reader"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.WorldGet(ctx, guest, WorldGetRequest{AssetID: asset.AssetID, VersionID: asset.VersionID}); err != nil {
		t.Fatal("shared asset unreadable", err)
	}
	access := Access{AssetID: asset.AssetID, AssetVersionID: asset.VersionID}
	if _, err = content.Read(ctx, guest, validRef, access, MaxManifestBytes); err != nil {
		t.Fatal("selected prompt unreadable", err)
	}
	for _, private := range []ContentRef{ref, saved.ManifestRef} {
		if _, err = content.Read(ctx, guest, private, access, MaxManifestBytes); !errors.Is(err, ErrForbidden) {
			t.Fatal("asset grant exposed private source", err)
		}
	}
	if _, err = svc.WorldDraft(ctx, guest, DraftRequest{Mode: "get", DraftID: saved.DraftID, RevisionID: saved.RevisionID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("asset grant exposed draft", err)
	}
	closure, err := svc.ReferencesWithGrants(ctx, guest, AssetRef{AssetID: asset.AssetID, VersionID: asset.VersionID, Purpose: "prompt"})
	if err != nil || len(closure.Refs) != 2 || len(closure.Grants) != 1 || closure.Grants[0].ResourceID != asset.AssetID {
		t.Fatal("consumer closure includes private draft", err)
	}
	// Two concurrent edits of the same fixed parent yield one commit, one conflict.
	m1, m2 := m, m
	m1.Name = "edit one"
	m2.Name = "edit two"
	inputs := []DraftRequest{{Mode: "commit", DraftID: created.DraftID, ExpectedRevisionID: saved.RevisionID, CommitID: testID(84), Manifest: &m1}, {Mode: "commit", DraftID: created.DraftID, ExpectedRevisionID: saved.RevisionID, CommitID: testID(85), Manifest: &m2}}
	outcomes := make(chan error, 2)
	for _, r := range inputs {
		go func(r DraftRequest) { _, err := svc.WorldDraft(ctx, p, r); outcomes <- err }(r)
	}
	success, conflict := 0, 0
	for range 2 {
		err := <-outcomes
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("CAS failed", success, conflict)
	}
	for _, r := range inputs {
		status, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "status", CommitID: r.CommitID})
		if err != nil {
			t.Fatal(err)
		}
		st := status.(DraftStatus)
		if st.State == "conflict" && !ValidID(st.CurrentRevisionID) {
			t.Fatal("conflict lost durable head")
		}
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_draft_revisions SET manifest_json=manifest_json WHERE revision_id=$1`, saved.RevisionID); err == nil {
		t.Fatal("immutable revision updated")
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_drafts SET purpose='learning' WHERE draft_id=$1`, saved.DraftID); err == nil {
		t.Fatal("purpose changed")
	}
}
