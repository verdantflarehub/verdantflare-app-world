package analysis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func truncatedAssociationFixture(t *testing.T) (Bundle, AssociationRecord) {
	b, old := failedAssociationFixture(t)
	var doc map[string]any
	json.Unmarshal(old.Result, &doc)
	doc["state"] = "failed"
	doc["reason_code"] = "output_incomplete"
	a := doc["analysis"].(map[string]any)
	a["finish_reason"] = "MAX_TOKENS"
	a["document"] = nil
	a["text"] = `{"shot_groups":[`
	a["response_schema_verified"] = false
	old.Result, _ = json.Marshal(doc)
	old.ErrorCode = "native_failed"
	return b, old
}
func TestAssociationTruncationRetryUsesLosslessShortReferences(t *testing.T) {
	b, old := truncatedAssociationFixture(t)
	next, err := RetryAssociationRequest(b, old, execOrg)
	if err != nil {
		t.Fatal(err)
	}
	if next.Encoding != "short-v1" || next.TextHash == old.Request.TextHash || next.BundleHash != old.Request.BundleHash || next.BudgetLimit != old.Request.BudgetLimit || !reflect.DeepEqual(next.Retry.FailureReceipt, old.Result) {
		t.Fatal("binding lost")
	}
	var original, short associationPayload
	json.Unmarshal(old.Request.Payload, &original)
	json.Unmarshal(next.Payload, &short)
	if len(short.Text) >= len(original.Text) || original.Tokens != short.Tokens {
		t.Fatal("no bounded compression")
	}
	var a, c map[string]any
	json.Unmarshal([]byte(original.Text), &a)
	json.Unmarshal([]byte(short.Text), &c)
	an, cn := a["nodes"].([]any), c["nodes"].([]any)
	if len(an) != len(cn) {
		t.Fatal("node omitted")
	}
	for i := range an {
		if !reflect.DeepEqual(an[i].(map[string]any)["value"], cn[i].(map[string]any)["value"]) {
			t.Fatal("observation rewritten")
		}
	}
	association := associationForBundle(b)
	raw := associationBytes(t, association)
	aliases, _ := associationAliases(b)
	// Build a synthetic native output in the exact provider reference vocabulary.
	for full, alias := range aliases {
		raw = []byte(strings.ReplaceAll(string(raw), `"`+full+`"`, `"`+alias+`"`))
	}
	var output any
	json.Unmarshal(raw, &output)
	schema, _ := associationSchema()
	native, _ := json.Marshal(map[string]any{"schema_version": 1, "call_id": execOwner, "task_id": b.TaskID, "model_id": next.ModelID, "media": "analysis", "operation": "analyze", "state": "completed", "accepted": true, "analysis": map[string]any{"text": string(raw), "document": output, "model_id": next.ModelID, "finish_reason": "STOP", "response_schema_verified": true, "input": map[string]any{"source_text_sha256": next.TextHash, "response_schema_sha256": canonicalHash(schema)}}})
	if got, err := VerifyAssociationNative(b, next, execOwner, native); err != nil || !reflect.DeepEqual(got, association) {
		t.Fatal("native short reference proof", err)
	}
	decoded, err := decodeAssociationReferences(b, next.Encoding, raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ValidateAssociation(b, decoded)
	if err != nil || !reflect.DeepEqual(got, association) {
		t.Fatal("roundtrip", err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "e000001", "e999999"))
	if _, err = decodeAssociationReferences(b, next.Encoding, raw); err == nil {
		t.Fatal("unknown alias accepted")
	}
}
func TestAssociationTruncationRetryRejectsForgedOrOtherFailures(t *testing.T) {
	for _, field := range []string{"call_id", "task_id", "model_id", "reason_code", "state", "finish_reason", "source_text_sha256", "response_schema_sha256", "text", "response_schema_verified"} {
		t.Run(field, func(t *testing.T) {
			b, old := truncatedAssociationFixture(t)
			var n map[string]any
			json.Unmarshal(old.Result, &n)
			a := n["analysis"].(map[string]any)
			switch field {
			case "finish_reason":
				a[field] = "STOP"
			case "source_text_sha256", "response_schema_sha256":
				a["input"].(map[string]any)[field] = strings.Repeat("0", 64)
			case "text":
				a[field] = ""
			case "response_schema_verified":
				a[field] = true
			default:
				n[field] = "invalid"
			}
			old.Result, _ = json.Marshal(n)
			if _, err := RetryAssociationRequest(b, old, execOrg); err == nil {
				t.Fatal("accepted forged truncation")
			}
		})
	}
}

func TestFinalAssociationCorrectionOnlyRepairsEntityOmissions(t *testing.T) {
	b, a := associationFixture(t)
	f, call, native := associationNativeFixture(t, b, a)
	f.Encoding = "short-v1"
	var payload associationPayload
	json.Unmarshal(f.Payload, &payload)
	payload.Text, _ = associationRequestText(b, f.Encoding)
	f.TextHash = hashBytes([]byte(payload.Text))
	f.Payload = promptBytes(t, payload)
	f.Retry = &AssociationRetry{ID: execOrg, Number: 2, PreviousCallID: execOwner, PreviousRequestHash: strings.Repeat("a", 64), Issue: "previous attempt"}
	f.OperationID = f.TaskID + ":" + AssociationStepKey + ":retry:" + execOrg
	for _, mode := range []string{"entities", "shots", "voices", "boundary", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			current := a
			current.Entities = append([]AssociationGroup{}, a.Entities[1:]...)
			current.VoiceLinks = []VoiceLink{}
			switch mode {
			case "shots":
				current.Shots = current.Shots[:len(current.Shots)-1]
			case "voices":
				current.Voices = current.Voices[:len(current.Voices)-1]
			case "boundary":
				current.Boundaries = nil
			}
			data := promptBytes(t, current)
			var doc any
			json.Unmarshal(data, &doc)
			aliases, _ := associationAliases(b)
			translateEvidence(doc, aliases, false)
			output := native["analysis"].(map[string]any)
			output["text"] = string(promptBytes(t, doc))
			output["document"] = doc
			output["input"].(map[string]any)["source_text_sha256"] = f.TextHash
			output["finish_reason"] = "STOP"
			if mode == "truncated" {
				output["finish_reason"] = "MAX_TOKENS"
			}
			old := AssociationRecord{OrganizationID: execOrg, OwnerID: execOwner, Request: f, RequestHash: f.Digest(), Revision: 4, State: "failed", CallID: call, Result: promptBytes(t, native), ErrorCode: "association_invalid"}
			next, err := RetryAssociationRequest(b, old, call)
			if mode != "entities" {
				if err == nil {
					t.Fatal("unsafe third attempt")
				}
				return
			}
			if err != nil || next.Retry.Number != 3 || !strings.HasPrefix(next.Retry.Issue, "missing entity members: ") {
				t.Fatal(err)
			}
			old.Request = next
			old.RequestHash = next.Digest()
			if _, err = RetryAssociationRequest(b, old, execOrg); err == nil {
				t.Fatal("unbounded attempts")
			}
		})
	}
}

