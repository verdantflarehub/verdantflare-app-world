package world

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

var ErrLearningTooLarge = errors.New("analysis learning document exceeds supported size")

const learningEvidenceCharacters = 262144

// AnalysisLearningDocument reads the owner's immutable native records and
// rechecks source access. No inference, project, publication, or approval occurs.
func (s *Service) AnalysisLearningDocument(ctx context.Context, p Principal, taskID string) ([]byte, error) {
	b, err := s.AnalysisBundle(ctx, p, taskID)
	if err != nil {
		return nil, err
	}
	store := analysis.PGStore{DB: s.db}
	task, err := store.Task(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return nil, err
	}
	ar, err := store.LoadAssociation(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return nil, err
	}
	prompts := []analysis.PromptRecord{}
	if len(task.PromptTargets) > 0 {
		prompts, err = store.PromptRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
		if err != nil {
			return nil, err
		}
	}
	results, err := store.ResultRecords(ctx, p.OrganizationID, p.SubjectID, taskID)
	if err != nil {
		return nil, err
	}
	material, err := analysis.AssembleLearningMaterial(task, b, ar, prompts, results)
	if err != nil {
		return nil, err
	}
	return assembleAnalysisLearning(material)
}

type learningEvidencePosition struct{ Chunk, Line int }
type learningEvidenceArchive struct {
	chunks     []string
	lines      int
	characters int
	bytes      int
}

// One NDJSON record per line preserves source text as inert JSON data. Records
// are never cut, summarized or discarded to satisfy a document size limit.
func (a *learningEvidenceArchive) add(kind string, value any) (learningEvidencePosition, error) {
	raw, err := json.Marshal(contentObject{"kind": kind, "value": value})
	if err != nil {
		return learningEvidencePosition{}, err
	}
	text := string(raw) + "\n"
	n := utf8.RuneCountInString(text)
	if n > learningEvidenceCharacters || a.bytes+len(text) > MaxManifestBytes {
		return learningEvidencePosition{}, ErrLearningTooLarge
	}
	if len(a.chunks) == 0 || a.characters+n > learningEvidenceCharacters {
		a.chunks = append(a.chunks, "")
		a.lines, a.characters = 0, 0
	}
	i := len(a.chunks) - 1
	a.chunks[i] += text
	a.lines++
	a.characters += n
	a.bytes += len(text)
	return learningEvidencePosition{i, a.lines}, nil
}

func learningArchiveKey(i int) string                 { return fmt.Sprintf("evidence.%04d", i) }
func learningLocalRef(kind, key string) contentObject { return contentObject{"kind": kind, "key": key} }
func learningTime(r analysis.Range) contentObject {
	return contentObject{"kind": "time", "start_tick": r.StartMS, "end_tick": r.EndMS, "timebase_num": 1, "timebase_den": 1000}
}

