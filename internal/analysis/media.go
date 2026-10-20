package analysis

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrMediaUnsupported = errors.New("analysis media layout unsupported")
var ErrMediaInvalid = errors.New("analysis media integrity or timeline invalid")

const MaxMediaBytes int64 = 48 << 20
const AudioSampleRate int64 = 48000

type MediaInfo struct {
	Source      Source `json:"source"`
	VideoStream int    `json:"video_stream"`
	AudioStream *int   `json:"audio_stream,omitempty"`
	StartMS     int64  `json:"container_start_ms"`
}
type Preparer struct{ FFprobe, FFmpeg, TempRoot string }
type Material struct {
	dir, path string
	info      MediaInfo
	processor Preparer
}

// Stage accepts only a writer callback from the authenticated immutable reader.
// Callers cannot pass a source path or URL to the media parser.
func (p Preparer) Stage(ctx context.Context, hash string, size int64, download func(io.Writer) error) (*Material, error) {
	if !digestPattern.MatchString(hash) || size < 1 || size > MaxMediaBytes || download == nil || p.FFprobe == "" || p.FFmpeg == "" {
		return nil, ErrInvalid
	}
	dir, err := os.MkdirTemp(p.TempRoot, "world-analysis-")
	if err != nil {
		return nil, ErrUnavailable
	}
	m := &Material{dir: dir, path: filepath.Join(dir, "source.mp4"), processor: p}
	complete := false
	defer func() {
		if !complete {
			m.Close()
		}
	}()
	file, err := os.OpenFile(m.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, ErrUnavailable
	}
	// Enforce the declared bound even if a replacement reader is faulty.
	bounded := &boundedWriter{Writer: file, remaining: size}
	err = download(bounded)
	closeErr := file.Close()
	if err != nil || closeErr != nil || bounded.remaining != 0 {
		return nil, ErrMediaInvalid
	}
	raw, err := os.ReadFile(m.path)
	if err != nil || int64(len(raw)) != size || hashBytes(raw) != hash {
		return nil, ErrMediaInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	data, err := runMedia(ctx, 30*time.Second, p.FFprobe, 64<<10, "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mov", "-enable_drefs", "0", "-show_entries", "format=duration,start_time:stream=index,codec_type,duration:stream_disposition=attached_pic", "-of", "json", m.path)
	if err != nil {
		return nil, err
	}
	info, err := ParseProbe(data, hash)
	if err != nil {
		return nil, err
	}
	m.info = info
	complete = true
	return m, nil
}
func (m *Material) Info() MediaInfo { return m.info }
func (m *Material) Close() error {
	if m == nil || m.dir == "" {
		return nil
	}
	err := os.RemoveAll(m.dir)
	m.dir = ""
	m.path = ""
	return err
}
func (m *Material) Prepare(ctx context.Context, step Step) (Prepared, []byte, error) {
	if m == nil || m.dir == "" || step.Validate() != nil || step.SourceHash != m.info.Source.SHA256 || step.DurationMS != m.info.Source.DurationMS {
		return Prepared{}, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, nil, err
	}
	prepared := Prepared{SourceHash: step.SourceHash, Range: step.Input}
	if step.Kind == "visual" {
		data, err := os.ReadFile(m.path)
		if err != nil || int64(len(data)) > MaxMediaBytes || hashBytes(data) != step.SourceHash {
			return Prepared{}, nil, ErrMediaInvalid
		}
		prepared.MIME = "video/mp4"
		prepared.MediaHash = step.SourceHash
		return prepared, data, nil
	}
	if m.info.AudioStream == nil {
		return Prepared{}, nil, ErrMediaUnsupported
	}
	f, err := os.CreateTemp(m.dir, "audio-*.wav")
	if err != nil {
		return Prepared{}, nil, ErrUnavailable
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	startSample := step.Core.StartMS * AudioSampleRate / 1000
	endSample := step.Core.EndMS * AudioSampleRate / 1000
	filter := fmt.Sprintf("aresample=48000:async=1:first_pts=0,apad,atrim=start_sample=%d:end_sample=%d,asetpts=N/SR/TB", startSample, endSample)
	_, err = runMedia(ctx, 2*time.Minute, m.processor.FFmpeg, 64<<10, "-nostdin", "-hide_banner", "-v", "error", "-y", "-copyts", "-start_at_zero", "-protocol_whitelist", "file", "-format_whitelist", "mov", "-enable_drefs", "0", "-i", m.path, "-map", fmt.Sprintf("0:%d", *m.info.AudioStream), "-vn", "-sn", "-dn", "-af", filter, "-ac", "2", "-ar", "48000", "-c:a", "pcm_s16le", "-map_metadata", "-1", "-fflags", "+bitexact", "-flags:a", "+bitexact", "-threads", "1", "-f", "wav", path)
	if err != nil {
		return Prepared{}, nil, err
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Size() > MaxMediaBytes {
		return Prepared{}, nil, ErrMediaInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Prepared{}, nil, ErrUnavailable
	}
	frames, err := WAVFrames(data)
	if err != nil || frames != endSample-startSample {
		return Prepared{}, nil, ErrMediaInvalid
	}
	prepared.MIME = "audio/wav"
	prepared.MediaHash = hashBytes(data)
	return prepared, data, nil
}

// ParseProbe normalizes container time, never trusting source request claims or
// model output. Attached cover images are ignored; ambiguous real tracks fail.
func ParseProbe(data []byte, hash string) (MediaInfo, error) {
	var v struct {
		Streams []struct {
			Index       int    `json:"index"`
			Kind        string `json:"codec_type"`
			Disposition struct {
				Attached int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			Start    string `json:"start_time"`
		} `json:"format"`
	}
	if !digestPattern.MatchString(hash) || len(data) > 64<<10 || json.Unmarshal(data, &v) != nil {
		return MediaInfo{}, ErrMediaInvalid
	}
	duration, err := decimalMS(v.Format.Duration)
	if err != nil || duration < 1 || duration > 86400000 {
		return MediaInfo{}, ErrMediaInvalid
	}
	start := int64(0)
	if v.Format.Start != "" {
		start, err = decimalMS(v.Format.Start)
		if err != nil || start < 0 || start > 86400000 {
			return MediaInfo{}, ErrMediaInvalid
		}
	}
	out := MediaInfo{Source: Source{SHA256: hash, DurationMS: duration}, VideoStream: -1, StartMS: start}
	seen := map[int]bool{}
	for _, s := range v.Streams {
		if s.Index < 0 || seen[s.Index] {
			return MediaInfo{}, ErrMediaInvalid
		}
		seen[s.Index] = true
		switch s.Kind {
		case "video":
			if s.Disposition.Attached == 0 {
				if out.VideoStream != -1 {
					return MediaInfo{}, ErrMediaUnsupported
				}
				out.VideoStream = s.Index
			}
		case "audio":
			if out.AudioStream != nil {
				return MediaInfo{}, ErrMediaUnsupported
			}
			index := s.Index
			out.AudioStream = &index
			out.Source.HasAudio = true
		}
	}
	if out.VideoStream < 0 {
		return MediaInfo{}, ErrMediaUnsupported
	}
	return out, nil
}
func decimalMS(value string) (int64, error) {
	if len(value) == 0 || len(value) > 32 || strings.ContainsAny(value, "/eE ") {
		return 0, ErrMediaInvalid
	}
	n, ok := new(big.Rat).SetString(value)
	if !ok || n.Sign() < 0 {
		return 0, ErrMediaInvalid
	}
	n.Mul(n, big.NewRat(1000, 1))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n.Num(), n.Denom(), r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, ErrMediaInvalid
	}
	return q.Int64(), nil
}

// WAVFrames verifies the concrete output format and exact PCM sample count.
func WAVFrames(data []byte) (int64, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || int64(binary.LittleEndian.Uint32(data[4:8]))+8 != int64(len(data)) {
		return 0, ErrMediaInvalid
	}
	foundFmt, foundData := false, false
	frames := int64(0)
	for pos := 12; pos < len(data); {
		if len(data)-pos < 8 {
			return 0, ErrMediaInvalid
		}
		kind := string(data[pos : pos+4])
		size := int64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if size > int64(len(data)-pos) {
			return 0, ErrMediaInvalid
		}
		part := data[pos : pos+int(size)]
		switch kind {
		case "fmt ":
			if foundFmt || len(part) < 16 || binary.LittleEndian.Uint16(part[:2]) != 1 || binary.LittleEndian.Uint16(part[2:4]) != 2 || binary.LittleEndian.Uint32(part[4:8]) != 48000 || binary.LittleEndian.Uint32(part[8:12]) != 192000 || binary.LittleEndian.Uint16(part[12:14]) != 4 || binary.LittleEndian.Uint16(part[14:16]) != 16 {
				return 0, ErrMediaInvalid
			}
			foundFmt = true
		case "data":
			if foundData || size%4 != 0 {
				return 0, ErrMediaInvalid
			}
			foundData = true
			frames = size / 4
		}
		pos += int(size + size%2)
		if pos > len(data) {
			return 0, ErrMediaInvalid
		}
	}
	if !foundFmt || !foundData || frames < 1 {
		return 0, ErrMediaInvalid
	}
	return frames, nil
}

type boundedWriter struct {
	io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, ErrMediaInvalid
	}
	n, e := w.Writer.Write(p)
	w.remaining -= int64(n)
	return n, e
}
func runMedia(ctx context.Context, timeout time.Duration, executable string, max int64, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	var output bytes.Buffer
	out := &boundedWriter{Writer: &output, remaining: max}
	cmd.Stdout = out
	// ffmpeg errors can contain source tags and paths. Keep them out of API/logs.
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrMediaInvalid
	}
	return output.Bytes(), nil
}
