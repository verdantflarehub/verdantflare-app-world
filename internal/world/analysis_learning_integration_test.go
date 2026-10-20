//go:build integration

package world

import (
	"encoding/json"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

func verifyAnalysisLearningAssembly(t *testing.T, svc *Service, p Principal, b analysis.Bundle) {
	t.Helper()
	ctx := t.Context()
	raw, err := svc.AnalysisLearningDocument(ctx, p, b.TaskID)
	if err != nil {
		t.Fatal("persisted native task assembly", err)
	}
	again, err := svc.AnalysisLearningDocument(ctx, p, b.TaskID)
	if err != nil || string(raw) != string(again) {
		t.Fatal("task assembly changed on reread", err)
	}
	doc, err := ValidateLearningContent(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.RunRefs) != 1 || doc.RunRefs[0].RunID != "analysis-v1:"+b.TaskID || len(doc.RemoteRefs) != 1 || doc.RemoteRefs[0].RevisionID != b.RevisionID {
		t.Fatal("lost fixed source or producer")
	}
	manifest := DraftManifest{SourceRefs: doc.RemoteRefs, RunRefs: doc.RunRefs}
	if err = svc.checkLearningBindings(ctx, p, doc, manifest, &draftInputs{}); err != nil {
		t.Fatal("trusted task refused", err)
	}
	// Editing a copy may produce a new manual work, but must never keep the
	// original task's trusted generated identity or claim a fabricated sample.
	for _, mutation := range []string{"body", "sample", "source", "producer", "version", "run", "manifest"} {
		t.Run(mutation, func(t *testing.T) {
			d, err := ValidateLearningContent(raw, nil)
			if err != nil {
				t.Fatal(err)
			}
			m := manifest
			switch mutation {
			case "body":
				objectOf(listOf(d.Document["results"])[0])["title"] = "forged title"
			case "sample":
				d.Document["uses"] = []any{contentObject{"forged_execution": "not a native result"}}
			case "source":
				objectOf(objectOf(listOf(d.Document["sources"])[0])["content"])["revision_id"] = newID()
			case "producer":
				objectOf(d.Document["provenance"])["producer"] = "other"
			case "version":
				objectOf(d.Document["provenance"])["version"] = "2"
			case "run":
				d.RunRefs[0].RunID = "analysis-v1:" + newID()
			case "manifest":
				m.SourceRefs = nil
			}
			if err = svc.checkLearningBindings(ctx, p, d, m, &draftInputs{}); err == nil {
				t.Fatal("forged generated content accepted")
			}
		})
	}
	// Public body contains all dispositions and no fictitious engineering file.
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	if value["origin"] != "generated" {
		t.Fatal("native provenance downgraded to manual")
	}
	pending, na := false, false
	for _, v := range listOf(value["coverage"]) {
		o := objectOf(v)
		pending = pending || o["state"] == "pending"
		na = na || o["state"] == "not_applicable"
	}
	if !pending || !na {
		t.Fatal("incomplete scopes disappeared")
	}
}
