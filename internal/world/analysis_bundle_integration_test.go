//go:build integration

package world

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

// Actual PostgreSQL and World permission checks, with synthetic native output.
// This is not a real model call or a completed learning asset.
func TestAnalysisBundleFixedSourceAndCurrentPermission(t *testing.T) { testAnalysisBundle(t, nil) }
func testAnalysisBundle(t *testing.T, targets []analysis.PromptTarget, failPrompts ...bool) {
	options := bundleOptions{targets: targets}
	if len(failPrompts) > 0 {
		options.failPrompts = failPrompts[0]
	}
	testAnalysisBundleOptions(t, options)
}

type bundleOptions struct {
	targets                         []analysis.PromptTarget
	failPrompts, typed, failResults bool
	saveLearning                    bool
	saveConflict                    bool
	external                        Artifact
	writeSource                     func(*testing.T, Principal, string) ContentVersion
	verifySave                      func(*testing.T, *Service, Principal, analysis.Bundle)
}

// RunAnalysisSaveFixture is a test-only bridge for the external package to
// exercise the production Artifact client without a world/artifactclient cycle.
func RunAnalysisSaveFixture(t *testing.T, port Artifact, source func(*testing.T, Principal, string) ContentVersion, verify func(*testing.T, *Service, Principal, analysis.Bundle)) {
	testAnalysisBundleOptions(t, bundleOptions{typed: true, saveLearning: true, external: port, writeSource: source, verifySave: verify})
}

