package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

// Prepared records the authorized input before submit. For audio, the media hash
// belongs to decoded audio bytes while SourceHash still pins the original file.
type Prepared struct {
	SourceHash string `json:"source_sha256"`
	MediaHash  string `json:"media_sha256"`
	MIME       string `json:"mime"`
	Range      Range  `json:"range"`
}

// Receipt is populated from the original channel result by the World worker,
// never accepted from browser/MCP callers or from the model's own JSON.
type Receipt struct {
	CallID         string `json:"call_id"`
	State          string `json:"state"`
	Accepted       bool   `json:"accepted"`
	Integrity      bool   `json:"provider_file_integrity_verified"`
	SchemaVerified bool   `json:"response_schema_verified"`
	MediaHash      string `json:"media_sha256"`
	MIME           string `json:"mime"`
	SchemaHash     string `json:"response_schema_sha256"`
	OutputHash     string `json:"output_sha256"`
	VideoRange     *Range `json:"video_range,omitempty"`
}

type Issue struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

type Report struct {
	Passed bool    `json:"passed"`
	Issues []Issue `json:"issues"`
}

type Beat struct {
	Range
	Description string `json:"description"`
}
type Shot struct {
	Range
	Key             string   `json:"key"`
	ContinuesBefore bool     `json:"continues_before"`
	ContinuesAfter  bool     `json:"continues_after"`
	Composition     string   `json:"composition"`
	CameraMotion    string   `json:"camera_motion"`
	SubjectMotion   string   `json:"subject_motion"`
	GraphicMotion   string   `json:"graphic_motion"`
	Lighting        string   `json:"lighting"`
	EntityKeys      []string `json:"entity_keys"`
	Beats           []Beat   `json:"beats"`
	VisibleText     []struct {
		Range
		Text string `json:"text"`
	} `json:"visible_text"`
}
type Visual struct {
	Range
	Shots    []Shot `json:"shots"`
	Entities []struct {
		Key, Kind, Name, Description, Basis string
	} `json:"entities"`
	Findings []struct {
		Dimension, Description, Basis string
		ShotKeys                      []string `json:"shot_keys"`
	} `json:"findings"`
	Uncertainties []string `json:"uncertainties"`
}

type Audio struct {
	Range
	Intervals []struct {
		Range
		Kind, Description string
	} `json:"intervals"`
	Speech []struct {
		Range
		SpeakerKey      string `json:"speaker_key"`
		Text            string `json:"text"`
		Language        string `json:"language"`
		Intelligibility string `json:"intelligibility"`
	} `json:"speech"`
	Speakers []struct{ Key, Description string } `json:"speakers"`
	Music    []struct {
		Range
		Description, Rhythm, Instruments, Vocal, Basis string
	} `json:"music"`
	SoundEffects []struct {
		Range
		Description, Basis string
	} `json:"sound_effects"`
	Uncertainties []string `json:"uncertainties"`
}

