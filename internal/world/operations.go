package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalid           = errors.New("INVALID_ARGUMENT")
	ErrForbidden         = errors.New("PERMISSION_DENIED")
	ErrNotFound          = errors.New("NOT_FOUND")
	ErrConflict          = errors.New("REVISION_CONFLICT")
	ErrIdempotency       = errors.New("IDEMPOTENCY_CONFLICT")
	ErrNotReady          = errors.New("CONTENT_NOT_READY")
	ErrDependency        = errors.New("DEPENDENCY_UNAVAILABLE")
	ErrEmptySetHasActors = errors.New("EMPTY_SET_HAS_ACTORS")
)

type ConflictError struct{ CurrentRevisionID string }

func (e *ConflictError) Error() string { return ErrConflict.Error() }
func (e *ConflictError) Unwrap() error { return ErrConflict }

func newID() string { return uuid.Must(uuid.NewV7()).String() }
func digest(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

type Principal struct {
	OrganizationID string `json:"organization_id"`
	SubjectID      string `json:"subject_id"`
	RequestID      string `json:"request_id"`
	ReviewOrigin   string `json:"-"`
}

func (p Principal) Valid() bool {
	return ValidID(p.OrganizationID) && ValidID(p.SubjectID) && ValidID(p.RequestID)
}

type CreateRequest struct {
	CommitID  string `json:"commit_id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	EntryPath string `json:"entry_path"`
	EntryText string `json:"entry_text"`
}
type MetadataChange struct {
	Name            *string `json:"name,omitempty"`
	Category        *string `json:"category,omitempty"`
	Status          *string `json:"status,omitempty"`
	EntryDocumentID *string `json:"entry_document_id,omitempty"`
}
type FileUpdate struct {
	ID      string      `json:"file_id,omitempty"`
	Path    string      `json:"path"`
	Role    string      `json:"role"`
	Content *ContentRef `json:"content_ref,omitempty"`
	Text    *string     `json:"text,omitempty"`
	MIME    string      `json:"mime,omitempty"`
}
type Changes struct {
	Metadata        *MetadataChange   `json:"set_metadata,omitempty"`
	UpsertFiles     *[]FileUpdate     `json:"upsert_files,omitempty"`
	RemoveFileIDs   *[]string         `json:"remove_file_ids,omitempty"`
	Selections      *[]Selection      `json:"set_selections,omitempty"`
	AssetRefs       *[]AssetRef       `json:"set_asset_refs,omitempty"`
	RunRefs         *[]RunRef         `json:"set_run_refs,omitempty"`
	DomainDocuments *[]DomainDocument `json:"set_domain_documents,omitempty"`
}
type CommitRequest struct {
	ProjectID          string    `json:"project_id"`
	ExpectedRevisionID string    `json:"expected_revision_id"`
	CommitID           string    `json:"commit_id"`
	Changes            *Changes  `json:"changes,omitempty"`
	Manifest           *Manifest `json:"manifest,omitempty"`
	Reselected         []string  `json:"reselected_purposes,omitempty"`
}
type Result struct {
	ProjectID   string     `json:"project_id"`
	RevisionID  string     `json:"revision_id"`
	ManifestRef ContentRef `json:"manifest_ref"`
	Manifest    Manifest   `json:"manifest"`
	Invalidated []string   `json:"invalidated_selections"`
}
type OpenResult struct {
	Result
	HeadRevisionID string `json:"head_revision_id"`
}
type CommitStatus struct {
	State             string  `json:"state"`
	RequestHash       string  `json:"request_sha256"`
	Result            *Result `json:"result,omitempty"`
	ErrorCode         string  `json:"error_code,omitempty"`
	CurrentRevisionID string  `json:"current_revision_id,omitempty"`
}
type ListRequest struct {
	RequireWrite bool   `json:"require_write,omitempty"`
	Category     string `json:"category,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}
type ListItem struct {
	ProjectID      string    `json:"project_id"`
	HeadRevisionID string    `json:"head_revision_id"`
	Name           string    `json:"name"`
	Category       string    `json:"category"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}
type ListResult struct {
	Items      []ListItem `json:"items"`
	NextCursor string     `json:"next_cursor"`
}

type Source struct {
	WorldDraftID         string       `json:"world_draft_id,omitempty"`
	WorldDraftRevisionID string       `json:"world_draft_revision_id,omitempty"`
	Kind                 string       `json:"kind"`
	ProjectID            string       `json:"project_id,omitempty"`
	ServiceID            string       `json:"service_id,omitempty"`
	RunID                string       `json:"run_id,omitempty"`
	TaskID               string       `json:"task_id,omitempty"`
	Original             *OriginalRef `json:"original_ref,omitempty"`
	AssetID              string       `json:"asset_id,omitempty"`
	AssetVersionID       string       `json:"asset_version_id,omitempty"`
}
type OriginalRef struct {
	ServiceID  string `json:"service_id"`
	ArtifactID string `json:"artifact_id"`
	VersionID  string `json:"version_id"`
	TaskID     string `json:"task_id,omitempty"`
}
type ContentVersion struct {
	SchemaVersion int `json:"schema_version"`
	ContentRef
	OrganizationID string    `json:"organization_id"`
	SHA256         string    `json:"sha256"`
	Size           int64     `json:"size"`
	MIME           string    `json:"mime"`
	Source         Source    `json:"source"`
	VersionNo      int64     `json:"version_no"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}
type Access struct {
	WorldDraftID         string `json:"world_draft_id,omitempty"`
	WorldDraftRevisionID string `json:"world_draft_revision_id,omitempty"`
	ProjectID            string `json:"project_id,omitempty"`
	RevisionID           string `json:"project_revision_id,omitempty"`
	AssetID              string `json:"asset_id,omitempty"`
	AssetVersionID       string `json:"asset_version_id,omitempty"`
}
type RetentionOwner struct {
	Kind     string `json:"owner_kind"`
	ID       string `json:"owner_id"`
	CommitID string `json:"commit_id"`
}
type TextWrite struct {
	WriteID string      `json:"write_id"`
	FileID  string      `json:"file_id"`
	Text    string      `json:"text"`
	MIME    string      `json:"mime"`
	Content *ContentRef `json:"content_ref,omitempty"`
}
type Artifact interface {
	Write(context.Context, Principal, string, TextWrite) (ContentVersion, error)
	WriteAssetManifest(context.Context, Principal, string, string, TextWrite) (ContentVersion, error)
	Metadata(context.Context, Principal, ContentRef, Access) (ContentVersion, error)
	Read(context.Context, Principal, ContentRef, Access, int64) ([]byte, error)
	Retain(context.Context, Principal, RetentionOwner, []ContentRef) error
}

// AssetResolver must enforce current World grants and return the exact pinned
// asset manifest and files for retention. Missing integration fails closed.
type AssetResolver interface {
	References(context.Context, Principal, AssetRef) ([]ContentRef, error)
}
type preparePlan struct {
	Manifest        Manifest     `json:"manifest"`
	Writes          []TextWrite  `json:"writes"`
	ManifestWriteID string       `json:"manifest_write_id"`
	ManifestRef     *ContentRef  `json:"manifest_ref,omitempty"`
	Refs            []ContentRef `json:"refs"`
	Invalidated     []string     `json:"invalidated_selections"`
}
