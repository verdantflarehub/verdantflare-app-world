package world

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

type publicationProjects struct {
	ProjectReader
	t         *testing.T
	principal Principal
	snapshots map[string]OpenResult
	denied    bool
}

func (p *publicationProjects) Open(_ context.Context, actor Principal, projectID, revisionID string) (OpenResult, error) {
	if p.denied {
		return OpenResult{}, ErrForbidden
	}
	if actor != p.principal || projectID != testID(1) {
		p.t.Fatal("subject changed")
	}
	value, ok := p.snapshots[revisionID]
	if !ok {
		return OpenResult{}, ErrNotFound
	}
	return value, nil
}

type publicationContent struct {
	Artifact
	t         *testing.T
	principal Principal
	data      map[ContentRef][]byte
	access    map[ContentRef]Access
}

func (c *publicationContent) Read(_ context.Context, p Principal, ref ContentRef, access Access, limit int64) ([]byte, error) {
	if p != c.principal || access != c.access[ref] || limit != MaxManifestBytes {
		c.t.Fatal("Artifact fixed access changed")
	}
	data, ok := c.data[ref]
	if !ok {
		return nil, ErrNotFound
	}
	return data, nil
}
func publicationBytes(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func publicationSourceFixture(t *testing.T) (*Service, Principal, PublicationSourceRequest, contentObject, contentObject, contentObject) {
	t.Helper()
	p := Principal{OrganizationID: testID(80), SubjectID: testID(81), RequestID: testID(82)}
	m := Manifest{SchemaVersion: 1, Kind: "project", ProjectID: testID(1), Name: "Synthetic source", Category: "ai_short_drama", Status: "active", EntryDocumentID: testID(14), Selections: []Selection{{Purpose: "drama/globals/hall/night/lighting", FileIDs: []string{testID(11)}}}, AssetRefs: []AssetRef{}, RunRefs: []RunRef{}, DomainDocuments: []DomainDocument{{Type: "project-content", FileID: testID(10)}}}
	for n := 10; n <= 14; n++ {
		m.Files = append(m.Files, File{ID: testID(n), Path: fmt.Sprintf("files/%d.json", n), Role: "document", Content: ContentRef{testID(100), testID(n + 100), testID(n + 200)}})
	}
	m.Files[4].Path = "review.md"
	file := func(n int, purpose string) contentObject {
		f := m.Files[n]
		return contentObject{"file_id": f.ID, "content_ref": f.Content, "purpose": purpose}
	}
	locator := contentObject{"kind": "global", "key": "hall"}
	candidate := contentObject{"key": "hall-package", "name": "Synthetic package", "source": locator, "publication_record_file_id": testID(13), "review": contentObject{"state": "approved", "record_file_id": testID(14)}}
	version := contentObject{"key": "v1", "label": "L1", "state_key": "night", "basis_revision_id": testID(2), "change_note": "Synthetic", "file_refs": []any{file(1, "lighting")}, "lighting_record_file_id": testID(12), "generation_record_file_ids": []any{}, "review": contentObject{"state": "unreviewed"}}
	owner := contentObject{"key": "hall", "kind": "location", "name": "Synthetic hall", "states": []any{contentObject{"key": "night", "name": "Night", "definition_refs": []any{}, "marks": []any{}}}, "versions": []any{version}, "selected_versions": []any{contentObject{"state_key": "night", "purpose": "lighting", "version_key": "v1"}}}
	original := contentObject{"schema_version": 1, "kind": "project-content", "category": "ai_short_drama", "overview": "PRIVATE_SOURCE_TEXT", "ai_short_drama": contentObject{"globals": []any{owner}, "episodes": []any{}, "world_candidates": []any{candidate}}}
	var prepared contentObject
	_ = json.Unmarshal(publicationBytes(t, original), &prepared)
	request := WorldRegisterRequest{CommitID: testID(20), SourceProjectID: testID(1), SourceRevisionID: testID(2), FileIDs: []string{testID(13), testID(11), testID(12)}, Name: "Synthetic package", AssetType: "location", Subjects: []string{"Synthetic hall"}, Relation: "produced_in", ReviewFileID: testID(14)}
	objectOf(listOf(objectOf(prepared["ai_short_drama"])["world_candidates"])[0])["registration_request"] = request
	record := contentObject{"schema_version": 1, "kind": "short-drama-publication", "source": locator, "versions": []any{contentObject{"version_key": "v1", "file_refs": []any{file(1, "lighting")}}}, "supporting_file_refs": []any{file(2, "lighting_record")}}
	opened := OpenResult{Result: Result{ProjectID: testID(1), RevisionID: testID(2), Manifest: m, ManifestRef: ContentRef{testID(100), testID(500), testID(501)}, Invalidated: []string{}}, HeadRevisionID: testID(3)}
	r1 := opened
	r1.RevisionID = testID(3)
	r1.Manifest.Files = append([]File{}, m.Files...)
	r1.Manifest.Files[0].Content.VersionID = testID(900)
	projects := &publicationProjects{t: t, principal: p, snapshots: map[string]OpenResult{testID(2): opened, testID(3): r1}}
	content := &publicationContent{t: t, principal: p, data: map[ContentRef][]byte{}, access: map[ContentRef]Access{m.Files[0].Content: {ProjectID: testID(1), RevisionID: testID(2)}, r1.Manifest.Files[0].Content: {ProjectID: testID(1), RevisionID: testID(3)}, m.Files[3].Content: {ProjectID: testID(1), RevisionID: testID(2)}}}
	return &Service{projects: projects, content: content}, p, PublicationSourceRequest{ProjectID: testID(1), PreparedRevisionID: testID(3), CandidateKey: "hall-package", CommitID: testID(20)}, original, prepared, record
}
func TestPublicationSourceResolvesOnlyPersistedApprovedFixedScope(t *testing.T) {
	for _, kind := range []string{"valid", "not approved", "wrong commit", "changed source", "changed record", "extra selected file", "missing evidence", "denied", "foreign revision", "private scope"} {
		t.Run(kind, func(t *testing.T) {
			s, p, r, original, prepared, record := publicationSourceFixture(t)
			projects := s.projects.(*publicationProjects)
			content := s.content.(*publicationContent)
			candidate := objectOf(listOf(objectOf(prepared["ai_short_drama"])["world_candidates"])[0])
			request := candidate["registration_request"].(WorldRegisterRequest)
			switch kind {
			case "not approved":
				objectOf(candidate["review"])["state"] = "unreviewed"
			case "wrong commit":
				r.CommitID = testID(999)
			case "changed source":
				objectOf(candidate["source"])["key"] = "other"
			case "changed record":
				candidate["publication_record_file_id"] = testID(14)
			case "extra selected file":
				request.FileIDs = append(request.FileIDs, testID(10))
				candidate["registration_request"] = request
			case "missing evidence":
				record["supporting_file_refs"] = []any{}
			case "denied":
				projects.denied = true
			case "foreign revision":
				value := projects.snapshots[testID(2)]
				value.RevisionID = testID(999)
				projects.snapshots[testID(2)] = value
			case "private scope":
				request.SourceRevisionID = testID(999)
				candidate["registration_request"] = request
			}
			m := projects.snapshots[testID(2)].Manifest
			m1 := projects.snapshots[testID(3)].Manifest
			content.data[m.Files[0].Content] = publicationBytes(t, original)
			content.data[m1.Files[0].Content] = publicationBytes(t, prepared)
			content.data[m.Files[3].Content] = publicationBytes(t, record)
			out, err := s.PublicationSource(context.Background(), p, r)
			if kind == "valid" {
				normalized, _ := normalizeWorld(request)
				hash, _ := digest(normalized)
				raw := publicationBytes(t, out)
				if err != nil || out.RequestHash != hash || out.Request.SourceRevisionID != testID(2) || len(out.Versions) != 1 || out.Versions[0].Label != "L1 · Night" || out.Versions[0].FileIDs[0] != testID(11) || strings.Contains(string(raw), "PRIVATE_SOURCE_TEXT") || strings.Contains(string(raw), "manifest") {
					t.Fatal("fixed preview failed", err)
				}
			} else if err == nil {
				t.Fatal("invalid publication source accepted")
			}
		})
	}
}
func TestPublicationSourceHTTPRejectsUnknownFieldsAndPrincipal(t *testing.T) {
	s, p, r, _, _, _ := publicationSourceFixture(t)
	h, err := s.HTTPHandler(strings.Repeat("s", 32), strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/api/v1/world/publication_source", bytes.NewBufferString(`{"endpoint":"https://untrusted.invalid"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
		req.Header.Set("X-Organization-Id", p.OrganizationID)
		req.Header.Set("X-Request-Id", p.RequestID)
		if authorized {
			req.Header.Set("X-User-Id", p.SubjectID)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		want := 403
		if authorized {
			want = 400
		}
		if w.Code != want {
			t.Fatal("authority or shape bypass", w.Code)
		}
	}
	if _, err := s.PublicationSource(context.Background(), Principal{}, r); !errors.Is(err, ErrForbidden) {
		t.Fatal("invalid subject accepted")
	}
}