// Verify checks fixed input/call identity, strict shape and domain coherence.
// Passing proves a structurally usable observation, not human review, complete
// asset learning or a generated engineering file.
func Verify(step Step, prepared Prepared, receipt Receipt, data []byte) Report {
	r := Report{Issues: []Issue{}}
	add := func(code, path string) { r.Issues = append(r.Issues, Issue{code, path}) }
	if step.Validate() != nil {
		add("invalid_step", "step")
		return r
	}
	if prepared.SourceHash != step.SourceHash || !digestPattern.MatchString(prepared.MediaHash) || prepared.Range != step.Input {
		add("source_mismatch", "input")
	}
	if step.Kind == "visual" && (prepared.MIME != "video/mp4" || prepared.MediaHash != step.SourceHash) ||
		step.Kind == "audio" && prepared.MIME != "audio/wav" && prepared.MIME != "audio/mpeg" {
		add("wrong_modality", "input.mime")
	}
	id, idErr := uuid.Parse(receipt.CallID)
	if idErr != nil || id.String() != receipt.CallID || receipt.State != "completed" || !receipt.Accepted || !receipt.Integrity {
		add("native_call_unverified", "receipt")
	}
	if receipt.MediaHash != prepared.MediaHash || receipt.MIME != prepared.MIME {
		add("native_input_mismatch", "receipt.input")
	}
	if step.Kind == "visual" && (receipt.VideoRange == nil || *receipt.VideoRange != step.Input) || step.Kind == "audio" && receipt.VideoRange != nil {
		add("native_range_mismatch", "receipt.input.video_range")
	}
	schemaHash, err := step.SchemaHash()
	if err != nil || !receipt.SchemaVerified || receipt.SchemaHash != schemaHash {
		add("native_schema_mismatch", "receipt.input.response_schema_sha256")
	}
	hash := sha256.Sum256(data)
	if receipt.OutputHash != hex.EncodeToString(hash[:]) {
		add("native_output_mismatch", "receipt.output")
	}
	clean, err := strictjson.ReadValue(bytes.NewReader(data), 4*1024*1024)
	var value any
	if err != nil || json.Unmarshal(clean, &value) != nil {
		add("invalid_json", "output")
		return r
	}
	schemas, err := compiledSchemas()
	if err != nil || schemas[step.Kind].Validate(value) != nil {
		add("invalid_schema", "output")
		return r
	}
	if step.Kind == "visual" {
		var doc Visual
		if json.Unmarshal(clean, &doc) != nil {
			add("invalid_time", "output")
		} else {
			r.visual(step, doc)
		}
	} else {
		var doc Audio
		if json.Unmarshal(clean, &doc) != nil {
			add("invalid_time", "output")
		} else {
			r.audio(step, doc)
		}
	}
	r.Passed = len(r.Issues) == 0
	return r
}

func (r *Report) add(code, path string) { r.Issues = append(r.Issues, Issue{code, path}) }
func (r *Report) text(value, path string) {
	if strings.TrimSpace(value) == "" {
		r.add("empty_observation", path)
	}
}
func (r *Report) inside(scope, parent Range, path string) {
	if !scope.Valid() || scope.StartMS < parent.StartMS || scope.EndMS > parent.EndMS {
		r.add("range_outside_source", path)
	}
}
func (r *Report) timeline(ranges []Range, scope Range, path string) {
	end := scope.StartMS
	for i, part := range ranges {
		at := fmt.Sprintf("%s[%d]", path, i)
		r.inside(part, scope, at)
		if part.StartMS > end {
			r.add("timeline_gap", at)
		} else if part.StartMS < end {
			r.add("timeline_overlap", at)
		}
		end = part.EndMS
	}
	if len(ranges) == 0 || end != scope.EndMS {
		r.add("timeline_incomplete", path)
	}
}
func (r *Report) key(seen map[string]bool, key, path string) {
	if strings.TrimSpace(key) == "" || seen[key] {
		r.add("duplicate_or_empty_key", path)
	}
	seen[key] = true
}
func (r *Report) refs(seen map[string]bool, keys []string, path string) {
	for _, key := range keys {
		if !seen[key] {
			r.add("unresolved_reference", path)
		}
	}
}

func (r *Report) visual(step Step, doc Visual) {
	if doc.Range != step.Core {
		r.add("output_scope_mismatch", "output")
	}
	entities, shots, dimensions := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, entity := range doc.Entities {
		path := fmt.Sprintf("entities[%d]", i)
		r.key(entities, entity.Key, path+".key")
		r.text(entity.Name, path+".name")
		r.text(entity.Description, path+".description")
	}
	ranges := make([]Range, len(doc.Shots))
	for i, shot := range doc.Shots {
		path := fmt.Sprintf("shots[%d]", i)
		ranges[i] = shot.Range
		r.key(shots, shot.Key, path+".key")
		r.refs(entities, shot.EntityKeys, path+".entity_keys")
		if shot.ContinuesBefore && (i != 0 || step.Core.StartMS == 0) || shot.ContinuesAfter && (i != len(doc.Shots)-1 || step.Core.EndMS == step.DurationMS) {
			r.add("invalid_continuation", path)
		}
		for key, value := range map[string]string{"composition": shot.Composition, "camera_motion": shot.CameraMotion, "subject_motion": shot.SubjectMotion, "graphic_motion": shot.GraphicMotion, "lighting": shot.Lighting} {
			r.text(value, path+"."+key)
		}
		beats := make([]Range, len(shot.Beats))
		for j, beat := range shot.Beats {
			beats[j] = beat.Range
			r.text(beat.Description, fmt.Sprintf("%s.beats[%d].description", path, j))
		}
		r.timeline(beats, shot.Range, path+".beats")
		for j, text := range shot.VisibleText {
			r.inside(text.Range, shot.Range, fmt.Sprintf("%s.visible_text[%d]", path, j))
			r.text(text.Text, fmt.Sprintf("%s.visible_text[%d].text", path, j))
		}
	}
	r.timeline(ranges, step.Core, "shots")
	for i, finding := range doc.Findings {
		path := fmt.Sprintf("findings[%d]", i)
		dimensions[finding.Dimension] = true
		r.text(finding.Description, path+".description")
		r.refs(shots, finding.ShotKeys, path+".shot_keys")
	}
	for _, dimension := range []string{"narrative", "character", "scene", "prop", "cinematography", "editing", "graphics", "style"} {
		if !dimensions[dimension] {
			r.add("missing_dimension", "findings."+dimension)
		}
	}
}

