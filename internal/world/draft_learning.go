package world

import (
	"context"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

// A source object is resolved from the exact authorized file and revision. Its
// descriptive claims remain claims; parsing never executes embedded instructions.
func (s *Service) readLearningSourceObject(ctx context.Context, p Principal, ref DraftSourceRef, file File, files []File, access Access) (contentObject, error) {
	data, err := s.content.Read(ctx, p, file.Content, access, MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	doc, err := ValidateLearningContent(data, files)
	if err != nil {
		return nil, err
	}
	if object := doc.Results[ref.ObjectKey]; object != nil {
		return object, nil
	}
	if object := doc.Sources[ref.ObjectKey]; object != nil {
		return object, nil
	}
	return nil, learningError("source_refs.object_key", "missing object in fixed source file")
}
func (s *Service) checkDraftDocuments(ctx context.Context, p Principal, m DraftManifest, inputs *draftInputs) error {
	files := map[string]File{}
	for _, f := range m.Files {
		files[f.ID] = f
	}
	for _, entry := range m.DomainDocuments {
		if entry.Type != "reference-learning" {
			return ErrNotReady
		}
		file, ok := files[entry.FileID]
		if !ok {
			return ErrInvalid
		}
		data, err := s.content.Read(ctx, p, file.Content, inputs.Access[file.Content], MaxManifestBytes)
		if err != nil {
			return err
		}
		doc, err := ValidateLearningContent(data, m.Files)
		if err != nil {
			return err
		}
		if err = s.checkLearningBindings(ctx, p, doc, m, inputs); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) checkLearningBindings(ctx context.Context, p Principal, doc LearningContent, m DraftManifest, inputs *draftInputs) error {
	sources := map[DraftSourceRef]bool{}
	for _, ref := range m.SourceRefs {
		sources[ref] = true
	}
	for _, ref := range doc.RemoteRefs {
		if !sources[ref] {
			return learningError("references", "fixed reference is absent from manifest source_refs")
		}
	}
	// Only a fully reconstructed, currently authorized World analysis document
	// may claim native provenance. Other execution statements remain untrusted.
	if len(doc.RunRefs) > 0 {
		if err := s.checkAnalysisLearning(ctx, p, doc); err != nil {
			return err
		}
	}
	for _, r := range doc.Results {
		if str(r["type_key"]) != "creative_system" {
			continue
		}
		for _, module := range listOf(objectOf(r["payload"])["modules"]) {
			ref := objectOf(objectOf(module)["method_ref"])
			object, err := resolveLearningObject(doc, *inputs, ref)
			if err != nil {
				return err
			}
			kind := str(object["type_key"])
			if kind != "method" && kind != "style" {
				return learningError("modules", "fixed module is not a method or style")
			}
		}
	}
	checkRule := func(ref contentObject, key string) error {
		object, err := resolveLearningObject(doc, *inputs, ref)
		if err != nil {
			return err
		}
		field := map[string]string{"method": "constraints", "style": "rules", "creative_system": "coordination_rules"}[str(object["type_key"])]
		if field != "" {
			for _, r := range listOf(objectOf(object["payload"])[field]) {
				if str(objectOf(r)["key"]) == key {
					return nil
				}
			}
		}
		return learningError("rule_key", "rule absent from fixed source object")
	}
	for _, v := range listOf(doc.Document["relations"]) {
		e := objectOf(v)
		if str(e["relation_type"]) == "constrains" {
			if err := checkRule(objectOf(e["from_ref"]), str(objectOf(e["attributes"])["rule_key"])); err != nil {
				return err
			}
		}
	}
	for _, v := range listOf(doc.Document["uses"]) {
		use := objectOf(v)
		if str(use["target_context"]) == "project" {
			id, revision := str(use["target_project_id"]), str(use["base_revision_id"])
			opened, err := s.Open(ctx, p, id, revision)
			if err != nil {
				return err
			}
			if opened.ProjectID != id || opened.RevisionID != revision {
				return ErrNotReady
			}
			inputs.Grants["project"] = append(inputs.Grants["project"], coordination.Grant{ResourceID: id})
		}
		for _, v := range listOf(use["rule_effects"]) {
			e := objectOf(v)
			if err := checkRule(objectOf(e["method_ref"]), str(e["rule_key"])); err != nil {
				return err
			}
		}
	}
	return nil
}
func resolveLearningObject(doc LearningContent, inputs draftInputs, ref contentObject) (contentObject, error) {
	if str(ref["kind"]) == "result" {
		if o := doc.Results[str(ref["key"])]; o != nil {
			return o, nil
		}
		return nil, ErrInvalid
	}
	if str(ref["kind"]) == "source" {
		return nil, learningError("references", "source material is not a declared method")
	}
	r := learningSourceRef(ref)
	if r.ObjectKey == "" {
		return nil, ErrNotReady
	}
	if o := inputs.Objects[r]; o != nil {
		return o, nil
	}
	return nil, learningError("references", "unresolved fixed object")
}
func learningSourceRef(node contentObject) DraftSourceRef {
	return DraftSourceRef{Kind: str(node["kind"]), DraftID: str(node["draft_id"]), ProjectID: str(node["project_id"]), RevisionID: str(node["revision_id"]), AssetID: str(node["asset_id"]), AssetVersionID: str(node["asset_version_id"]), FileID: str(node["file_id"]), Purpose: str(node["purpose"]), ObjectKey: str(node["object_key"])}
}
