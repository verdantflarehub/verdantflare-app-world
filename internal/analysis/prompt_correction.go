package analysis

import (
	"encoding/json"
	"strings"
)

const promptFormatInstruction = "\n提示词格式检查：变量 key 使用 ASCII 英文字母、数字、下划线、点或短横线；variables 的每个 key 必须在 prompt_text 或 negative_prompt 中以 {{key}} 原样出现，不声明未使用变量，也不使用未声明占位符。不需要变量时返回空数组。没有负面词时完全省略 negative_prompt，禁止输出空字符串。保留全部对象与真实证据，重新返回完整 JSON。"

const promptEvidenceMarker = "\n本批提示词证据遗漏诊断："

// Only proves an omission. The completed copy is discarded, never published.
func promptEvidenceOnly(b Bundle, a Association, f PromptRequest, call string, native []byte) error {
	if f.Batch.Version != 2 {
		return ErrInvalid
	}
	schema, err := promptSchema()
	if err != nil {
		return err
	}
	raw, err := verifyTextNative(Binding{call, f.TaskID, f.ModelID}, f.TextHash, schema, native)
	if err != nil {
		return err
	}
	raw, err = decodeBatchEvidence(b, f.Encoding, raw)
	if err != nil {
		return err
	}
	var value any
	schemas, schemaErr := compiledSchemas()
	if schemaErr != nil || json.Unmarshal(raw, &value) != nil || schemas["prompts"].Validate(value) != nil {
		return ErrInvalid
	}
	var doc PromptCandidates
	if json.Unmarshal(raw, &doc) != nil {
		return ErrInvalid
	}
	scopes := map[string]PromptScope{}
	for _, s := range f.Batch.Scopes {
		scopes[s.Key] = s
	}
	changed := false
	for i := range doc.Items {
		seen := map[string]bool{}
		for _, k := range doc.Items[i].Evidence {
			if seen[k] {
				return ErrInvalid
			}
			seen[k] = true
		}
		for _, k := range scopes[doc.Items[i].Scope].Evidence {
			if !seen[k] {
				doc.Items[i].Evidence = append(doc.Items[i].Evidence, k)
				seen[k] = true
				changed = true
			}
		}
	}
	if !changed {
		return ErrInvalid
	}
	temporary, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = ValidatePrompts(b, a, f.Batch, temporary)
	return err
}

// Emit the exact same (possibly short) identifiers the model receives.
func promptEvidenceInstruction(f PromptRequest, native []byte) string {
	var p associationPayload
	var input struct {
		Batch PromptBatch `json:"batch"`
	}
	var n struct {
		Analysis struct {
			Document json.RawMessage `json:"document"`
		} `json:"analysis"`
	}
	var doc PromptCandidates
	if json.Unmarshal(f.Payload, &p) != nil || json.Unmarshal([]byte(p.Text), &input) != nil || json.Unmarshal(native, &n) != nil || json.Unmarshal(n.Analysis.Document, &doc) != nil {
		return ""
	}
	scopes := map[string]PromptScope{}
	for _, s := range input.Batch.Scopes {
		scopes[s.Key] = s
	}
	notes := []map[string]any{}
	for _, item := range doc.Items {
		seen := map[string]bool{}
		for _, k := range item.Evidence {
			seen[k] = true
		}
		missing := []string{}
		for _, k := range scopes[item.Scope].Evidence {
			if !seen[k] {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			notes = append(notes, map[string]any{"scope_key": item.Scope, "target_key": item.Target, "missing_evidence_keys": missing})
		}
	}
	if len(notes) == 0 {
		return ""
	}
	raw, _ := json.Marshal(notes)
	return promptEvidenceMarker + string(raw) + "。逐项补齐上述原输入已存在的必需证据，保留其他真实证据与全部对象；同一 scope 的不同目标模型都须保留该 scope 的完整 evidence_keys。重新返回完整批次，不修改变量规则，不编造编号。"
}

func replacePromptEvidenceInstruction(prompt, instruction string) string {
	if i := strings.Index(prompt, promptEvidenceMarker); i >= 0 {
		prompt = prompt[:i]
	}
	return prompt + instruction
}

// This diagnostic copy is never persisted or accepted as a result. It proves
// that empty optional negatives fully explain a native schema rejection.
func promptEmptyNegativeOnly(b Bundle, a Association, f PromptRequest, call string, native []byte) error {
	if f.Batch.Version != 2 {
		return ErrInvalid
	}
	schema, err := promptSchema()
	if err != nil {
		return err
	}
	if verifySchemaRejectedBatch(Binding{call, f.TaskID, f.ModelID}, f.TextHash, schema, native) != nil {
		return ErrInvalid
	}
	var n struct {
		Analysis struct {
			Document json.RawMessage `json:"document"`
		} `json:"analysis"`
	}
	if json.Unmarshal(native, &n) != nil {
		return ErrInvalid
	}
	raw, err := decodeBatchEvidence(b, f.Encoding, n.Analysis.Document)
	if err != nil {
		return err
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return ErrInvalid
	}
	items, ok := doc["items"].([]any)
	if !ok {
		return ErrInvalid
	}
	changed := false
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			return ErrInvalid
		}
		if v, exists := item["negative_prompt"]; exists && v == "" {
			delete(item, "negative_prompt")
			changed = true
		}
	}
	if !changed {
		return ErrInvalid
	}
	temporary, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = ValidatePrompts(b, a, f.Batch, temporary)
	return err
}
