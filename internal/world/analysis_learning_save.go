package world

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

type AnalysisArtifact interface {
	WriteAnalysisDocument(context.Context, Principal, string, TextWrite) (ContentVersion, error)
}

type AnalysisLearningSave struct {
	TaskID    string         `json:"task_id"`
	State     string         `json:"state"`
	Revision  *DraftRevision `json:"revision,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
}

type analysisLearningSaveRecord struct {
	Version, Hash, CreateID, WriteID, CommitID, FileID string
	AnalysisLearningSave
}

func analysisDocumentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func analysisDocumentSource(task string) Source {
	return Source{Kind: "task_output", ServiceID: "world", RunID: "analysis-v1:" + task, TaskID: task}
}
func isAnalysisDocumentSource(source Source) bool {
	return ValidID(source.TaskID) && source == analysisDocumentSource(source.TaskID)
}

func (s *Service) loadAnalysisLearningSave(ctx context.Context, p Principal, id string) (analysisLearningSaveRecord, error) {
	r := analysisLearningSaveRecord{AnalysisLearningSave: AnalysisLearningSave{TaskID: id}}
	if !p.Valid() || !ValidID(id) {
		return r, ErrForbidden
	}
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT assembly_version,document_sha256,create_id::text,write_id::text,commit_id::text,file_id::text,state,result,COALESCE(error_code,'') FROM studio.world_analysis_learning WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, p.OrganizationID, p.SubjectID, id).Scan(&r.Version, &r.Hash, &r.CreateID, &r.WriteID, &r.CommitID, &r.FileID, &r.State, &raw, &r.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if len(raw) > 0 {
		r.Revision = &DraftRevision{}
		if json.Unmarshal(raw, r.Revision) != nil {
			return r, ErrNotReady
		}
	}
	return r, nil
}

// Verify before calling Artifact; it is a byte proxy, not a business authorizer.
// No source label or original model call ID alone can attest this output.
func (s *Service) checkAnalysisOutput(ctx context.Context, p Principal, v ContentVersion) error {
	if !isAnalysisDocumentSource(v.Source) || v.OrganizationID != p.OrganizationID || v.CreatedBy != p.SubjectID || !v.ContentRef.Valid() || v.MIME != "application/json" {
		return ErrForbidden
	}
	r, err := s.loadAnalysisLearningSave(ctx, p, v.Source.TaskID)
	if err != nil {
		return err
	}
	if r.Version != "1" || (r.State != "pending" && r.State != "saved") {
		return ErrNotReady
	}
	expected, err := s.AnalysisLearningDocument(ctx, p, v.Source.TaskID)
	if err != nil {
		return err
	}
	if r.Hash != analysisDocumentHash(expected) || v.SHA256 != r.Hash || v.Size != int64(len(expected)) {
		return ErrNotReady
	}
	data, err := s.content.Read(ctx, p, v.ContentRef, Access{}, MaxManifestBytes)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expected) {
		return ErrNotReady
	}
	return nil
}

func (s *Service) finishAnalysisLearningSave(ctx context.Context, p Principal, r analysisLearningSaveRecord, state, code string, revision *DraftRevision) (AnalysisLearningSave, error) {
	var raw []byte
	if revision != nil {
		var err error
		raw, err = json.Marshal(revision)
		if err != nil {
			return AnalysisLearningSave{}, err
		}
	}
	_, err := s.db.Exec(ctx, `UPDATE studio.world_analysis_learning SET state=$1,error_code=NULLIF($2,''),result=$3,updated_at=now() WHERE organization_id=$4 AND owner_id=$5 AND task_id=$6 AND state='pending' AND document_sha256=$7`, state, code, raw, p.OrganizationID, p.SubjectID, r.TaskID, r.Hash)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	saved, err := s.loadAnalysisLearningSave(ctx, p, r.TaskID)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	if saved.State != state || saved.ErrorCode != code || learningJSONKey(saved.Revision) != learningJSONKey(revision) {
		return AnalysisLearningSave{}, ErrIdempotency
	}
	return saved.AnalysisLearningSave, nil
}

func (s *Service) learningCommitFailure(ctx context.Context, p Principal, r analysisLearningSaveRecord, err error) (AnalysisLearningSave, error) {
	// A dependency outage is retryable. A known terminal commit is not replaced
	// by a new operation, even if its original response was lost.
	if errors.Is(err, ErrConflict) {
		return s.finishAnalysisLearningSave(ctx, p, r, "conflict", "REVISION_CONFLICT", nil)
	}
	for _, id := range []string{r.CreateID, r.CommitID} {
		status, e := loadDraftCommit(ctx, s.db, p, id)
		if e == nil && (status.State == "failed" || status.State == "conflict") {
			state := "failed"
			if status.State == "conflict" {
				state = "conflict"
			}
			code := status.Error
			if code == "" {
				code = "REVISION_CONFLICT"
			}
			return s.finishAnalysisLearningSave(ctx, p, r, state, code, nil)
		}
	}
	return r.AnalysisLearningSave, err
}

// SaveAnalysisLearning resumes one frozen save, regardless of inference
// dispatch. The learning revision remains unreviewed and is not published.
func (s *Service) SaveAnalysisLearning(ctx context.Context, p Principal, id string) (AnalysisLearningSave, error) {
	if !p.Valid() || !ValidID(id) {
		return AnalysisLearningSave{}, ErrForbidden
	}
	task, err := (analysis.PGStore{DB: s.db}).Task(ctx, p.OrganizationID, p.SubjectID, id)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	if !task.SaveLearning || !task.IncludeTypedResults {
		return AnalysisLearningSave{}, ErrNotReady
	}
	data, err := s.AnalysisLearningDocument(ctx, p, id)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	doc, err := ValidateLearningContent(data, nil)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	hash := analysisDocumentHash(data)
	_, err = s.db.Exec(ctx, `INSERT INTO studio.world_analysis_learning(organization_id,owner_id,task_id,assembly_version,document_sha256,create_id,write_id,commit_id,file_id) VALUES($1,$2,$3,'1',$4,$5,$6,$7,$8) ON CONFLICT(organization_id,owner_id,task_id) DO NOTHING`, p.OrganizationID, p.SubjectID, id, hash, newID(), newID(), newID(), newID())
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	r, err := s.loadAnalysisLearningSave(ctx, p, id)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	if r.Version != "1" || r.Hash != hash {
		return AnalysisLearningSave{}, ErrIdempotency
	}
	if r.State == "conflict" || r.State == "failed" {
		return r.AnalysisLearningSave, nil
	}
	if r.State == "saved" {
		return s.readSavedAnalysisLearning(ctx, p, r, data)
	}
	writer, ok := s.content.(AnalysisArtifact)
	if !ok {
		return r.AnalysisLearningSave, ErrDependency
	}
	subjects := []string{"reference"}
	created, err := s.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: r.CreateID, Name: "作品拆解成果", Purpose: "learning", Subjects: &subjects})
	if err != nil {
		return s.learningCommitFailure(ctx, p, r, err)
	}
	initial, ok := created.(DraftRevision)
	if !ok {
		return r.AnalysisLearningSave, ErrDependency
	}
	current, err := s.AnalysisLearningDocument(ctx, p, id)
	if err != nil {
		return r.AnalysisLearningSave, err
	}
	if !bytes.Equal(current, data) {
		return r.AnalysisLearningSave, ErrIdempotency
	}
	version, err := writer.WriteAnalysisDocument(ctx, p, id, TextWrite{WriteID: r.WriteID, FileID: r.FileID, Text: string(data), MIME: "application/json"})
	if err != nil {
		return r.AnalysisLearningSave, err
	}
	if version.Source != analysisDocumentSource(id) {
		return r.AnalysisLearningSave, ErrNotReady
	}
	if err = s.checkAnalysisOutput(ctx, p, version); err != nil {
		return r.AnalysisLearningSave, err
	}
	manifest := initial.Manifest
	manifest.Files = []File{{ID: r.FileID, Path: "learning/reference-learning.json", Role: "document", Content: version.ContentRef}}
	manifest.DomainDocuments = []DomainDocument{{Type: "reference-learning", FileID: r.FileID}}
	manifest.SourceRefs = doc.RemoteRefs
	manifest.RunRefs = doc.RunRefs
	committed, err := s.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: initial.DraftID, ExpectedRevisionID: initial.RevisionID, CommitID: r.CommitID, Manifest: &manifest})
	if err != nil {
		return s.learningCommitFailure(ctx, p, r, err)
	}
	revision, ok := committed.(DraftRevision)
	if !ok {
		return r.AnalysisLearningSave, ErrDependency
	}
	r.Revision = &revision
	// Read fixed bytes before recording a successful terminal save. A lost
	// readback leaves the original create/write/commit identities recoverable.
	if _, err = s.readSavedAnalysisLearning(ctx, p, r, data); err != nil {
		return r.AnalysisLearningSave, err
	}
	return s.finishAnalysisLearningSave(ctx, p, r, "saved", "", &revision)
}

func (s *Service) readSavedAnalysisLearning(ctx context.Context, p Principal, r analysisLearningSaveRecord, expected []byte) (AnalysisLearningSave, error) {
	if r.Revision == nil || !ValidID(r.Revision.DraftID) || !ValidID(r.Revision.RevisionID) {
		return AnalysisLearningSave{}, ErrNotReady
	}
	current, err := s.getDraft(ctx, p, r.Revision.DraftID, r.Revision.RevisionID)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	if learningJSONKey(current.Manifest) != learningJSONKey(r.Revision.Manifest) || current.ManifestRef != r.Revision.ManifestRef || len(current.Manifest.Files) != 1 || current.Manifest.Files[0].ID != r.FileID {
		return AnalysisLearningSave{}, ErrNotReady
	}
	file := current.Manifest.Files[0]
	access := Access{WorldDraftID: current.DraftID, WorldDraftRevisionID: current.RevisionID}
	v, err := s.content.Metadata(ctx, p, file.Content, access)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	if v.ContentRef != file.Content || v.Source != analysisDocumentSource(r.TaskID) {
		return AnalysisLearningSave{}, ErrNotReady
	}
	if err = s.checkAnalysisOutput(ctx, p, v); err != nil {
		return AnalysisLearningSave{}, err
	}
	data, err := s.content.Read(ctx, p, file.Content, access, MaxManifestBytes)
	if err != nil {
		return AnalysisLearningSave{}, err
	}
	if !bytes.Equal(data, expected) {
		return AnalysisLearningSave{}, ErrNotReady
	}
	return r.AnalysisLearningSave, nil
}
