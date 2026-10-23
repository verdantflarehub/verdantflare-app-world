package world

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
	"golang.org/x/text/unicode/norm"
)

const maxReviewRequest = 1 << 20

var (
	ErrReviewConflict = errors.New("REVIEW_CONFLICT")
	ErrTargetMismatch = errors.New("TARGET_MISMATCH")
	ErrReviewScope    = errors.New("REVIEW_SCOPE_INCOMPLETE")
	ErrReviewNotReady = errors.New("CONTENT_NOT_READY")
)

type ReviewLocator struct {
	Kind      string `json:"kind"`
	AssetID   string `json:"asset_id"`
	VersionID string `json:"asset_version_id"`
}

func (l ReviewLocator) valid() bool {
	return l.Kind == "world" && ValidID(l.AssetID) && ValidID(l.VersionID)
}

type ReviewOpenRequest struct {
	Target       ReviewLocator `json:"target_locator"`
	ExpectedHash string        `json:"expected_target_hash,omitempty"`
	Cursor       string        `json:"cursor,omitempty"`
}
type ReviewFrameRange struct {
	Start          int64 `json:"start"`
	End            int64 `json:"end"`
	FPSNumerator   int64 `json:"fps_numerator"`
	FPSDenominator int64 `json:"fps_denominator"`
}
type ReviewImageRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}
type ReviewScope struct {
	Kind        string            `json:"kind"`
	EvidenceIDs []string          `json:"evidence_ids"`
	CameraIDs   []string          `json:"camera_ids"`
	RegionIDs   []string          `json:"region_ids"`
	Claims      []string          `json:"claims"`
	ImageRect   *ReviewImageRect  `json:"image_rect,omitempty"`
	FrameRange  *ReviewFrameRange `json:"frame_range,omitempty"`
}
type ReviewAppendRequest struct {
	input            json.RawMessage
	Target           ReviewLocator `json:"target_locator"`
	ExpectedHash     string        `json:"expected_target_hash"`
	CommitID         string        `json:"commit_id"`
	ExpectedRecordID *string       `json:"expected_record_id"`
	Action           string        `json:"action"`
	Scope            ReviewScope   `json:"scope"`
	Body             string        `json:"body"`
	Supersedes       *string       `json:"supersedes_record_id,omitempty"`
	TechnicalRefs    []string      `json:"technical_refs,omitempty"`
}
type ReviewStatusRequest struct {
	AssetID  string `json:"asset_id"`
	CommitID string `json:"commit_id"`
}
type ReviewFileSource struct {
	ContentRef ContentRef `json:"content_ref"`
	SHA256     string     `json:"sha256"`
	Size       int64      `json:"size_bytes"`
}
type ReviewEvidence struct {
	ID         string            `json:"evidence_id"`
	FileID     string            `json:"file_id"`
	ContentRef ContentRef        `json:"content_ref"`
	SHA256     string            `json:"sha256"`
	Size       int64             `json:"size_bytes"`
	MIME       string            `json:"mime"`
	Role       string            `json:"role"`
	CameraIDs  []string          `json:"camera_ids"`
	RegionIDs  []string          `json:"region_ids"`
	FrameRange *ReviewFrameRange `json:"frame_range"`
}
type ReviewDependency struct {
	AssetID     string      `json:"asset_id"`
	VersionID   string      `json:"asset_version_id"`
	Purpose     string      `json:"purpose"`
	ManifestRef *ContentRef `json:"manifest_ref"`
	SHA256      *string     `json:"manifest_sha256"`
}
type ReviewCoverage struct {
	CameraIDs  []string          `json:"camera_ids"`
	RegionIDs  []string          `json:"region_ids"`
	SpaceBasis *ReviewSpaceBasis `json:"space_basis"`
	Cameras    []ReviewCamera    `json:"cameras"`
}
type ReviewSpaceBasis struct {
	EvidenceID string `json:"evidence_id"`
	SHA256     string `json:"sha256"`
}
type ReviewCamera struct {
	CameraID         string   `json:"camera_id"`
	ParametersID     string   `json:"parameters_evidence_id"`
	ParametersSHA256 string   `json:"parameters_sha256"`
	EvidenceIDs      []string `json:"evidence_ids"`
	RegionIDs        []string `json:"region_ids"`
	Verified         bool     `json:"verified"`
}
type ReviewRequirement struct {
	ID          string   `json:"requirement_id"`
	Claim       string   `json:"claim"`
	EvidenceIDs []string `json:"evidence_ids"`
	State       string   `json:"state"`
	Reason      string   `json:"reason_code"`
}
type ReviewTarget struct {
	SchemaVersion int                 `json:"schema_version"`
	Locator       ReviewLocator       `json:"target_locator"`
	Source        ReviewFileSource    `json:"source_manifest"`
	Definition    map[string]any      `json:"definition"`
	Evidence      []ReviewEvidence    `json:"evidence"`
	Dependencies  []ReviewDependency  `json:"dependencies"`
	Coverage      ReviewCoverage      `json:"coverage"`
	Requirements  []ReviewRequirement `json:"requirements"`
}
type ReviewActor struct {
	SubjectID      string `json:"subject_id"`
	OrganizationID string `json:"organization_id"`
	Origin         string `json:"origin"`
}
type ReviewRecord struct {
	SchemaVersion int          `json:"schema_version"`
	ID            string       `json:"record_id"`
	TargetHash    string       `json:"target_hash"`
	Target        ReviewTarget `json:"target_manifest"`
	PreviousID    *string      `json:"previous_record_id"`
	Action        string       `json:"action"`
	Scope         ReviewScope  `json:"scope"`
	Body          string       `json:"body"`
	Actor         ReviewActor  `json:"actor"`
	CreatedAt     time.Time    `json:"created_at"`
	Supersedes    *string      `json:"supersedes_record_id"`
	TechnicalRefs []string     `json:"technical_refs"`
}
type ReviewDecision struct {
	ScopeHash string      `json:"scope_hash"`
	Scope     ReviewScope `json:"scope"`
	Action    string      `json:"action"`
	RecordID  string      `json:"record_id"`
}
type ReviewAvailability struct {
	Code       string `json:"code"`
	EvidenceID string `json:"evidence_id,omitempty"`
}
type ReviewCapabilities struct {
	CanComment  bool     `json:"can_comment"`
	CanDecide   bool     `json:"can_decide"`
	ReasonCodes []string `json:"reason_codes"`
}
type ReviewRecordView struct {
	ID         string `json:"record_id"`
	Visibility string `json:"visibility"`
	View       any    `json:"record_view"`
}
type ReviewOpenResult struct {
	Locator      ReviewLocator        `json:"target_locator"`
	TargetHash   string               `json:"target_hash"`
	Manifest     *ReviewTarget        `json:"target_manifest,omitempty"`
	View         ReviewTarget         `json:"target_view"`
	Availability []ReviewAvailability `json:"availability"`
	Head         *string              `json:"records_head_record_id"`
	State        string               `json:"review_state"`
	Decisions    []ReviewDecision     `json:"scope_decisions"`
	Records      []any                `json:"records"`
	NextCursor   string               `json:"next_cursor"`
	Capabilities ReviewCapabilities   `json:"capabilities"`
	Missing      []ReviewAvailability `json:"missing"`
	MappingState string               `json:"mapping_state"`
}
type ReviewAppendResult struct {
	State       string `json:"state"`
	CommitID    string `json:"commit_id"`
	RecordID    string `json:"record_id"`
	TargetHash  string `json:"target_hash"`
	ReviewState string `json:"review_state"`
	Head        string `json:"records_head_record_id"`
}
type ReviewStatusResult struct {
	target      ReviewLocator
	origin      string
	State       string              `json:"state"`
	RequestHash string              `json:"request_sha256"`
	Result      *ReviewAppendResult `json:"result,omitempty"`
	ErrorCode   string              `json:"error_code,omitempty"`
}

