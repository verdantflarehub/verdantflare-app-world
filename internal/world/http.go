package world

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type CommitReceipt struct {
	Actor       string       `json:"actor"`
	RequestHash string       `json:"request_sha256"`
	State       string       `json:"state"`
	VersionID   string       `json:"version_id"`
	Result      *WorldResult `json:"result,omitempty"`
}

func (s *Service) HTTPHandler(serviceToken, authorityToken string) (http.Handler, error) {
	if len(serviceToken) < 32 || strings.ContainsAny(serviceToken, " \r\n\t") || serviceToken == authorityToken {
		return nil, ErrInvalid
	}
	authority, err := s.AuthorityHandler(authorityToken)
	if err != nil {
		return nil, err
	}
	expected := sha256.Sum256([]byte("Bearer " + serviceToken))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/internal/v1/artifact/authorize" {
			authority.ServeHTTP(w, r)
			return
		}
		p := Principal{OrganizationID: r.Header.Get("X-Organization-Id"), SubjectID: r.Header.Get("X-User-Id"), RequestID: r.Header.Get("X-Request-Id")}
		for _, header := range []string{"Authorization", "X-Organization-Id", "X-User-Id", "X-Request-Id"} {
			if len(r.Header.Values(header)) != 1 {
				httpError(w, p, ErrForbidden)
				return
			}
		}
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if !p.Valid() || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			httpError(w, p, ErrForbidden)
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if r.Method != "POST" || r.URL.RawQuery != "" || r.URL.ForceQuery || strings.Contains(r.URL.EscapedPath(), "%") || len(r.Header.Values("Content-Type")) != 1 || err != nil || mt != "application/json" || len(r.Header.Values("Content-Encoding")) != 0 {
			httpError(w, p, ErrInvalid)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, MaxManifestBytes+1))
		if err != nil || len(data) > MaxManifestBytes {
			httpError(w, p, ErrInvalid)
			return
		}
		decode := func(value any) bool {
			if strictjson.Decode(bytes.NewReader(data), MaxManifestBytes, value) != nil {
				httpError(w, p, ErrInvalid)
				return false
			}
			return true
		}
		var result any
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/v1/world/") {
			path = "/internal/v1/world/" + strings.TrimPrefix(path, "/api/v1/world/")
		}
		switch path {
		case "/internal/v1/world/draft":
			request, decodeErr := DecodeDraftRequest(data)
			if decodeErr != nil {
				httpError(w, p, decodeErr)
				return
			}
			result, err = s.WorldDraft(r.Context(), p, request)
		case "/internal/v1/world/publication_source":
			var request PublicationSourceRequest
			if !decode(&request) {
				return
			}
			result, err = s.PublicationSource(r.Context(), p, request)
		case "/internal/v1/world/register":
			var request WorldRegisterRequest
			if !decode(&request) {
				return
			}
			result, err = s.WorldRegister(r.Context(), p, request)
		case "/internal/v1/world/list":
			var request WorldListRequest
			if !decode(&request) {
				return
			}
			result, err = s.WorldList(r.Context(), p, request)
		case "/internal/v1/world/get":
			var request WorldGetRequest
			if !decode(&request) {
				return
			}
			result, err = s.WorldGet(r.Context(), p, request)
		case "/internal/v1/world/commit_status":
			var request WorldStatusRequest
			if !decode(&request) {
				return
			}
			result, err = s.WorldCommitStatus(r.Context(), p, request)
		case "/internal/v1/world/grant":
			var request WorldGrantRequest
			if !decode(&request) {
				return
			}
			result, err = s.WorldGrant(r.Context(), p, request)
		case "/internal/v1/world/revoke":
			var request WorldRevokeRequest
			if !decode(&request) {
				return
			}
			result, err = s.WorldRevoke(r.Context(), p, request)
		case "/internal/v1/world/references":
			if r.URL.Path != path {
				err = ErrNotFound
				break
			}
			var request AssetRef
			if !decode(&request) {
				return
			}
			result, err = s.ReferencesWithGrants(r.Context(), p, request)
		case "/internal/v1/world/commit_receipt":
			if r.URL.Path != path {
				err = ErrNotFound
				break
			}
			var request WorldStatusRequest
			if !decode(&request) {
				return
			}
			if !ValidID(request.CommitID) || !ValidID(request.AssetID) {
				err = ErrInvalid
				break
			}
			if _, err = assetRole(r.Context(), s.db, p, request.AssetID); err == nil {
				var c worldCommit
				c, err = loadWorldCommit(r.Context(), s.db, p.OrganizationID, request.AssetID, request.CommitID)
				if err == nil {
					result = CommitReceipt{Actor: c.Actor, RequestHash: c.Hash, State: c.State, VersionID: c.Version, Result: c.Result}
				}
			}
		default:
			err = ErrNotFound
		}
		if err != nil {
			httpError(w, p, err)
			return
		}
		w.Header().Set("X-Request-Id", p.RequestID)
		_ = json.NewEncoder(w).Encode(result)
	}), nil
}
func httpError(w http.ResponseWriter, p Principal, err error) {
	status, code := 500, "INTERNAL"
	for _, item := range []struct {
		err    error
		status int
	}{{ErrInvalid, 400}, {ErrForbidden, 403}, {ErrNotFound, 404}, {ErrConflict, 409}, {ErrIdempotency, 409}, {ErrNotReady, 409}, {ErrDependency, 503}, {coordination.ErrPending, 409}} {
		if errors.Is(err, item.err) {
			status, code = item.status, item.err.Error()
			break
		}
	}
	if !ValidID(p.RequestID) {
		p.RequestID = newID()
	}
	w.Header().Set("X-Request-Id", p.RequestID)
	w.WriteHeader(status)
	var details map[string]string
	var contentErr *contentValidationError
	if errors.As(err, &contentErr) {
		details = map[string]string{"document_type": "project-content", "path": contentErr.Path, "reason": contentErr.Reason}
	}
	var learningErr *learningValidationError
	if errors.As(err, &learningErr) {
		details = map[string]string{"document_type": "reference-learning", "path": learningErr.Path, "reason": learningErr.Reason}
	}
	var draftConflict *ConflictError
	if errors.As(err, &draftConflict) {
		details = map[string]string{"current_revision_id": draftConflict.CurrentRevisionID}
	}
	var conflict *WorldConflictError
	if errors.As(err, &conflict) {
		details = map[string]string{"current_asset_version_id": conflict.CurrentVersionID}
	}
	_ = json.NewEncoder(w).Encode(struct {
		Code      string            `json:"code"`
		Message   string            `json:"message"`
		RequestID string            `json:"request_id"`
		Details   map[string]string `json:"details,omitempty"`
	}{code, "World request failed", p.RequestID, details})
}
