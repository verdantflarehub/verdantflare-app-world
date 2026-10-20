package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

func TestNativeAuditInputKeepsNullableChannelBodyAndStrictLocalFields(t *testing.T) {
	plan, err := analysis.PlanVideo(analysis.Source{SHA256: strings.Repeat("a", 64), DurationMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{"step": plan.Steps[0], "prepared": analysis.Prepared{SourceHash: strings.Repeat("a", 64), MediaHash: strings.Repeat("a", 64), MIME: "video/mp4", Range: plan.Steps[0].Input},
		"binding": analysis.Binding{CallID: "10000000-0000-4000-8000-000000000001", TaskID: "synthetic", ModelID: "synthetic"},
		"result":  map[string]any{"reason_code": nil, "settlement": map[string]any{"amount": nil}}}
	data, _ := json.Marshal(fields)
	parsed, err := readNativeInput(bytes.NewReader(data))
	if err != nil || !bytes.Contains(parsed.Result, []byte("null")) {
		t.Fatal("legitimate nullable provider metadata was rejected or rewritten")
	}
	fields["Step"] = fields["step"]
	delete(fields, "step")
	data, _ = json.Marshal(fields)
	if _, err := readNativeInput(bytes.NewReader(data)); err == nil {
		t.Fatal("case changed audit field accepted")
	}
	if _, err := readNativeInput(strings.NewReader(`{"result":{},"result":{},"step":{},"prepared":{}}`)); err == nil {
		t.Fatal("duplicate key accepted")
	}
}
