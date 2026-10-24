package world

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
)

var producerHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// External executions can attest individual samples, never arbitrary generated
// documents or use histories. All content still passes normal project retention.
func (s *Service) checkLearningExecutions(ctx context.Context, p Principal, doc LearningContent, m DraftManifest, inputs *draftInputs) error {
	if s.producer == nil || str(doc.Document["origin"]) == "generated" || doc.Document["provenance"] != nil {
		return ErrNotReady
	}
	for _, u := range listOf(doc.Document["uses"]) {
		if len(listOf(objectOf(u)["run_refs"])) > 0 {
			return ErrNotReady
		}
	}
	checked := map[RunRef]bool{}
	for _, result := range doc.Results {
		if str(result["type_key"]) != "prompt" {
			continue
		}
		for _, v := range listOf(objectOf(result["payload"])["variants"]) {
			variant := objectOf(v)
			for _, e := range listOf(variant["execution_evidence"]) {
				execution := objectOf(e)
				ref := objectOf(execution["run_ref"])
				run := RunRef{ServiceID: str(ref["service_id"]), RunID: str(ref["run_id"])}
				project := ""
				resolve := func(refs any) ([]ProducerMedia, error) {
					media := []ProducerMedia{}
					for _, v := range listOf(refs) {
						ref := objectOf(v)
						var file File
						var access Access
						switch str(ref["kind"]) {
						case "file":
							for _, f := range m.Files {
								if f.ID == str(ref["file_id"]) {
									file = f
									access = inputs.Access[f.Content]
									break
								}
							}
						case "project":
							fixed := learningSourceRef(ref)
							file = inputs.Files[fixed]
							access = Access{ProjectID: fixed.ProjectID, RevisionID: fixed.RevisionID}
						default:
							return nil, ErrNotReady
						}
						if file.ID == "" || !ValidID(access.ProjectID) || !ValidID(access.RevisionID) {
							return nil, ErrNotReady
						}
						if project != "" && project != access.ProjectID {
							return nil, ErrNotReady
						}
						project = access.ProjectID
						version, err := s.content.Metadata(ctx, p, file.Content, access)
						if err != nil {
							return nil, err
						}
						if version.ContentRef != file.Content || version.OrganizationID != p.OrganizationID {
							return nil, ErrNotReady
						}
						media = append(media, ProducerMedia{version.SHA256, version.Size, version.MIME})
					}
					return media, nil
				}
				in, err := resolve(execution["input_refs"])
				if err != nil {
					return err
				}
				out, err := resolve(execution["output_refs"])
				if err != nil {
					return err
				}
				if project == "" {
					return ErrNotReady
				}
				proof, err := s.producer.Read(ctx, p, run, project)
				if err != nil {
					return err
				}
				if err = matchLearningExecution(variant, execution, run, project, proof, in, out); err != nil {
					return err
				}
				checked[run] = true
			}
		}
	}
	if len(checked) != len(doc.RunRefs) {
		return ErrNotReady
	}
	for _, r := range doc.RunRefs {
		if !checked[r] {
			return ErrNotReady
		}
	}
	if inputs.VerifiedRuns == nil {
		inputs.VerifiedRuns = map[RunRef]bool{}
	}
	for r := range checked {
		inputs.VerifiedRuns[r] = true
	}
	return nil
}
func matchLearningExecution(v, e contentObject, run RunRef, project string, p ProducerProof, in, out []ProducerMedia) error {
	prompt := str(v["prompt_text"])
	if p.Version != 1 || p.ServiceID != run.ServiceID || p.RunID != run.RunID || p.ProjectID != project || p.State != "succeeded" || str(e["state"]) != p.State || prompt == "" || v["messages"] != nil || v["negative_prompt"] != nil {
		return ErrNotReady
	}
	model, route := str(v["model_id"]), str(v["route"])
	allowed := run.ServiceID == "image" && model == "gpt-image-2.5-sunburst" && route == "codex" || run.ServiceID == "video" && (model == "minimax-h3-ref2va" && route == "h3-singularity" || model == "seedance_2_5" && route == "higgsfield")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(prompt)))
	if !allowed || p.ModelID != model || p.Route != route || str(e["model_id"]) != model || str(e["route"]) != route || p.PromptHash != hash || str(e["prompt_sha256"]) != hash {
		return ErrNotReady
	}
	for k, value := range objectOf(v["parameters"]) {
		actual, ok := p.Parameters[k]
		if !ok || learningJSONKey(value) != learningJSONKey(actual) {
			return ErrNotReady
		}
	}
	same := func(a, b []ProducerMedia) bool {
		if len(a) != len(b) {
			return false
		}
		for i, f := range a {
			if !producerHashPattern.MatchString(f.SHA256) || f.Size <= 0 || f.MIME == "" || f != b[i] {
				return false
			}
		}
		return true
	}
	if len(out) == 0 || !same(in, p.Inputs) || !same(out, p.Outputs) {
		return ErrNotReady
	}
	bindings, refs := listOf(v["reference_bindings"]), listOf(e["input_refs"])
	if len(bindings) != len(refs) {
		return ErrNotReady
	}
	for i, b := range bindings {
		if learningJSONKey(objectOf(b)["input_ref"]) != learningJSONKey(refs[i]) {
			return ErrNotReady
		}
	}

	return nil
}
