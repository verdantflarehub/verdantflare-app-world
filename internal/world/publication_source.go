package world

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type PublicationSourceRequest struct {
	ProjectID          string `json:"project_id"`
	PreparedRevisionID string `json:"prepared_revision_id"`
	CandidateKey       string `json:"candidate_key"`
	CommitID           string `json:"commit_id"`
}
type PublicationVersion struct {
	VersionKey string   `json:"version_key"`
	Label      string   `json:"label"`
	FileIDs    []string `json:"file_ids"`
}
type PublicationSourceResult struct {
	PublicationSourceRequest
	Request     WorldRegisterRequest `json:"request"`
	RequestHash string               `json:"request_sha256"`
	Versions    []PublicationVersion `json:"versions"`
}

var publicationKey = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (r PublicationSourceRequest) valid() bool {
	return ValidID(r.ProjectID) && ValidID(r.PreparedRevisionID) && ValidID(r.CommitID) && publicationKey.MatchString(r.CandidateKey)
}
func publicationCandidate(d contentObject, key string) (contentObject, error) {
	var found contentObject
	for _, value := range listOf(d["world_candidates"]) {
		candidate := objectOf(value)
		if str(candidate["key"]) == key {
			if found != nil {
				return nil, ErrInvalid
			}
			found = candidate
		}
	}
	if found == nil || objectOf(found["review"])["state"] != "approved" || !ValidID(str(found["publication_record_file_id"])) {
		return nil, ErrInvalid
	}
	return found, nil
}
func preparedPublication(c contentObject, r PublicationSourceRequest) (WorldRegisterRequest, error) {
	var request WorldRegisterRequest
	raw, err := json.Marshal(c["registration_request"])
	if err != nil || strictjson.Decode(bytes.NewReader(raw), MaxManifestBytes, &request) != nil {
		return request, ErrInvalid
	}
	request, err = normalizeWorld(request)
	if err != nil || request.CommitID != r.CommitID || request.SourceProjectID != r.ProjectID || request.ReviewFileID != str(objectOf(c["review"])["record_file_id"]) {
		return request, ErrInvalid
	}
	return request, nil
}
func (s *Service) publicationSnapshot(ctx context.Context, p Principal, projectID, revisionID string) (OpenResult, contentObject, error) {
	opened, err := s.Open(ctx, p, projectID, revisionID)
	if err != nil {
		return OpenResult{}, nil, err
	}
	if opened.ProjectID != projectID || opened.RevisionID != revisionID || opened.Manifest.ProjectID != projectID || opened.Manifest.Category != "ai_short_drama" || opened.Manifest.Validate() != nil || !opened.ManifestRef.Valid() {
		return OpenResult{}, nil, ErrDependency
	}
	drama, err := s.readDramaRevision(ctx, p, opened.Manifest, revisionID)
	return opened, drama, err
}

// PublicationSource resolves one persisted intent. It never accepts a browser
// supplied request or exposes the source project's complete private manifest.
func (s *Service) PublicationSource(ctx context.Context, p Principal, r PublicationSourceRequest) (PublicationSourceResult, error) {
	var out PublicationSourceResult
	if !p.Valid() {
		return out, ErrForbidden
	}
	if !r.valid() {
		return out, ErrInvalid
	}
	prepared, drama, err := s.publicationSnapshot(ctx, p, r.ProjectID, r.PreparedRevisionID)
	if err != nil {
		return out, err
	}
	candidate, err := publicationCandidate(drama, r.CandidateKey)
	if err != nil {
		return out, err
	}
	request, err := preparedPublication(candidate, r)
	if err != nil {
		return out, err
	}
	original, sourceDrama, err := s.publicationSnapshot(ctx, p, r.ProjectID, request.SourceRevisionID)
	if err != nil {
		return out, err
	}
	sourceCandidate, err := publicationCandidate(sourceDrama, r.CandidateKey)
	if err != nil {
		return out, err
	}
	for _, field := range []string{"source", "review", "publication_record_file_id"} {
		if !contentEqual(candidate[field], sourceCandidate[field]) {
			return out, ErrInvalid
		}
	}
	if sourceCandidate["registration_request"] != nil {
		prior, e := preparedPublication(sourceCandidate, r)
		if e != nil || !contentEqual(prior, request) {
			return out, ErrInvalid
		}
	}
	// Reuse the registration manifest checks for exact fixed source, selected
	// files, review membership and allowed dependency references.
	if _, err = prepareWorld(request, r.CommitID, original); err != nil {
		return out, err
	}
	locator := objectOf(candidate["source"])
	prefix := "globals/"
	if locator["kind"] == "deliverable" {
		prefix = "deliverables/"
	}
	owner := dramaOwners(sourceDrama)[prefix+str(locator["key"])]
	if owner == nil || len(listOf(owner["versions"])) == 0 {
		return out, ErrInvalid
	}
	recordID := str(candidate["publication_record_file_id"])
	var record File
	for _, f := range original.Manifest.Files {
		if f.ID == recordID {
			record = f
		}
	}
	unchanged := false
	for _, f := range prepared.Manifest.Files {
		if f.ID == recordID && f.Content == record.Content {
			unchanged = true
		}
	}
	if !unchanged || !record.Content.Valid() {
		return out, ErrInvalid
	}
	data, err := s.content.Read(ctx, p, record.Content, Access{ProjectID: r.ProjectID, RevisionID: request.SourceRevisionID}, MaxManifestBytes)
	if err != nil {
		return out, err
	}
	if err = validateDramaPublication(data, sourceCandidate, owner, original.Manifest, recordID, request); err != nil {
		return out, err
	}
	var publication dramaPublication
	if json.Unmarshal(data, &publication) != nil {
		return out, ErrInvalid
	}
	versions, err := indexObjects(owner["versions"], "publication")
	if err != nil {
		return out, err
	}
	states, err := indexObjects(owner["states"], "publication")
	if err != nil {
		return out, err
	}
	selected := make([]PublicationVersion, 0, len(publication.Versions))
	for _, entry := range publication.Versions {
		version := versions[entry.VersionKey]
		label := str(version["label"])
		if label == "" {
			label = entry.VersionKey
		}
		if state := str(states[str(version["state_key"])]["name"]); state != "" {
			label = strings.Join([]string{label, state}, " · ")
		}
		item := PublicationVersion{VersionKey: entry.VersionKey, Label: label, FileIDs: []string{}}
		for _, file := range entry.FileRefs {
			item.FileIDs = append(item.FileIDs, file.FileID)
		}
		selected = append(selected, item)
	}
	hash, err := digest(request)
	if err != nil {
		return out, err
	}
	return PublicationSourceResult{PublicationSourceRequest: r, Request: request, RequestHash: hash, Versions: selected}, nil
}
