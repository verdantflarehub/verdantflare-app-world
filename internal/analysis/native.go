package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

	"github.com/verdantflarehub/verdantflare-app-world/internal/strictjson"
)

// Binding comes from the durable step-to-channel mapping, not the response.
type Binding struct {
	CallID  string `json:"call_id"`
	TaskID  string `json:"task_id"`
	ModelID string `json:"model_id"`
}

// VerifyNative decodes an authenticated channel result, enforcing the original
// task/call/model before checking exact returned text. It performs no IO and
// cannot authenticate arbitrary user-provided receipts by itself.
func VerifyNative(step Step, prepared Prepared, binding Binding, data []byte) Report {
	bad := func(code string) Report { return Report{Issues: []Issue{{code, "native_result"}}} }
	clean, err := strictjson.ReadValue(bytes.NewReader(data), 8*1024*1024)
	if err != nil {
		return bad("invalid_native_result")
	}
	var result struct {
		SchemaVersion int `json:"schema_version"`
		Binding
		Media     string `json:"media"`
		Operation string `json:"operation"`
		State     string `json:"state"`
		Accepted  *bool  `json:"accepted"`
		Analysis  struct {
			Text           string          `json:"text"`
			Document       json.RawMessage `json:"document"`
			ModelID        string          `json:"model_id"`
			FinishReason   string          `json:"finish_reason"`
			SchemaVerified bool            `json:"response_schema_verified"`
			Input          struct {
				Media struct {
					Hash string `json:"sha256"`
					MIME string `json:"mime_type"`
				} `json:"media"`
				VideoRange *Range `json:"video_range"`
				Integrity  bool   `json:"provider_file_integrity_verified"`
				SchemaHash string `json:"response_schema_sha256"`
			} `json:"input"`
		} `json:"analysis"`
	}
	if json.Unmarshal(clean, &result) != nil || result.SchemaVersion != 1 || result.Binding != binding ||
		binding.TaskID == "" || binding.ModelID == "" || result.Media != "analysis" || result.Operation != "analyze" ||
		result.Analysis.ModelID != binding.ModelID || result.Analysis.FinishReason != "STOP" {
		return bad("native_binding_mismatch")
	}
	var document, text any
	if json.Unmarshal(result.Analysis.Document, &document) != nil || json.Unmarshal([]byte(result.Analysis.Text), &text) != nil || !reflect.DeepEqual(document, text) {
		return bad("native_document_mismatch")
	}
	a := result.Analysis
	digest := sha256.Sum256([]byte(a.Text))
	receipt := Receipt{CallID: binding.CallID, State: result.State, Accepted: result.Accepted != nil && *result.Accepted,
		Integrity: a.Input.Integrity, SchemaVerified: a.SchemaVerified, MediaHash: a.Input.Media.Hash, MIME: a.Input.Media.MIME,
		SchemaHash: a.Input.SchemaHash, OutputHash: hex.EncodeToString(digest[:]), VideoRange: a.Input.VideoRange}
	return Verify(step, prepared, receipt, []byte(a.Text))
}
