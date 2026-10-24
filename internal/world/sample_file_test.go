package world

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSampleFileFixedDocumentProjectAndPermission(t *testing.T) {
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	source := sourceFixture()
	projects := &publicationProjects{t: t, principal: p, snapshots: map[string]OpenResult{source.RevisionID: source}}
	file := File{ID: testID(40), Path: "learning.json", Role: "document", Content: ContentRef{testID(41), testID(42), testID(43)}}
	asset := WorldResult{AssetID: testID(50), VersionID: testID(51), Manifest: AssetManifest{Files: []File{file}, DomainDocuments: &[]DomainDocument{{Type: "reference-learning", FileID: file.ID}}}}
	r := ProjectSampleReference{file.ID, source.ProjectID, source.RevisionID, source.Manifest.Files[0].ID}
	document := map[string]any{"schema_version": 1, "document_kind": "reference-learning", "origin": "manual", "sources": []any{map[string]any{"key": "sample", "title": "Sample", "media_kind": "image", "role": "created", "acquisition_state": "ready", "content": map[string]any{"kind": "project", "project_id": r.ProjectID, "revision_id": r.RevisionID, "file_id": r.FileID, "purpose": "sample"}}}, "results": []any{}, "coverage": []any{}, "relations": []any{}, "uses": []any{}}
	data, _ := json.Marshal(document)
	content := &publicationContent{t: t, principal: p, data: map[ContentRef][]byte{file.Content: data}, access: map[ContentRef]Access{file.Content: {AssetID: asset.AssetID, AssetVersionID: asset.VersionID}}}
	s := &Service{content: content, projects: projects}
	got, err := s.resolveSampleFile(t.Context(), p, asset, r)
	if err != nil || got.ProjectSampleReference != r || got.Content != source.Manifest.Files[0].Content {
		t.Fatal(got, err)
	}
	for _, field := range []string{"document", "project", "revision", "file"} {
		t.Run(field, func(t *testing.T) {
			bad := r
			switch field {
			case "document":
				bad.DocumentFileID = testID(99)
			case "project":
				bad.ProjectID = testID(99)
			case "revision":
				bad.RevisionID = testID(99)
			case "file":
				bad.FileID = source.Manifest.Files[1].ID
			}
			if _, err := s.resolveSampleFile(t.Context(), p, asset, bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("undeclared reference accepted", err)
			}
		})
	}
	projects.denied = true
	if _, err = s.resolveSampleFile(t.Context(), p, asset, r); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked project accepted", err)
	}
	projects.denied = false
	delete(projects.snapshots, source.RevisionID)
	if _, err = s.resolveSampleFile(t.Context(), p, asset, r); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing fixed revision accepted", err)
	}
	projects.snapshots[source.RevisionID] = source
	wrong := source
	wrong.RevisionID = testID(99)
	projects.snapshots[source.RevisionID] = wrong
	if _, err = s.resolveSampleFile(t.Context(), p, asset, r); !errors.Is(err, ErrDependency) {
		t.Fatal("wrong revision accepted", err)
	}
}
