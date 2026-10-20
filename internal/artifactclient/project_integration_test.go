//go:build integration

package artifactclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
)

// Project is an actual separately built service process. All databases, roles,
// files and requests here belong to the disposable remote test job.
func checkPromptProject(t *testing.T, service *world.Service, content *Client, worldURL, worldToken, authorityToken, artifactToken, projectCoordToken, worldCoordToken, artifactURL string, actor world.Principal, asset world.WorldResult, promptRef, privateRef world.ContentRef, setAuthority func(http.Handler)) {
	t.Helper()
	ctx := t.Context()
	binary := os.Getenv("WORLD_TEST_PROJECT_BINARY")
	db := testdb.New(t)
	dsn, err := url.Parse(db.Config().ConnString())
	if err != nil {
		t.Fatal("invalid disposable project database")
	}
	dsn.Path = "/" + db.Config().ConnConfig.Database
	dsn.RawPath = ""
	q := dsn.Query()
	q.Del("dbname")
	q.Del("database")
	dsn.RawQuery = q.Encode()
	projectToken := newTestID()
	address := testAddress(t)
	env := []string{"PATH=" + os.Getenv("PATH"), "PROJECT_DATABASE_URL=" + dsn.String(), "PROJECT_COORD_FENCE=1", "WORLD_COORD_FENCE=1", "PROJECT_SERVICE_TOKEN=" + projectToken, "PROJECT_AUTHORITY_TOKEN=" + authorityToken, "PROJECT_COORD_TOKEN=" + projectCoordToken, "WORLD_COORD_TOKEN=" + worldCoordToken, "WORLD_SERVICE_URL=" + worldURL, "WORLD_SERVICE_TOKEN=" + worldToken, "ARTIFACT_URL=" + artifactURL, "ARTIFACT_SERVICE_TOKEN=" + artifactToken, "PROJECT_LISTEN=" + address}
	for _, command := range []string{"migrate-fresh", "activate-fresh"} {
		cmd := exec.CommandContext(ctx, binary, command)
		cmd.Env = env
		if err := cmd.Run(); err != nil {
			t.Fatal("disposable Project migration failed", command)
		}
	}
	role := "project_runtime_" + strings.ReplaceAll(newTestID(), "-", "")
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err = db.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN NOINHERIT NOSUPERUSER"); err != nil {
		t.Fatal("isolated runtime role unavailable")
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "DROP OWNED BY "+quoted+"; DROP ROLE "+quoted); err != nil {
			t.Error("isolated runtime role cleanup failed")
		}
	})
	if _, err = db.Exec(ctx, "GRANT USAGE ON SCHEMA studio TO "+quoted+"; GRANT SELECT ON ALL TABLES IN SCHEMA studio TO "+quoted+"; GRANT EXECUTE ON FUNCTION studio.project_locked_fence(text) TO "+quoted); err != nil {
		t.Fatal("runtime read grants failed")
	}
	rows, err := db.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='studio' AND tablename NOT IN ('project_schema_migrations','project_coord_fences','project_handoff')")
	if err != nil {
		t.Fatal("runtime table discovery failed")
	}
	var tables []string
	for rows.Next() {
		var table string
		if rows.Scan(&table) != nil {
			t.Fatal("runtime table read failed")
		}
		tables = append(tables, pgx.Identifier{"studio", table}.Sanitize())
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal("runtime table read failed")
	}
	if _, err = db.Exec(ctx, "GRANT INSERT,UPDATE,DELETE ON "+strings.Join(tables, ",")+" TO "+quoted); err != nil {
		t.Fatal("runtime write grants failed")
	}
	runtimeDSN := *dsn
	runtimeDSN.User = url.User(role)
	env[1] = "PROJECT_DATABASE_URL=" + runtimeDSN.String()
	target, _ := url.Parse("http://" + address)
	reverse := httputil.NewSingleHostReverseProxy(target)
	director := reverse.Director
	reverse.Director = func(r *http.Request) { director(r); r.URL.Path = "/internal/v1/artifact/authorize" }
	setAuthority(reverse)
	peer, err := coordination.NewClient("http://"+address, worldCoordToken)
	if err != nil {
		t.Fatal(err)
	}
	service.Coordination().Peers["project"] = peer
	start := func() func() {
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			t.Fatal("Project process did not start")
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
		for i := 0; i < 100; i++ {
			r, e := client.Get("http://" + address + "/healthz")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 204 {
					return stop
				}
			}
			select {
			case <-done:
				stopped = true
				t.Fatal("Project exited before readiness")
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("Project readiness timeout")
		return stop
	}
	stop := start()
	call := func(p world.Principal, operation string, input, output any, want int) {
		t.Helper()
		raw, e := json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+address+"/internal/v1/project/"+operation, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+projectToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", p.SubjectID)
		req.Header.Set("X-Organization-Id", p.OrganizationID)
		req.Header.Set("X-Request-Id", p.RequestID)
		response, e := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if e != nil {
			t.Fatal("Project transport failed", operation)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			var failure struct {
				Code string `json:"code"`
			}
			_ = json.NewDecoder(response.Body).Decode(&failure)
			t.Fatalf("Project %s: status=%d code=%s want=%d", operation, response.StatusCode, failure.Code, want)
		}
		if output != nil && json.NewDecoder(response.Body).Decode(output) != nil {
			t.Fatal("invalid Project result", operation)
		}
	}
	before, err := service.WorldList(ctx, actor, world.WorldListRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/prompt-project.generated.json")
	if err != nil {
		t.Fatal("Project Dashboard fixture missing")
	}
	for old, actual := range map[string]string{"01900000-0000-7000-8000-000000000001": asset.AssetID, "01900000-0000-7000-8000-000000000002": asset.VersionID, "01900000-0000-7000-8000-000000000003": promptRef.StoreID, "01900000-0000-7000-8000-000000000004": promptRef.ArtifactID, "01900000-0000-7000-8000-000000000005": promptRef.VersionID} {
		fixture = bytes.ReplaceAll(fixture, []byte(old), []byte(actual))
	}
	var input struct {
		Entry string `json:"entry"`
	}
	if json.Unmarshal(fixture, &input) != nil || input.Entry == "" {
		t.Fatal("Project Dashboard fixture invalid")
	}
	create := world.CreateRequest{CommitID: newTestID(), Name: "World prompt project integration", Category: "image", EntryPath: "world-prompt-input.md", EntryText: input.Entry}
	var created, repeated world.Result
	call(actor, "create", create, &created, 200)
	call(actor, "create", create, &repeated, 200)
	if !reflect.DeepEqual(created, repeated) {
		t.Fatal("Project create replay changed result")
	}
	var actualRole string
	var privileged bool
	if err = db.QueryRow(ctx, "SELECT rolname,rolsuper FROM pg_roles WHERE rolname=$1", role).Scan(&actualRole, &privileged); err != nil || privileged || actualRole != role {
		t.Fatal("Project runtime role became privileged")
	}
	var count int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM studio.projects").Scan(&count); err != nil || count != 1 {
		t.Fatal("Project creation duplicated")
	}
	use := world.UseAssetRequest{ProjectID: created.ProjectID, ExpectedRevisionID: created.RevisionID, CommitID: newTestID(), AssetID: asset.AssetID, VersionID: asset.VersionID, Purpose: "world_prompt_creation"}
	var used world.Result
	call(actor, "use_asset", use, &used, 200)
	call(actor, "use_asset", use, &repeated, 200)
	if !reflect.DeepEqual(used, repeated) || len(used.Manifest.AssetRefs) != 1 || used.Manifest.AssetRefs[0].VersionID != asset.VersionID {
		t.Fatal("Project binding replay changed fixed asset")
	}
	stop()
	stop = start()
	var status world.CommitStatus
	call(actor, "commit_status", map[string]string{"project_id": used.ProjectID, "commit_id": use.CommitID}, &status, 200)
	if status.State != "committed" || status.Result == nil || !reflect.DeepEqual(*status.Result, used) {
		t.Fatal("Project restart lost fixed binding")
	}
	var opened world.OpenResult
	call(actor, "open", map[string]string{"project_id": used.ProjectID, "revision_id": used.RevisionID}, &opened, 200)
	if !reflect.DeepEqual(opened.Result, used) {
		t.Fatal("fixed Project read differs from saved revision")
	}
	entryRef := used.Manifest.Files[0].Content
	body, err := content.Read(ctx, actor, entryRef, world.Access{ProjectID: used.ProjectID, RevisionID: used.RevisionID}, world.MaxManifestBytes)
	if err != nil || string(body) != input.Entry {
		t.Fatal("Project entry bytes did not survive restart", err)
	}
	if _, err = content.Read(ctx, actor, privateRef, world.Access{ProjectID: used.ProjectID, RevisionID: used.RevisionID}, world.MaxManifestBytes); !errors.Is(err, world.ErrForbidden) {
		t.Fatal("Project reference exposed private source file", err)
	}
	stranger := actor
	stranger.SubjectID = newTestID()
	call(stranger, "open", map[string]string{"project_id": used.ProjectID, "revision_id": used.RevisionID}, nil, 403)
	after, err := service.WorldList(ctx, actor, world.WorldListRequest{Limit: 50})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("project handoff registered or changed a world asset", err)
	}
	unchanged, err := service.WorldGet(ctx, actor, world.WorldGetRequest{AssetID: asset.AssetID, VersionID: asset.VersionID})
	if err != nil || !reflect.DeepEqual(unchanged, asset) {
		t.Fatal("Project use mutated fixed source asset", err)
	}
	// Recovery must really deliver Project's outbox, not be inferred from a committed response.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var pending int
		err = db.QueryRow(ctx, "SELECT count(*) FROM studio.project_coord_outbox WHERE NOT delivered").Scan(&pending)
		if err != nil {
			t.Fatal("Project recovery outbox query failed", err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Project recovery outbox did not settle")
		}
		time.Sleep(200 * time.Millisecond)
	}
	stop()
	t.Log("actual Project process + private S3: one project, idempotent create/use_asset, fixed input readback, restart, source unchanged, unauthorized/private reads denied; no generation or World registration")
}
