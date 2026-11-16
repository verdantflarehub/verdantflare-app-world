package world

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPackageReferenceHTTPAndMCPKeepAuthorization(t *testing.T) {
	for _, transport := range []string{"http", "mcp"} {
		for _, denied := range []bool{false, true} {
			t.Run(transport+map[bool]string{false: "_allowed", true: "_denied"}[denied], func(t *testing.T) {
				s, p, input, content, projects := packageReferenceFixture(t)
				projects.denied = denied
				token := strings.Repeat("x", 32)
				handler, err := s.HTTPHandler(token)
				body, _ := json.Marshal(input)
				path := "/api/v1/world/draft"
				if transport == "mcp" {
					p.ReviewOrigin = "agent"
					content.p, projects.principal = p, p
					handler, err = s.MCPHandler(token)
					path = "/mcp"
					body, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "world.draft", "arguments": input}})
				}
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-User-Id", p.SubjectID)
				req.Header.Set("X-Organization-Id", p.OrganizationID)
				req.Header.Set("X-Request-Id", p.RequestID)
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, req)
				if denied {
					if out.Code != 403 || content.metadata != 0 || content.reads != 0 {
						t.Fatal("denied reference reached content", out.Code)
					}
				} else if out.Code != 200 || content.metadata != 1 || content.reads != 0 || !strings.Contains(out.Body.String(), `"kind":"reference"`) {
					t.Fatal("reference transport failed", out.Code, out.Body.String())
				}
			})
		}
	}
}

type packageReferenceContent struct {
	Artifact
	t        *testing.T
	p        Principal
	access   Access
	ref      ContentRef
	version  ContentVersion
	denied   bool
	reads    int
	metadata int
	data     []byte
}

func (c *packageReferenceContent) Metadata(_ context.Context, p Principal, ref ContentRef, access Access) (ContentVersion, error) {
	c.metadata++
	if p != c.p || ref != c.ref || access != c.access {
		c.t.Fatal("reference scope changed")
	}
	if c.denied {
		return ContentVersion{}, ErrForbidden
	}
	return c.version, nil
}
func (c *packageReferenceContent) Read(_ context.Context, p Principal, ref ContentRef, access Access, limit int64) ([]byte, error) {
	c.reads++
	if p != c.p || ref != c.ref || access != c.access || limit != MaxManifestBytes {
		c.t.Fatal("object read scope changed")
	}
	if c.denied {
		return nil, ErrForbidden
	}
	return c.data, nil
}
func packageReferenceFixture(t *testing.T) (*Service, Principal, DraftRequest, *packageReferenceContent, *publicationProjects) {
	t.Helper()
	p := Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32)}
	source := sourceFixture()
	projects := &publicationProjects{t: t, principal: p, snapshots: map[string]OpenResult{source.RevisionID: source}}
	ref := source.Manifest.Files[0].Content
	content := &packageReferenceContent{t: t, p: p, ref: ref, access: Access{ProjectID: source.ProjectID, RevisionID: source.RevisionID}, version: ContentVersion{SchemaVersion: 2, ContentRef: ref, OrganizationID: p.OrganizationID, Size: 123, MIME: "application/json", SHA256: strings.Repeat("a", 64)}}
	r := DraftRequest{Mode: "resolve_reference", Reference: &DraftSourceRef{Kind: "project", ProjectID: source.ProjectID, RevisionID: source.RevisionID, FileID: source.Manifest.Files[0].ID, Purpose: "learning_source"}}
	return &Service{content: content, projects: projects}, p, r, content, projects
}
func TestPackageReferenceReadsOnlyOneAuthorizedFixedFile(t *testing.T) {
	s, p, r, c, _ := packageReferenceFixture(t)
	raw, err := s.WorldDraft(t.Context(), p, r)
	if err != nil {
		t.Fatal(err)
	}
	got := raw.(PackageReference)
	if got.Reference != *r.Reference || got.Content != c.ref || got.Size != 123 || c.metadata != 1 || c.reads != 0 {
		t.Fatal(got, c.metadata, c.reads)
	}
	data, _ := json.Marshal(got)
	if validateLearningJSON(data, "world-draft-response") != nil {
		t.Fatal("response differs from central schema")
	}
	for _, forbidden := range []string{"private.md", "Source", "token", "access", "files", "organization_id"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("extra source details exposed", forbidden)
		}
	}
}
func TestPackageReferenceClosedRequestAndCurrentPermissions(t *testing.T) {
	for _, scenario := range []string{"principal", "project_denied", "content_denied", "revision", "file", "wrong_revision", "wrong_content", "wrong_org", "bad_hash", "extra_request"} {
		t.Run(scenario, func(t *testing.T) {
			s, p, r, c, projects := packageReferenceFixture(t)
			switch scenario {
			case "principal":
				p.SubjectID = ""
			case "project_denied":
				projects.denied = true
			case "content_denied":
				c.denied = true
			case "revision":
				r.Reference.RevisionID = ""
			case "file":
				r.Reference.FileID = testID(99)
			case "wrong_revision":
				source := projects.snapshots[r.Reference.RevisionID]
				source.RevisionID = testID(99)
				projects.snapshots[r.Reference.RevisionID] = source
			case "wrong_content":
				c.version.VersionID = testID(99)
			case "wrong_org":
				c.version.OrganizationID = testID(99)
			case "bad_hash":
				c.version.SHA256 = "bad"
			case "extra_request":
				r.CommitID = testID(99)
			}
			got, err := s.WorldDraft(t.Context(), p, r)
			if err == nil {
				t.Fatal("accepted", got)
			}
			if scenario == "project_denied" && c.metadata != 0 {
				t.Fatal("read before project authorization")
			}
		})
	}
}
func TestPackageReferenceObjectMustExistInValidatedDocument(t *testing.T) {
	s, p, r, c, _ := packageReferenceFixture(t)
	c.data = []byte(`{"schema_version":1,"document_kind":"reference-learning","origin":"manual","sources":[{"key":"source","title":"Original","media_kind":"text","role":"original","acquisition_state":"ready","content":{"kind":"inline","text":"original bytes"}}],"results":[],"coverage":[],"relations":[],"uses":[]}`)
	r.Reference.ObjectKey = "source"
	if _, err := s.WorldDraft(t.Context(), p, r); err != nil {
		t.Fatal(err)
	}
	if c.reads != 1 {
		t.Fatal("object not read")
	}
	r.Reference.ObjectKey = "missing"
	if _, err := s.WorldDraft(t.Context(), p, r); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing object accepted", err)
	}
	c.data = []byte(`{"schema_version":99}`)
	if _, err := s.WorldDraft(t.Context(), p, r); err == nil {
		t.Fatal("unknown document accepted")
	}
}
