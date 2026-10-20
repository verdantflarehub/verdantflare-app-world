//go:build integration

package world_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/artifactclient"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
)

// Executed only in the immutable Artifact image by the isolated dev Job.
// Copies its delivered executable; never builds Artifact from local sources.
func TestExportArtifactBinaryForLearning(t *testing.T) {
	if os.Getenv("WORLD_TEST_EXPORT_ARTIFACT") != "1" {
		t.Skip("Artifact-image export mode required")
	}
	data, err := os.ReadFile("/station-artifact")
	if err != nil {
		t.Fatal("read delivered Artifact binary")
	}
	if err = os.WriteFile("/work/station-artifact", data, 0700); err != nil {
		t.Fatal("copy delivered Artifact binary")
	}
	sum := sha256.Sum256(data)
	t.Log("ARTIFACT_BINARY_SHA256", hex.EncodeToString(sum[:]))
}

type realLearningPort struct {
	*artifactclient.Client
	loseWrite, loseFinalRead bool
}

func (p *realLearningPort) WriteAnalysisDocument(ctx context.Context, actor world.Principal, task string, w world.TextWrite) (world.ContentVersion, error) {
	v, err := p.Client.WriteAnalysisDocument(ctx, actor, task, w)
	if err == nil && p.loseWrite {
		p.loseWrite = false
		return world.ContentVersion{}, world.ErrDependency
	}
	return v, err
}
func (p *realLearningPort) Read(ctx context.Context, actor world.Principal, ref world.ContentRef, access world.Access, max int64) ([]byte, error) {
	data, err := p.Client.Read(ctx, actor, ref, access, max)
	if err == nil && p.loseFinalRead && access.WorldDraftID != "" {
		v, e := p.Client.Metadata(ctx, actor, ref, access)
		if e != nil {
			return nil, e
		}
		if v.Source.Kind == "task_output" {
			p.loseFinalRead = false
			return nil, world.ErrDependency
		}
	}
	return data, err
}

