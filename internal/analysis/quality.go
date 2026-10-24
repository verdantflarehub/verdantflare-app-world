package analysis

import (
	"encoding/json"
	"strings"
)

func resultInstructionFor(version int) string {
	if version != 2 {
		return resultInstruction
	}
	return resultInstruction + " 新版质量要求：用简体中文撰写业务说明（固定键保持原样）。逐对象给出可辨识的具体特征，不准用‘原片中观察到的镜头’、‘镜头内观察到的神态、肢体动作与表演方式’或‘原片镜头内可见的服装款式、色彩与配饰特征’代替描述。可选且无证据的服装、焦段或转场字段应省略并在 uncertainties 写明未知，不得批量套用标准焦段或硬切；无衣物的动物写无衣物，拟人动作与真实动物行为分开。相机运动与主体动作必须分别说明，无法从观察中判断运动、焦段、构图时保留未知。每条 style 规则在 rule_evidence 中以 rule_key 与 evidence_keys 指向 observations 中实际支持该条规则的节点；payload 中 evidence_refs 仍为空，由系统绑定。缺关键证据时用 insufficient_evidence，不得凑齐 ready。此阶段仅整理证据；不能编造已验证模型、实际生成样例或空间测量。"
}

// Quality gates reject known empty templates, not every uncertain conclusion.
// Missing optional details remain unknown; a failed essential detail must be
// emitted as insufficient_evidence with the actual missing input.
func validateResultQuality(kind string, item ResultCandidate, nodes map[string]bool) error {
	if len(item.Payloads) != 1 {
		return ErrInvalid
	}
	raw, _ := json.Marshal(item.Payloads[0])
	for _, placeholder := range []string{"原片镜头内可见的服装款式、色彩与配饰特征", "镜头内观察到的神态、肢体动作与表演方式", "原片中观察到的镜头", "标准焦段自然透视与景深控制。"} {
		if strings.Contains(string(raw), placeholder) {
			return ErrInvalid
		}
	}
	if kind != "style" {
		if len(item.RuleEvidence) > 0 {
			return ErrInvalid
		}
		return nil
	}
	evidence := map[string]bool{}
	for _, key := range item.Evidence {
		evidence[key] = true
	}
	rules, _ := item.Payloads[0]["rules"].([]any)
	wanted := map[string]bool{}
	for _, r := range rules {
		v, _ := r.(map[string]any)
		k, _ := v["key"].(string)
		wanted[k] = true
	}
	if len(wanted) == 0 || len(item.RuleEvidence) != len(wanted) {
		return ErrInvalid
	}
	for _, rule := range item.RuleEvidence {
		if !wanted[rule.RuleKey] || len(rule.Evidence) == 0 {
			return ErrInvalid
		}
		delete(wanted, rule.RuleKey)
		seen := map[string]bool{}
		for _, key := range rule.Evidence {
			if !nodes[key] || !evidence[key] || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
		}
	}
	return nil
}
