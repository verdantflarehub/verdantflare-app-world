package world

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

// Generated from the central contracts; never edit the embedded projection.
//
//go:embed world_learning_schema.generated.json
var learningSchemaBundle []byte
var draftSchemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	var bundle map[string]any
	if err := json.Unmarshal(learningSchemaBundle, &bundle); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		re.MatchTimeout = 100 * time.Millisecond
		return schemaRegexp{re}, nil
	})
	for id, schema := range bundle {
		if err := c.AddResource(id, schema); err != nil {
			return nil, err
		}
		if err := c.AddResource(id+".schema.json", schema); err != nil {
			return nil, err
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, name := range []string{"world-draft-request", "world-draft-manifest", "world-draft-response", "reference-learning"} {
		s, err := c.Compile("station://contracts/v2/" + name)
		if err != nil {
			return nil, err
		}
		out[name] = s
	}
	return out, nil
})

func validateLearningJSON(data []byte, name string) error {
	clean, err := strictjson.ReadValue(bytes.NewReader(data), MaxManifestBytes)
	if err != nil {
		return ErrInvalid
	}
	var value any
	if json.Unmarshal(clean, &value) != nil {
		return ErrInvalid
	}
	schemas, err := draftSchemas()
	if err != nil {
		return ErrDependency
	}
	if schemas[name] == nil || schemas[name].Validate(value) != nil {
		return ErrInvalid
	}
	return nil
}

