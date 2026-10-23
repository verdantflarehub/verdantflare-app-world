package world

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func reviewTestPrincipal() Principal {
	return Principal{OrganizationID: testID(30), SubjectID: testID(31), RequestID: testID(32), ReviewOrigin: "human_interactive"}
}
func reviewTestRequest() ReviewAppendRequest {
	return ReviewAppendRequest{Target: ReviewLocator{"world", testID(1), testID(2)}, ExpectedHash: strings.Repeat("a", 64), CommitID: testID(3), Action: "comment", Body: "Read this image", Scope: ReviewScope{Kind: "evidence", EvidenceIDs: []string{testID(4)}, CameraIDs: []string{}, RegionIDs: []string{}, Claims: []string{"image_reading"}}}
}
func reviewTestTarget() ReviewTarget {
	return ReviewTarget{SchemaVersion: 1, Locator: ReviewLocator{"world", testID(1), testID(2)}, Definition: map[string]any{}, Evidence: []ReviewEvidence{{ID: testID(4), FileID: testID(4), MIME: "image/png", Role: "render", SHA256: strings.Repeat("a", 64), CameraIDs: []string{"front"}}}, Dependencies: []ReviewDependency{}, Coverage: ReviewCoverage{CameraIDs: []string{"front"}, RegionIDs: []string{"main"}, Cameras: []ReviewCamera{}}, Requirements: []ReviewRequirement{{ID: "all", Claim: "full_set", EvidenceIDs: []string{testID(4)}, State: "missing", Reason: "required_evidence_missing"}}}
}

