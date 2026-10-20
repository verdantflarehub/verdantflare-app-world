//go:build integration

package artifactclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

type noProject struct{ calls atomic.Int32 }

func (p *noProject) Open(context.Context, world.Principal, string, string) (world.OpenResult, error) {
	p.calls.Add(1)
	return world.OpenResult{}, world.ErrForbidden
}
func (p *noProject) Authorize(context.Context, world.Principal, world.Permission) error {
	p.calls.Add(1)
	return world.ErrForbidden
}
func newTestID() string { return uuid.Must(uuid.NewV7()).String() }
func testAddress(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a := l.Addr().String()
	l.Close()
	return a
}

// Runs only on dev with a separate Artifact executable, PostgreSQL and private
// disposable S3. Compiling this test does not start any local service.
func TestWorldDraftAcrossArtifactS3AndRestart(t *testing.T) {
	runWorldDraftAcrossArtifact(t, false)
}

func TestWorldPromptProjectAcrossArtifact(t *testing.T) {
	if os.Getenv("WORLD_TEST_PROJECT_BINARY") == "" {
		t.Skip("remote Project executable required")
	}
	runWorldDraftAcrossArtifact(t, true)
}

func runWorldDraftAcrossArtifact(t *testing.T, withProject bool) {
	binary, endpoint := os.Getenv("WORLD_TEST_ARTIFACT_BINARY"), os.Getenv("WORLD_TEST_S3_ENDPOINT")
	if binary == "" || endpoint == "" {
		t.Skip("remote Artifact executable and disposable S3 endpoint required")
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("test S3 must be loopback")
	}
	readCredential := func(name string) string {
		t.Helper()
		b, e := os.ReadFile(os.Getenv(name))
		if e != nil {
			t.Fatal("test S3 credential file unavailable")
		}
		return strings.TrimSpace(string(b))
	}
	access, secret := readCredential("WORLD_TEST_S3_USER_FILE"), readCredential("WORLD_TEST_S3_PASSWORD_FILE")
	s3, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: false, Region: "us-east-1"})
	if err != nil {
		t.Fatal("test S3 client initialization failed")
	}
	ctx := t.Context()
	bucket := "world-check-" + uuid.NewString()
	if err = s3.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: "us-east-1"}); err != nil {
		t.Fatal("disposable bucket creation failed", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for obj := range s3.ListObjects(c, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err != nil {
				t.Error("test object listing failed")
				break
			}
			if e := s3.RemoveObject(c, bucket, obj.Key, minio.RemoveObjectOptions{}); e != nil {
				t.Error("test object cleanup failed")
			}
		}
		if e := s3.RemoveBucket(c, bucket); e != nil {
			t.Error("test bucket cleanup failed")
		}
	})
	worldDB, artifactDB := testdb.New(t), testdb.New(t)
	if err = migrations.ApplyFresh(ctx, worldDB, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err = migrations.ActivateFresh(ctx, worldDB); err != nil {
		t.Fatal(err)
	}
	// pgx retains its original DSN when Database changes; explicitly replace it.
	dsn, err := url.Parse(artifactDB.Config().ConnString())
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	dsn.Path = "/" + artifactDB.Config().ConnConfig.Database
	dsn.RawPath = ""
	q := dsn.Query()
	q.Del("dbname")
	q.Del("database")
	dsn.RawQuery = q.Encode()
	serviceToken, authorityToken, projectToken, worldToken := newTestID(), newTestID(), newTestID(), newTestID()
	type handlers struct{ h http.Handler }
	var handler atomic.Pointer[handlers]
	handler.Store(&handlers{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })})
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.Load().h.ServeHTTP(w, r) }))
	defer authority.Close()
	var projectCalls atomic.Int32
	var projectHandler atomic.Pointer[handlers]
	projectAuthority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectCalls.Add(1)
		if h := projectHandler.Load(); h != nil {
			h.h.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(403)
		io.WriteString(w, `{"allowed":false}`)
	}))
	defer projectAuthority.Close()
	address := testAddress(t)
	env := []string{"PATH=" + os.Getenv("PATH"), "ARTIFACT_DATABASE_URL=" + dsn.String(), "ARTIFACT_STORE_ID=" + newTestID(), "ARTIFACT_SERVICE_TOKEN=" + serviceToken, "ARTIFACT_AUTHORITY_MODE=split", "ARTIFACT_PROJECT_AUTHORITY_URL=" + projectAuthority.URL, "ARTIFACT_PROJECT_AUTHORITY_TOKEN=" + projectToken, "ARTIFACT_WORLD_AUTHORITY_URL=" + authority.URL + "/internal/v1/artifact/authorize", "ARTIFACT_WORLD_AUTHORITY_TOKEN=" + authorityToken, "ARTIFACT_LISTEN_ADDR=" + address, "ARTIFACT_STORAGE_BACKEND=s3", "ARTIFACT_S3_ENDPOINT=" + endpoint, "ARTIFACT_S3_BUCKET=" + bucket, "ARTIFACT_S3_ACCESS_KEY=" + access, "ARTIFACT_S3_SECRET_KEY=" + secret, "ARTIFACT_S3_REGION=us-east-1", "ARTIFACT_S3_SECURE=false"}
	migrate := exec.CommandContext(ctx, binary, "migrate")
	migrate.Env = env
	if out, e := migrate.CombinedOutput(); e != nil {
		t.Fatalf("Artifact migration failed: %s", out)
	}
	start := func() func() {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, "serve")
		cmd.Env = env
		if e := cmd.Start(); e != nil {
			t.Fatal("Artifact process did not start")
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
		t.Cleanup(stop)
		client := &http.Client{Timeout: time.Second}
		for attempt := 0; attempt < 100; attempt++ {
			response, e := client.Get("http://" + address + "/health")
			if e == nil {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode == 200 {
					return stop
				}
			}
			select {
			case <-done:
				stopped = true
				t.Fatal("Artifact process exited before readiness")
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("Artifact process readiness timeout")
		return stop
	}
	stop := start()
	target, _ := url.Parse("http://" + address)
	reverse := httputil.NewSingleHostReverseProxy(target)
	reverse.ErrorLog = log.New(io.Discard, "", 0)
	var loseRetention atomic.Bool
	reverse.ModifyResponse = func(r *http.Response) error {
		if r.Request.Method == "POST" && r.Request.URL.Path == "/v2/artifacts/retentions" && r.StatusCode == 200 && loseRetention.CompareAndSwap(true, false) {
			r.Body.Close()
			return errors.New("synthetic lost retention response")
		}
		return nil
	}
	proxy := httptest.NewServer(reverse)
	defer proxy.Close()
	content, err := New(proxy.URL, serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	projects := &noProject{}
	projectCoordToken, worldCoordToken := newTestID(), newTestID()
	var service *world.Service
	rebuild := func() {
		t.Helper()
		s, e := world.NewService(ctx, worldDB, content, projects, 1, map[string]coordination.Peer{})
		if e != nil {
			t.Fatal(e)
		}
		service = s
		m, e := s.MCPHandler(worldToken)
		if e != nil {
			t.Fatal(e)
		}
		mux := http.NewServeMux()
		mux.Handle("/mcp", m)
		internal, e := s.HTTPHandler(worldToken)
		if e != nil {
			t.Fatal(e)
		}
		coord, e := s.Coordination().Handler(map[string]string{"project": projectCoordToken})
		if e != nil {
			t.Fatal(e)
		}
		mux.Handle("/internal/v1/coordination/", coord)
		mux.Handle("/internal/v1/world/", internal)
		handler.Store(&handlers{mux})
	}
	rebuild()
	p := world.Principal{OrganizationID: newTestID(), SubjectID: newTestID(), RequestID: newTestID()}
	call := func(actor world.Principal, name string, args any, out any, wantFailure bool) {
		t.Helper()
		raw, e := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": actor.RequestID, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		if e != nil {
			t.Fatal(e)
		}
		req, _ := http.NewRequestWithContext(ctx, "POST", authority.URL+"/mcp", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+worldToken)
		req.Header.Set("X-Organization-Id", actor.OrganizationID)
		req.Header.Set("X-User-Id", actor.SubjectID)
		req.Header.Set("X-Request-Id", actor.RequestID)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var result struct {
			Result struct {
				IsError bool            `json:"isError"`
				Data    json.RawMessage `json:"structuredContent"`
			} `json:"result"`
		}
		if json.NewDecoder(resp.Body).Decode(&result) != nil || len(result.Result.Data) == 0 || result.Result.IsError != wantFailure {
			t.Fatal("unexpected MCP result", name, resp.StatusCode)
		}
		if out != nil && json.Unmarshal(result.Result.Data, out) != nil {
			t.Fatal("invalid structured MCP data", name)
		}
	}
	subjects := []string{}
	create := world.DraftRequest{Mode: "create", CommitID: newTestID(), Purpose: "learning", Name: "Independent learning", Subjects: &subjects}
	loseRetention.Store(true)
	if _, err = service.WorldDraft(ctx, p, create); !errors.Is(err, world.ErrDependency) {
		t.Fatal("lost retention acknowledgement did not leave resumable operation", err)
	}
	var pending world.DraftStatus
	call(p, "world.draft", world.DraftRequest{Mode: "status", CommitID: create.CommitID}, &pending, false)
	if pending.State != "preparing" {
		t.Fatal("lost creation response was not recoverable")
	}
	var before int
	if err = artifactDB.QueryRow(ctx, "SELECT count(*) FROM station.artifact_versions").Scan(&before); err != nil || before != 1 {
		t.Fatal("initial manifest was not physically committed", err)
	}
	stop()
	stop = start()
	rebuild()
	var created world.DraftRevision
	call(p, "world.draft", create, &created, false)
	var after int
	if err = artifactDB.QueryRow(ctx, "SELECT count(*) FROM station.artifact_versions").Scan(&after); err != nil || after != before {
		t.Fatal("restart duplicated immutable content", err)
	}
	write := func(text, mime string) world.ContentRef {
		t.Helper()
		v, e := content.write(ctx, p, world.Source{Kind: "user_edit", WorldDraftID: created.DraftID}, world.TextWrite{WriteID: newTestID(), Text: text, MIME: mime})
		if e != nil {
			t.Fatal("draft content write failed", e)
		}
		return v.ContentRef
	}
	var cases []struct {
		Case     string          `json:"case"`
		Valid    bool            `json:"valid"`
		Document json.RawMessage `json:"document"`
	}
	fixture, e := os.ReadFile("../world/testdata/reference_learning_cases.generated.json")
	if e != nil || json.Unmarshal(fixture, &cases) != nil {
		t.Fatal("learning fixture unavailable")
	}
	var learning string
	for _, c := range cases {
		if c.Valid && c.Case == "test_original_prompt_needs_no_project_or_source" {
			learning = string(c.Document)
		}
	}
	if learning == "" {
		t.Fatal("learning fixture missing")
	}
	promptRef, privateRef := write(learning, "application/json"), write("PRIVATE_REFERENCE_NOT_PUBLISHED", "text/plain")
	promptID, privateID := newTestID(), newTestID()
	manifest := created.Manifest
	manifest.Files = []world.File{{ID: promptID, Path: "learning.json", Role: "knowledge", Content: promptRef}, {ID: privateID, Path: "private.txt", Role: "reference", Content: privateRef}}
	manifest.DomainDocuments = []world.DomainDocument{{Type: "reference-learning", FileID: promptID}}
	var saved world.DraftRevision
	call(p, "world.draft", world.DraftRequest{Mode: "commit", DraftID: created.DraftID, ExpectedRevisionID: created.RevisionID, CommitID: newTestID(), Manifest: &manifest}, &saved, false)
	var asset world.WorldResult
	register := world.WorldRegisterRequest{CommitID: newTestID(), SourceDraftID: saved.DraftID, SourceRevisionID: saved.RevisionID, FileIDs: []string{promptID}, Name: "Reusable prompt", AssetType: "prompt", Subjects: []string{}, Relation: "produced_in"}
	call(p, "world.register", register, &asset, false)
	var status world.WorldStatus
	call(p, "world.commit_status", world.WorldStatusRequest{CommitID: register.CommitID}, &status, false)
	if status.State != "committed" || status.Result == nil || status.Result.AssetID != asset.AssetID {
		t.Fatal("registration lost its fixed result")
	}
	guest := world.Principal{OrganizationID: p.OrganizationID, SubjectID: newTestID(), RequestID: newTestID()}
	call(p, "world.grant", world.WorldGrantRequest{AssetID: asset.AssetID, SubjectID: guest.SubjectID, Role: "reader"}, nil, false)
	assetAccess := world.Access{AssetID: asset.AssetID, AssetVersionID: asset.VersionID}
	got, err := content.Read(ctx, guest, promptRef, assetAccess, world.MaxManifestBytes)
	if err != nil || string(got) != learning {
		t.Fatal("shared prompt bytes did not survive Artifact/S3", err)
	}
	for _, ref := range []world.ContentRef{privateRef, saved.ManifestRef} {
		if _, err = content.Read(ctx, guest, ref, assetAccess, world.MaxManifestBytes); !errors.Is(err, world.ErrForbidden) {
			t.Fatal("asset grant exposed private draft content", err)
		}
	}
	if _, err = content.Read(ctx, guest, promptRef, world.Access{WorldDraftID: saved.DraftID, WorldDraftRevisionID: saved.RevisionID}, world.MaxManifestBytes); !errors.Is(err, world.ErrForbidden) {
		t.Fatal("asset grant exposed source workspace", err)
	}
	foreign := guest
	foreign.OrganizationID = newTestID()
	if _, err = content.Read(ctx, foreign, promptRef, assetAccess, world.MaxManifestBytes); !errors.Is(err, world.ErrForbidden) && !errors.Is(err, world.ErrNotFound) {
		t.Fatal("cross-organization read was not denied", err)
	}
	call(guest, "world.commit_status", world.WorldStatusRequest{CommitID: register.CommitID}, nil, true)
	// Use the actual Dashboard derivation shape, rebinding only the synthetic
	// source identifiers. A source asset reader owns no source draft permission.
	derivedFixture, err := os.ReadFile("testdata/prompt-copy.generated.json")
	if err != nil {
		t.Fatal("Dashboard prompt fixture unavailable", err)
	}
	for old, actual := range map[string]string{
		"01900000-0000-7000-8000-000000000001": asset.AssetID,
		"01900000-0000-7000-8000-000000000002": asset.VersionID,
		"01900000-0000-7000-8000-000000000005": promptID,
	} {
		derivedFixture = bytes.ReplaceAll(derivedFixture, []byte(old), []byte(actual))
	}
	var derivative struct {
		SourceDocument any `json:"source_document"`
		Plan           struct {
			Name       string                 `json:"name"`
			Subjects   []string               `json:"subjects"`
			Document   json.RawMessage        `json:"document"`
			SourceRefs []world.DraftSourceRef `json:"source_refs"`
			AssetRefs  []world.AssetRef       `json:"asset_refs"`
		} `json:"plan"`
	}
	var sourceDocument any
	if json.Unmarshal(derivedFixture, &derivative) != nil || json.Unmarshal([]byte(learning), &sourceDocument) != nil || !reflect.DeepEqual(derivative.SourceDocument, sourceDocument) {
		t.Fatal("Dashboard fixture does not describe the published original prompt")
	}
	var derivedDraft world.DraftRevision
	call(guest, "world.draft", world.DraftRequest{Mode: "create", CommitID: newTestID(), Purpose: "creation", Name: derivative.Plan.Name, Subjects: &derivative.Plan.Subjects}, &derivedDraft, false)
	written, err := content.write(ctx, guest, world.Source{Kind: "user_edit", WorldDraftID: derivedDraft.DraftID}, world.TextWrite{WriteID: newTestID(), Text: string(derivative.Plan.Document), MIME: "application/json"})
	if err != nil {
		t.Fatal("derived prompt write failed", err)
	}
	derivedID := newTestID()
	derivedManifest := derivedDraft.Manifest
	derivedManifest.Files = []world.File{{ID: derivedID, Path: "prompt.json", Role: "prompt", Content: written.ContentRef}}
	derivedManifest.DomainDocuments = []world.DomainDocument{{Type: "reference-learning", FileID: derivedID}}
	derivedManifest.SourceRefs = derivative.Plan.SourceRefs
	derivedManifest.AssetRefs = derivative.Plan.AssetRefs
	derivedCommit := world.DraftRequest{Mode: "commit", DraftID: derivedDraft.DraftID, ExpectedRevisionID: derivedDraft.RevisionID, CommitID: newTestID(), Manifest: &derivedManifest}
	var derivedSaved world.DraftRevision
	call(guest, "world.draft", derivedCommit, &derivedSaved, false)
	var fixedDraft world.DraftRevision
	call(guest, "world.draft", world.DraftRequest{Mode: "get", DraftID: derivedSaved.DraftID, RevisionID: derivedSaved.RevisionID}, &fixedDraft, false)
	if !reflect.DeepEqual(fixedDraft.Manifest, derivedManifest) {
		t.Fatal("derived draft fixed readback changed")
	}
	derivedRegister := world.WorldRegisterRequest{CommitID: newTestID(), SourceDraftID: derivedSaved.DraftID, SourceRevisionID: derivedSaved.RevisionID, FileIDs: []string{derivedID}, Name: derivative.Plan.Name, AssetType: "prompt", Subjects: derivative.Plan.Subjects, Relation: "curated_in", DependsOn: &derivative.Plan.AssetRefs}
	var derivedAsset world.WorldResult
	call(guest, "world.register", derivedRegister, &derivedAsset, false)
	if derivedAsset.AssetID == asset.AssetID || !reflect.DeepEqual(derivedAsset.Manifest.DependsOn, derivative.Plan.AssetRefs) {
		t.Fatal("derivation overwrote source or lost fixed dependencies")
	}
	var recovered world.WorldResult
	call(guest, "world.register", derivedRegister, &recovered, false)
	if !reflect.DeepEqual(derivedAsset, recovered) {
		t.Fatal("registration retry duplicated derived asset")
	}
	var derivedStatus world.WorldStatus
	call(guest, "world.commit_status", world.WorldStatusRequest{CommitID: derivedRegister.CommitID}, &derivedStatus, false)
	if derivedStatus.State != "committed" || derivedStatus.Result == nil || derivedStatus.Result.AssetID != derivedAsset.AssetID {
		t.Fatal("derived registration status lost asset")
	}
	derivedAccess := world.Access{AssetID: derivedAsset.AssetID, AssetVersionID: derivedAsset.VersionID}
	checkDerived := func() {
		t.Helper()
		call(guest, "world.get", world.WorldGetRequest{AssetID: derivedAsset.AssetID, VersionID: derivedAsset.VersionID}, &recovered, false)
		if !reflect.DeepEqual(recovered, derivedAsset) {
			t.Fatal("fixed derived asset changed")
		}
		body, e := content.Read(ctx, guest, written.ContentRef, derivedAccess, world.MaxManifestBytes)
		if e != nil || !bytes.Equal(body, derivative.Plan.Document) {
			t.Fatal("derived prompt body changed", e)
		}
		for _, ref := range []world.ContentRef{promptRef, privateRef, saved.ManifestRef} {
			if _, e = content.Read(ctx, guest, ref, derivedAccess, world.MaxManifestBytes); !errors.Is(e, world.ErrForbidden) {
				t.Fatal("derived grant exposed source content", e)
			}
		}
	}
	checkDerived()
	var unchanged world.WorldResult
	call(p, "world.get", world.WorldGetRequest{AssetID: asset.AssetID, VersionID: asset.VersionID}, &unchanged, false)
	if !reflect.DeepEqual(unchanged, asset) {
		t.Fatal("derivation changed original asset version")
	}
	var listing world.WorldListResult
	call(p, "world.list", world.WorldListRequest{AssetType: "prompt", Limit: 50}, &listing, false)
	foundOriginal := false
	for _, item := range listing.Items {
		if item.AssetID == asset.AssetID {
			foundOriginal = true
			if item.HeadVersionID != asset.VersionID {
				t.Fatal("derivation changed original asset head")
			}
		}
	}
	if !foundOriginal {
		t.Fatal("original asset disappeared from directory")
	}
	// Reconstruct World and restart the Artifact process before the final read.
	stop()
	stop = start()
	rebuild()
	call(guest, "world.get", world.WorldGetRequest{AssetID: asset.AssetID, VersionID: asset.VersionID}, &asset, false)
	got, err = content.Read(ctx, guest, promptRef, assetAccess, world.MaxManifestBytes)
	if err != nil || string(got) != learning {
		t.Fatal("fixed asset failed after restart", err)
	}
	checkDerived()
	// This harness has no background worker. A durable pending revocation must
	// wait for the same recovery/outbox processing used by the real service.
	var revokePending struct {
		Code string `json:"code"`
	}
	call(p, "world.revoke", world.WorldRevokeRequest{AssetID: asset.AssetID, SubjectID: guest.SubjectID}, &revokePending, true)
	if revokePending.Code != coordination.ErrPending.Error() {
		t.Fatal("revocation did not report outstanding reservations", revokePending.Code)
	}
	if _, err = content.Read(ctx, guest, promptRef, assetAccess, world.MaxManifestBytes); err != nil {
		t.Fatal("pending revocation prematurely removed access", err)
	}
	if err = service.Recover(ctx); err != nil {
		t.Fatal("restart recovery failed to finalize committed operations", err)
	}
	call(p, "world.revoke", world.WorldRevokeRequest{AssetID: asset.AssetID, SubjectID: guest.SubjectID}, nil, false)
	if _, err = content.Read(ctx, guest, promptRef, assetAccess, world.MaxManifestBytes); !errors.Is(err, world.ErrForbidden) {
		t.Fatal("revoked reader kept Artifact access", err)
	}
	// A saved derivative remains its own content; its source reference grants no
	// access after revocation, and future dependency-consuming use must recheck.
	checkDerived()
	if _, err = service.References(ctx, guest, world.AssetRef{AssetID: derivedAsset.AssetID, VersionID: derivedAsset.VersionID, Purpose: "test_reuse"}); !errors.Is(err, world.ErrForbidden) {
		t.Fatal("dependency-consuming use bypassed revoked source", err)
	}
	t.Log("Dashboard prompt derivation: reader-only source -> independent creation draft -> new asset; fixed body, idempotent register, restart, unchanged original and source revocation passed")
	if projects.calls.Load() != 0 || projectCalls.Load() != 0 {
		t.Fatal("independent learning fell back to Project")
	}
	if withProject {
		// Independent analysis above must not use Project. Actual creative use
		// below intentionally creates one real project and binds the asset.
		call(p, "world.grant", world.WorldGrantRequest{AssetID: asset.AssetID, SubjectID: guest.SubjectID, Role: "reader"}, nil, false)
		checkPromptProject(t, service, content, authority.URL, worldToken, projectToken, serviceToken,
			projectCoordToken, worldCoordToken, proxy.URL, guest, asset, promptRef, privateRef,
			func(h http.Handler) { projectHandler.Store(&handlers{h}) })
	}
	objects := 0
	for obj := range s3.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			t.Fatal("S3 evidence listing failed")
		}
		objects++
		response, e := http.Get("http://" + endpoint + "/" + bucket + "/" + obj.Key)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("S3 object available without credentials")
		}
	}
	if objects < 5 {
		t.Fatal("missing physical S3 objects")
	}
	t.Logf("real Artifact HTTP + private S3: %d objects; restart/recovery/read/revoke passed; Project authority calls=%d", objects, projectCalls.Load())
}