type DraftSourceRef struct {
	Kind           string `json:"kind"`
	DraftID        string `json:"draft_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	RevisionID     string `json:"revision_id,omitempty"`
	AssetID        string `json:"asset_id,omitempty"`
	AssetVersionID string `json:"asset_version_id,omitempty"`
	FileID         string `json:"file_id"`
	Purpose        string `json:"purpose"`
	ObjectKey      string `json:"object_key,omitempty"`
}
type DraftManifest struct {
	SchemaVersion   int              `json:"schema_version"`
	Kind            string           `json:"kind"`
	DraftID         string           `json:"draft_id"`
	Purpose         string           `json:"purpose"`
	Name            string           `json:"name"`
	Subjects        []string         `json:"subjects"`
	Files           []File           `json:"files"`
	DomainDocuments []DomainDocument `json:"domain_documents"`
	SourceRefs      []DraftSourceRef `json:"source_refs"`
	AssetRefs       []AssetRef       `json:"asset_refs"`
	RunRefs         []RunRef         `json:"run_refs"`
}
type DraftRequest struct {
	Mode               string         `json:"mode"`
	CommitID           string         `json:"commit_id,omitempty"`
	DraftID            string         `json:"draft_id,omitempty"`
	RevisionID         string         `json:"revision_id,omitempty"`
	ExpectedRevisionID string         `json:"expected_revision_id,omitempty"`
	Name               string         `json:"name,omitempty"`
	Purpose            string         `json:"purpose,omitempty"`
	Subjects           *[]string      `json:"subjects,omitempty"`
	Manifest           *DraftManifest `json:"manifest,omitempty"`
	Query              string         `json:"q,omitempty"`
	Subject            string         `json:"subject,omitempty"`
	Cursor             string         `json:"cursor,omitempty"`
	Limit              int            `json:"limit,omitempty"`
}
type DraftRevision struct {
	Kind           string        `json:"kind"`
	DraftID        string        `json:"draft_id"`
	RevisionID     string        `json:"revision_id"`
	HeadRevisionID string        `json:"head_revision_id"`
	CreatedAt      time.Time     `json:"created_at"`
	ManifestRef    ContentRef    `json:"manifest_ref"`
	Manifest       DraftManifest `json:"manifest"`
}
type DraftStatus struct {
	Kind              string         `json:"kind"`
	State             string         `json:"state"`
	RequestHash       string         `json:"request_sha256"`
	Result            *DraftRevision `json:"result,omitempty"`
	ErrorCode         string         `json:"error_code,omitempty"`
	CurrentRevisionID string         `json:"current_revision_id,omitempty"`
}
type DraftListItem struct {
	DraftID        string    `json:"draft_id"`
	HeadRevisionID string    `json:"head_revision_id"`
	Name           string    `json:"name"`
	Purpose        string    `json:"purpose"`
	Subjects       []string  `json:"subjects"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	HasContent     bool      `json:"has_content"`
}
type DraftList struct {
	Kind       string          `json:"kind"`
	Items      []DraftListItem `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}
type draftPlan struct {
	Manifest    DraftManifest          `json:"manifest"`
	WriteID     string                 `json:"write_id"`
	ManifestRef *ContentRef            `json:"manifest_ref,omitempty"`
	Refs        []ContentRef           `json:"refs"`
	Grants      coordination.Plan      `json:"grants,omitempty"`
	Operation   coordination.Operation `json:"operation"`
}
type draftCommit struct {
	DraftID, CommitID, Actor, Hash, Expected, Revision, State, Error, ConflictHead string
	Plan                                                                           draftPlan
	Result                                                                         *DraftRevision
}

// Kept separate from the legacy Artifact interface so old publication clients
// fail closed until this capability is provided, without changing their contract.
type DraftArtifact interface {
	WriteDraftManifest(context.Context, Principal, string, string, TextWrite) (ContentVersion, error)
}

func DecodeDraftRequest(data []byte) (DraftRequest, error) {
	var r DraftRequest
	if err := validateLearningJSON(data, "world-draft-request"); err != nil {
		return r, err
	}
	if json.Unmarshal(data, &r) != nil {
		return r, ErrInvalid
	}
	return normalizeDraftRequest(r)
}
func normalizeDraftRequest(r DraftRequest) (DraftRequest, error) {
	// Typed callers must pass the same exact mode-specific schema as HTTP callers.
	data, err := json.Marshal(r)
	if err != nil {
		return r, ErrInvalid
	}
	if err = validateLearningJSON(data, "world-draft-request"); err != nil {
		return r, err
	}
	if r.Subjects != nil {
		subjects := append([]string{}, (*r.Subjects)...)
		sort.Strings(subjects)
		r.Subjects = &subjects
	}
	if r.Manifest != nil {
		copyData, _ := json.Marshal(r.Manifest)
		var m DraftManifest
		if json.Unmarshal(copyData, &m) != nil {
			return r, ErrInvalid
		}
		if err = m.Validate(); err != nil {
			return r, err
		}
		if m.DraftID != r.DraftID {
			return r, ErrInvalid
		}
		sort.Strings(m.Subjects)
		sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].ID < m.Files[j].ID })
		sort.Slice(m.DomainDocuments, func(i, j int) bool { return m.DomainDocuments[i].Type < m.DomainDocuments[j].Type })
		sort.Slice(m.SourceRefs, func(i, j int) bool {
			a, _ := json.Marshal(m.SourceRefs[i])
			b, _ := json.Marshal(m.SourceRefs[j])
			return string(a) < string(b)
		})
		sort.Slice(m.AssetRefs, func(i, j int) bool {
			a, _ := json.Marshal(m.AssetRefs[i])
			b, _ := json.Marshal(m.AssetRefs[j])
			return string(a) < string(b)
		})
		sort.Slice(m.RunRefs, func(i, j int) bool {
			a, b := m.RunRefs[i], m.RunRefs[j]
			if a.ServiceID != b.ServiceID {
				return a.ServiceID < b.ServiceID
			}
			return a.RunID < b.RunID
		})
		r.Manifest = &m
	}
	if r.Mode == "create" && !validText(r.Name) {
		return r, ErrInvalid
	}
	return r, nil
}
func (m DraftManifest) Validate() error {
	data, err := json.Marshal(m)
	if err != nil {
		return ErrInvalid
	}
	if err = validateLearningJSON(data, "world-draft-manifest"); err != nil {
		return err
	}
	if !validText(m.Name) {
		return ErrInvalid
	}
	paths := map[string]bool{}
	files := map[string]bool{}
	for _, f := range m.Files {
		if PortablePath(f.Path) != nil || files[f.ID] || paths[PathKey(f.Path)] {
			return ErrInvalid
		}
		files[f.ID] = true
		paths[PathKey(f.Path)] = true
	}
	docs := map[string]bool{}
	for _, d := range m.DomainDocuments {
		if !files[d.FileID] || docs[d.Type] {
			return ErrInvalid
		}
		docs[d.Type] = true
	}
	refs := map[DraftSourceRef]bool{}
	for _, v := range m.SourceRefs {
		if !validText(v.Purpose) || refs[v] {
			return ErrInvalid
		}
		refs[v] = true
	}
	assets := map[AssetRef]bool{}
	for _, v := range m.AssetRefs {
		if !validText(v.Purpose) || assets[v] {
			return ErrInvalid
		}
		assets[v] = true
	}
	runs := map[RunRef]bool{}
	for _, v := range m.RunRefs {
		if runs[v] {
			return ErrInvalid
		}
		runs[v] = true
	}
	return nil
}
func emptyDraft(id string, r DraftRequest) DraftManifest {
	return DraftManifest{SchemaVersion: 1, Kind: "world-draft", DraftID: id, Purpose: r.Purpose, Name: r.Name, Subjects: append([]string{}, (*r.Subjects)...), Files: []File{}, DomainDocuments: []DomainDocument{}, SourceRefs: []DraftSourceRef{}, AssetRefs: []AssetRef{}, RunRefs: []RunRef{}}
}

type draftCursor struct {
	After   string `json:"after"`
	Binding string `json:"binding"`
}

func draftCursorBinding(p Principal, r DraftRequest) string {
	return coordination.Fingerprint([]string{p.OrganizationID, p.SubjectID, r.Purpose, r.Query, r.Subject})
}
func decodeDraftCursor(p Principal, r DraftRequest) (string, error) {
	if r.Cursor == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(r.Cursor)
	if err != nil {
		return "", ErrInvalid
	}
	var c draftCursor
	if strictjson.Decode(bytes.NewReader(data), 2048, &c) != nil || !ValidID(c.After) || c.Binding != draftCursorBinding(p, r) {
		return "", ErrInvalid
	}
	return c.After, nil
}
func encodeDraftCursor(p Principal, r DraftRequest, after string) string {
	data, _ := json.Marshal(draftCursor{after, draftCursorBinding(p, r)})
	return base64.RawURLEncoding.EncodeToString(data)
}