func TestReviewDecoderRequiredNullAndFingerprint(t *testing.T) {
	raw, _ := json.Marshal(reviewTestRequest())
	var r ReviewAppendRequest
	if e := decodeReview(raw, &r); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"expected_record_id":null,`, "", 1), strings.Replace(string(raw), `"action":"comment"`, `"action":"comment","actor":{}`, 1), strings.Replace(string(raw), `"action":"comment"`, `"action":"comment","action":"comment"`, 1), strings.Replace(string(raw), `"camera_ids":[]`, `"camera_ids":null`, 1), strings.Replace(string(raw), `"body":`, `"Body":`, 1)} {
		if decodeReview([]byte(bad), &ReviewAppendRequest{}) == nil {
			t.Fatal("invalid shape accepted")
		}
	}
	for _, field := range []string{"image_rect", "frame_range"} {
		bad := strings.Replace(string(raw), `"camera_ids":[]`, `"camera_ids":[],"`+field+`":null`, 1)
		if decodeReview([]byte(bad), &ReviewAppendRequest{}) == nil {
			t.Fatal("nullable scope accepted")
		}
	}
	openRaw, _ := json.Marshal(ReviewOpenRequest{Target: reviewTestRequest().Target})
	badOpen := strings.TrimSuffix(string(openRaw), "}") + `,"cursor":""}`
	if decodeReview([]byte(badOpen), &ReviewOpenRequest{}) == nil {
		t.Fatal("empty optional cursor accepted")
	}
	h, err := reviewRequestHash(reviewTestPrincipal(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, addition := range []string{`,"technical_refs":[]`, `,"supersedes_record_id":null`} {
		modified := strings.TrimSuffix(string(raw), "}") + addition + "}"
		var other ReviewAppendRequest
		if decodeReview([]byte(modified), &other) != nil {
			t.Fatal("explicit nullable/empty rejected")
		}
		changed, _ := reviewRequestHash(reviewTestPrincipal(), other)
		if changed == h {
			t.Fatal("explicit field identity lost")
		}
	}
	p := reviewTestPrincipal()
	p.RequestID = testID(999)
	same, _ := reviewRequestHash(p, r)
	if same != h {
		t.Fatal("tracking request ID changed intent")
	}
	p.ReviewOrigin = "agent"
	different, _ := reviewRequestHash(p, r)
	if different != h {
		t.Fatal("trusted identity polluted business-input fingerprint")
	}
	r.Scope.Claims = []string{"image_reading", "space_layout"}
	raw, _ = json.Marshal(r)
	var a, b ReviewAppendRequest
	_ = decodeReview(raw, &a)
	_ = decodeReview([]byte(strings.Replace(string(raw), `["image_reading","space_layout"]`, `["space_layout","image_reading"]`, 1)), &b)
	ha, _ := reviewRequestHash(p, a)
	hb, _ := reviewRequestHash(p, b)
	if ha == hb {
		t.Fatal("original array order lost")
	}
}

func TestReviewCanonicalNumbersAndScope(t *testing.T) {
	_, canonical, e := reviewHash(map[string]any{"z": 1.0, "a": "证据"})
	if e != nil || string(canonical) != `{"a":"证据","z":1}` {
		t.Fatal(string(canonical), e)
	}
	for _, v := range []any{int64(1 << 53), math.Inf(1), "e\u0301"} {
		if _, _, e = reviewHash(v); e == nil {
			t.Fatal("noncanonical value accepted")
		}
	}
	for _, number := range []string{`9007199254740990.5`, `0.10000000000000000001`, `1e-999999999`} {
		if _, _, e := reviewHash(json.RawMessage(`{"number":` + number + `}`)); e == nil {
			t.Fatal("precision loss accepted", number)
		}
	}
	for _, number := range []string{`0.10`, `1e-1`, `0e-999999999`, `-0`} {
		if _, _, e := reviewHash(json.RawMessage(`{"number":` + number + `}`)); e != nil {
			t.Fatal("equivalent decimal rejected", number, e)
		}
	}
	var rectangle ReviewImageRect
	if decodeReview([]byte(`{"x":0.10000000000000000001,"y":0,"w":1,"h":1}`), &rectangle) == nil {
		t.Fatal("typed decode silently rounded original JSON")
	}
	r := reviewTestRequest()
	r.Scope.CameraIDs = []string{"b", "a"}
	normalized, e := normalizeScope(r.Scope)
	if e != nil || normalized.CameraIDs[0] != "a" || r.Scope.CameraIDs[0] != "b" {
		t.Fatal("sort changed caller or failed")
	}
	r.Scope.EvidenceIDs = append(r.Scope.EvidenceIDs, r.Scope.EvidenceIDs[0])
	if _, e = normalizeScope(r.Scope); e == nil {
		t.Fatal("duplicate reference accepted")
	}
	r = reviewTestRequest()
	r.Scope.ImageRect = &ReviewImageRect{X: math.NaN(), W: 1, H: 1}
	if _, e = normalizeScope(r.Scope); e == nil {
		t.Fatal("NaN accepted")
	}
}

func TestReviewHumanScopeAggregationAndExplicitSupersession(t *testing.T) {
	target := reviewTestTarget()
	r := reviewTestRequest()
	r.Action = "approve"
	if e := validateReviewScope(target, r.Scope, r.Action); e != nil {
		t.Fatal(e)
	}
	agent := reviewTestPrincipal()
	agent.ReviewOrigin = "agent"
	if !errors.Is(validateReviewAppend(r, agent), ErrForbidden) {
		t.Fatal("agent decision accepted")
	}
	record := ReviewRecord{ID: testID(10), Action: "comment", Scope: r.Scope, Actor: ReviewActor{Origin: "agent"}}
	if state, _ := reviewAggregate(target, []ReviewRecord{record}); state != "unreviewed" {
		t.Fatal("agent changed human state")
	}
	record.Actor.Origin = "human_interactive"
	record.Action = "approve"
	if state, _ := reviewAggregate(target, []ReviewRecord{record}); state != "in_review" {
		t.Fatal("image approval promoted whole scene")
	}
	r.Action = "request_changes"
	if !errors.Is(validateReviewDecision([]ReviewRecord{record}, r), ErrReviewConflict) {
		t.Fatal("prior decision silently overwritten")
	}
	r.Supersedes = &record.ID
	if e := validateReviewDecision([]ReviewRecord{record}, r); e != nil {
		t.Fatal(e)
	}
	record.Action = "request_changes"
	if state, _ := reviewAggregate(target, []ReviewRecord{record}); state != "rejected" {
		t.Fatal("required evidence rejection lost")
	}
	all := ReviewScope{Kind: "full_target", EvidenceIDs: []string{testID(4)}, CameraIDs: []string{"front"}, RegionIDs: []string{"main"}, Claims: []string{"space_layout", "source_recovery", "full_set"}}
	if !errors.Is(validateReviewScope(target, all, "approve"), ErrReviewScope) {
		t.Fatal("missing recovery accepted")
	}
	target.Requirements = []ReviewRequirement{}
	for _, claim := range all.Claims {
		target.Requirements = append(target.Requirements, ReviewRequirement{ID: claim, Claim: claim, EvidenceIDs: all.EvidenceIDs, State: "satisfied"})
	}
	record.Scope = all
	record.Action = "approve"
	if state, _ := reviewAggregate(target, []ReviewRecord{record}); state != "approved" {
		t.Fatal("fully evidenced explicit decision lost")
	}
	local := ReviewRecord{ID: testID(11), Target: target, Scope: r.Scope, Action: "request_changes", Actor: ReviewActor{Origin: "human_interactive"}}
	resolved := local
	resolved.ID = testID(12)
	resolved.Action = "approve"
	resolved.Supersedes = &local.ID
	if state, _ := reviewAggregate(target, []ReviewRecord{record, local, resolved}); state != "in_review" {
		t.Fatal("resolving local objection revived an old full approval")
	}
}

func TestReviewMappingsCannotManufactureRecovery(t *testing.T) {
	target := reviewTestTarget()
	target.Evidence = append(target.Evidence, ReviewEvidence{ID: testID(5), FileID: testID(5), MIME: "application/json", SHA256: strings.Repeat("b", 64), CameraIDs: []string{}})
	doc := `{"schema_version":1,"kind":"set-review-evidence","coverage":{"camera_ids":["front"],"region_ids":["main"],"space_basis_file_id":"` + testID(5) + `","cameras":[{"camera_id":"front","parameters_file_id":"` + testID(5) + `","evidence_file_ids":["` + testID(4) + `"],"region_ids":["main"]}]},"evidence":[{"file_id":"` + testID(4) + `","role":"render","camera_ids":["front"],"region_ids":["main"],"frame_range":null}],"checks":[{"claim":"source_recovery","evidence_file_ids":["` + testID(5) + `"]}]}`
	if e := applyReviewMapping(&target, []byte(doc)); e != nil {
		t.Fatal(e)
	}
	if target.Coverage.Cameras[0].Verified || target.Requirements[1].State != "unverified" {
		t.Fatal("declaration became verified recovery")
	}
	for _, bad := range []string{strings.Replace(doc, `"claim":"source_recovery"`, `"claim":"source_recovery","pass":true`, 1), strings.Replace(doc, testID(5), testID(999), 1), strings.Replace(doc, `"camera_ids":["front"]`, `"camera_ids":["front","front"]`, 1)} {
		copy := reviewTestTarget()
		if applyReviewMapping(&copy, []byte(bad)) == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
}

type reviewArtifact struct {
	Artifact
	meta   ContentVersion
	data   []byte
	access Access
}

func (a *reviewArtifact) Metadata(_ context.Context, _ Principal, _ ContentRef, access Access) (ContentVersion, error) {
	a.access = access
	return a.meta, nil
}
func (a *reviewArtifact) Read(context.Context, Principal, ContentRef, Access, int64) ([]byte, error) {
	return a.data, nil
}
func (a *reviewArtifact) Download(_ context.Context, _ Principal, _ ContentRef, _ Access, w io.Writer, _ int64) error {
	_, e := w.Write(a.data)
	return e
}
func TestReviewVerifiesStreamBytesAndPublishedAccess(t *testing.T) {
	p := reviewTestPrincipal()
	ref := ContentRef{testID(1), testID(2), testID(3)}
	b := []byte("fixed image bytes")
	a := &reviewArtifact{data: b, meta: ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, SHA256: reviewDigest(b), Size: int64(len(b))}}
	s := &Service{content: a}
	access := Access{AssetID: testID(4), AssetVersionID: testID(5)}
	if _, e := s.reviewVerify(t.Context(), p, ref, access); e != nil || a.access != access {
		t.Fatal(e)
	}
	a.data = []byte("different bytes")
	if _, e := s.reviewVerify(t.Context(), p, ref, access); !errors.Is(e, ErrNotReady) {
		t.Fatal("stream trusted without byte verification", e)
	}
}

func TestReviewTrustedOriginAndMCPBoundary(t *testing.T) {
	svc := &Service{}
	token := strings.Repeat("x", 32)
	handler, _ := svc.HTTPHandler(token)
	makeRequest := func(path, body string) *http.Request {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Organization-Id", testID(30))
		r.Header.Set("X-User-Id", testID(31))
		r.Header.Set("X-Request-Id", testID(32))
		return r
	}
	for _, values := range [][]string{{"human_interactive", "agent"}, {"spoof"}, {""}} {
		r := makeRequest("/internal/v1/world/review/append", "{}")
		for _, v := range values {
			r.Header.Add("X-Review-Origin", v)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("malformed origin accepted", w.Code)
		}
	}
	request := reviewTestRequest()
	request.Action = "approve"
	raw, _ := json.Marshal(request)
	r := makeRequest("/internal/v1/world/review/append", string(raw))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing origin inferred human")
	}
	mcp, _ := svc.MCPHandler(token)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"world.review_append","arguments":` + string(raw) + `}}`
	r = makeRequest("/mcp", body)
	r.Header.Set("X-Review-Origin", "human_interactive")
	w = httptest.NewRecorder()
	mcp.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "PERMISSION_DENIED") {
		t.Fatal("MCP human spoof reached service", w.Code, w.Body.String())
	}
}

