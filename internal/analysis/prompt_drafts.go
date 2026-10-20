package analysis

import "encoding/json"

// PromptDraft is an internal typed candidate. It is neither a saved World asset
// nor proof that any target generation model was called. The final learning
// assembler must retain its evidence, missing inputs and uncertainties.
type PromptDraft struct {
	Key                  string         `json:"key"`
	Scope                string         `json:"scope_key"`
	Title                string         `json:"title"`
	BasisNote            string         `json:"basis_note"`
	Evidence             []string       `json:"evidence_keys"`
	MissingInputs        []string       `json:"missing_inputs"`
	Uncertainties        []string       `json:"uncertainties"`
	ContextUncertainties []string       `json:"context_uncertainties"`
	Payload              map[string]any `json:"payload"`
}

func PromptDraftsFromNative(b Bundle, a Association, f PromptRequest, callID string, data []byte) ([]PromptDraft, error) {
	output, err := VerifyPromptsNative(b, a, f, callID, data)
	if err != nil {
		return nil, err
	}
	byPair := map[PromptPair]PromptCandidate{}
	for _, item := range output.Items {
		byPair[PromptPair{item.Scope, item.Target}] = item
	}
	targets := map[string]PromptTarget{}
	for _, target := range f.Batch.Targets {
		targets[target.Key] = target
	}
	drafts := []PromptDraft{}
	contextUncertainties := append([]string{}, a.Uncertainties...)
	for _, n := range b.Nodes {
		if n.Kind == "uncertainties" {
			var note string
			if json.Unmarshal(n.Value, &note) != nil {
				return nil, ErrInvalid
			}
			contextUncertainties = append(contextUncertainties, note)
		}
	}
	// Preserve the frozen ordering even if the model returned items out of order.
	for _, pair := range f.Batch.Pairs {
		item := byPair[pair]
		target := targets[pair.Target]
		variables := []any{}
		for _, v := range item.Variables {
			variables = append(variables, map[string]any{"key": v.Key, "value_type": "string", "required": v.Required, "description": v.Description})
		}
		variant := map[string]any{
			"key": target.Key, "revision": 1, "requested_label": target.RequestedLabel,
			"task_type": target.TaskType, "input_mode": target.InputMode,
			"prompt_text": item.Text, "reference_bindings": []any{},
			"compatibility": "unresolved", "execution_evidence": []any{},
			"quality_review": map[string]any{"state": "unreviewed", "note": "尚未生成或审核效果样例。"},
		}
		if target.ModelID != "" {
			variant["model_id"] = target.ModelID
			variant["compatibility"] = "draft"
		}
		if item.Negative != "" {
			variant["negative_prompt"] = item.Negative
		}
		key := "prompt." + canonicalHash(struct {
			Plan string
			Pair PromptPair
		}{f.Batch.PlanHash, pair})[:32]
		drafts = append(drafts, PromptDraft{key, item.Scope, item.Title,
			"依据原作观察形成的重建提示词候选，不代表原作者提示词。" + item.AdaptationNote,
			append([]string{}, item.Evidence...), append([]string{}, item.MissingInputs...), append([]string{}, item.Uncertainties...),
			append([]string{}, contextUncertainties...),
			map[string]any{"creative_intent": item.CreativeIntent, "variables": variables, "variants": []any{variant}},
		})
	}
	return drafts, nil
}