// Real Artifact 0.2.6 and private disposable S3; observations remain synthetic.
// No business Project, model inference, or user installation participates.
func TestAnalysisLearningSaveArtifactS3(t *testing.T) {
	binary, endpoint := os.Getenv("WORLD_TEST_ARTIFACT_BINARY"), os.Getenv("WORLD_TEST_S3_ENDPOINT")
	if binary == "" || endpoint == "" {
		t.Skip("remote Artifact and S3 required")
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("S3 must be disposable loopback")
	}
	readSecret := func(key string) string {
		b, e := os.ReadFile(os.Getenv(key))
		if e != nil {
			t.Fatal("isolated credential file unavailable")
		}
		return strings.TrimSpace(string(b))
	}
	access, secret := readSecret("WORLD_TEST_S3_USER_FILE"), readSecret("WORLD_TEST_S3_PASSWORD_FILE")
	s3, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: false, Region: "us-east-1"})
	if err != nil {
		t.Fatal("S3 client")
	}
	ctx := t.Context()
	bucket := "world-learning-" + uuid.NewString()
	for attempt := 0; attempt < 60; attempt++ {
		err = s3.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: "us-east-1"})
		if err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("disposable bucket creation", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for obj := range s3.ListObjects(c, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err != nil {
				t.Error("list cleanup")
				break
			}
			if s3.RemoveObject(c, bucket, obj.Key, minio.RemoveObjectOptions{}) != nil {
				t.Error("object cleanup")
			}
		}
		if s3.RemoveBucket(c, bucket) != nil {
			t.Error("bucket cleanup")
		}
	})
	countObjects := func() int {
		n := 0
		for obj := range s3.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err != nil {
				t.Fatal("S3 listing")
			}
			n++
		}
		return n
	}
	db := testdb.New(t)
	dsn, err := url.Parse(db.Config().ConnString())
	if err != nil {
		t.Fatal("DSN")
	}
	dsn.Path = "/" + db.Config().ConnConfig.Database
	dsn.RawPath = ""
	q := dsn.Query()
	q.Del("database")
	q.Del("dbname")
	dsn.RawQuery = q.Encode()
	id := func() string { return uuid.Must(uuid.NewV7()).String() }
	token, store := id(), id()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("loopback address")
	}
	address := l.Addr().String()
	l.Close()
	// Artifact is deliberately given no World/Project authorization callback.
	env := []string{"ARTIFACT_DATABASE_URL=" + dsn.String(), "ARTIFACT_STORE_ID=" + store, "ARTIFACT_SERVICE_TOKEN=" + token, "ARTIFACT_LISTEN_ADDR=" + address, "ARTIFACT_STORAGE_BACKEND=s3", "ARTIFACT_S3_ENDPOINT=" + endpoint, "ARTIFACT_S3_BUCKET=" + bucket, "ARTIFACT_S3_ACCESS_KEY=" + access, "ARTIFACT_S3_SECRET_KEY=" + secret, "ARTIFACT_S3_REGION=us-east-1", "ARTIFACT_S3_SECURE=false"}
	migrate := exec.CommandContext(ctx, binary, "migrate")
	migrate.Env = env
	if migrate.Run() != nil {
		t.Fatal("Artifact migration")
	}
	start := func() func() {
		cmd := exec.CommandContext(ctx, binary, "serve")
		cmd.Env = env
		if cmd.Start() != nil {
			t.Fatal("Artifact start")
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
		httpClient := &http.Client{Timeout: time.Second}
		for range 100 {
			r, e := httpClient.Get("http://" + address + "/health")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return stop
				}
			}
			select {
			case <-done:
				stopped = true
				t.Fatal("Artifact exited")
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("Artifact readiness")
		return stop
	}
	stop := start()
	client, err := artifactclient.New("http://"+address, token)
	if err != nil {
		t.Fatal(err)
	}
	port := &realLearningPort{Client: client}
	writeSource := func(t *testing.T, p world.Principal, draft string) world.ContentVersion {
		// Synthetic media bytes exercise fixed storage, not video understanding.
		data := []byte("0123456789")
		sum := sha256.Sum256(data)
		call := func(method, path string, body []byte, out any) {
			req, _ := http.NewRequestWithContext(ctx, method, "http://"+address+path, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-User-Id", p.SubjectID)
			req.Header.Set("X-Organization-Id", p.OrganizationID)
			req.Header.Set("X-Request-Id", p.RequestID)
			req.Header.Set("Content-Type", "application/json")
			r, e := (&http.Client{Timeout: 10 * time.Second}).Do(req)
			if e != nil {
				t.Fatal("source transfer")
			}
			defer r.Body.Close()
			if r.StatusCode != 200 {
				t.Fatal("source transfer status", r.StatusCode)
			}
			if out != nil && json.NewDecoder(r.Body).Decode(out) != nil {
				t.Fatal("source response")
			}
		}
		input, _ := json.Marshal(map[string]any{"write_id": id(), "source": world.Source{Kind: "user_import", WorldDraftID: draft}, "sha256": hex.EncodeToString(sum[:]), "size": len(data), "mime": "video/mp4"})
		var upload struct {
			UploadID    string `json:"upload_id"`
			ContentPath string `json:"content_path"`
		}
		call("POST", "/v2/artifacts/uploads", input, &upload)
		if !world.ValidID(upload.UploadID) || upload.ContentPath != "/v2/artifacts/uploads/"+upload.UploadID+"/content" {
			t.Fatal("unexpected source upload")
		}
		call("PUT", upload.ContentPath, data, nil)
		var v world.ContentVersion
		call("POST", "/v2/artifacts/uploads/"+upload.UploadID+"/commit", nil, &v)
		return v
	}
	world.RunAnalysisSaveFixture(t, port, writeSource, func(t *testing.T, svc *world.Service, p world.Principal, b analysis.Bundle) {
		before := countObjects()
		port.loseWrite = true
		port.loseFinalRead = true
		for attempt := 0; attempt < 2; attempt++ {
			r, e := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
			if !errors.Is(e, world.ErrDependency) || r.State != "pending" {
				t.Fatal("real save outage did not remain recoverable", attempt, r.State, e)
			}
			stop()
			stop = start()
			svc = world.RestartAnalysisTestService(t, svc, port)
		}
		saved, e := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
		if e != nil || saved.State != "saved" || saved.Revision == nil {
			t.Fatal("real saved revision", e)
		}
		if countObjects() != before+3 {
			t.Fatal("retry duplicated immutable S3 objects", countObjects()-before)
		}
		expected, e := svc.AnalysisLearningDocument(ctx, p, b.TaskID)
		if e != nil {
			t.Fatal(e)
		}
		file := saved.Revision.Manifest.Files[0]
		scope := world.Access{WorldDraftID: saved.Revision.DraftID, WorldDraftRevisionID: saved.Revision.RevisionID}
		actual, e := client.Read(ctx, p, file.Content, scope, world.MaxManifestBytes)
		if e != nil || !bytes.Equal(actual, expected) {
			t.Fatal("fixed document mismatch", e)
		}
		objMeta, e := client.Metadata(ctx, p, file.Content, scope)
		if e != nil || objMeta.Source.TaskID != b.TaskID || objMeta.Source.ServiceID != "world" || objMeta.CreatedBy != p.SubjectID {
			t.Fatal("source binding lost", e)
		}
		copied := saved.Revision.Manifest
		copied.Name = "User renamed result"
		if _, e = svc.WorldDraft(ctx, p, world.DraftRequest{Mode: "commit", DraftID: saved.Revision.DraftID, ExpectedRevisionID: saved.Revision.RevisionID, CommitID: id(), Manifest: &copied}); e != nil {
			t.Fatal(e)
		}
		replay, e := svc.SaveAnalysisLearning(ctx, p, b.TaskID)
		if e != nil || replay.Revision == nil || replay.Revision.RevisionID != saved.Revision.RevisionID {
			t.Fatal("saved replay followed head", e)
		}
		raw, e := json.Marshal(saved.Revision)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(raw, []byte(`"project_id"`)) {
			t.Fatal("unexpected project binding")
		}
		t.Logf("REAL_LEARNING_SAVE objects_created=%d fixed_revision=true original_source=true recovery_after_write=true recovery_after_commit=true artifact_restart=true model_calls=synthetic", 3)
	})
}
