//go:build integration

package world

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

// Models the Artifact 0.2.6 byte-proxy boundary without reverse callbacks.
// This adapter is deliberately not evidence of a real S3/Artifact execution.
type analysisSaveTestArtifact struct {
	*draftTestArtifact
	loseWrite, loseRead, loseCommit bool
}

func (a *analysisSaveTestArtifact) write(p Principal, source Source, w TextWrite) (ContentVersion, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !p.Valid() {
		return ContentVersion{}, ErrForbidden
	}
	if ref, ok := a.writes[w.WriteID]; ok {
		v := a.versions[ref]
		if v.Source != source || v.SHA256 != analysisDocumentHash([]byte(w.Text)) {
			return ContentVersion{}, ErrIdempotency
		}
		return v, nil
	}
	ref := ContentRef{testID(3), newID(), newID()}
	v := ContentVersion{SchemaVersion: 2, ContentRef: ref, OrganizationID: p.OrganizationID, Source: source, MIME: w.MIME, SHA256: analysisDocumentHash([]byte(w.Text)), Size: int64(len(w.Text)), CreatedBy: p.SubjectID, VersionNo: 1, CreatedAt: time.Now().UTC()}
	a.versions[ref] = v
	a.bytes[ref] = []byte(w.Text)
	a.writes[w.WriteID] = ref
	return v, nil
}
func (a *analysisSaveTestArtifact) WriteDraftManifest(_ context.Context, p Principal, id, rev string, w TextWrite) (ContentVersion, error) {
	return a.write(p, Source{Kind: "world_draft_manifest", WorldDraftID: id, WorldDraftRevisionID: rev}, w)
}
func (a *analysisSaveTestArtifact) WriteAnalysisDocument(_ context.Context, p Principal, id string, w TextWrite) (ContentVersion, error) {
	v, err := a.write(p, analysisDocumentSource(id), w)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil && a.loseWrite {
		a.loseWrite = false
		return ContentVersion{}, ErrDependency
	}
	return v, err
}
func (a *analysisSaveTestArtifact) Metadata(_ context.Context, p Principal, ref ContentRef, _ Access) (ContentVersion, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.versions[ref]
	if !ok {
		return v, ErrNotFound
	}
	if !p.Valid() || v.OrganizationID != p.OrganizationID {
		return ContentVersion{}, ErrForbidden
	}
	return v, nil
}
func (a *analysisSaveTestArtifact) Read(ctx context.Context, p Principal, ref ContentRef, access Access, max int64) ([]byte, error) {
	v, err := a.Metadata(ctx, p, ref, access)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if v.Source.Kind == "task_output" && a.loseRead {
		a.loseRead = false
		return nil, ErrDependency
	}
	data := a.bytes[ref]
	if int64(len(data)) > max {
		return nil, ErrInvalid
	}
	return append([]byte{}, data...), nil
}
func (a *analysisSaveTestArtifact) Retain(ctx context.Context, p Principal, _ RetentionOwner, refs []ContentRef) error {
	for _, ref := range refs {
		v, err := a.Metadata(ctx, p, ref, Access{})
		if err != nil {
			return err
		}
		if v.Source.Kind == "task_output" {
			a.mu.Lock()
			fail := a.loseCommit
			a.loseCommit = false
			a.mu.Unlock()
			if fail {
				return ErrDependency
			}
		}
	}
	return nil
}

func TestAnalysisLearningSaveRecoveryAndFixedRead(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{targets: syntheticPromptTargets(), typed: true, saveLearning: true})
}

