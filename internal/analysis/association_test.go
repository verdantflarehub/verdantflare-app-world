package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func associationFixture(t *testing.T) (Bundle, Association) {
	t.Helper()
	task, rows := bundleFixture(t)
	b, err := Assemble(task, rows)
	if err != nil {
		t.Fatal(err)
	}
	return b, associationForBundle(b)
}

func associationForBundle(b Bundle) Association {
	a := Association{Version: 1, BundleHash: b.SHA256, Entities: []AssociationGroup{}, Shots: []ShotGroup{}, Voices: []AssociationGroup{}, VoiceLinks: []VoiceLink{}, Boundaries: []BoundaryDecision{}, Uncertainties: []string{"Synthetic test; unresolved shot boundary"}}
	for i, n := range b.Nodes {
		g := AssociationGroup{Key: fmt.Sprintf("g%d", i), Members: []string{n.Key}, State: "single", Evidence: []string{n.Key}, Reason: "Synthetic observation", Description: "Synthetic description"}
		switch n.Kind {
		case "entities":
			g.Kind = "character"
			g.Name = "Synthetic character"
			a.Entities = append(a.Entities, g)
		case "speakers":
			a.Voices = append(a.Voices, g)
		case "shots":
			a.Shots = append(a.Shots, ShotGroup{g.Key, g.Members, n.Scope.StartMS, n.Scope.EndMS, g.State, g.Evidence, g.Reason})
		}
	}
	for _, boundary := range b.Boundaries {
		a.Boundaries = append(a.Boundaries, BoundaryDecision{boundary.Left, boundary.Right, "undetermined", []string{boundary.Left, boundary.Right}, "Conflicting original continuity flags"})
	}
	if len(a.Voices) > 0 && len(a.Entities) > 0 {
		a.VoiceLinks = []VoiceLink{{a.Voices[0].Key, a.Entities[0].Key, "undetermined", []string{a.Voices[0].Members[0], a.Entities[0].Members[0]}, "Synthetic independent observations; no claimed speaking identity"}}
	}
	return a
}

func associationBytes(t *testing.T, a Association) []byte {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAssociationProjectionPreservesAllEvidenceWithoutRepeatingChildren(t *testing.T) {
	b, a := associationFixture(t)
	if _, err := ValidateAssociation(b, associationBytes(t, a)); err != nil {
		t.Fatal(err)
	}
	before := canonicalHash(b)
	f, err := FreezeAssociation(b, "gemini-3.1-pro-preview", "", "1.00")
	if err != nil {
		t.Fatal(err)
	}
	var p associationPayload
	if json.Unmarshal(f.Payload, &p) != nil {
		t.Fatal("payload")
	}
	if strings.Contains(p.Prompt, "ignore all previous instructions") || !strings.Contains(p.Text, "ignore all previous instructions") {
		t.Fatal("source promoted to task instruction or dropped")
	}
	var projection struct {
		Nodes []struct {
			Key, Kind string
			Links     map[string][]string
			Value     json.RawMessage
		}
	}
	if json.Unmarshal([]byte(p.Text), &projection) != nil || len(projection.Nodes) != len(b.Nodes) {
		t.Fatal("lost evidence")
	}
	for i, n := range projection.Nodes {
		original := b.Nodes[i]
		if n.Key != original.Key || n.Kind != original.Kind {
			t.Fatal("changed identity")
		}
		if n.Kind == "shots" {
			var object map[string]any
			if json.Unmarshal(n.Value, &object) != nil {
				t.Fatal("shot")
			}
			if _, exists := object["beats"]; exists {
				t.Fatal("duplicated beats")
			}
			if len(n.Links["beats"]) != 1 || len(n.Links["visible_text"]) != 1 || !reflect.DeepEqual(n.Links["entities"], original.Links["entities"]) {
				t.Fatal("lost child or entity references")
			}
		} else if n.Kind != "findings" && n.Kind != "speech" && canonicalHash(n.Value) != canonicalHash(original.Value) {
			t.Fatal("changed original evidence")
		}
	}
	if before != canonicalHash(b) {
		t.Fatal("mutated fixed bundle")
	}
	b.Nodes[0].Value = json.RawMessage(`{"changed":true}`)
	if _, err := AssociationText(b); err == nil {
		t.Fatal("changed bundle accepted")
	}
}

func TestAssociationRejectsIncompletePartitionsAndFalseRelationships(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Association, Bundle)
	}{
		{"missing entity", func(a *Association, _ Bundle) { a.Entities = a.Entities[1:] }},
		{"missing voice", func(a *Association, _ Bundle) { a.Voices = a.Voices[1:] }},
		{"missing shot", func(a *Association, _ Bundle) { a.Shots = a.Shots[1:] }},
		{"duplicate member", func(a *Association, _ Bundle) {
			a.Entities[1].Members = a.Entities[0].Members
			a.Entities[1].Evidence = a.Entities[0].Evidence
		}},
		{"wrong entity kind", func(a *Association, _ Bundle) { a.Entities[0].Kind = "prop" }},
		{"speaker as visual entity", func(a *Association, _ Bundle) {
			a.Entities[0].Members = a.Voices[0].Members
			a.Entities[0].Evidence = a.Voices[0].Evidence
		}},
		{"reordered shots", func(a *Association, _ Bundle) { a.Shots[0], a.Shots[1] = a.Shots[1], a.Shots[0] }},
		{"wrong shot time", func(a *Association, _ Bundle) { a.Shots[0].StartMS = 1 }},
		{"unproven merge", func(a *Association, _ Bundle) { a.Boundaries[0].Decision = "merge" }},
		{"missing boundary", func(a *Association, _ Bundle) { a.Boundaries = []BoundaryDecision{} }},
		{"unknown evidence", func(a *Association, _ Bundle) {
			a.Entities[0].Evidence = append(a.Entities[0].Evidence, "foreign-node")
		}},
		{"same group identifier", func(a *Association, _ Bundle) { a.Voices[0].Key = a.Entities[0].Key }},
		{"verified identity claim", func(a *Association, _ Bundle) { a.Entities[0].State = "verified" }},
		{"visual text for voice", func(a *Association, b Bundle) {
			for _, n := range b.Nodes {
				if n.Kind == "visible_text" {
					a.VoiceLinks[0].Evidence = []string{n.Key, a.Entities[0].Members[0]}
					break
				}
			}
		}},
		{"unrelated speaker evidence", func(a *Association, _ Bundle) {
			a.VoiceLinks[0].Evidence = []string{a.Voices[1].Members[0], a.Entities[0].Members[0]}
		}},
		{"foreign bundle", func(a *Association, _ Bundle) { a.BundleHash = strings.Repeat("f", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, a := associationFixture(t)
			tc.change(&a, b)
			got, err := ValidateAssociation(b, associationBytes(t, a))
			if err == nil || len(got.Shots) > 0 {
				t.Fatal("invalid association returned", err)
			}
		})
	}
}

