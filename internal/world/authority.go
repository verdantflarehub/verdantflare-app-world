package world

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type Permission struct {
	Action  string          `json:"action"`
	Source  *Source         `json:"source,omitempty"`
	Version *ContentVersion `json:"version,omitempty"`
	Owner   *RetentionOwner `json:"owner,omitempty"`
	Access  *Access         `json:"access,omitempty"`
}

func (s *Service) AuthorizeArtifact(ctx context.Context, p Principal, permission Permission) error {
	if !p.Valid() {
		return ErrForbidden
	}
	switch permission.Action {
	case "write":
		if permission.Source == nil || permission.Version != nil || permission.Owner != nil || permission.Access != nil {
			return ErrForbidden
		}
		return s.authorizeWorldWrite(ctx, p, *permission.Source)
	case "read":
		if permission.Version == nil || permission.Access == nil || permission.Source != nil || permission.Owner != nil {
			return ErrForbidden
		}
		return s.authorizeAssetRead(ctx, p, *permission.Version, *permission.Access)
	case "retain", "retain_content", "inspect_retention", "release":
		return s.authorizeWorldRetention(ctx, p, permission)
	default:
		return ErrForbidden
	}
}

// AuthorityHandler is an internal-only endpoint. Its service credential is
// independent from the Gateway credential; user headers are never accepted.
func (s *Service) AuthorityHandler(token string) (http.Handler, error) {
	if len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
		return nil, ErrInvalid
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		deny := func(status int) { w.WriteHeader(status); _, _ = w.Write([]byte(`{"allowed":false}`)) }
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if r.Method != "POST" || r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			deny(403)
			return
		}
		var req struct {
			Principal  Principal  `json:"principal"`
			Permission Permission `json:"permission"`
		}
		if strictjson.Decode(r.Body, 1<<20, &req) != nil {
			deny(400)
			return
		}
		e := s.AuthorizeArtifact(r.Context(), req.Principal, req.Permission)
		if e != nil {
			if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
				deny(403)
			} else {
				deny(503)
			}
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Allowed bool `json:"allowed"`
		}{true})
	}), nil
}
