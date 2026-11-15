package world

import (
	"encoding/json"
	"sort"

	"github.com/verdantflarehub/verdantflare-app-world/internal/analysis"
)

const learningCurationAdapterRevision = "world-analysis/pipeline-2/curation-2"

// The caller must select version from immutable task metadata, never from a
// submitted document. Old tasks retain the original v1 assembly path.
func assembleAnalysisLearningVersion(m analysis.LearningMaterial, task analysis.Task, version string) ([]byte, error) {
	if version == "1" {
		return assembleAnalysisLearning(m)
	}
	if version != "2" {
		return nil, ErrNotReady
	}
	b := m.Bundle
	if task.PipelineVersion != 2 || !task.IncludeTypedResults || task.TaskID != b.TaskID || task.DraftID != b.DraftID || task.RevisionID != b.RevisionID || task.FileID != b.FileID || task.Plan.Source != b.Source {
		return nil, ErrInvalid
	}
	legacy, err := assembleAnalysisLearning(m)
	if err != nil {
		return nil, err
	}
	// Decode with the same number-preserving semantics as service validation.
	parsed, err := ValidateLearningContent(legacy, nil)
	if err != nil {
		return nil, err
	}
	doc := parsed.Document
	planHash, err := digest(task.Plan)
	if err != nil {
		return nil, err
	}
	doc["schema_version"] = 2
	objectOf(doc["provenance"])["version"] = "2"
	doc["processing_snapshot"] = contentObject{
		"task_id": task.TaskID, "pipeline_version": task.PipelineVersion,
		"assembly_version": "2", "adapter_revision": learningCurationAdapterRevision,
		"plan_sha256": planHash,
	}
	packages := map[string][]any{}
	assessments := []any{}
	for _, item := range listOf(doc["results"]) {
		result := objectOf(item)
		key, kind := str(result["key"]), str(result["type_key"])
		packageKey := "type." + kind
		packages[kind] = append(packages[kind], key)
		previews := []any{}
		for _, source := range listOf(result["source_refs"]) {
			ref := objectOf(source)
			if str(ref["kind"]) == "source" && str(ref["key"]) == "original" {
				previews = append(previews, contentObject{
					"key": "original", "state": "ready", "media_kind": "video", "role": "original_reference",
					"note":     "完整原作参考；并非单独生成、裁切或重建的素材。",
					"file_ref": objectOf(parsed.Sources["original"])["content"],
					"locator":  contentObject{"kind": "whole"}, "sha256": b.Source.SHA256,
				})
				break
			}
		}
		assessments = append(assessments, contentObject{
			"result_key": key, "package_keys": []any{packageKey}, "selection": "unassessed",
			"reason_codes":  []any{"pending_use_review"},
			"note":          "原生分析正文保留于本文件；尚未按具体用途审核，不自动推荐。",
			"evidence_refs": []any{}, "merged_from_refs": []any{}, "previews": previews,
		})
	}
	kinds := []string{}
	for kind := range packages {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	groups := []any{}
	for _, kind := range kinds {
		groups = append(groups, contentObject{"key": "type." + kind, "purpose": "review:" + kind, "result_keys": packages[kind]})
	}
	doc["curation"] = contentObject{"packages": groups, "assessments": assessments}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxManifestBytes {
		return nil, ErrLearningTooLarge
	}
	if _, err = ValidateLearningContent(raw, nil); err != nil {
		return nil, err
	}
	return raw, nil
}