func reviewOrigin(p Principal) string {
	if p.ReviewOrigin == "human_interactive" {
		return "human_interactive"
	}
	return "agent"
}
func reviewHash(v any) (string, []byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return "", nil, e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || !reviewCanonicalValue(value) {
		return "", nil, ErrInvalid
	}
	canonical, e := jsoncanonicalizer.Transform(raw)
	if e != nil {
		return "", nil, ErrInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var normalized any
	if decoder.Decode(&normalized) != nil || !reviewSameNumbers(value, normalized) {
		return "", nil, ErrInvalid
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), canonical, nil
}

func reviewSameNumbers(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		left, le, lok := reviewDecimal(string(x))
		right, re, rok := reviewDecimal(string(y))
		return lok && rok && left == right && le == re
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if !reviewSameNumbers(v, y[k]) {
				return false
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !reviewSameNumbers(v, y[i]) {
				return false
			}
		}
	}
	return true
}

// Compare decimal values without allocating powers of ten from untrusted
// exponents. JCS may normalize notation, but must preserve the decimal value.
func reviewDecimal(number string) (string, int64, bool) {
	mantissa, exponent := number, "0"
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		mantissa, exponent = number[:i], number[i+1:]
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0", 0, true
	}
	exp, err := strconv.ParseInt(exponent, 10, 64)
	if err != nil || exp > 1<<50 || exp < -(1<<50) {
		return "", 0, false
	}
	trimmed := strings.TrimRight(mantissa, "0")
	exp += int64(len(mantissa) - len(trimmed) - fraction)
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, exp, true
}

