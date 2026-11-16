package analysis

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
)

func TestProbeRequiresActualVideoAndUnambiguousTracks(t *testing.T) {
	cases := []struct {
		body     string
		duration int64
		audio    bool
		bad      bool
	}{
		{`{"format":{"duration":"345.001001","start_time":"0.000000"},"streams":[{"index":0,"codec_type":"video"},{"index":1,"codec_type":"audio"}]}`, 345002, true, false},
		{`{"format":{"duration":"45","start_time":"0"},"streams":[{"index":0,"codec_type":"video"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1}}]}`, 45000, false, false},
		{`{"format":{"duration":"0"},"streams":[{"index":0,"codec_type":"video"}]}`, 0, false, true},
		{`{"format":{"duration":"NaN"},"streams":[{"index":0,"codec_type":"video"}]}`, 0, false, true},
		{`{"format":{"duration":"1"},"streams":[{"index":0,"codec_type":"audio"}]}`, 0, false, true},
		{`{"format":{"duration":"1"},"streams":[{"index":0,"codec_type":"video"},{"index":1,"codec_type":"audio"},{"index":2,"codec_type":"audio"}]}`, 0, false, true},
		{`{"format":{"duration":"1"},"streams":[{"index":0,"codec_type":"video"},{"index":1,"codec_type":"video"}]}`, 0, false, true},
		{`{"format":{"duration":"86400.00001"},"streams":[{"index":0,"codec_type":"video"}]}`, 0, false, true},
	}
	for _, c := range cases {
		v, err := ParseProbe([]byte(c.body), sourceHash)
		if c.bad {
			if err == nil {
				t.Fatal("ambiguous/invalid input accepted")
			}
			continue
		}
		if err != nil || v.Source.DurationMS != c.duration || v.Source.HasAudio != c.audio {
			t.Fatal("probe mismatch", v, err)
		}
	}
}
func TestStagingChecksHashAndSizeBeforeParserAndCleansOnFailure(t *testing.T) {
	data := []byte("synthetic-source")
	root := t.TempDir()
	p := Preparer{FFprobe: "must-not-execute", FFmpeg: "must-not-execute", TempRoot: root}
	for _, mode := range []string{"size", "hash", "cancel"} {
		ctx, cancel := context.WithCancel(t.Context())
		hash := hashBytes(data)
		size := int64(len(data))
		if mode == "size" {
			size--
		}
		if mode == "hash" {
			hash = sourceHash
		}
		if mode == "cancel" {
			cancel()
		}
		_, err := p.Stage(ctx, hash, size, func(dst io.Writer) error { _, e := dst.Write(data); return e })
		cancel()
		if err == nil {
			t.Fatal("invalid staged source accepted")
		}
		if mode == "cancel" && !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost")
		}
		entries, e := os.ReadDir(root)
		if e != nil || len(entries) != 0 {
			t.Fatal("failed staging left private source bytes")
		}
	}
}
func wavFixture(frames int) []byte {
	data := make([]byte, 44+frames*4)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 2)
	binary.LittleEndian.PutUint32(data[24:], 48000)
	binary.LittleEndian.PutUint32(data[28:], 192000)
	binary.LittleEndian.PutUint16(data[32:], 4)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(frames*4))
	return data
}
func TestWAVVerifiesRealPCMShapeAndExactSamples(t *testing.T) {
	data := wavFixture(48000)
	if n, err := WAVFrames(data); err != nil || n != 48000 {
		t.Fatal(n, err)
	}
	for _, offset := range []int{0, 8, 16, 20, 22, 24, 28, 32, 34, 36, 40} {
		bad := bytes.Clone(data)
		bad[offset] ^= 1
		if _, err := WAVFrames(bad); err == nil {
			t.Fatalf("invalid audio field at %d accepted", offset)
		}
	}
	if _, err := WAVFrames(data[:len(data)-1]); err == nil {
		t.Fatal("truncated data accepted")
	}
}

func TestAudioProbeRequiresOneAudioTrackAndIgnoresCover(t *testing.T) {
	for _, mime := range []string{"audio/wav", "audio/mpeg"} {
		for _, tc := range []struct {
			streams string
			valid   bool
		}{
			{`[{"index":0,"codec_type":"audio"}]`, true},
			{`[{"index":0,"codec_type":"audio"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1}}]`, true},
			{`[]`, false},
			{`[{"index":0,"codec_type":"video"}]`, false},
			{`[{"index":0,"codec_type":"audio"},{"index":1,"codec_type":"video"}]`, false},
			{`[{"index":0,"codec_type":"audio"},{"index":1,"codec_type":"audio"}]`, false},
			{`[{"index":0,"codec_type":"audio"},{"index":0,"codec_type":"video","disposition":{"attached_pic":1}}]`, false},
		} {
			body := []byte(`{"format":{"duration":"60.0001"},"streams":` + tc.streams + `}`)
			v, err := ParseProbeMIME(body, sourceHash, mime)
			if (err == nil) != tc.valid {
				t.Fatal(mime, tc, err)
			}
			if tc.valid && (v.Source.Kind != "audio" || !v.Source.HasAudio || v.VideoStream != -1 || v.Source.DurationMS != 60001) {
				t.Fatal(v)
			}
		}
	}
	if _, err := ParseProbeMIME([]byte(`{}`), sourceHash, "audio/ogg"); !errors.Is(err, ErrMediaUnsupported) {
		t.Fatal(err)
	}
}

func TestAudioStageRejectsUnsupportedMIMEBeforeReading(t *testing.T) {
	p := Preparer{FFprobe: "must-not-execute", FFmpeg: "must-not-execute", TempRoot: t.TempDir()}
	_, err := p.StageMIME(t.Context(), "audio/ogg", sourceHash, 1, func(io.Writer) error { t.Fatal("unsupported source downloaded"); return nil })
	if !errors.Is(err, ErrMediaUnsupported) {
		t.Fatal(err)
	}
	for _, mime := range []string{"audio/wav", "audio/mpeg"} {
		_, err := p.StageMIME(t.Context(), mime, sourceHash, 1, func(w io.Writer) error { _, e := w.Write([]byte("wrong")); return e })
		if !errors.Is(err, ErrMediaInvalid) {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(p.TempRoot)
		if err != nil || len(entries) != 0 {
			t.Fatal("failed audio staging leaked files", err)
		}
	}
}
