package analysis

import (
	"encoding/json"
	"errors"
	"strings"
)

// Diagnostic text is recomputed from the immutable native receipt, never from
// user-supplied correction instructions or a modified candidate.
func methodLinkInstruction(native []byte) string {
	var n struct {
		Analysis struct {
			Document json.RawMessage `json:"document"`
		} `json:"analysis"`
	}
	if json.Unmarshal(native, &n) != nil {
		return ""
	}
	var value any
	if json.Unmarshal(n.Analysis.Document, &value) != nil {
		return ""
	}
	schemas, err := compiledSchemas()
	if err != nil || schemas["results-v2-method"].Validate(value) != nil {
		return ""
	}
	var document ResultCandidates
	if json.Unmarshal(n.Analysis.Document, &document) != nil {
		return ""
	}
	issues := []string{}
	for _, item := range document.Items {
		if len(item.Payloads) != 1 {
			continue
		}
		var issue *methodLinkError
		if errors.As(validateResultPayload("method", item.Payloads[0], ResultScope{}, 0), &issue) {
			issues = append(issues, issue.Problems...)
		}
	}
	if len(issues) == 0 {
		return ""
	}
	return "\n本批方法引用诊断：" + strings.Join(issues, "；") + "。每个步骤 input_keys 必须来自 inputs 或此前步骤的 output_keys；每个 expected_outputs.key 必须在实际步骤 output_keys 中逐字出现。请依据原观察重新输出完整方法，增加真正完成目标的步骤或修正结果键的语义对应，不允许仅空写字段。创作方法用 creative_reuse 或 production_procedure，analysis_procedure 仅用于分析过程。未知信息继续保留未知，不编造原作制作过程。"
}
