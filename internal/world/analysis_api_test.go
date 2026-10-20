package world

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

func analyzeFixture() AnalyzeRequest {
	return AnalyzeRequest{Mode: "submit", TaskID: newID(), Source: &AnalysisSource{newID(), newID(), newID()}, ModelID: "gemini-3.1-pro-preview", BudgetLimit: "10.00", BudgetPerCall: "0.20", PromptTargets: []analysis.PromptTarget{{Key: "h3", RequestedLabel: "H3", TaskType: "video", InputMode: "text-to-video", Guidance: "保留动作与时序，未知模型参数不猜测。"}}}
}
func TestAnalyzeRequestSchemaAndSemanticBoundaries(t *testing.T) {
	for _, r := range []AnalyzeRequest{analyzeFixture(), {Mode: "capabilities"}, {Mode: "get", TaskID: newID()}, {Mode: "list", Limit: 50, BeforeTaskID: newID()}} {
		raw, _ := json.Marshal(r)
		if _, err := DecodeAnalyzeRequest(raw); err != nil {
			t.Fatal("valid request", string(raw), err)
		}
	}
	for _, name := range []string{"zero", "total-small", "too-precise", "exponent", "escaped-dot", "duplicate-target", "project", "owner", "url", "source-missing", "unknown-mode", "invalid-model", "empty-targets", "unrelated-field", "duplicate-json"} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(analyzeFixture())
			var v map[string]any
			_ = json.Unmarshal(raw, &v)
			switch name {
			case "zero":
				v["budget_limit"] = "0.00000000"
			case "total-small":
				v["budget_limit"] = "0.19"
			case "too-precise":
				v["budget_per_call"] = "0.000000001"
			case "exponent":
				v["budget_limit"] = "1e3"
			case "escaped-dot":
				v["budget_limit"] = `1\x20`
			case "duplicate-target":
				v["prompt_targets"] = append(v["prompt_targets"].([]any), v["prompt_targets"].([]any)[0])
			case "project":
				v["project_id"] = newID()
			case "owner":
				v["owner_id"] = newID()
			case "url":
				v["source"].(map[string]any)["url"] = "https://example.invalid/video.mp4"
			case "source-missing":
				delete(v, "source")
			case "unknown-mode":
				v["mode"] = "generate"
			case "invalid-model":
				v["model_id"] = "invalid/model"
			case "empty-targets":
				v["prompt_targets"] = []any{}
			case "unrelated-field":
				v = map[string]any{"mode": "capabilities", "limit": 1}
			}
			raw, _ = json.Marshal(v)
			if name == "duplicate-json" {
				raw = append([]byte(`{"mode":"get",`), raw[1:]...)
			}
			if _, err := DecodeAnalyzeRequest(raw); !errors.Is(err, ErrInvalid) {
				t.Fatal("unsafe request accepted", err)
			}
		})
	}
}
func TestAnalysisSubmissionIntegrity(t *testing.T) {
	r := analyzeFixture()
	raw, _ := json.Marshal(r)
	row := analysis.Submission{TaskID: r.TaskID, DraftID: r.Source.DraftID, RevisionID: r.Source.RevisionID, FileID: r.Source.FileID, Hash: analysisDocumentHash(raw)}
	if !analysisSubmissionMatches(row, r) {
		t.Fatal("valid persisted request rejected")
	}
	r.BudgetLimit = "11.00"
	if analysisSubmissionMatches(row, r) {
		t.Fatal("changed request accepted")
	}
	r = analyzeFixture()
	r.Source = nil
	if analysisSubmissionMatches(row, r) {
		t.Fatal("missing source accepted")
	}
}

type capabilityReader struct {
	value analysis.Capabilities
	err   error
	calls int
}

func (c *capabilityReader) Capabilities(context.Context, string) (analysis.Capabilities, error) {
	c.calls++
	return c.value, c.err
}
func TestAnalysisAdmissionCapabilityProjection(t *testing.T) {
	p := Principal{newID(), newID(), newID()}
	for _, name := range []string{"disabled", "runtime-off", "missing-audio", "small-input", "empty-models", "unavailable", "enabled"} {
		t.Run(name, func(t *testing.T) {
			c := &capabilityReader{value: analysis.Capabilities{State: "enabled", Models: []string{"gemini-3.1-pro-preview"}, MIME: []string{"video/mp4", "audio/wav"}, MaxMediaBytes: analysis.MaxMediaBytes}}
			enabled := true
			switch name {
			case "disabled":
				c.value.State = "disabled"
			case "runtime-off":
				enabled = false
			case "missing-audio":
				c.value.MIME = []string{"video/mp4"}
			case "small-input":
				c.value.MaxMediaBytes--
			case "empty-models":
				c.value.Models = nil
			case "unavailable":
				c.err = analysis.ErrUnavailable
			}
			svc := &Service{}
			if err := svc.ConfigureAnalysis(AnalysisRuntime{Enabled: enabled, Channel: c}); err != nil {
				t.Fatal(err)
			}
			value, err := svc.WorldAnalyze(t.Context(), p, AnalyzeRequest{Mode: "capabilities"})
			if name == "unavailable" {
				if !errors.Is(err, ErrDependency) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			v := value.(AnalysisAvailability)
			if v.Enabled != (name == "enabled") {
				t.Fatal("incorrect admission", v)
			}
			if name == "runtime-off" && c.calls != 0 {
				t.Fatal("disabled runtime queried provider")
			}
			if err := svc.ConfigureAnalysis(AnalysisRuntime{}); !errors.Is(err, ErrInvalid) {
				t.Fatal("runtime mutated after configuration")
			}
		})
	}
	svc := &Service{}
	if _, err := svc.WorldAnalyze(t.Context(), Principal{}, AnalyzeRequest{Mode: "capabilities"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("anonymous admission")
	}
}