func TestAssociationMergesOnlySupportedAdjacentBoundary(t *testing.T) {
	task, rows := bundleFixture(t)
	// Modify the synthetic native response, then rerun the original validator.
	var envelope map[string]any
	if json.Unmarshal(rows[1].Result, &envelope) != nil {
		t.Fatal("fixture")
	}
	native := envelope["analysis"].(map[string]any)
	doc := native["document"].(map[string]any)
	doc["shots"].([]any)[0].(map[string]any)["continues_before"] = true
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	native["text"] = string(raw)
	rows[1].Result, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	report := VerifyNative(rows[1].Request.Step, rows[1].Request.Prepared, Binding{rows[1].CallID, task.TaskID, rows[1].Request.ModelID}, rows[1].Result)
	if !report.Passed {
		t.Fatal(report)
	}
	rows[1].Report = &report
	b, err := Assemble(task, rows)
	if err != nil {
		t.Fatal(err)
	}
	a := associationForBundle(b)
	a.VoiceLinks = []VoiceLink{}
	first, last := a.Shots[0], a.Shots[1]
	first.Members = append(first.Members, last.Members...)
	first.Evidence = append(first.Evidence, last.Evidence...)
	first.EndMS = last.EndMS
	first.State = "inferred"
	a.Shots = []ShotGroup{first}
	boundary := b.Boundaries[0]
	a.Boundaries = []BoundaryDecision{{boundary.Left, boundary.Right, "merge", []string{boundary.Left, boundary.Right}, "Synthetic continuity evidence"}}
	if _, err := ValidateAssociation(b, associationBytes(t, a)); err != nil {
		t.Fatal(err)
	}
	a.Boundaries[0].Decision = "undetermined"
	if _, err := ValidateAssociation(b, associationBytes(t, a)); err == nil {
		t.Fatal("merged unknown boundary")
	}
}

func TestAssociationNoAudioAndOversizeEvidence(t *testing.T) {
	task, rows := bundleFixtureSource(t, false)
	b, err := Assemble(task, rows)
	if err != nil {
		t.Fatal(err)
	}
	a := associationForBundle(b)
	if len(a.Voices) != 0 || len(a.VoiceLinks) != 0 {
		t.Fatal("invented audio identity")
	}
	if _, err := ValidateAssociation(b, associationBytes(t, a)); err != nil {
		t.Fatal(err)
	}
	// A valid, verbose observation must be refused intact instead of truncated.
	for i := range rows {
		var envelope map[string]any
		if json.Unmarshal(rows[i].Result, &envelope) != nil {
			t.Fatal("fixture")
		}
		native := envelope["analysis"].(map[string]any)
		doc := native["document"].(map[string]any)
		uncertain := make([]string, 70)
		for j := range uncertain {
			uncertain[j] = fmt.Sprintf("Uncertainty %d: ", j) + strings.Repeat("x", 3000)
		}
		doc["uncertainties"] = uncertain
		raw, _ := json.Marshal(doc)
		native["text"] = string(raw)
		rows[i].Result, _ = json.Marshal(envelope)
		report := VerifyNative(rows[i].Request.Step, rows[i].Request.Prepared, Binding{rows[i].CallID, task.TaskID, rows[i].Request.ModelID}, rows[i].Result)
		if !report.Passed {
			t.Fatal(report)
		}
		rows[i].Report = &report
	}
	b, err = Assemble(task, rows)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := AssociationText(b); !errors.Is(err, ErrBundleTooLarge) || text != "" {
		t.Fatal("oversize input silently truncated", err)
	}
}