func testAnalysisBundleOptions(t *testing.T, options bundleOptions) {
	targets := options.targets
	ctx := t.Context()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	content := &draftTestArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{}}
	var port Artifact = content
	var saveProxy *analysisSaveTestArtifact
	if options.saveLearning {
		saveProxy = &analysisSaveTestArtifact{draftTestArtifact: content}
		port = saveProxy
	}
	if options.external != nil {
		port = options.external
	}
	svc, err := NewService(ctx, db, port, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	content.svc = svc
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: newID()}
	subjects := []string{"reference"}
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "Synthetic bundle source", Purpose: "collection", Subjects: &subjects})
	if err != nil {
		t.Fatal(err)
	}
	initial := raw.(DraftRevision)
	ref := ContentRef{testID(3), newID(), newID()}
	fileID := newID()
	hash := strings.Repeat("a", 64)
	content.versions[ref] = ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_import", WorldDraftID: initial.DraftID}, MIME: "video/mp4", Size: 10, SHA256: hash}
	if options.writeSource != nil {
		v := options.writeSource(t, p, initial.DraftID)
		ref, hash = v.ContentRef, v.SHA256
	}
	manifest := initial.Manifest
	manifest.Files = []File{{ID: fileID, Path: "reference.mp4", Role: "reference", Content: ref}}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: initial.DraftID, ExpectedRevisionID: initial.RevisionID, CommitID: newID(), Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	source := raw.(DraftRevision)
	plan, err := analysis.PlanVideo(analysis.Source{SHA256: hash, DurationMS: 45000})
	if err != nil {
		t.Fatal(err)
	}
	task := analysis.Task{OrganizationID: p.OrganizationID, OwnerID: p.SubjectID, TaskID: newID(), DraftID: source.DraftID, RevisionID: source.RevisionID, FileID: fileID, Plan: plan, PromptTargets: targets, IncludeTypedResults: options.typed, SaveLearning: options.saveLearning}
	step := plan.Steps[0]
	prepared := analysis.Prepared{SourceHash: hash, MediaHash: hash, MIME: "video/mp4", Range: step.Input}
	request, err := analysis.Freeze(task.TaskID, "gemini-3.1-pro-preview", "", "1.00", step, prepared, 10)
	if err != nil {
		t.Fatal(err)
	}
	store := analysis.PGStore{DB: db}
	if err = store.Create(ctx, task, []analysis.FrozenRequest{request}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AnalysisBundle(ctx, p, task.TaskID); !errors.Is(err, analysis.ErrObservationsIncomplete) {
		t.Fatal("planned task became bundle", err)
	}
	r, err := store.Load(ctx, p.OrganizationID, p.SubjectID, task.TaskID, step.Key)
	if err != nil {
		t.Fatal(err)
	}
	next := r
	next.State = "dispatching"
	next.Revision++
	if ok, err := store.CompareAndSwap(ctx, r, next); err != nil || !ok {
		t.Fatal("persist intent", err)
	}
	r = next
	findings := []any{}
	for _, dimension := range []string{"narrative", "character", "scene", "prop", "cinematography", "editing", "graphics", "style"} {
		findings = append(findings, map[string]any{"dimension": dimension, "description": "Synthetic observation", "shot_keys": []string{"s1"}, "basis": "observed"})
	}
	doc := map[string]any{"start_ms": 0, "end_ms": 45000, "entities": []any{}, "findings": findings, "uncertainties": []string{}, "shots": []any{map[string]any{
		"key": "s1", "start_ms": 0, "end_ms": 45000, "continues_before": false, "continues_after": false,
		"composition": "Synthetic wide shot", "camera_motion": "Static", "subject_motion": "None", "graphic_motion": "None", "lighting": "Synthetic light", "entity_keys": []string{},
		"beats": []any{map[string]any{"start_ms": 0, "end_ms": 45000, "description": "Synthetic still frame"}}, "visible_text": []any{},
	}}}
	text, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	callID := newID()
	schemaHash, err := step.SchemaHash()
	if err != nil {
		t.Fatal(err)
	}
	native, err := json.Marshal(map[string]any{"schema_version": 1, "call_id": callID, "task_id": task.TaskID, "model_id": request.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{
		"text": string(text), "document": doc, "model_id": request.ModelID, "finish_reason": "STOP", "response_schema_verified": true,
		"input": map[string]any{"media": map[string]any{"mime_type": prepared.MIME, "sha256": hash}, "provider_file_integrity_verified": true, "response_schema_sha256": schemaHash, "video_range": step.Input},
	}})
	if err != nil {
		t.Fatal(err)
	}
	report := analysis.VerifyNative(step, prepared, analysis.Binding{CallID: callID, TaskID: task.TaskID, ModelID: request.ModelID}, native)
	if !report.Passed {
		t.Fatal(report)
	}
	next = r
	next.State, next.CallID, next.Result, next.Report = "observation_ready", callID, native, &report
	next.Revision++
	if ok, err := store.CompareAndSwap(ctx, r, next); err != nil || !ok {
		t.Fatal("persist observation", err)
	}
	bundle, err := svc.AnalysisBundle(ctx, p, task.TaskID)
	if err != nil || len(bundle.Nodes) != 10 || bundle.Stage != "association_input" || bundle.RevisionID != source.RevisionID {
		t.Fatal("fixed bundle", err)
	}
	// Editing the collection head must not redirect the existing analysis.
	manifest = source.Manifest
	manifest.Files = []File{}
	if _, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: source.DraftID, ExpectedRevisionID: source.RevisionID, CommitID: newID(), Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	again, err := svc.AnalysisBundle(ctx, p, task.TaskID)
	if err != nil || !reflect.DeepEqual(bundle, again) {
		t.Fatal("head edit changed fixed evidence", err)
	}
	t.Run("persistent association recovery", func(t *testing.T) { verifyAssociationExecution(t, svc, p, bundle) })
	if len(targets) > 0 {
		if options.failPrompts {
			t.Run("failed prompt stops new spending", func(t *testing.T) { verifyPromptFailure(t, svc, p, bundle) })
		} else {
			t.Run("persistent prompt recovery", func(t *testing.T) { verifyPromptExecution(t, svc, p, bundle) })
		}
	}
	if options.typed {
		if _, err := svc.AnalysisLearningDocument(ctx, p, task.TaskID); err == nil {
			t.Fatal("learning document available before typed results")
		}
		if options.failPrompts {
			if err := svc.PrepareAnalysisResults(ctx, p, bundle.TaskID, true); err == nil {
				t.Fatal("results started after prompt failure")
			}
		} else if options.failResults {
			t.Run("result failure stops spending", func(t *testing.T) { verifyResultFailure(t, svc, p, bundle) })
		} else {
			t.Run("persistent typed results", func(t *testing.T) { verifyResultExecution(t, svc, p, bundle) })
			t.Run("trusted learning assembly", func(t *testing.T) { verifyAnalysisLearningAssembly(t, svc, p, bundle) })
			if options.saveLearning {
				if options.verifySave != nil {
					options.verifySave(t, svc, p, bundle)
				} else if options.saveConflict {
					t.Run("preserve user edit on conflict", func(t *testing.T) { verifyAnalysisSaveConflict(t, svc, p, bundle, saveProxy) })
				} else {
					t.Run("durable learning save", func(t *testing.T) { verifyAnalysisLearningSave(t, svc, p, bundle, saveProxy) })
				}
			}
		}
		if options.failPrompts || options.failResults {
			if _, err := svc.AnalysisLearningDocument(ctx, p, task.TaskID); err == nil {
				t.Fatal("failed batch promoted to learning document")
			}
		}
	}
	for _, actor := range []Principal{{}, {OrganizationID: newID(), SubjectID: p.SubjectID, RequestID: newID()}, {OrganizationID: p.OrganizationID, SubjectID: newID(), RequestID: newID()}} {
		if options.typed {
			if options.saveLearning {
				if _, err := svc.SaveAnalysisLearning(ctx, actor, task.TaskID); err == nil {
					t.Fatal("foreign save")
				}
			}
			if _, err := svc.AnalysisLearningDocument(ctx, actor, task.TaskID); err == nil {
				t.Fatal("foreign learning document read")
			}
			if err := svc.PrepareAnalysisResults(ctx, actor, task.TaskID, true); err == nil {
				t.Fatal("foreign result preparation")
			}
			if _, err := svc.AnalysisResultDrafts(ctx, actor, task.TaskID); err == nil {
				t.Fatal("foreign result read")
			}
			if _, err := svc.AdvanceAnalysisResult(ctx, actor, task.TaskID, "results-v1-shot-0000", &resultChannelFixture{}, true); err == nil {
				t.Fatal("foreign result advance")
			}
		}
		b, err := svc.AnalysisBundle(ctx, actor, task.TaskID)
		if err == nil || len(b.Nodes) > 0 {
			t.Fatal("foreign evidence leaked")
		}
		if _, err := svc.AnalysisAssociation(ctx, actor, task.TaskID); err == nil {
			t.Fatal("foreign association leaked")
		}
	}
	if len(targets) > 0 {
		for _, actor := range []Principal{{}, {OrganizationID: newID(), SubjectID: p.SubjectID, RequestID: newID()}, {OrganizationID: p.OrganizationID, SubjectID: newID(), RequestID: newID()}} {
			if _, err := svc.AnalysisPromptDrafts(ctx, actor, task.TaskID); err == nil {
				t.Fatal("foreign prompt content leaked")
			}
		}
	}
	// Re-read current Artifact authority; the stored pass flag cannot grant access.
	if options.external != nil {
		return // Real-store revocation checks are performed by its verifier.
	}
	version := content.versions[ref]
	version.OrganizationID = newID()
	content.versions[ref] = version
	if b, err := svc.AnalysisBundle(ctx, p, task.TaskID); err == nil || len(b.Nodes) > 0 {
		t.Fatal("revoked source still readable")
	}
	if _, err := svc.AnalysisAssociation(ctx, p, task.TaskID); err == nil {
		t.Fatal("revoked association still readable")
	}
	if len(targets) > 0 {
		if _, err := svc.AnalysisPromptDrafts(ctx, p, task.TaskID); err == nil {
			t.Fatal("revoked prompt source still readable")
		}
	}
	if options.typed {
		if options.saveLearning {
			if _, err := svc.SaveAnalysisLearning(ctx, p, task.TaskID); err == nil {
				t.Fatal("revoked save")
			}
		}
		if _, err := svc.AnalysisLearningDocument(ctx, p, task.TaskID); err == nil {
			t.Fatal("revoked learning source still readable")
		}
		if _, err := svc.AnalysisResultDrafts(ctx, p, task.TaskID); err == nil {
			t.Fatal("revoked results readable")
		}
		if _, err := svc.AdvanceAnalysisResult(ctx, p, task.TaskID, "results-v1-shot-0000", &resultChannelFixture{}, true); err == nil {
			t.Fatal("revoked result advance")
		}
	}
	version.OrganizationID = p.OrganizationID
	version.SHA256 = strings.Repeat("b", 64)
	content.versions[ref] = version
	if _, err := svc.AnalysisBundle(ctx, p, task.TaskID); !errors.Is(err, ErrNotReady) {
		t.Fatal("changed source accepted", err)
	}
}

func RestartAnalysisTestService(t *testing.T, svc *Service, port Artifact) *Service {
	t.Helper()
	rebuilt, err := NewService(t.Context(), svc.db, port, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	return rebuilt
}