func verifyAnalysisLearningSave(t *testing.T, svc *Service, p Principal, b analysis.Bundle, port *analysisSaveTestArtifact) {
	t.Helper()
	ctx := t.Context()
	store := analysis.PGStore{DB: svc.db}
	before := len(port.writes)
	port.loseWrite, port.loseRead, port.loseCommit = true, true, true
	var identity analysisLearningSaveRecord
	for attempt := 0; attempt < 3; attempt++ {
		r, err := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
		if !errors.Is(err, ErrDependency) || r.State != "pending" {
			t.Fatal("lost response did not preserve pending save", attempt, r.State, err)
		}
		record, err := svc.loadAnalysisLearningSave(ctx, p, b.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			identity = record
		} else if identity.CreateID != record.CreateID || identity.WriteID != record.WriteID || identity.CommitID != record.CommitID || identity.Hash != record.Hash {
			t.Fatal("retry changed save identity")
		}
		// Reconstruct the service to prove recovery does not rely on local state.
		svc, err = NewService(ctx, svc.db, port, integrationProject{}, 1, map[string]coordination.Peer{})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); err != nil {
		t.Fatal(err)
	}
	// Deterministic save continues while inference dispatch is disabled.
	worker := analysis.Scheduler{Queue: store, Dispatch: false, Advance: func(ctx context.Context, w analysis.Work) (string, error) {
		if w.StepKey != analysis.LearningSaveStepKey {
			t.Fatal("wrong phase", w.StepKey)
		}
		r, err := svc.SaveAnalysisLearning(ctx, Principal{w.OrganizationID, w.OwnerID, w.LeaseID}, w.TaskID)
		return r.State, err
	}}
	if worked, err := worker.Tick(ctx); err != nil || !worked {
		t.Fatal("automatic recovery did not save", err)
	}
	saved, err := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
	if err != nil || saved.State != "saved" || saved.Revision == nil {
		t.Fatal("saved result unreadable", err)
	}
	if len(port.writes) != before+3 {
		t.Fatal("duplicate writes", len(port.writes)-before)
	}
	progress, err := store.Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || progress.Stage != "learning_saved_need_evidence" {
		t.Fatal("missing evidence promoted to complete", progress, err)
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_learning SET write_id=$1 WHERE task_id=$2`, newID(), b.TaskID); err == nil {
		t.Fatal("terminal identity mutable")
	}
	if _, err = svc.db.Exec(ctx, `UPDATE studio.world_analysis_schedule SET next_attempt_at=now() WHERE task_id=$1`, b.TaskID); err != nil {
		t.Fatal(err)
	}
	if worked, err := worker.Tick(ctx); worked || err != nil {
		t.Fatal("saved task scheduled again", err)
	}
	manifest := saved.Revision.Manifest
	manifest.Name = "User title after save"
	if _, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: saved.Revision.DraftID, ExpectedRevisionID: saved.Revision.RevisionID, CommitID: newID(), Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
	if err != nil || replayed.Revision.RevisionID != saved.Revision.RevisionID || replayed.Revision.Manifest.Name == manifest.Name {
		t.Fatal("save replay followed mutable head", err)
	}
	v := port.versions[saved.Revision.Manifest.Files[0].Content]
	for _, mutate := range []func(*ContentVersion){func(v *ContentVersion) { v.SHA256 = "bad" }, func(v *ContentVersion) { v.Size++ }, func(v *ContentVersion) { v.MIME = "text/plain" }, func(v *ContentVersion) { v.CreatedBy = newID() }, func(v *ContentVersion) { v.Source.RunID = "fake" }} {
		candidate := v
		mutate(&candidate)
		if err = svc.checkAnalysisOutput(ctx, p, candidate); err == nil {
			t.Fatal("forged output metadata accepted")
		}
	}
}

func TestAnalysisLearningSaveConflictPreservesUserEdit(t *testing.T) {
	testAnalysisBundleOptions(t, bundleOptions{typed: true, saveLearning: true, saveConflict: true})
}
func verifyAnalysisSaveConflict(t *testing.T, svc *Service, p Principal, b analysis.Bundle, port *analysisSaveTestArtifact) {
	t.Helper()
	ctx := t.Context()
	port.loseWrite = true
	if _, err := svc.SaveAnalysisLearning(ctx, p, b.TaskID); !errors.Is(err, ErrDependency) {
		t.Fatal("outage not injected", err)
	}
	record, err := svc.loadAnalysisLearningSave(ctx, p, b.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := loadDraftCommit(ctx, svc.db, p, record.CreateID)
	if err != nil || created.Result == nil {
		t.Fatal("initial revision absent", err)
	}
	original := *created.Result
	changed := original.Manifest
	changed.Name = "User edit before automatic save"
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: original.DraftID, ExpectedRevisionID: original.RevisionID, CommitID: newID(), Manifest: &changed})
	if err != nil {
		t.Fatal(err)
	}
	user := raw.(DraftRevision)
	writes := len(port.writes)
	for range 2 {
		saved, err := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
		if err != nil || saved.State != "conflict" || saved.ErrorCode != "REVISION_CONFLICT" {
			t.Fatal("conflict not preserved", saved.State, err)
		}
		r, err := svc.loadAnalysisLearningSave(ctx, p, b.TaskID)
		if err != nil || r.CreateID != record.CreateID || r.WriteID != record.WriteID || r.CommitID != record.CommitID {
			t.Fatal("conflict replaced identity", err)
		}
	}
	head, err := svc.getDraft(ctx, p, user.DraftID, "")
	if err != nil || head.RevisionID != user.RevisionID || head.Manifest.Name != changed.Name || len(port.writes) != writes {
		t.Fatal("user edit overwritten or new bytes written", err)
	}
	progress, err := (analysis.PGStore{DB: svc.db}).Progress(ctx, p.OrganizationID, p.SubjectID, b.TaskID)
	if err != nil || progress.Stage != "learning_save_failed" {
		t.Fatal("conflict hidden", progress, err)
	}
}
