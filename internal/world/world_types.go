package world

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type AssetSource struct {
	Kind            string `json:"kind,omitempty"`
	DraftID         string `json:"draft_id,omitempty"`
	DraftRevisionID string `json:"draft_revision_id,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	RevisionID      string `json:"project_revision_id,omitempty"`
	Relation        string `json:"relation"`
	ReviewFileID    string `json:"review_file_id,omitempty"`
}
type AssetManifest struct {
	DomainDocuments *[]DomainDocument          `json:"domain_documents,omitempty"`
	SchemaVersion   int                        `json:"schema_version"`
	Kind            string                     `json:"kind"`
	AssetID         string                     `json:"asset_id"`
	Name            string                     `json:"name"`
	AssetType       string                     `json:"asset_type"`
	Subjects        []string                   `json:"subjects"`
	Files           []File                     `json:"files"`
	Source          AssetSource                `json:"source"`
	DependsOn       []AssetRef                 `json:"depends_on"`
	Extensions      map[string]json.RawMessage `json:"extensions,omitempty"`
}
type WorldRegisterRequest struct {
	CommitID          string      `json:"commit_id"`
	SourceProjectID   string      `json:"source_project_id,omitempty"`
	SourceDraftID     string      `json:"source_draft_id,omitempty"`
	SourceRevisionID  string      `json:"source_revision_id"`
	FileIDs           []string    `json:"file_ids"`
	Name              string      `json:"name"`
	AssetType         string      `json:"asset_type"`
	Subjects          []string    `json:"subjects"`
	Relation          string      `json:"relation"`
	AssetID           string      `json:"asset_id,omitempty"`
	ExpectedVersionID string      `json:"expected_asset_version_id,omitempty"`
	ReviewFileID      string      `json:"review_file_id,omitempty"`
	DependsOn         *[]AssetRef `json:"depends_on,omitempty"`
}
type WorldResult struct {
	AssetID     string        `json:"asset_id"`
	VersionID   string        `json:"asset_version_id"`
	ManifestRef ContentRef    `json:"manifest_ref"`
	Manifest    AssetManifest `json:"manifest"`
}
type WorldGetRequest struct {
	AssetID   string `json:"asset_id"`
	VersionID string `json:"asset_version_id"`
}
type WorldListRequest struct {
	AssetType string `json:"asset_type,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}
type WorldListItem struct {
	AssetID       string    `json:"asset_id"`
	HeadVersionID string    `json:"head_asset_version_id"`
	Name          string    `json:"name"`
	AssetType     string    `json:"asset_type"`
	Subjects      []string  `json:"subjects"`
	CreatedAt     time.Time `json:"created_at"`
}
type WorldListResult struct {
	Items      []WorldListItem `json:"items"`
	NextCursor string          `json:"next_cursor"`
}
type WorldStatusRequest struct {
	AssetID  string `json:"asset_id,omitempty"`
	CommitID string `json:"commit_id"`
}
type WorldStatus struct {
	State            string       `json:"state"`
	RequestHash      string       `json:"request_sha256"`
	Result           *WorldResult `json:"result,omitempty"`
	ErrorCode        string       `json:"error_code,omitempty"`
	CurrentVersionID string       `json:"current_asset_version_id,omitempty"`
}
type WorldGrantRequest struct {
	AssetID   string `json:"asset_id"`
	SubjectID string `json:"subject_id"`
	Role      string `json:"role"`
}
type WorldRevokeRequest struct {
	AssetID   string `json:"asset_id"`
	SubjectID string `json:"subject_id"`
}
type WorldGrantResult struct {
	AssetID   string `json:"asset_id"`
	SubjectID string `json:"subject_id"`
	Role      string `json:"role"`
}
type UseAssetRequest struct {
	ProjectID          string `json:"project_id"`
	ExpectedRevisionID string `json:"expected_revision_id"`
	CommitID           string `json:"commit_id"`
	AssetID            string `json:"asset_id"`
	VersionID          string `json:"asset_version_id"`
	Purpose            string `json:"purpose"`
}
type WorldConflictError struct{ CurrentVersionID string }

func (e *WorldConflictError) Error() string { return ErrConflict.Error() }
func (e *WorldConflictError) Unwrap() error { return ErrConflict }

type worldPlan struct {
	Manifest    AssetManifest           `json:"manifest"`
	SourceRef   ContentRef              `json:"source_ref"`
	WriteID     string                  `json:"write_id"`
	ManifestRef *ContentRef             `json:"manifest_ref,omitempty"`
	Refs        []ContentRef            `json:"refs"`
	Grants      coordination.Plan       `json:"grants,omitempty"`
	Operation   *coordination.Operation `json:"operation,omitempty"`
}

func (m AssetManifest) Validate() error {
	if (m.SchemaVersion != 1 && m.SchemaVersion != 2) || m.Kind != "asset-version" || !ValidID(m.AssetID) || !validText(m.Name) || !tokenPattern.MatchString(m.AssetType) || m.Subjects == nil || m.DependsOn == nil || len(m.DependsOn) > MaxFiles || len(m.Subjects) > 256 || len(m.Files) == 0 || len(m.Files) > MaxFiles {
		return ErrInvalid
	}
	if (m.Source.Relation != "produced_in" && m.Source.Relation != "curated_in") || (m.Source.ReviewFileID != "" && !ValidID(m.Source.ReviewFileID)) {
		return ErrInvalid
	}
	switch m.SchemaVersion {
	case 1:
		if !ValidID(m.Source.ProjectID) || !ValidID(m.Source.RevisionID) || m.Source.Kind != "" || m.Source.DraftID != "" || m.Source.DraftRevisionID != "" || m.DomainDocuments != nil {
			return ErrInvalid
		}
	case 2:
		if m.Source.Kind != "world_draft" || !ValidID(m.Source.DraftID) || !ValidID(m.Source.DraftRevisionID) || m.Source.ProjectID != "" || m.Source.RevisionID != "" || m.DomainDocuments == nil || *m.DomainDocuments == nil || len(*m.DomainDocuments) > 256 {
			return ErrInvalid
		}
	}
	subjects := map[string]bool{}
	for _, v := range m.Subjects {
		if !validText(v) || subjects[v] {
			return ErrInvalid
		}
		subjects[v] = true
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	for _, f := range m.Files {
		key := PathKey(f.Path)
		if !ValidID(f.ID) || !f.Content.Valid() || !tokenPattern.MatchString(f.Role) || PortablePath(f.Path) != nil || ids[f.ID] || paths[key] {
			return ErrInvalid
		}
		ids[f.ID] = true
		paths[key] = true
	}
	if m.DomainDocuments != nil {
		docs := map[string]bool{}
		for _, d := range *m.DomainDocuments {
			if !ids[d.FileID] || !tokenPattern.MatchString(d.Type) || docs[d.Type] {
				return ErrInvalid
			}
			docs[d.Type] = true
		}
	}
	seen := map[AssetRef]bool{}
	for _, a := range m.DependsOn {
		if !ValidID(a.AssetID) || !ValidID(a.VersionID) || !validText(a.Purpose) || seen[a] {
			return ErrInvalid
		}
		seen[a] = true
	}
	for key := range m.Extensions {
		if !extensionPattern.MatchString(key) {
			return ErrInvalid
		}
	}
	return nil
}
func decodeAsset(data []byte) (AssetManifest, error) {
	var m AssetManifest
	if len(data) > MaxManifestBytes || !utf8.Valid(data) {
		return m, ErrInvalid
	}
	scan := json.NewDecoder(bytes.NewReader(data))
	scan.UseNumber()
	if uniqueJSON(scan, 0) != nil {
		return m, ErrInvalid
	}
	if _, e := scan.Token(); e != io.EOF {
		return m, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return m, ErrInvalid
	}
	var ext map[string]json.RawMessage
	if raw, ok := fields["extensions"]; ok {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &ext) != nil {
			return m, ErrInvalid
		}
		delete(fields, "extensions")
		data, _ = json.Marshal(fields)
	}
	if strictjson.Decode(bytes.NewReader(data), MaxManifestBytes, &m) != nil {
		return m, ErrInvalid
	}
	m.Extensions = ext
	return m, m.Validate()
}
