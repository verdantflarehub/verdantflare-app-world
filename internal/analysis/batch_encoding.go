package analysis

import (
	"bytes"
	"encoding/json"
	"reflect"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

// Only structured evidence fields are translated. Prompts, descriptions,
// variable values and model-produced payloads are not text-rewritten.
func translateEvidence(value any, aliases map[string]string, strict bool) error {
	switch x := value.(type) {
	case []any:
		for _, v := range x {
			if err := translateEvidence(v, aliases, strict); err != nil {
				return err
			}
		}
	case map[string]any:
		for field, v := range x {
			if field == "evidence_keys" || field == "member_keys" {
				refs, ok := v.([]any)
				if !ok {
					return ErrInvalid
				}
				for i, ref := range refs {
					key, ok := ref.(string)
					if !ok {
						return ErrInvalid
					}
					mapped, ok := aliases[key]
					if !ok {
						if strict {
							return ErrInvalid
						}
						continue
					}
					refs[i] = mapped
				}
			} else if field == "left_shot" || field == "right_shot" {
				key, ok := v.(string)
				if !ok {
					return ErrInvalid
				}
				mapped, ok := aliases[key]
				if !ok {
					if strict {
						return ErrInvalid
					}
					continue
				}
				x[field] = mapped
			} else if field != "payloads" {
				if err := translateEvidence(v, aliases, strict); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func encodedBatchText(b Bundle, a Association, batch any, encoding string) (string, error) {
	if encoding != "" && encoding != "short-v1" {
		return "", ErrInvalid
	}
	observations, err := associationRequestText(b, encoding)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Observations json.RawMessage `json:"observations"`
		Association  Association     `json:"association"`
		Batch        any             `json:"batch"`
	}{json.RawMessage(observations), a, batch})
	if err != nil {
		return "", err
	}
	if encoding == "short-v1" {
		var doc map[string]any
		json.Unmarshal(raw, &doc)
		aliases, _ := associationAliases(b)
		if err = translateEvidence(doc["association"], aliases, false); err != nil {
			return "", err
		}
		target := doc["batch"].(map[string]any)
		if pairs, ok := target["pairs"].([]any); ok {
			selected := map[string]bool{}
			for _, p := range pairs {
				selected[p.(map[string]any)["scope_key"].(string)] = true
			}
			for _, field := range []string{"scopes", "typed_results"} {
				items, ok := target[field].([]any)
				if !ok {
					return "", ErrInvalid
				}
				kept := []any{}
				for _, item := range items {
					if selected[item.(map[string]any)["key"].(string)] {
						kept = append(kept, item)
					}
				}
				target[field] = kept
			}
		}
		if err = translateEvidence(target, aliases, false); err != nil {
			return "", err
		}
		doc["reference_encoding"] = encoding
		doc["association_sha256"] = canonicalHash(a)
		raw, err = json.Marshal(doc)
		if err != nil {
			return "", err
		}
	}
	if utf8.RuneCount(raw) > MaxAssociationCharacters {
		return "", ErrBundleTooLarge
	}
	return string(raw), nil
}
func decodeBatchEvidence(b Bundle, encoding string, raw []byte) ([]byte, error) {
	if encoding == "" {
		return raw, nil
	}
	if encoding != "short-v1" {
		return nil, ErrInvalid
	}
	clean, err := strictjson.ReadValue(bytes.NewReader(raw), 4<<20)
	if err != nil {
		return nil, ErrInvalid
	}
	var v any
	if json.Unmarshal(clean, &v) != nil {
		return nil, ErrInvalid
	}
	_, reverse := associationAliases(b)
	if err = translateEvidence(v, reverse, true); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func encodedBatchIdentity(text, encoding, associationHash string, batch any) bool {
	if encoding != "short-v1" {
		return false
	}
	var doc struct {
		Encoding        string                     `json:"reference_encoding"`
		AssociationHash string                     `json:"association_sha256"`
		Batch           map[string]json.RawMessage `json:"batch"`
	}
	if json.Unmarshal([]byte(text), &doc) != nil || doc.Encoding != encoding || doc.AssociationHash != associationHash {
		return false
	}
	raw, _ := json.Marshal(batch)
	var full map[string]json.RawMessage
	json.Unmarshal(raw, &full)
	for _, key := range []string{"schema_version", "task_id", "bundle_sha256", "association_sha256", "plan_sha256", "batch_key", "type_key", "pairs", "targets"} {
		var a, c any
		if len(full[key]) == 0 && len(doc.Batch[key]) == 0 {
			continue
		}
		if json.Unmarshal(full[key], &a) != nil || json.Unmarshal(doc.Batch[key], &c) != nil || !reflect.DeepEqual(a, c) {
			return false
		}
	}
	return true
}
