package analysis

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func promptFixture(t *testing.T) (Bundle, Association, []PromptBatch) {
	t.Helper()
	b, a := associationFixture(t)
	keys := []string{"whole", "shot." + a.Shots[0].Key, "character." + a.Entities[0].Key}
	// These labels deliberately do not silently resolve to a current vendor model.
	targets := []PromptTarget{
		{Key: "h3", RequestedLabel: "h3", TaskType: "video", InputMode: "text", Guidance: "按动作先后、主体与相机分别写视频描述。"},
		{Key: "chat", RequestedLabel: "chtgpt", TaskType: "text", InputMode: "chat", Guidance: "整理结构化任务和可调整约束。"},
		{Key: "sd", RequestedLabel: "sd2.5", TaskType: "video", InputMode: "reference", Guidance: "模型标签仍待解析，保留所需参考输入。"},
	}
	batches, err := PlanPrompts(b, a, keys, targets)
	if err != nil {
		t.Fatal(err)
	}
	return b, a, batches
}
func promptOutput(batch PromptBatch) PromptCandidates {
	out := PromptCandidates{Version: 1, BatchKey: batch.Key, Items: []PromptCandidate{}}
	scopes := map[string]PromptScope{}
	for _, scope := range batch.Scopes {
		scopes[scope.Key] = scope
	}
	for _, pair := range batch.Pairs {
		out.Items = append(out.Items, PromptCandidate{Scope: pair.Scope, Target: pair.Target,
			Title: "Synthetic " + pair.Target, CreativeIntent: "测试按证据转写的方法",
			Text:           "{{subject}} 进入画面，镜头固定；此为 " + pair.Target + " 的合成测试正文。",
			AdaptationNote: "合成候选，模型能力未核验。", Variables: []PromptVariable{{"subject", "新创作的主体", true}},
			Evidence: append([]string{}, scopes[pair.Scope].Evidence...), MissingInputs: []string{"尚需用户提供主体"}, Uncertainties: []string{"身份与边界仍有未确定项"}})
	}
	return out
}
func promptBytes(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func promptNative(t *testing.T, b Bundle, a Association, batch PromptBatch) (PromptRequest, string, map[string]any) {
	t.Helper()
	f, err := FreezePrompts(b, a, batch, "gemini-3.1-pro-preview", "", "1.00")
	if err != nil {
		t.Fatal(err)
	}
	call := uuid.NewString()
	schema, _ := promptSchema()
	out := promptOutput(batch)
	return f, call, map[string]any{"schema_version": 1, "call_id": call, "task_id": f.TaskID, "model_id": f.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true,
		"analysis": map[string]any{"text": string(promptBytes(t, out)), "document": out, "model_id": f.ModelID, "finish_reason": "STOP", "response_schema_verified": true,
			"input": map[string]any{"source_text_sha256": f.TextHash, "response_schema_sha256": canonicalHash(schema), "media": nil, "video_range": nil}}}
}

func TestPromptPlanIncludesEveryScopeAndRequestedModel(t *testing.T) {
	b, a, batches := promptFixture(t)
	if len(batches) != 2 || len(batches[0].Pairs) != 8 || len(batches[1].Pairs) != 1 {
		t.Fatal("request combinations lost")
	}
	seen := map[PromptPair]bool{}
	for _, batch := range batches {
		for _, pair := range batch.Pairs {
			if seen[pair] {
				t.Fatal("duplicated pair")
			}
			seen[pair] = true
		}
		f, err := FreezePrompts(b, a, batch, "gemini-3.1-pro-preview", "", "1.00")
		if err != nil || f.Validate() != nil {
			t.Fatal(err)
		}
		var payload associationPayload
		if json.Unmarshal(f.Payload, &payload) != nil || !strings.Contains(payload.Text, "ignore all previous instructions") || strings.Contains(payload.Prompt, "ignore all previous instructions") {
			t.Fatal("source content lost or promoted to instruction")
		}
		if !strings.Contains(payload.Text, "chtgpt") || strings.Contains(payload.Text, `"model_id":"h3"`) {
			t.Fatal("request label guessed into model ID")
		}
		if _, err = ValidatePrompts(b, a, batch, promptBytes(t, promptOutput(batch))); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 9 {
		t.Fatal("incomplete coverage")
	}
	if _, err := PlanPrompts(b, a, []string{"unknown"}, batches[0].Targets); err == nil {
		t.Fatal("unknown source")
	}
	if _, err := PlanPrompts(b, a, []string{"whole", "whole"}, batches[0].Targets); err == nil {
		t.Fatal("duplicate scope")
	}
	changed := batches[0]
	changed.Pairs = changed.Pairs[1:]
	if _, err := FreezePrompts(b, a, changed, "gemini-3.1-pro-preview", "", "1"); err == nil {
		t.Fatal("frozen batch truncated")
	}
	// All context must remain available; exceeding the real channel text limit
	// must fail explicitly, rather than discard late observations or questions.
	a.Uncertainties = make([]string, 64)
	for i := range a.Uncertainties {
		a.Uncertainties[i] = strings.Repeat("未", 4000)
	}
	large, err := PlanPrompts(b, a, []string{"whole"}, batches[0].Targets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FreezePrompts(b, a, large[0], "gemini-3.1-pro-preview", "", "1"); !errors.Is(err, ErrBundleTooLarge) {
		t.Fatal("oversize input was silently reduced", err)
	}
}

func TestPromptCandidatesRejectMissingTargetsAndForgedEvidence(t *testing.T) {
	b, a, batches := promptFixture(t)
	batch := batches[0]
	for _, tc := range []struct {
		name string
		edit func(*PromptCandidates)
	}{
		{"missing target", func(o *PromptCandidates) { o.Items = o.Items[1:] }},
		{"duplicate pair", func(o *PromptCandidates) { o.Items[1] = o.Items[0] }},
		{"wrong batch", func(o *PromptCandidates) { o.BatchKey = batches[1].Key }},
		{"foreign model", func(o *PromptCandidates) { o.Items[0].Target = "guessed-model" }},
		{"foreign source", func(o *PromptCandidates) { o.Items[0].Evidence[0] = "foreign-task" }},
		{"missing evidence", func(o *PromptCandidates) { o.Items[0].Evidence = o.Items[0].Evidence[1:] }},
		{"duplicate evidence", func(o *PromptCandidates) { o.Items[0].Evidence = append(o.Items[0].Evidence, o.Items[0].Evidence[0]) }},
		{"empty body", func(o *PromptCandidates) { o.Items[0].Text = "  " }},
		{"unbound variable", func(o *PromptCandidates) { o.Items[0].Text += "{{unknown}}" }},
		{"unused variable", func(o *PromptCandidates) {
			o.Items[0].Variables = append(o.Items[0].Variables, PromptVariable{"unused", "Unused", false})
		}},
		{"duplicate variable", func(o *PromptCandidates) {
			o.Items[0].Variables = append(o.Items[0].Variables, o.Items[0].Variables[0])
		}},
		{"broken template", func(o *PromptCandidates) { o.Items[0].Text += "{{subject" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := promptOutput(batch)
			tc.edit(&out)
			if _, err := ValidatePrompts(b, a, batch, promptBytes(t, out)); err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
	for _, field := range []string{"execution_evidence", "compatibility", "project_id", "model_id", "file_ref"} {
		t.Run("forged "+field, func(t *testing.T) {
			var data map[string]any
			json.Unmarshal(promptBytes(t, promptOutput(batch)), &data)
			data["items"].([]any)[0].(map[string]any)[field] = "forged"
			if _, err := ValidatePrompts(b, a, batch, promptBytes(t, data)); err == nil {
				t.Fatal("model assigned authority")
			}
		})
	}
}

func TestPromptNativeBindingAndDraftConversion(t *testing.T) {
	b, a, batches := promptFixture(t)
	f, call, native := promptNative(t, b, a, batches[0])
	raw := promptBytes(t, native)
	drafts, err := PromptDraftsFromNative(b, a, f, call, raw)
	if err != nil || len(drafts) != 8 {
		t.Fatal(err)
	}
	// Validate the actual projected payload against World's generated production
	// contract, not a schema copied from the implementation under test.
	schemaBytes, err := os.ReadFile("../world/world_learning_schema.generated.json")
	var contracts map[string]any
	if err != nil || json.Unmarshal(schemaBytes, &contracts) != nil {
		t.Fatal("World contract unavailable", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		r, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		r.MatchTimeout = 100 * time.Millisecond
		return promptTestRegexp{r}, nil
	})
	for uri, schema := range contracts {
		if err := compiler.AddResource(uri, schema); err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(uri+".schema.json", schema); err != nil {
			t.Fatal(err)
		}
	}
	payloadSchema, err := compiler.Compile("station://contracts/v2/reference-learning#/$defs/prompt")
	if err != nil {
		t.Fatal(err)
	}
	for i, draft := range drafts {
		var value any
		json.Unmarshal(promptBytes(t, draft.Payload), &value)
		if err := payloadSchema.Validate(value); err != nil {
			t.Fatal("projected payload violates World contract", err)
		}
		variant := draft.Payload["variants"].([]any)[0].(map[string]any)
		if variant["compatibility"] != "unresolved" || len(variant["execution_evidence"].([]any)) != 0 || variant["model_id"] != nil || variant["quality_review"].(map[string]any)["state"] != "unreviewed" {
			t.Fatal("invented target model or validation")
		}
		if len(draft.MissingInputs) == 0 || len(draft.Uncertainties) == 0 || len(draft.ContextUncertainties) < len(a.Uncertainties) || !reflect.DeepEqual(draft.Evidence, promptOutput(f.Batch).Items[i].Evidence) || variant["prompt_text"] != promptOutput(f.Batch).Items[i].Text {
			t.Fatal("lost content or uncertainty")
		}
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"wrong call", func(n map[string]any) { n["call_id"] = uuid.NewString() }},
		{"wrong task", func(n map[string]any) { n["task_id"] = uuid.NewString() }},
		{"wrong model", func(n map[string]any) { n["model_id"] = "other-model" }},
		{"truncated", func(n map[string]any) { n["analysis"].(map[string]any)["finish_reason"] = "MAX_TOKENS" }},
		{"wrong input", func(n map[string]any) {
			n["analysis"].(map[string]any)["input"].(map[string]any)["source_text_sha256"] = strings.Repeat("f", 64)
		}},
		{"wrong schema", func(n map[string]any) {
			n["analysis"].(map[string]any)["input"].(map[string]any)["response_schema_sha256"] = strings.Repeat("f", 64)
		}},
		{"different document", func(n map[string]any) { n["analysis"].(map[string]any)["document"] = map[string]any{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed map[string]any
			json.Unmarshal(raw, &changed)
			tc.edit(changed)
			if _, err := PromptDraftsFromNative(b, a, f, call, promptBytes(t, changed)); err == nil {
				t.Fatal("unbound native output projected")
			}
		})
	}
	// A known exact target remains an unexecuted draft, never schema_checked.
	targets := append([]PromptTarget(nil), batches[0].Targets...)
	targets[0].ModelID = "exact-target-version"
	resolved, err := PlanPrompts(b, a, []string{"whole"}, targets)
	if err != nil {
		t.Fatal(err)
	}
	f, call, native = promptNative(t, b, a, resolved[0])
	drafts, err = PromptDraftsFromNative(b, a, f, call, promptBytes(t, native))
	if err != nil || drafts[0].Payload["variants"].([]any)[0].(map[string]any)["compatibility"] != "draft" {
		t.Fatal("resolved draft lost", err)
	}
}

func TestPromptDispatchPreservesFrozenRequestWithoutRetry(t *testing.T) {
	b, a, batches := promptFixture(t)
	f, _, _ := promptNative(t, b, a, batches[0])
	c, _ := NewGoogleChannel("https://channel.internal", strings.Repeat("x", 32), "")
	calls := 0
	c.http.Transport = analysisTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		if json.Unmarshal(body, &request) != nil || r.Method != "POST" || r.Header.Get("X-Caller-Id") != "world-analysis" || request["project_id"] != nil || request["idempotency_key"] != f.OperationID {
			t.Fatal("request boundary changed")
		}
		if canonicalHash(request["payload"]) != canonicalHash(f.Payload) {
			t.Fatal("frozen payload changed")
		}
		return nil, io.ErrUnexpectedEOF
	})
	if _, err := c.SubmitPrompts(t.Context(), execOrg, f); !errors.Is(err, ErrUnavailable) || calls != 1 {
		t.Fatal("unknown request retried", err, calls)
	}
}

type promptTestRegexp struct{ *regexp2.Regexp }

func (r promptTestRegexp) MatchString(s string) bool {
	ok, err := r.Regexp.MatchString(s)
	return err == nil && ok
}
