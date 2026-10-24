package analysis

import (
	"encoding/json"
	"regexp"
	"strings"
)

const styleRepresentationInstruction = "\n本批最终诊断纠正：palette 每个元素必须仅为 # 加六位十六进制色值（例如 #F5DEB3），禁止附带括号、中文或英文色名；色名说明放在 scope_note 正文中。每条 rule_evidence 的 evidence_keys 必须同时列入该 item.evidence_keys，使用 observations 中真实已有的编号，不得编造或引用 uncertainties。返回完整结果，所有规则均逐条绑定依据。"

var diagnosticHex = regexp.MustCompile(`#[0-9a-fA-F]{6}`)

// The temporary document is never returned, stored or accepted as generated
// content. It only proves the failure is entirely explained by these two
// representational defects before allowing one final native model attempt.
func styleRepresentationOnly(b Bundle, a Association, f ResultRequest, call string, native []byte) error {
	if f.Batch.Version != 2 || f.Batch.Type != "style" {
		return ErrInvalid
	}
	schema, err := resultSchemaVersion(f.Batch.Type, f.Batch.Version)
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
	if _, err = ValidateResults(b, a, f.Batch, raw); err == nil {
		return ErrInvalid
	}
	var d ResultCandidates
	if json.Unmarshal(raw, &d) != nil {
		return ErrInvalid
	}
	nodes := map[string]bool{}
	for _, n := range b.Nodes {
		if n.Kind != "uncertainties" {
			nodes[n.Key] = true
		}
	}
	changed := false
	for i := range d.Items {
		item := &d.Items[i]
		if item.State != "ready" || len(item.Payloads) != 1 {
			return ErrInvalid
		}
		if palette, ok := item.Payloads[0]["palette"].([]any); ok {
			for j, v := range palette {
				text, ok := v.(string)
				if !ok {
					return ErrInvalid
				}
				matches := diagnosticHex.FindAllString(text, -1)
				if len(matches) != 1 {
					return ErrInvalid
				}
				if text != matches[0] {
					// Reject malformed longer hex codes rather than silently truncating them.
					tail := strings.SplitN(text, matches[0], 2)[1]
					if len(tail) > 0 && strings.ContainsRune("0123456789abcdefABCDEF", rune(tail[0])) {
						return ErrInvalid
					}
					palette[j] = matches[0]
					changed = true
				}
			}
		}
		evidence := map[string]bool{}
		for _, k := range item.Evidence {
			evidence[k] = true
		}
		for _, rule := range item.RuleEvidence {
			seen := map[string]bool{}
			for _, k := range rule.Evidence {
				if !nodes[k] || seen[k] {
					return ErrInvalid
				}
				seen[k] = true
				if !evidence[k] {
					item.Evidence = append(item.Evidence, k)
					evidence[k] = true
					changed = true
				}
			}
		}
	}
	if !changed {
		return ErrInvalid
	}
	temporary, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = ValidateResults(b, a, f.Batch, temporary)
	return err
}