func overlap(a, b Range) bool { return a.StartMS < b.EndMS && b.StartMS < a.EndMS }
func (r *Report) audio(step Step, doc Audio) {
	if doc.Range != step.Core {
		r.add("output_scope_mismatch", "output")
	}
	speakers := map[string]bool{}
	for i, speaker := range doc.Speakers {
		path := fmt.Sprintf("speakers[%d]", i)
		r.key(speakers, speaker.Key, path+".key")
		r.text(speaker.Description, path+".description")
	}
	for i, speech := range doc.Speech {
		path := fmt.Sprintf("speech[%d]", i)
		r.inside(speech.Range, step.Core, path)
		r.refs(speakers, []string{speech.SpeakerKey}, path+".speaker_key")
		if speech.Intelligibility != "unintelligible" {
			r.text(speech.Text, path+".text")
		}
		r.text(speech.Language, path+".language")
	}
	for i, music := range doc.Music {
		path := fmt.Sprintf("music[%d]", i)
		r.inside(music.Range, step.Core, path)
		for key, value := range map[string]string{"description": music.Description, "rhythm": music.Rhythm, "instruments": music.Instruments, "vocal": music.Vocal} {
			r.text(value, path+"."+key)
		}
	}
	for i, effect := range doc.SoundEffects {
		path := fmt.Sprintf("sound_effects[%d]", i)
		r.inside(effect.Range, step.Core, path)
		r.text(effect.Description, path+".description")
	}
	ranges := make([]Range, len(doc.Intervals))
	for i, interval := range doc.Intervals {
		path := fmt.Sprintf("intervals[%d]", i)
		ranges[i] = interval.Range
		r.text(interval.Description, path+".description")
		layers := 0
		found := false
		for _, speech := range doc.Speech {
			if overlap(speech.Range, interval.Range) {
				found = true
				if interval.Kind == "silence" || interval.Kind == "music" || interval.Kind == "sound_effect" {
					r.add("audio_layer_conflict", path)
				}
			}
		}
		if interval.Kind == "speech" && !found {
			r.add("missing_speech", path)
		}
		if found {
			layers++
		}
		found = false
		for _, music := range doc.Music {
			if overlap(music.Range, interval.Range) {
				found = true
				if interval.Kind == "silence" || interval.Kind == "speech" || interval.Kind == "sound_effect" {
					r.add("audio_layer_conflict", path)
				}
			}
		}
		if interval.Kind == "music" && !found {
			r.add("missing_music", path)
		}
		if found {
			layers++
		}
		found = false
		for _, effect := range doc.SoundEffects {
			if overlap(effect.Range, interval.Range) {
				found = true
				if interval.Kind == "silence" || interval.Kind == "speech" || interval.Kind == "music" {
					r.add("audio_layer_conflict", path)
				}
			}
		}
		if interval.Kind == "sound_effect" && !found {
			r.add("missing_sound_effect", path)
		}
		if found {
			layers++
		}
		if interval.Kind == "mixed" && layers < 2 {
			r.add("missing_mixed_layers", path)
		}
	}
	r.timeline(ranges, step.Core, "intervals")
}
