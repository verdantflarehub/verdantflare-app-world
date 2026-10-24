package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

type AssociationResultReader interface {
	Result(context.Context, string, string) ([]byte, error)
}

func associationAliases(b Bundle) (map[string]string, map[string]string) {
	forward, reverse := map[string]string{}, map[string]string{}
	for i, n := range b.Nodes {
		alias := fmt.Sprintf("e%06d", i+1)
		forward[n.Key] = alias
		reverse[alias] = n.Key
	}
	return forward, reverse
}

// Only typed identifiers are projected. Descriptions, raw observation values,
// local source keys, timestamps and node ordering remain unchanged.
func associationRequestText(b Bundle, encoding string) (string, error) {
	text, err := AssociationText(b)
	if err != nil || encoding == "" {
		return text, err
	}
	if encoding != "short-v1" {
		return "", ErrInvalid
	}
	aliases, _ := associationAliases(b)
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &doc) != nil {
		return "", ErrInvalid
	}
	var nodes []map[string]json.RawMessage
	if json.Unmarshal(doc["nodes"], &nodes) != nil {
		return "", ErrInvalid
	}
	for _, n := range nodes {
		var key string
		json.Unmarshal(n["key"], &key)
		n["key"], _ = json.Marshal(aliases[key])
		var links map[string][]string
		json.Unmarshal(n["links"], &links)
		for k, refs := range links {
			for i, ref := range refs {
				alias, ok := aliases[ref]
				if !ok {
					return "", ErrInvalid
				}
				refs[i] = alias
			}
			links[k] = refs
		}
		n["links"], _ = json.Marshal(links)
	}
	doc["nodes"], _ = json.Marshal(nodes)
	var bounds []ShotBoundary
	if json.Unmarshal(doc["boundaries"], &bounds) != nil {
		return "", ErrInvalid
	}
	// Boundary structs contain only source metadata and typed shot references.
	raw, _ := json.Marshal(bounds)
	var v any
	json.Unmarshal(raw, &v)
	var replace func(any) any
	replace = func(v any) any {
		switch x := v.(type) {
		case string:
			if a, ok := aliases[x]; ok {
				return a
			}
		case []any:
			for i := range x {
				x[i] = replace(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = replace(x[k])
			}
		}
		return v
	}
	doc["boundaries"], _ = json.Marshal(replace(v))
	raw, err = json.Marshal(doc)
	return string(raw), err
}

func decodeAssociationReferences(b Bundle, encoding string, raw []byte) ([]byte, error) {
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
	var doc map[string]json.RawMessage
	if json.Unmarshal(clean, &doc) != nil {
		return nil, ErrInvalid
	}
	_, aliases := associationAliases(b)
	mapOne := func(v json.RawMessage) (json.RawMessage, error) {
		var key string
		if json.Unmarshal(v, &key) != nil {
			return nil, ErrInvalid
		}
		full, ok := aliases[key]
		if !ok {
			return nil, ErrInvalid
		}
		r, _ := json.Marshal(full)
		return r, nil
	}
	for _, name := range []string{"entities", "voices", "shot_groups", "voice_links", "boundaries"} {
		var rows []map[string]json.RawMessage
		if json.Unmarshal(doc[name], &rows) != nil {
			return nil, ErrInvalid
		}
		for _, row := range rows {
			for _, field := range []string{"member_keys", "evidence_keys"} {
				if v, ok := row[field]; ok {
					var refs []json.RawMessage
					if json.Unmarshal(v, &refs) != nil {
						return nil, ErrInvalid
					}
					for i := range refs {
						refs[i], err = mapOne(refs[i])
						if err != nil {
							return nil, err
						}
					}
					row[field], _ = json.Marshal(refs)
				}
			}
			if name == "boundaries" {
				for _, field := range []string{"left_shot", "right_shot"} {
					row[field], err = mapOne(row[field])
					if err != nil {
						return nil, err
					}
				}
			}
		}
		doc[name], _ = json.Marshal(rows)
	}
	return json.Marshal(doc)
}

// A truncation receipt authorizes a new attempt, never successful content.
func verifyTruncatedAssociation(binding Binding, textHash string, schema map[string]any, data []byte) error {
	clean, err := strictjson.ReadValue(bytes.NewReader(data), 8<<20)
	if err != nil {
		return ErrInvalid
	}
	var r struct {
		Call
		Reason   string `json:"reason_code"`
		Analysis struct {
			Model    string          `json:"model_id"`
			Finish   string          `json:"finish_reason"`
			Text     string          `json:"text"`
			Document json.RawMessage `json:"document"`
			Verified bool            `json:"response_schema_verified"`
			Input    struct {
				Text   string          `json:"source_text_sha256"`
				Schema string          `json:"response_schema_sha256"`
				Media  json.RawMessage `json:"media"`
				Range  json.RawMessage `json:"video_range"`
			} `json:"input"`
		} `json:"analysis"`
	}
	if json.Unmarshal(clean, &r) != nil || !r.Call.matches(binding.TaskID, binding.ModelID, binding.CallID) || r.Binding != binding || r.State != "failed" || r.Reason != "output_incomplete" || r.Analysis.Model != binding.ModelID || r.Analysis.Finish != "MAX_TOKENS" || r.Analysis.Text == "" || r.Analysis.Verified {
		return ErrInvalid
	}
	input := r.Analysis.Input
	if input.Text != textHash || input.Schema != canonicalHash(schema) || len(input.Media) > 0 && string(input.Media) != "null" || len(input.Range) > 0 && string(input.Range) != "null" {
		return ErrInvalid
	}
	if len(r.Analysis.Document) > 0 && string(r.Analysis.Document) != "null" {
		return ErrInvalid
	}
	return nil
}

func associationChecklist(b Bundle, encoding string) string {
	aliases, _ := associationAliases(b)
	groups := map[string][]string{}
	for _, n := range b.Nodes {
		if n.Kind == "entities" || n.Kind == "shots" || n.Kind == "speakers" {
			key := n.Key
			if encoding == "short-v1" {
				key = aliases[key]
			}
			groups[n.Kind] = append(groups[n.Kind], key)
		}
	}
	out := "\n完整成员核对清单（每项恰好使用一次，不能遗漏边缘道具）："
	for _, kind := range []string{"entities", "shots", "speakers"} {
		out += "\n" + kind + ": " + strings.Join(groups[kind], ",")
	}
	return out
}
func omittedAssociationEntities(b Bundle, f AssociationRequest, call string, native []byte) ([]string, error) {
	schema, err := associationSchema()
	if err != nil {
		return nil, err
	}
	raw, err := verifyTextNative(Binding{call, f.TaskID, f.ModelID}, f.TextHash, schema, native)
	if err != nil {
		return nil, err
	}
	raw, err = decodeAssociationReferences(b, f.Encoding, raw)
	if err != nil {
		return nil, err
	}
	a, err := validateAssociationCoverage(b, raw, false)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, g := range a.Entities {
		for _, k := range g.Members {
			seen[k] = true
		}
	}
	for _, g := range a.Voices {
		for _, k := range g.Members {
			seen[k] = true
		}
	}
	for _, g := range a.Shots {
		for _, k := range g.Members {
			seen[k] = true
		}
	}
	aliases, _ := associationAliases(b)
	missing := []string{}
	for _, n := range b.Nodes {
		if (n.Kind == "entities" || n.Kind == "speakers" || n.Kind == "shots") && !seen[n.Key] {
			if n.Kind != "entities" {
				return nil, ErrInvalid
			}
			missing = append(missing, aliases[n.Key])
		}
	}
	return missing, nil
}