func reviewCameraJSON(basis string) []byte {
	return []byte(`{"schema_version":1,"kind":"set-review-camera","space_basis_sha256":"` + basis + `","coordinate_system":"blender_z_up_m","camera":{"projection":"PERSP","location_blender_m":[0,-5,2],"rotation_euler_xyz_rad":[1.2,0,0],"lens_mm":50,"sensor_width_mm":36,"sensor_height_mm":24,"sensor_fit":"AUTO","clip_start_m":0.1,"clip_end_m":1000,"ortho_scale":10,"shift_x":0,"shift_y":0},"render":{"resolution_x":1920,"resolution_y":1080,"pixel_aspect_x":1,"pixel_aspect_y":1,"frame":1}}`)
}
func TestReviewCameraParametersRequireFixedImageMapping(t *testing.T) {
	basis := strings.Repeat("a", 64)
	raw := reviewCameraJSON(basis)
	if !validateReviewCamera(raw, basis) {
		t.Fatal("valid fixed camera rejected")
	}
	for _, bad := range []string{strings.Replace(string(raw), `"clip_end_m":1000`, `"clip_end_m":0.01`, 1), strings.Replace(string(raw), `[0,-5,2]`, `[0,-5]`, 1), strings.Replace(string(raw), `"frame":1`, `"frame":1,"extra":1`, 1), strings.Replace(string(raw), basis, strings.Repeat("b", 64), 1), strings.Replace(string(raw), `"frame":1`, `"frame":9007199254740992`, 1)} {
		if validateReviewCamera([]byte(bad), basis) {
			t.Fatal("invalid camera accepted")
		}
	}
	target := reviewTestTarget()
	target.Evidence[0].SHA256 = basis
	parameter := ReviewEvidence{ID: testID(5), FileID: testID(5), MIME: "application/json", SHA256: reviewDigest(raw), Size: int64(len(raw))}
	target.Evidence = append(target.Evidence, parameter)
	target.Coverage.SpaceBasis = &ReviewSpaceBasis{testID(4), basis}
	target.Coverage.Cameras = []ReviewCamera{{CameraID: "front", ParametersID: testID(5), ParametersSHA256: parameter.SHA256, EvidenceIDs: []string{testID(4)}, RegionIDs: []string{}}}
	svc := &Service{content: &reviewArtifact{data: raw}}
	if e := svc.verifyReviewCameras(t.Context(), reviewTestPrincipal(), &target); e != nil || !target.Coverage.Cameras[0].Verified {
		t.Fatal("fixed static mapping not verified", e)
	}
	if target.Requirements[0].State != "missing" {
		t.Fatal("camera verification manufactured recovery")
	}
	target.Evidence[0].MIME = "video/mp4"
	if e := svc.verifyReviewCameras(t.Context(), reviewTestPrincipal(), &target); e != nil || target.Coverage.Cameras[0].Verified {
		t.Fatal("static camera extrapolated to video")
	}
	target.Evidence[0].MIME = "image/png"
	target.Coverage.Cameras[0].EvidenceIDs = []string{testID(999)}
	if e := svc.verifyReviewCameras(t.Context(), reviewTestPrincipal(), &target); e != nil || target.Coverage.Cameras[0].Verified {
		t.Fatal("unregistered evidence accepted")
	}
}
