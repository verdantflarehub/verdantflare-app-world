package analysis

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

// Native text outputs are accepted only for the frozen input and original call.
// This verifies transport bindings, not the truth of the model's conclusions.
func verifyTextNative(binding Binding, textHash string, schema map[string]any, data []byte) ([]byte, error) {
	if !validUUID(binding.CallID) || !validUUID(binding.TaskID) || !identifier.MatchString(binding.ModelID) || !digestPattern.MatchString(textHash) || schema == nil {
		return nil, ErrInvalid
	}
	clean, err := strictjson.ReadValue(bytes.NewReader(data), 8<<20)
	if err != nil {
		return nil, ErrInvalid
	}
	var result struct {
		SchemaVersion int `json:"schema_version"`
		Binding
		Media     string `json:"media"`
		Operation string `json:"operation"`
		State     string `json:"state"`
		Accepted  bool   `json:"accepted"`
		Analysis  struct {
			Text           string          `json:"text"`
			Document       json.RawMessage `json:"document"`
			ModelID        string          `json:"model_id"`
			Finish         string          `json:"finish_reason"`
			SchemaVerified bool            `json:"response_schema_verified"`
			Input          struct {
				TextHash   string          `json:"source_text_sha256"`
				SchemaHash string          `json:"response_schema_sha256"`
				Media      json.RawMessage `json:"media"`
				Range      json.RawMessage `json:"video_range"`
			} `json:"input"`
		} `json:"analysis"`
	}
	if json.Unmarshal(clean, &result) != nil || result.SchemaVersion != 1 || result.Binding != binding || result.Media != "analysis" || result.Operation != "analyze" || result.State != "completed" || !result.Accepted || result.Analysis.ModelID != binding.ModelID || result.Analysis.Finish != "STOP" || !result.Analysis.SchemaVerified {
		return nil, ErrInvalid
	}
	input := result.Analysis.Input
	if input.TextHash != textHash || input.SchemaHash != canonicalHash(schema) || len(input.Media) > 0 && string(input.Media) != "null" || len(input.Range) > 0 && string(input.Range) != "null" {
		return nil, ErrInvalid
	}
	var original, document any
	if json.Unmarshal([]byte(result.Analysis.Text), &original) != nil || json.Unmarshal(result.Analysis.Document, &document) != nil || !reflect.DeepEqual(original, document) {
		return nil, ErrInvalid
	}
	return []byte(result.Analysis.Text), nil
}