func associationNativeFixture(t *testing.T, b Bundle, a Association) (AssociationRequest, string, map[string]any) {
	t.Helper()
	f, err := FreezeAssociation(b, "gemini-3.1-pro-preview", "", "1.00")
	if err != nil {
		t.Fatal(err)
	}
	call := uuid.NewString()
	schema, _ := associationSchema()
	return f, call, map[string]any{"schema_version": 1, "call_id": call, "task_id": f.TaskID, "model_id": f.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{
		"text": string(associationBytes(t, a)), "document": a, "model_id": f.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"source_text_sha256": f.TextHash, "response_schema_sha256": canonicalHash(schema), "media": nil, "video_range": nil},
	}}
}

func TestAssociationNativeRequiresExactTextTaskModelAndSchema(t *testing.T) {
	b, a := associationFixture(t)
	f, call, envelope := associationNativeFixture(t, b, a)
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAssociationNative(b, f, call, raw); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong call", func(e map[string]any) { e["call_id"] = uuid.NewString() }},
		{"wrong task", func(e map[string]any) { e["task_id"] = uuid.NewString() }},
		{"wrong model", func(e map[string]any) { e["model_id"] = "different-model" }},
		{"truncated", func(e map[string]any) { e["analysis"].(map[string]any)["finish_reason"] = "MAX_TOKENS" }},
		{"source changed", func(e map[string]any) {
			e["analysis"].(map[string]any)["input"].(map[string]any)["source_text_sha256"] = strings.Repeat("f", 64)
		}},
		{"wrong schema", func(e map[string]any) {
			e["analysis"].(map[string]any)["input"].(map[string]any)["response_schema_sha256"] = strings.Repeat("f", 64)
		}},
		{"video substitute", func(e map[string]any) {
			e["analysis"].(map[string]any)["input"].(map[string]any)["media"] = map[string]any{"mime_type": "video/mp4"}
		}},
		{"document differs", func(e map[string]any) { e["analysis"].(map[string]any)["document"] = map[string]any{"forged": true} }},
		{"schema flag false", func(e map[string]any) { e["analysis"].(map[string]any)["response_schema_verified"] = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var altered map[string]any
			if json.Unmarshal(raw, &altered) != nil {
				t.Fatal("fixture")
			}
			tc.mutate(altered)
			data, _ := json.Marshal(altered)
			if _, err := VerifyAssociationNative(b, f, call, data); err == nil {
				t.Fatal("unbound native output accepted")
			}
		})
	}
}

func TestAssociationFrozenTextDispatchDoesNotRetryOrCreateProject(t *testing.T) {
	b, _ := associationFixture(t)
	f, err := FreezeAssociation(b, "gemini-3.1-pro-preview", uuid.NewString(), "1.00")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewGoogleChannel("https://channel.internal", strings.Repeat("x", 32), "")
	calls := 0
	c.http.Transport = analysisTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.Header.Get("X-Caller-Id") != "world-analysis" || r.Header.Get("X-Organization-Id") != execOrg {
			t.Fatal("wrong request")
		}
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		if json.Unmarshal(body, &request) != nil {
			t.Fatal("invalid body")
		}
		if _, ok := request["project_id"]; ok {
			t.Fatal("implicit project")
		}
		if request["account_id"] != f.AccountID || request["idempotency_key"] != f.OperationID || request["model_id"] != f.ModelID || request["budget_limit"] != f.BudgetLimit {
			t.Fatal("execution identity changed")
		}
		var payload associationPayload
		praw, _ := json.Marshal(request["payload"])
		if json.Unmarshal(praw, &payload) != nil || hashBytes([]byte(payload.Text)) != f.TextHash {
			t.Fatal("text changed")
		}
		return nil, errors.New("simulated lost response")
	})
	if _, err := c.SubmitAssociation(t.Context(), execOrg, f); !errors.Is(err, ErrUnavailable) || calls != 1 {
		t.Fatal("unsafe retry", err, calls)
	}
	var payload map[string]any
	if json.Unmarshal(f.Payload, &payload) != nil {
		t.Fatal("fixture")
	}
	payload["media"] = map[string]any{"data": "untrusted"}
	f.Payload, _ = json.Marshal(payload)
	if _, err := c.SubmitAssociation(t.Context(), execOrg, f); !errors.Is(err, ErrInvalid) || calls != 1 {
		t.Fatal("unfrozen payload reached network")
	}
}