func TestVoiceLinkUsesCharacterBoundVisualBeatAndIndependentAudio(t *testing.T) {
	b, a := associationFixture(t)
	if len(a.VoiceLinks) == 0 {
		t.Fatal("fixture lacks voice link")
	}
	link := a.VoiceLinks[0]
	var entity AssociationGroup
	for _, e := range a.Entities {
		if e.Key == link.Entity {
			entity = e
		}
	}
	nodes := map[string]EvidenceNode{}
	for _, n := range b.Nodes {
		nodes[n.Key] = n
	}
	beat := ""
	for _, n := range b.Nodes {
		if n.Kind == "beats" {
			for _, parent := range n.Links["shot"] {
				for _, m := range entity.Members {
					if containsKey(nodes[parent].Links["entities"], m) {
						beat = n.Key
					}
				}
			}
		}
	}
	if beat == "" {
		t.Fatal("fixture lacks character bound beat")
	}
	audio := []string{}
	for _, k := range link.Evidence {
		if nodes[k].Kind == "speakers" || nodes[k].Kind == "speech" {
			audio = append(audio, k)
		}
	}
	if len(audio) == 0 {
		t.Fatal("fixture lacks independent audio")
	}
	a.VoiceLinks[0].Evidence = append(audio, beat)
	if _, e := ValidateAssociation(b, associationBytes(t, a)); e != nil {
		t.Fatal("valid visual chain", e)
	}
	a.VoiceLinks[0].Evidence = []string{beat}
	if _, e := ValidateAssociation(b, associationBytes(t, a)); e != nil {
		t.Fatal("timed speech relation not resolved", e)
	}
	isolated := b
	isolated.Nodes = append([]EvidenceNode{}, b.Nodes...)
	for i, n := range isolated.Nodes {
		if n.Kind == "speech" {
			n.Links = map[string][]string{}
			isolated.Nodes[i] = n
		}
	}
	isolated.SHA256 = ""
	isolated.SHA256 = canonicalHash(isolated)
	altered := a
	altered.BundleHash = isolated.SHA256
	if _, e := ValidateAssociation(isolated, associationBytes(t, altered)); e == nil {
		t.Fatal("unbound audio accepted")
	}

	separated := b
	separated.Nodes = append([]EvidenceNode{}, b.Nodes...)
	for i, n := range separated.Nodes {
		if n.Kind == "speech" && n.Scope != nil {
			scope := *n.Scope
			scope.StartMS = nodes[beat].Scope.EndMS
			scope.EndMS = scope.StartMS + 1
			n.Scope = &scope
			separated.Nodes[i] = n
		}
	}
	separated.SHA256 = ""
	separated.SHA256 = canonicalHash(separated)
	altered.BundleHash = separated.SHA256
	if _, e := ValidateAssociation(separated, associationBytes(t, altered)); e == nil {
		t.Fatal("nonoverlapping speech accepted")
	}

	a.VoiceLinks[0].Evidence = audio
	if _, e := ValidateAssociation(b, associationBytes(t, a)); e == nil {
		t.Fatal("audio without visual evidence accepted")
	}
}
