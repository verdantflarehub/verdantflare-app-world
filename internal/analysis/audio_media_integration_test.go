//go:build integration

package analysis

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This is a decoder/timeline test with synthetic tones, never song acceptance.
func TestStandaloneAudioRealMediaPreparation(t *testing.T) {
	if os.Getenv("WORLD_ANALYSIS_MEDIA_TEST") != "1" {
		t.Skip("remote media runtime required")
	}
	p := Preparer{FFprobe: "/usr/bin/ffprobe", FFmpeg: "/usr/bin/ffmpeg", TempRoot: t.TempDir()}
	dir := t.TempDir()
	wav := filepath.Join(dir, "tone.wav")
	run := func(args ...string) {
		t.Helper()
		if err := exec.CommandContext(t.Context(), p.FFmpeg, args...).Run(); err != nil {
			t.Fatal("fixture encoding failed", err)
		}
	}
	run("-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=61.125", "-ac", "2", "-c:a", "pcm_s16le", "-y", wav)
	cover := filepath.Join(dir, "cover.ppm")
	if err := os.WriteFile(cover, append([]byte("P6\n1 1\n255\n"), 255, 0, 0), 0600); err != nil {
		t.Fatal(err)
	}
	mp3 := filepath.Join(dir, "tone.mp3")
	run("-nostdin", "-v", "error", "-i", wav, "-c:a", "libmp3lame", "-b:a", "128k", "-y", mp3)
	covered := filepath.Join(dir, "covered.mp3")
	run("-nostdin", "-v", "error", "-i", mp3, "-i", cover, "-map", "0:a", "-map", "1:v", "-c:a", "copy", "-c:v", "png", "-disposition:v", "attached_pic", "-id3v2_version", "3", "-y", covered)
	for _, tc := range []struct{ name, path, mime string }{{"wav", wav, "audio/wav"}, {"mp3", mp3, "audio/mpeg"}, {"mp3-cover", covered, "audio/mpeg"}} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			stage := func(mime, hash string, size int64) (*Material, error) {
				return p.StageMIME(t.Context(), mime, hash, size, func(w io.Writer) error { _, err := w.Write(data); return err })
			}
			m, err := stage(tc.mime, hashBytes(data), int64(len(data)))
			if err != nil {
				t.Fatal("real audio staging", err)
			}
			defer m.Close()
			info := m.Info()
			if info.Source.Kind != "audio" || info.VideoStream != -1 || info.AudioStream == nil {
				t.Fatal("wrong audio layout", info)
			}
			if tc.name == "wav" && info.Source.DurationMS != 61125 {
				t.Fatal("WAV duration changed", info)
			}
			plan, err := PlanSource(info.Source)
			if err != nil || len(plan.Steps) != 2 {
				t.Fatal("audio segmentation", err)
			}
			for _, step := range plan.Steps {
				prepared, pcm, err := m.Prepare(t.Context(), step)
				if err != nil {
					t.Fatal("decode", err)
				}
				frames, err := WAVFrames(pcm)
				if err != nil || frames != (step.Core.EndMS-step.Core.StartMS)*48 {
					t.Fatal("sample count", err)
				}
				again, repeat, err := m.Prepare(t.Context(), step)
				if err != nil || again != prepared || hashBytes(pcm) != hashBytes(repeat) {
					t.Fatal("recovery changes PCM", err)
				}
				pcmData := func(wav []byte) []byte {
					for pos := 12; pos+8 <= len(wav); {
						n := int(binary.LittleEndian.Uint32(wav[pos+4 : pos+8]))
						if n < 0 || n > len(wav)-pos-8 {
							t.Fatal("invalid PCM chunk")
						}
						if string(wav[pos:pos+4]) == "data" {
							return wav[pos+8 : pos+8+n]
						}
						pos += 8 + n + n%2
					}
					t.Fatal("missing PCM data")
					return nil
				}
				if tc.name == "wav" {
					original := pcmData(data)
					start, end := step.Core.StartMS*192, step.Core.EndMS*192
					if !bytes.Equal(pcmData(pcm), original[start:end]) {
						t.Fatal("audio slice shifted or waveform changed")
					}
				}
				if step.Kind != "audio" || prepared.MIME != "audio/wav" {
					t.Fatal("wrong modality")
				}
			}
			visual := Step{Key: "visual-0-1000", Kind: "visual", SourceHash: info.Source.SHA256, DurationMS: info.Source.DurationMS, Core: Range{0, 1000}, Input: Range{0, 1000}}
			if _, _, err := m.Prepare(t.Context(), visual); err == nil {
				t.Fatal("audio served as video")
			}
			wrong := "audio/wav"
			if tc.mime == wrong {
				wrong = "audio/mpeg"
			}
			if bad, err := stage(wrong, hashBytes(data), int64(len(data))); err == nil {
				bad.Close()
				t.Fatal("MIME/container mismatch accepted")
			}
			if bad, err := stage(tc.mime, sourceHash, int64(len(data))); err == nil {
				bad.Close()
				t.Fatal("digest mismatch accepted")
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(p.TempRoot)
			if err != nil || len(entries) != 0 {
				t.Fatal("staging leaked", err)
			}
			t.Logf("real decoder passed: %s, duration_ms=%d, steps=%d", tc.name, info.Source.DurationMS, len(plan.Steps))
		})
	}
}
