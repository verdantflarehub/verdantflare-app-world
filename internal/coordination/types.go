// Package coordination implements durable business authorization reservations.
// The protocol is defined in the design repository's Project/World split change.
package coordination

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
)

var (
	ErrInvalid  = errors.New("INVALID_COORDINATION_ARGUMENT")
	ErrConflict = errors.New("COORDINATION_IDEMPOTENCY_CONFLICT")
	ErrDenied   = errors.New("COORDINATION_PERMISSION_DENIED")
	ErrPending  = errors.New("AUTHORIZATION_CHANGE_PENDING")
	ErrUnknown  = errors.New("COORDINATION_STATE_UNKNOWN")
	ErrFenced   = errors.New("COORDINATOR_FENCED")
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Operation struct {
	Coordinator    string `json:"coordinator"`
	OrganizationID string `json:"organization_id"`
	SubjectID      string `json:"subject_id"`
	ResourceID     string `json:"resource_id"`
	CommitID       string `json:"commit_id"`
	RequestHash    string `json:"request_sha256"`
	Fence          int64  `json:"fence"`
}

func (o Operation) Valid() bool {
	return (o.Coordinator == "project" || o.Coordinator == "world") && idPattern.MatchString(o.OrganizationID) && idPattern.MatchString(o.SubjectID) && idPattern.MatchString(o.ResourceID) && idPattern.MatchString(o.CommitID) && hashPattern.MatchString(o.RequestHash) && o.Fence > 0
}

// Grant scopes the actor's permission on a resource. It never grants file access.
type Grant struct {
	ResourceID string `json:"resource_id"`
	Write      bool   `json:"write"`
}

type Plan map[string][]Grant
type Status struct {
	Operation Operation `json:"operation"`
	State     string    `json:"state"`
}

func Normalize(p Plan) (Plan, error) {
	if len(p) == 0 || len(p) > 2 {
		return nil, ErrInvalid
	}
	out := Plan{}
	for domain, grants := range p {
		if domain != "project" && domain != "world" || len(grants) == 0 || len(grants) > 10000 {
			return nil, ErrInvalid
		}
		seen := map[string]bool{}
		for _, grant := range grants {
			if !idPattern.MatchString(grant.ResourceID) {
				return nil, ErrInvalid
			}
			seen[grant.ResourceID] = seen[grant.ResourceID] || grant.Write
		}
		for resource, write := range seen {
			out[domain] = append(out[domain], Grant{resource, write})
		}
		sort.Slice(out[domain], func(i, j int) bool { return out[domain][i].ResourceID < out[domain][j].ResourceID })
	}
	return out, nil
}

func Fingerprint(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func terminal(state string) bool { return state == "committed" || state == "cancelled" }

// VerifyTerminal requires a trusted coordinator response for this exact operation.
// Unknown, prepared, mismatched actor/hash/fence, and malformed outcomes never release.
func VerifyTerminal(expected Operation, status Status) error {
	if !expected.Valid() || expected != status.Operation {
		return ErrConflict
	}
	if !terminal(status.State) {
		return ErrUnknown
	}
	return nil
}
