package analysis

import (
	"context"
	"encoding/json"
	"fmt"
)

// A disposition for every type/target combination keeps exclusions visible.
// No native inference is charged for a combination that cannot serve its task.
type PromptOmission struct {
	ResultKey string `json:"result_key"`
	TargetKey string `json:"target_key"`
	Reason    string `json:"reason"`
}

func promptApplicable(kind, task string) bool {
	switch task {
	case "image":
		return kind == "character" || kind == "scene" || kind == "prop"
	case "video":
		return kind == "shot"
	case "text":
		return kind == "script_unit" || kind == "style" || kind == "method"
	case "speech":
		return kind == "sound_cue"
	case "music":
		return kind == "music_structure"
	case "audio":
		return kind == "sound_cue" || kind == "music_structure"
	}
	return false
}

func derivedPromptBase(b Bundle, a Association, results []ResultDraft, targets []PromptTarget) (PromptBatch, []PromptOmission, error) {
	if _, err := promptScopes(b, a); err != nil {
		return PromptBatch{}, nil, err
	}
	if len(results) == 0 || len(results) > 8192 || len(targets) == 0 || ValidatePromptTargets(targets) != nil {
		return PromptBatch{}, nil, ErrInvalid
	}
	base := PromptBatch{Version: 2, TaskID: b.TaskID, BundleHash: b.SHA256, AssociationHash: canonicalHash(a), Scopes: []PromptScope{}, Targets: append([]PromptTarget{}, targets...), Pairs: []PromptPair{}, Results: results}
	omitted := []PromptOmission{}
	seen := map[string]bool{}
	for _, r := range results {
		if !groupKey.MatchString(r.Key) || seen[r.Key] {
			return base, nil, ErrInvalid
		}
		seen[r.Key] = true
		if r.State != "ready" && r.State != "insufficient_evidence" && r.State != "not_applicable" {
			return base, nil, ErrInvalid
		}
		selected := false
		for _, target := range targets {
			reason := ""
			if r.State != "ready" {
				reason = "result_" + r.State
			} else if !promptApplicable(r.Type, target.TaskType) {
				reason = "task_type_not_applicable"
			}
			if reason != "" {
				omitted = append(omitted, PromptOmission{r.Key, target.Key, reason})
				continue
			}
			selected = true
			base.Pairs = append(base.Pairs, PromptPair{r.Key, target.Key})
		}
		if selected {
			if len(r.Payloads) != 1 || len(r.Evidence) == 0 || len(r.Evidence) > 1000 {
				return base, nil, ErrInvalid
			}
			base.Scopes = append(base.Scopes, PromptScope{Key: r.Key, Evidence: append([]string{}, r.Evidence...)})
		}
	}
	return base, omitted, nil
}

func PlanDerivedPrompts(b Bundle, a Association, results []ResultDraft, targets []PromptTarget) ([]PromptBatch, []PromptOmission, string, error) {
	base, omissions, err := derivedPromptBase(b, a, results, targets)
	if err != nil {
		return nil, nil, "", err
	}
	hash := canonicalHash(base)
	plan := []PromptBatch{}
	for start := 0; start < len(base.Pairs); start += 8 {
		batch := base
		batch.PlanHash = hash
		// Stable per-task operation addresses; the plan schema/hash carry v2.
		batch.Key = fmt.Sprintf("prompts-v1-%04d", start/8)
		batch.Pairs = append([]PromptPair{}, base.Pairs[start:min(start+8, len(base.Pairs))]...)
		plan = append(plan, batch)
	}
	return plan, omissions, hash, nil
}

func (s PGStore) TypedDrafts(ctx context.Context, org, owner, task string, b Bundle, a Association) ([]ResultDraft, error) {
	records, err := s.ResultRecords(ctx, org, owner, task)
	if err != nil {
		return nil, err
	}
	out := []ResultDraft{}
	for _, r := range records {
		if r.State != "result_ready" {
			return nil, ErrObservationsIncomplete
		}
		ds, err := ResultDraftsFromNative(b, a, r.Request, r.CallID, r.Result)
		if err != nil {
			return nil, err
		}
		out = append(out, ds...)
	}
	return out, nil
}

func (s PGStore) PlannedPrompts(ctx context.Context, t Task, b Bundle, a Association) ([]PromptBatch, string, error) {
	if t.PipelineVersion != 2 {
		plan, err := PlanPrompts(b, a, AllPromptScopes(a), t.PromptTargets)
		return plan, "", err
	}
	ds, err := s.TypedDrafts(ctx, t.OrganizationID, t.OwnerID, t.TaskID, b, a)
	if err != nil {
		return nil, "", err
	}
	plan, _, hash, err := PlanDerivedPrompts(b, a, ds, t.PromptTargets)
	return plan, hash, err
}

// validateDerivedPromptRequest checks every pair and the exact all-results
// snapshot. Stored native result bindings are separately checked at admission.
func validateDerivedPromptRequest(f PromptRequest, text string) error {
	var input struct {
		Association Association `json:"association"`
	}
	if json.Unmarshal([]byte(text), &input) != nil {
		return ErrInvalid
	}
	// The bundle is rechecked when preparing and consuming the request. Here
	// rebuild only the versioned base from its frozen results and target set.
	base := f.Batch
	base.Key = ""
	base.PlanHash = ""
	base.Pairs = []PromptPair{}
	base.Scopes = []PromptScope{}
	seen := map[string]bool{}
	for _, r := range base.Results {
		if !groupKey.MatchString(r.Key) || seen[r.Key] {
			return ErrInvalid
		}
		seen[r.Key] = true
		selected := false
		for _, target := range base.Targets {
			if r.State == "ready" && promptApplicable(r.Type, target.TaskType) {
				selected = true
				base.Pairs = append(base.Pairs, PromptPair{r.Key, target.Key})
			}
		}
		if selected {
			base.Scopes = append(base.Scopes, PromptScope{Key: r.Key, Evidence: append([]string{}, r.Evidence...)})
		}
	}
	if canonicalHash(base) != f.Batch.PlanHash || canonicalHash(base.Scopes) != canonicalHash(f.Batch.Scopes) {
		return ErrInvalid
	}
	for start := 0; start < len(base.Pairs); start += 8 {
		if f.Batch.Key == fmt.Sprintf("prompts-v1-%04d", start/8) && canonicalHash(f.Batch.Pairs) == canonicalHash(base.Pairs[start:min(start+8, len(base.Pairs))]) {
			return nil
		}
	}
	return ErrInvalid
}
