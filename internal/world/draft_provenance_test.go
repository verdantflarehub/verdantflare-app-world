package world

import (
	"context"
	"errors"
	"testing"
)

// The producer is represented by immutable Artifact metadata in these unit
// tests. This does not claim a media engine has executed or attest task state.
type provenanceProject struct {
	source OpenResult
	deny   bool
}

func (p *provenanceProject) Open(_ context.Context, _ Principal, id, revision string) (OpenResult, error) {
	if p.deny || id != p.source.ProjectID || revision != p.source.RevisionID {
		return OpenResult{}, ErrForbidden
	}
	return p.source, nil
}
func (p *provenanceProject) Authorize(_ context.Context, _ Principal, permission Permission) error {
	if !p.deny && permission.Action == "read" && permission.Version != nil && permission.Access != nil &&
		*permission.Access == (Access{ProjectID: p.source.ProjectID, RevisionID: p.source.RevisionID}) {
		for _, file := range p.source.Manifest.Files {
			if file.Content == permission.Version.ContentRef {
				return nil
			}
		}
	}
	return ErrForbidden
}

func TestDraftNativeOutputRequiresAuthorizedSelectedFile(t *testing.T) {
	p := Principal{testID(30), testID(31), testID(32)}
	m := draftFixture()
	m.Purpose = "creation"
	file := File{ID: testID(4), Path: "created.wav", Role: "audio", Content: ContentRef{testID(1), testID(2), testID(3)}}
	project := &provenanceProject{source: OpenResult{Result: Result{ProjectID: testID(40), RevisionID: testID(41), Manifest: Manifest{Files: []File{file}}}}}
	run := RunRef{ServiceID: "speech", RunID: "original-runtime-id"}
	m.Files = []File{file}
	m.RunRefs = []RunRef{run}
	m.SourceRefs = []DraftSourceRef{{Kind: "project", ProjectID: project.source.ProjectID, RevisionID: project.source.RevisionID, FileID: file.ID, Purpose: "reference"}}
	origin := Source{Kind: "task_output", ProjectID: project.source.ProjectID, ServiceID: run.ServiceID, RunID: run.RunID, TaskID: testID(42)}
	a := &learningTestArtifact{version: ContentVersion{ContentRef: file.Content, OrganizationID: p.OrganizationID, Source: origin}, access: Access{ProjectID: project.source.ProjectID, RevisionID: project.source.RevisionID}}
	svc := &Service{content: a, projects: project}
	check := func(m DraftManifest) ([]ContentRef, error) {
		t.Helper()
		refs, _, err := svc.checkDraftContents(context.Background(), p, draftCommit{DraftID: m.DraftID, Plan: draftPlan{Manifest: m}})
		return refs, err
	}
	refs, err := check(m)
	if err != nil || len(refs) != 1 || refs[0] != file.Content {
		t.Fatal("authorized archived output could not enter independent creation", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*DraftManifest)
		want error
	}{
		{"unselected source", func(m *DraftManifest) { m.Files = []File{} }, ErrNotReady},
		{"wrong run", func(m *DraftManifest) { m.RunRefs = []RunRef{{ServiceID: run.ServiceID, RunID: "another-runtime"}} }, ErrNotReady},
		{"wrong service", func(m *DraftManifest) { m.RunRefs = []RunRef{{ServiceID: "video", RunID: run.RunID}} }, ErrNotReady},
		{"task ID is not run ID", func(m *DraftManifest) { m.RunRefs = []RunRef{{ServiceID: run.ServiceID, RunID: origin.TaskID}} }, ErrNotReady},
		{"undeclared access", func(m *DraftManifest) { m.SourceRefs = []DraftSourceRef{} }, ErrForbidden},
		{"every run needs output", func(m *DraftManifest) { m.RunRefs = []RunRef{run, {ServiceID: "image", RunID: "missing"}} }, ErrNotReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := m
			tc.edit(&candidate)
			if _, err := check(candidate); !errors.Is(err, tc.want) {
				t.Fatal("unattested native origin accepted", err)
			}
		})
	}
	project.deny = true
	if _, err = check(m); !errors.Is(err, ErrForbidden) {
		t.Fatal("source denial bypassed", err)
	}
	project.deny = false
	a.deny = true
	if _, err = check(m); !errors.Is(err, ErrForbidden) {
		t.Fatal("Artifact denial bypassed", err)
	}
	a.deny = false
	for _, edit := range []func(*Source){
		func(s *Source) { s.Kind = "user_import" },
		func(s *Source) { s.Kind = "legacy_import" },
		func(s *Source) { s.WorldDraftID = m.DraftID },
		func(s *Source) { s.WorldDraftRevisionID = testID(50) },
		func(s *Source) { s.AssetID = testID(50) },
		func(s *Source) { s.AssetVersionID = testID(50) },
		func(s *Source) { s.Original = &OriginalRef{ServiceID: "speech", TaskID: run.RunID} },
	} {
		source := origin
		edit(&source)
		a.version.Source = source
		if _, err = check(m); !errors.Is(err, ErrNotReady) {
			t.Fatal("client or mixed origin accepted as producer proof", err)
		}
	}
}

func TestDraftOutputProofDoesNotAuthorizeExecutionClaims(t *testing.T) {
	m := draftFixture()
	run := RunRef{ServiceID: "speech", RunID: "archived-output"}
	m.RunRefs = []RunRef{run}
	doc := LearningContent{RunRefs: []RunRef{run}}
	svc := &Service{}
	if err := svc.checkLearningBindings(context.Background(), Principal{}, doc, m, &draftInputs{}); !errors.Is(err, ErrNotReady) {
		t.Fatal("archived output treated as model/input/execution attestation", err)
	}
}