func reviewCanonicalValue(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if !norm.NFC.IsNormalString(k) || !reviewCanonicalValue(v) {
				return false
			}
		}
	case []any:
		for _, v := range x {
			if !reviewCanonicalValue(v) {
				return false
			}
		}
	case string:
		return utf8.ValidString(x) && norm.NFC.IsNormalString(x)
	case json.Number:
		f, e := strconv.ParseFloat(string(x), 64)
		if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) == f && math.Abs(f) > 1<<53-1 {
			return false
		}
	}
	return true
}
func reviewDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func reviewSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func reviewIDs(ids []string) bool {
	if ids == nil || len(ids) > 1000 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range ids {
		if v == "" || utf8.RuneCountInString(v) > 128 || !norm.NFC.IsNormalString(v) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func normalizeScope(s ReviewScope) (ReviewScope, error) {
	if !reviewIDs(s.EvidenceIDs) || !reviewIDs(s.CameraIDs) || !reviewIDs(s.RegionIDs) || !reviewIDs(s.Claims) || len(s.Claims) == 0 {
		return s, ErrInvalid
	}
	switch s.Kind {
	case "evidence", "camera", "region", "full_target":
	default:
		return s, ErrInvalid
	}
	for _, c := range s.Claims {
		if c == "full_set" && s.Kind != "full_target" {
			return s, ErrInvalid
		}
		switch c {
		case "image_reading", "space_layout", "source_recovery", "new_camera", "full_set":
		default:
			return s, ErrInvalid
		}
	}
	s.EvidenceIDs = append([]string{}, s.EvidenceIDs...)
	s.CameraIDs = append([]string{}, s.CameraIDs...)
	s.RegionIDs = append([]string{}, s.RegionIDs...)
	s.Claims = append([]string{}, s.Claims...)
	sort.Strings(s.EvidenceIDs)
	sort.Strings(s.CameraIDs)
	sort.Strings(s.RegionIDs)
	sort.Strings(s.Claims)
	if s.ImageRect != nil {
		r := s.ImageRect
		if math.IsNaN(r.X) || math.IsNaN(r.Y) || math.IsNaN(r.W) || math.IsNaN(r.H) || r.X < 0 || r.Y < 0 || r.W <= 0 || r.H <= 0 || r.X+r.W > 1 || r.Y+r.H > 1 || len(s.EvidenceIDs) != 1 || s.FrameRange != nil {
			return s, ErrInvalid
		}
	}
	if s.FrameRange != nil {
		r := s.FrameRange
		if len(s.EvidenceIDs) != 1 || r.Start < 0 || r.End < r.Start || r.End > 1<<53-1 || r.FPSNumerator <= 0 || r.FPSNumerator > 1<<53-1 || r.FPSDenominator <= 0 || r.FPSDenominator > 1<<53-1 {
			return s, ErrInvalid
		}
	}
	return s, nil
}
func containsReview(items []string, key string) bool {
	for _, v := range items {
		if v == key {
			return true
		}
	}
	return false
}
func validateReviewScope(t ReviewTarget, s ReviewScope, action string) error {
	byID := map[string]ReviewEvidence{}
	for _, e := range t.Evidence {
		byID[e.ID] = e
	}
	for _, id := range s.EvidenceIDs {
		if _, ok := byID[id]; !ok {
			return ErrInvalid
		}
	}
	for _, id := range s.CameraIDs {
		if !containsReview(t.Coverage.CameraIDs, id) {
			return ErrInvalid
		}
	}
	for _, id := range s.RegionIDs {
		if !containsReview(t.Coverage.RegionIDs, id) {
			return ErrInvalid
		}
	}
	if s.Kind == "evidence" && len(s.EvidenceIDs) == 0 || s.Kind == "camera" && len(s.CameraIDs) == 0 || s.Kind == "region" && len(s.RegionIDs) == 0 {
		return ErrInvalid
	}
	if s.ImageRect != nil && !strings.HasPrefix(byID[s.EvidenceIDs[0]].MIME, "image/") {
		return ErrInvalid
	}
	if s.FrameRange != nil {
		e := byID[s.EvidenceIDs[0]]
		r := s.FrameRange
		if e.FrameRange == nil || r.Start < e.FrameRange.Start || r.End > e.FrameRange.End || r.FPSNumerator != e.FrameRange.FPSNumerator || r.FPSDenominator != e.FrameRange.FPSDenominator {
			return ErrInvalid
		}
	}
	if action != "approve" {
		return nil
	}
	for _, camera := range s.CameraIDs {
		mapped := false
		for _, id := range s.EvidenceIDs {
			if containsReview(byID[id].CameraIDs, camera) {
				mapped = true
			}
		}
		if !mapped {
			return ErrReviewScope
		}
	}
	for _, region := range s.RegionIDs {
		mapped := false
		for _, id := range s.EvidenceIDs {
			if containsReview(byID[id].RegionIDs, region) {
				mapped = true
			}
		}
		if !mapped && s.Kind != "full_target" {
			return ErrReviewScope
		}
	}
	for _, claim := range s.Claims {
		if claim == "image_reading" {
			if len(s.EvidenceIDs) == 0 {
				return ErrReviewScope
			}
			for _, id := range s.EvidenceIDs {
				m := byID[id].MIME
				if !strings.HasPrefix(m, "image/") && !strings.HasPrefix(m, "video/") {
					return ErrReviewScope
				}
			}
			continue
		}
		found := false
		for _, r := range t.Requirements {
			if r.Claim == claim {
				found = true
				if r.State != "satisfied" {
					return ErrReviewScope
				}
				for _, id := range r.EvidenceIDs {
					if !containsReview(s.EvidenceIDs, id) {
						return ErrReviewScope
					}
				}
			}
		}
		if !found {
			return ErrReviewScope
		}
	}
	if s.Kind == "full_target" {
		for _, claim := range []string{"space_layout", "source_recovery", "full_set"} {
			if !containsReview(s.Claims, claim) {
				return ErrReviewScope
			}
		}
		for _, e := range t.Evidence {
			if !containsReview(s.EvidenceIDs, e.ID) {
				return ErrReviewScope
			}
		}
		for _, id := range t.Coverage.CameraIDs {
			if !containsReview(s.CameraIDs, id) {
				return ErrReviewScope
			}
		}
		for _, id := range t.Coverage.RegionIDs {
			if !containsReview(s.RegionIDs, id) {
				return ErrReviewScope
			}
		}
		for _, r := range t.Requirements {
			if r.Claim != "new_camera" && r.State != "satisfied" {
				return ErrReviewScope
			}
		}
	} else if containsReview(s.Claims, "full_set") {
		return ErrReviewScope
	}
	return nil
}
func reviewRequired(t ReviewTarget, s ReviewScope) bool {
	if s.Kind == "full_target" {
		return true
	}
	if len(s.Claims) == 1 && s.Claims[0] == "new_camera" {
		return false
	}
	if len(s.CameraIDs) > 0 || len(s.RegionIDs) > 0 {
		return true
	}
	for _, r := range t.Requirements {
		for _, id := range r.EvidenceIDs {
			if containsReview(s.EvidenceIDs, id) {
				return true
			}
		}
	}
	return false
}
func reviewAggregate(t ReviewTarget, records []ReviewRecord) (string, []ReviewDecision) {
	state := "unreviewed"
	latestRequiredRejection, latestFullApproval := -1, -1
	decisions := map[string]ReviewDecision{}
	order := []string{}
	for i, r := range records {
		if r.Actor.Origin != "human_interactive" {
			continue
		}
		if state == "unreviewed" {
			state = "in_review"
		}
		if r.Action == "comment" {
			continue
		}
		h, _, _ := reviewHash(r.Scope)
		if _, ok := decisions[h]; !ok {
			order = append(order, h)
		}
		decisions[h] = ReviewDecision{h, r.Scope, r.Action, r.ID}
		if r.Action == "request_changes" && reviewRequired(t, r.Scope) {
			latestRequiredRejection = i
		}
		if r.Action == "approve" && r.Scope.Kind == "full_target" && validateReviewScope(t, r.Scope, "approve") == nil {
			latestFullApproval = i
		}
	}
	out := []ReviewDecision{}
	sort.Strings(order)
	for _, h := range order {
		out = append(out, decisions[h])
	}
	for _, d := range out {
		if d.Action == "request_changes" && reviewRequired(t, d.Scope) {
			return "rejected", out
		}
	}
	if latestFullApproval >= 0 && latestFullApproval > latestRequiredRejection {
		return "approved", out
	}
	return state, out
}
func validateReviewDecision(records []ReviewRecord, r ReviewAppendRequest) error {
	h, _, _ := reviewHash(r.Scope)
	var prior *ReviewRecord
	for i := range records {
		x := &records[i]
		sh, _, _ := reviewHash(x.Scope)
		if x.Actor.Origin == "human_interactive" && x.Action != "comment" && sh == h {
			prior = x
		}
	}
	if r.Action == "comment" {
		if r.Supersedes != nil {
			return ErrInvalid
		}
		return nil
	}
	if prior != nil {
		if r.Supersedes == nil || *r.Supersedes != prior.ID {
			return ErrReviewConflict
		}
	} else if r.Supersedes != nil {
		return ErrReviewConflict
	}
	if r.Action == "approve" && r.Scope.Kind == "full_target" && len(records) > 0 {
		_, decisions := reviewAggregate(records[0].Target, records)
		for _, d := range decisions {
			if d.ScopeHash != h && d.Action == "request_changes" && reviewRequired(records[0].Target, d.Scope) {
				return ErrReviewScope
			}
		}
	}
	return nil
}
func validateReviewAppend(r ReviewAppendRequest, p Principal) error {
	if !p.Valid() || p.ReviewOrigin != "" && p.ReviewOrigin != "agent" && p.ReviewOrigin != "human_interactive" {
		return ErrForbidden
	}
	if !r.Target.valid() || !ValidID(r.CommitID) || !reviewSHA(r.ExpectedHash) || r.ExpectedRecordID != nil && !ValidID(*r.ExpectedRecordID) || r.Supersedes != nil && !ValidID(*r.Supersedes) || !utf8.ValidString(r.Body) || !norm.NFC.IsNormalString(r.Body) || strings.TrimSpace(r.Body) == "" || utf8.RuneCountInString(r.Body) > 20000 {
		return ErrInvalid
	}
	switch r.Action {
	case "comment":
	case "approve", "request_changes":
		if reviewOrigin(p) != "human_interactive" {
			return ErrForbidden
		}
	default:
		return ErrInvalid
	}
	if r.TechnicalRefs != nil && !reviewIDs(r.TechnicalRefs) {
		return ErrInvalid
	}
	raw, _ := json.Marshal(r)
	if len(raw) > maxReviewRequest {
		return ErrInvalid
	}
	return nil
}

// Review messages have a required nullable CAS field. The general strictjson
// decoder deliberately disallows null, so retain its duplicate-key and bounded
// scan while checking the exact request shape (including null) here.
func decodeReview(data []byte, dst any) error {
	b, e := strictjson.ReadValue(bytes.NewReader(data), maxReviewRequest)
	if e != nil {
		return ErrInvalid
	}
	var raw any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&raw) != nil || !reviewShape(raw, reflect.TypeOf(dst).Elem()) {
		return ErrInvalid
	}
	if _, _, e := reviewHash(json.RawMessage(b)); e != nil {
		return ErrInvalid
	}
	if json.Unmarshal(b, dst) != nil {
		return ErrInvalid
	}
	if r, ok := dst.(*ReviewAppendRequest); ok {
		r.input = append(json.RawMessage{}, b...)
	}
	return nil
}
func reviewShape(v any, t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		if v == nil {
			return true
		}
		return reviewShape(v, t.Elem())
	}
	if v == nil {
		return false
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		allowed := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			parts := strings.Split(f.Tag.Get("json"), ",")
			k := parts[0]
			allowed[k] = true
			x, present := m[k]
			optional := len(parts) > 1 && parts[1] == "omitempty"
			if !present && !optional {
				return false
			}
			if present && optional && f.Type.Kind() == reflect.String && x == "" {
				return false
			}
			if present && x == nil && t == reflect.TypeOf(ReviewScope{}) {
				return false
			}
			if present && !reviewShape(x, f.Type) {
				return false
			}
		}
		for k := range m {
			if !allowed[k] {
				return false
			}
		}
		return true
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, x := range a {
			if !reviewShape(x, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		_, ok := v.(string)
		return ok
	default:
		_, ok := v.(json.Number)
		return ok
	}
}
