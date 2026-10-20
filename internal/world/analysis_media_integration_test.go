//go:build integration

package world

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

func mediaProcessor(t *testing.T) analysis.Preparer {
	t.Helper()
	if os.Getenv("WORLD_ANALYSIS_MEDIA_TEST") != "1" {
		t.Skip("remote Actions media runtime required")
	}
	for _, tool := range []string{"/usr/bin/ffprobe", "/usr/bin/ffmpeg"} {
		if output, err := exec.CommandContext(t.Context(), tool, "-version").CombinedOutput(); err != nil {
			t.Fatalf("media runtime dependency unavailable: %s", output)
		}
	}
	return analysis.Preparer{FFprobe: "/usr/bin/ffprobe", FFmpeg: "/usr/bin/ffmpeg", TempRoot: t.TempDir()}
}
func mediaHash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func checkMediaClean(t *testing.T, processor analysis.Preparer) {
	t.Helper()
	entries, err := os.ReadDir(processor.TempRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("private media staging not cleaned")
	}
}
func TestAnalysisDesignatedVideoMediaPreparation(t *testing.T) {
	processor := mediaProcessor(t)
	path := os.Getenv("WORLD_ANALYSIS_TEST_SOURCE")
	if path == "" {
		t.Fatal("designated source required")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read designated source")
	}
	const expected = "804fc28b37d0628f52ae913e454af31d1d7e3b52ee305690b9011fe11b20f5db"
	if len(original) != 43299592 || mediaHash(original) != expected {
		t.Fatal("designated video mismatch")
	}
	material, err := processor.Stage(t.Context(), expected, int64(len(original)), func(dst io.Writer) error { _, e := dst.Write(original); return e })
	if err != nil {
		t.Fatal("actual runtime probe", err)
	}
	defer material.Close()
	probe, probeErr := exec.CommandContext(t.Context(), processor.FFprobe, "-v", "error", "-show_entries", "format=duration,start_time", "-of", "json", path).Output()
	if probeErr != nil {
		t.Fatal("raw source probe unavailable")
	}
	var rawProbe any
	if json.Unmarshal(probe, &rawProbe) != nil {
		t.Fatal("invalid raw source metadata")
	}
	info := material.Info()
	if info.Source.DurationMS != 345003 || !info.Source.HasAudio {
		t.Fatal("actual source metadata", info)
	}
	plan, err := analysis.PlanVideo(info.Source)
	if err != nil || len(plan.Steps) != 14 {
		t.Fatal("full source plan", err)
	}
	reports := []map[string]any{}
	audio := 0
	for _, step := range plan.Steps {
		prepared, media, e := material.Prepare(t.Context(), step)
		if e != nil {
			t.Fatal("real input preparation", step.Key, e)
		}
		item := map[string]any{"step": step, "prepared": prepared, "bytes": len(media)}
		if step.Kind == "audio" {
			audio++
			frames, e := analysis.WAVFrames(media)
			if e != nil || frames != (step.Core.EndMS-step.Core.StartMS)*48 {
				t.Fatal("wrong actual samples", step.Key, e)
			}
			again, bytes, e := material.Prepare(t.Context(), step)
			if e != nil || again != prepared || mediaHash(bytes) != mediaHash(media) {
				t.Fatal("non-deterministic prepared input", step.Key, e)
			}
			item["sample_frames"] = frames
			item["repeat_digest_matched"] = true
		}
		reports = append(reports, item)
	}
	if audio != 6 {
		t.Fatal("missing audio coverage")
	}
	if err = material.Close(); err != nil {
		t.Fatal("cleanup failed")
	}
	checkMediaClean(t, processor)
	version, e := exec.Command(processor.FFmpeg, "-version").Output()
	if e != nil {
		t.Fatal("tool version unavailable")
	}
	report, _ := json.Marshal(map[string]any{"scope": "media preparation only; no model inference or complete learning asset", "source": info, "raw_container_probe": rawProbe, "steps": reports, "ffmpeg_version": strings.Split(string(version), "\n")[0], "staging_cleaned": true})
	t.Log("MEDIA_REPORT " + string(report))
}
func (a *draftTestArtifact) Download(ctx context.Context, p Principal, ref ContentRef, access Access, dst io.Writer, max int64) error {
	data, err := a.Read(ctx, p, ref, access, max)
	if err != nil {
		return err
	}
	if int64(len(data)) > max {
		return ErrInvalid
	}
	_, err = dst.Write(data)
	return err
}
func TestAnalysisAudioOffsetsAndWorldTaskPreparation(t *testing.T) {
	processor := mediaProcessor(t)
	ctx := t.Context()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "delayed-audio.mp4")
	cmd := exec.CommandContext(ctx, processor.FFmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=32x32:r=10:d=4", "-itsoffset", "2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "mpeg4", "-q:v", "10", "-c:a", "aac", "-t", "4", "-y", sourcePath)
	if err := cmd.Run(); err != nil {
		t.Fatal("synthetic offset fixture creation failed")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := mediaHash(data)
	material, err := processor.Stage(ctx, hash, int64(len(data)), func(dst io.Writer) error { _, e := dst.Write(data); return e })
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := analysis.PlanVideo(material.Info().Source)
	if len(plan.Steps) != 2 {
		t.Fatal("synthetic plan")
	}
	_, wav, err := material.Prepare(ctx, plan.Steps[1])
	if err != nil {
		t.Fatal("delayed audio preparation", err)
	}
	material.Close()
	checkMediaClean(t, processor)
	// Inspect actual PCM: silence at the start must not be trimmed away. The
	// later tone proves padding did not replace the source audio with silence.
	var pcm []byte
	for pos := 12; pos+8 <= len(wav); {
		size := int(binary.LittleEndian.Uint32(wav[pos+4 : pos+8]))
		if string(wav[pos:pos+4]) == "data" {
			pcm = wav[pos+8 : pos+8+size]
			break
		}
		pos += 8 + size + size%2
	}
	if len(pcm) < 4*48000*4 {
		t.Fatal("short normalized audio")
	}
	peak := func(start, end int) int {
		out := 0
		for i := start * 4; i < end*4; i += 2 {
			v := int(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
			if v < 0 {
				v = -v
			}
			if v > out {
				out = v
			}
		}
		return out
	}
	if peak(0, 48000) > 4 || peak(3*48000, 4*48000) < 100 {
		t.Fatal("audio offset shifted or real tone lost")
	}
	db := testdb.New(t)
	if err = migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err = migrations.ActivateFresh(ctx, db); err != nil {
		t.Fatal(err)
	}
	content := &draftTestArtifact{versions: map[ContentRef]ContentVersion{}, bytes: map[ContentRef][]byte{}, writes: map[string]ContentRef{}, retained: map[string][]ContentRef{}}
	svc, err := NewService(ctx, db, content, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	content.svc = svc
	p := Principal{testID(30), testID(31), testID(32)}
	subjects := []string{"reference"}
	raw, err := svc.WorldDraft(ctx, p, DraftRequest{Mode: "create", CommitID: newID(), Name: "Synthetic media preparation", Purpose: "collection", Subjects: &subjects})
	if err != nil {
		t.Fatal(err)
	}
	created := raw.(DraftRevision)
	ref := ContentRef{testID(3), newID(), newID()}
	fileID := newID()
	content.versions[ref] = ContentVersion{ContentRef: ref, OrganizationID: p.OrganizationID, Source: Source{Kind: "user_import", WorldDraftID: created.DraftID}, MIME: "video/mp4", Size: int64(len(data)), SHA256: hash}
	content.bytes[ref] = data
	manifest := created.Manifest
	manifest.Files = []File{{ID: fileID, Path: "reference.mp4", Role: "reference", Content: ref}}
	raw, err = svc.WorldDraft(ctx, p, DraftRequest{Mode: "commit", DraftID: created.DraftID, ExpectedRevisionID: created.RevisionID, CommitID: newID(), Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	saved := raw.(DraftRevision)
	taskID := newID()
	source := AnalysisSource{DraftID: saved.DraftID, RevisionID: saved.RevisionID, FileID: fileID}
	settings := AnalysisExecution{ModelID: "gemini-3.1-pro-preview", BudgetPerStep: "1.00"}
	task, err := svc.PrepareVideoAnalysis(ctx, p, taskID, source, settings, processor)
	if err != nil || len(task.Plan.Steps) != 2 {
		t.Fatal("World preparation persistence", err)
	}
	checkMediaClean(t, processor)
	// Restart must reuse frozen plan and prompts without needing media tools.
	svc, err = NewService(ctx, db, content, integrationProject{}, 1, map[string]coordination.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	content.svc = svc
	broken := analysis.Preparer{FFmpeg: "must-not-execute", FFprobe: "must-not-execute", TempRoot: processor.TempRoot}
	replay, err := svc.PrepareVideoAnalysis(ctx, p, taskID, source, settings, broken)
	if err != nil || replay.TaskID != taskID {
		t.Fatal("reentry regenerated input", err)
	}
	changed := settings
	changed.BudgetPerStep = "2.00"
	if _, err = svc.PrepareVideoAnalysis(ctx, p, taskID, source, changed, broken); !errors.Is(err, ErrIdempotency) {
		t.Fatal("changed request replaced frozen task", err)
	}
	guest := p
	guest.SubjectID = newID()
	if _, err = svc.PrepareVideoAnalysis(ctx, guest, newID(), source, settings, processor); err == nil {
		t.Fatal("unauthorized source read")
	}
	yes := true
	channel := &observationChannel{call: analysis.Call{SchemaVersion: 1, Binding: analysis.Binding{CallID: newID(), TaskID: taskID, ModelID: settings.ModelID}, Media: "analysis", Operation: "analyze", State: "queued", Accepted: &yes}}
	first := task.Plan.Steps[0].Key
	r, err := svc.AdvanceAnalysisStep(ctx, p, taskID, first, processor, channel)
	if err != nil || r.State != "running" || channel.submits != 1 {
		t.Fatal("authorized media not connected to execution", err)
	}
	checkMediaClean(t, processor)
	r, err = svc.AdvanceAnalysisStep(ctx, p, taskID, first, broken, channel)
	if err != nil || r.State != "running" || channel.submits != 1 || channel.polls != 1 {
		t.Fatal("recovery repeated media or submit", err)
	}
}
