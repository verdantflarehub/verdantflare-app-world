package analysis

import "encoding/json"

const promptFormatInstruction = "\n提示词格式检查：变量 key 使用 ASCII 英文字母、数字、下划线、点或短横线；variables 的每个 key 必须在 prompt_text 或 negative_prompt 中以 {{key}} 原样出现，不声明未使用变量，也不使用未声明占位符。不需要变量时返回空数组。没有负面词时完全省略 negative_prompt，禁止输出空字符串。保留全部对象与真实证据，重新返回完整 JSON。"

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
