package analysis

import "strings"

func validateResultPayload(kind string, payload map[string]any, scope ResultScope, duration int64) error {
	window := Range{0, duration}
	if scope.Range != nil {
		window = *scope.Range
	}
	var visit func(any) error
	visit = func(v any) error {
		switch x := v.(type) {
		case string:
			if strings.TrimSpace(x) == "" {
				return ErrInvalid
			}
		case map[string]any:
			if x["kind"] == "time" {
				start, _ := x["start_tick"].(float64)
				end, _ := x["end_tick"].(float64)
				if x["timebase_num"] != float64(1) || x["timebase_den"] != float64(1000) || start < float64(window.StartMS) || end > float64(window.EndMS) || end <= start {
					return ErrInvalid
				}
			}
			for _, child := range x {
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range x {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(payload); err != nil {
		return err
	}
	if scope.Range != nil {
		locator, ok := payload["scope"].(map[string]any)
		if !ok || locator["start_tick"] != float64(window.StartMS) || locator["end_tick"] != float64(window.EndMS) {
			return ErrInvalid
		}
	}
	if kind == "shot" && payload["segment_kind"] != "edit_shot" {
		return ErrInvalid
	}
	if kind == "sound_cue" {
		if strings.HasPrefix(scope.Key, "voice.") && payload["sound_kind"] != "voice_reference" ||
			strings.HasPrefix(scope.Key, "speech.") && payload["sound_kind"] != "dialogue" && payload["sound_kind"] != "narration" ||
			strings.HasPrefix(scope.Key, "sound_effects.") && payload["sound_kind"] != "sfx" && payload["sound_kind"] != "ambience" {
			return ErrInvalid
		}
	}
	unique := func(field string) (map[string]bool, error) {
		keys := map[string]bool{}
		values, _ := payload[field].([]any)
		for _, value := range values {
			item, _ := value.(map[string]any)
			key, _ := item["key"].(string)
			if !groupKey.MatchString(key) || keys[key] {
				return nil, ErrInvalid
			}
			keys[key] = true
		}
		return keys, nil
	}
	if kind == "style" {
		_, err := unique("rules")
		return err
	}
	if kind != "method" {
		return nil
	}
	inputs, err := unique("inputs")
	if err != nil {
		return err
	}
	outputs, err := unique("expected_outputs")
	if err != nil {
		return err
	}
	for _, field := range []string{"steps", "constraints"} {
		if _, err := unique(field); err != nil {
			return err
		}
	}
	declared, produced := map[string]bool{}, map[string]bool{}
	for key := range inputs {
		declared[key] = true
	}
	for _, value := range payload["steps"].([]any) {
		step := value.(map[string]any)
		seen := map[string]bool{}
		for _, value := range step["input_keys"].([]any) {
			key := value.(string)
			if !declared[key] || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
		}
		for _, value := range step["output_keys"].([]any) {
			key := value.(string)
			if declared[key] {
				return ErrInvalid
			}
			declared[key], produced[key] = true, true
		}
	}
	for key := range outputs {
		if !produced[key] {
			return ErrInvalid
		}
	}
	return nil
}