// assembleAnalysisLearning only accepts service-verified material. It stays
// unexported; external JSON cannot be promoted into a trusted native snapshot.
func assembleAnalysisLearning(m analysis.LearningMaterial) ([]byte, error) {
	b := m.Bundle
	for _, id := range []string{b.TaskID, b.DraftID, b.RevisionID, b.FileID} {
		if !ValidID(id) {
			return nil, ErrInvalid
		}
	}
	if b.Source.DurationMS < 1 {
		return nil, ErrInvalid
	}
	a := &learningEvidenceArchive{}
	if _, err := a.add("analysis_context", contentObject{"schema_version": 1, "task_id": b.TaskID, "draft_id": b.DraftID, "revision_id": b.RevisionID, "file_id": b.FileID, "source": b.Source, "bundle_sha256": b.SHA256, "shot_boundaries": b.Boundaries}); err != nil {
		return nil, err
	}
	for _, r := range b.Receipts {
		if _, err := a.add("observation_receipt", r); err != nil {
			return nil, err
		}
	}
	positions := map[string]learningEvidencePosition{}
	for _, n := range b.Nodes {
		if _, exists := positions[n.Key]; exists {
			return nil, ErrInvalid
		}
		pos, err := a.add("observation", n)
		if err != nil {
			return nil, err
		}
		positions[n.Key] = pos
	}
	if _, err := a.add("association", m.Association); err != nil {
		return nil, err
	}
	for _, r := range m.Calls {
		if _, err := a.add("native_call", r); err != nil {
			return nil, err
		}
	}
	candidates := map[string]learningEvidencePosition{}
	omissionPositions := map[string]learningEvidencePosition{}
	omissionGroups := map[string][]analysis.PromptOmission{}
	for _, o := range m.PromptOmissions {
		omissionGroups[o.TargetKey] = append(omissionGroups[o.TargetKey], o)
	}
	omissionKeys := []string{}
	for target := range omissionGroups {
		omissionKeys = append(omissionKeys, target)
	}
	sort.Strings(omissionKeys)
	for _, target := range omissionKeys {
		pos, err := a.add("prompt_applicability", omissionGroups[target])
		if err != nil {
			return nil, err
		}
		omissionPositions[target] = pos
	}
	for _, r := range m.Prompts {
		if _, exists := candidates[r.Key]; exists {
			return nil, ErrInvalid
		}
		pos, err := a.add("prompt_candidate", r)
		if err != nil {
			return nil, err
		}
		candidates[r.Key] = pos
	}
	for _, r := range m.Results {
		if _, exists := candidates[r.Key]; exists {
			return nil, ErrInvalid
		}
		pos, err := a.add("result_candidate", r)
		if err != nil {
			return nil, err
		}
		candidates[r.Key] = pos
	}
	original := learningLocalRef("source", "original")
	sources := []any{contentObject{"key": "original", "title": "原始作品", "media_kind": "video", "role": "original", "acquisition_state": "ready", "content": DraftSourceRef{Kind: "world_draft", DraftID: b.DraftID, RevisionID: b.RevisionID, FileID: b.FileID, Purpose: "analysis_source"}}}
	for i, text := range a.chunks {
		sources = append(sources, contentObject{"key": learningArchiveKey(i), "title": fmt.Sprintf("拆解证据与处理记录 %d", i+1), "media_kind": "text", "role": "extracted", "acquisition_state": "ready", "content": contentObject{"kind": "inline", "text": text}})
	}
	coverage := []any{}
	whole := learningTime(analysis.Range{StartMS: 0, EndMS: b.Source.DurationMS})
	for _, kind := range []string{"visual", "audio"} {
		ranges := []any{}
		for _, r := range b.Receipts {
			if r.Step.Kind == kind {
				ranges = append(ranges, learningTime(r.Step.Core))
			}
		}
		state, note := "complete", "固定观察步骤已处理；不代表逐帧测量或人工内容审核。"
		if kind == "audio" && !b.Source.HasAudio {
			state, note = "not_applicable", "原件无音轨；未从画面推断声音。"
		}
		coverage = append(coverage, contentObject{"source_key": "original", "dimension": kind + "_observations", "state": state, "requested_scope": whole, "covered_scopes": ranges, "note": note})
	}
	results, relations := []any{}, []any{}
	addCoverage := func(key, state, note string) {
		pos := candidates[key]
		locator := contentObject{"kind": "lines", "start_line": pos.Line, "end_line": pos.Line}
		ranges := []any{}
		if state == "complete" {
			ranges = append(ranges, locator)
		}
		coverage = append(coverage, contentObject{"source_key": learningArchiveKey(pos.Chunk), "dimension": key, "state": state, "requested_scope": locator, "covered_scopes": ranges, "note": note})
	}
	addResult := func(key, title, kind, basis, note string, keys, missing []string, payload map[string]any) error {
		// Group exact cited nodes by chunk. Span locators may include intervening
		// rows; the archived candidate retains the exact evidence key list.
		spans := map[int][2]int{}
		include := func(p learningEvidencePosition) {
			s, ok := spans[p.Chunk]
			if !ok {
				s = [2]int{p.Line, p.Line}
			}
			spans[p.Chunk] = [2]int{min(s[0], p.Line), max(s[1], p.Line)}
		}
		include(candidates[key])
		for _, k := range keys {
			p, ok := positions[k]
			if !ok {
				return ErrInvalid
			}
			include(p)
		}
		chunks := []int{}
		for i := range spans {
			chunks = append(chunks, i)
		}
		sort.Ints(chunks)
		refs, evidence := []any{original}, []any{}
		for _, i := range chunks {
			ref := learningLocalRef("source", learningArchiveKey(i))
			refs = append(refs, ref)
			span := spans[i]
			evidence = append(evidence, contentObject{"key": learningArchiveKey(i), "source_ref": ref, "locator": contentObject{"kind": "lines", "start_line": span[0], "end_line": span[1]}, "note": "范围包含证据节点和候选记录；精确引用键、原片时码、缺项及不确定性见候选和观察正文。"})
		}
		conditions := []any{contentObject{"mode": "reference", "availability": "ready", "required_inputs": []string{}, "missing_items": []string{}, "purpose": "阅读和复制拆解参考，结合原件审阅；不代表效果或工程已验证。"}}
		availability := "unknown"
		if len(missing) > 0 {
			availability = "missing"
		}
		conditions = append(conditions, contentObject{"mode": "recreate", "availability": availability, "required_inputs": []string{"新创作目标", "目标执行能力与参数"}, "missing_items": append([]string{}, missing...), "purpose": "选择项目后通过 MCP 另行创作；不修改原参考或直接生成世界资产。"})
		domain := map[string]string{"character": "character", "scene": "scene", "prop": "scene", "shot": "shot", "sound_cue": "sound", "music_structure": "music", "script_unit": "script", "style": "knowledge", "method": "knowledge", "prompt": "knowledge"}[kind]
		if domain == "" {
			return ErrInvalid
		}
		results = append(results, contentObject{"key": key, "title": title, "domain": domain, "type_key": kind, "type_version": 1, "basis": basis, "basis_note": note, "source_refs": refs, "evidence": evidence, "use_conditions": conditions, "payload": payload})
		relations = append(relations, contentObject{"key": "from." + key, "from_ref": learningLocalRef("result", key), "to_ref": original, "relation_type": "derived_from", "basis": "created", "description": "由固定原件的完整分析任务整理，保留原证据与未知项。", "attributes": contentObject{"change_summary": "确定性组装已校验候选，未执行新的创作生成。"}})
		return nil
	}
	for _, r := range m.Prompts {
		// The full adaptation note is archived without truncation. Its source
		// schema permits text longer than the result's bounded basis_note.
		note := r.BasisNote
		if utf8.RuneCountInString(note) > 4096 {
			note = "依据原作形成的重建提示词候选，不代表原作者提示词；完整适配说明见证据中的提示词候选记录。"
		}
		if err := addResult(r.Key, r.Title, "prompt", "reconstruction", note, r.Evidence, r.MissingInputs, r.Payload); err != nil {
			return nil, err
		}
		addCoverage(r.Key, "complete", "已整理提示词候选；适用模型仍须解析或实际运行验证。")
	}
	for _, r := range m.Results {
		switch r.State {
		case "ready":
			if len(r.Payloads) != 1 {
				return nil, ErrInvalid
			}
			payload := r.Payloads[0]
			if r.Type == "style" && len(r.RuleEvidence) > 0 {
				// Copy before binding: the native candidate and its hash stay immutable.
				raw, _ := json.Marshal(payload)
				payload = nil
				if json.Unmarshal(raw, &payload) != nil {
					return nil, ErrInvalid
				}
				refsByRule := map[string][]any{}
				for _, rule := range r.RuleEvidence {
					seen := map[string]bool{}
					for _, node := range rule.Evidence {
						pos, ok := positions[node]
						if !ok {
							return nil, ErrInvalid
						}
						key := learningArchiveKey(pos.Chunk)
						if !seen[key] {
							refsByRule[rule.RuleKey] = append(refsByRule[rule.RuleKey], learningLocalRef("source", key))
							seen[key] = true
						}
					}
				}
				for _, v := range payload["rules"].([]any) {
					rule := v.(map[string]any)
					refs := refsByRule[str(rule["key"])]
					if len(refs) == 0 {
						return nil, ErrInvalid
					}
					rule["evidence_refs"] = refs
				}
			}
			if err := addResult(r.Key, r.Title, r.Type, r.Basis, r.BasisNote, r.Evidence, r.MissingInputs, payload); err != nil {
				return nil, err
			}
			addCoverage(r.Key, "complete", "已整理此范围的候选；未代表人工审核或可执行工程交付。")
		case "insufficient_evidence", "not_applicable":
			if len(r.Payloads) != 0 {
				return nil, ErrInvalid
			}
			state := "pending"
			if r.State == "not_applicable" {
				state = r.State
			}
			addCoverage(r.Key, state, r.BasisNote)
		default:
			return nil, ErrInvalid
		}
	}
	// Only explicit shared object scopes establish a prompt-to-result link.
	// Whole-work synthesis does not invent narrative or spatial relationships.
	for _, prompt := range m.Prompts {
		if prompt.Scope == "whole" {
			continue
		}
		for _, result := range m.Results {
			if result.State != "ready" || (result.Scope != prompt.Scope && result.Key != prompt.Scope) {
				continue
			}
			relations = append(relations, contentObject{
				"key":      "adapt." + prompt.Key + "." + result.Key,
				"from_ref": learningLocalRef("result", prompt.Key), "to_ref": learningLocalRef("result", result.Key),
				"relation_type": "adapted_to", "basis": "created", "description": "提示词与此成果对应同一个固定分析对象。",
				"attributes": contentObject{"change_summary": "按请求模型整理该对象的提示词候选，未验证目标模型执行效果。"},
			})
		}
	}
	for _, target := range omissionKeys {
		pos := omissionPositions[target]
		locator := contentObject{"kind": "lines", "start_line": pos.Line, "end_line": pos.Line}
		pending := false
		for _, o := range omissionGroups[target] {
			if o.Reason != "task_type_not_applicable" {
				pending = true
			}
		}
		state, note := "not_applicable", "已逐项记录不适用组合；仅对符合用途的成果生成候选。"
		if pending {
			state = "pending"
			note = "部分成果证据不足，未生成适配提示词；完整原因见本段。"
		}
		coverage = append(coverage, contentObject{"source_key": learningArchiveKey(pos.Chunk), "dimension": "prompt-target." + target, "state": state, "requested_scope": locator, "covered_scopes": []any{}, "note": note})
	}
	doc := contentObject{"schema_version": 1, "document_kind": "reference-learning", "origin": "generated", "sources": sources, "results": results, "relations": relations, "uses": []any{}, "coverage": coverage, "provenance": contentObject{"producer": "world-analysis", "version": "1", "run_refs": []RunRef{{ServiceID: "world", RunID: "analysis-v1:" + b.TaskID}}}}
	if len(sources) > 1000 || len(results) > 10000 || len(relations) > 10000 || len(coverage) > 1000 {
		return nil, ErrLearningTooLarge
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxManifestBytes {
		return nil, ErrLearningTooLarge
	}
	if _, err = ValidateLearningContent(encoded, nil); err != nil {
		return nil, err
	}
	return encoded, nil
}

// A generated document is trusted only when it exactly reproduces the current
// authorized immutable task. Other producer or execution claims stay closed.
func (s *Service) checkAnalysisLearning(ctx context.Context, p Principal, doc LearningContent) error {
	provenance := objectOf(doc.Document["provenance"])
	if str(doc.Document["origin"]) != "generated" || str(provenance["producer"]) != "world-analysis" || str(provenance["version"]) != "1" || len(doc.RunRefs) != 1 {
		return ErrNotReady
	}
	run := doc.RunRefs[0]
	if run.ServiceID != "world" || !strings.HasPrefix(run.RunID, "analysis-v1:") {
		return ErrNotReady
	}
	id := strings.TrimPrefix(run.RunID, "analysis-v1:")
	if !ValidID(id) {
		return ErrNotReady
	}
	expected, err := s.AnalysisLearningDocument(ctx, p, id)
	if err != nil {
		return err
	}
	// Decode through the same number-preserving reader before structural compare.
	verified, err := ValidateLearningContent(expected, nil)
	if err != nil {
		return err
	}
	if learningJSONKey(verified.Document) != learningJSONKey(doc.Document) {
		return ErrNotReady
	}
	return nil
}
