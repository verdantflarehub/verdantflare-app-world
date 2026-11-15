package world

import "context"

func curationPreviewMatchesPrompt(result, preview contentObject) bool {
	wanted := curationIdentity(learningSourceRef(objectOf(preview["file_ref"])))
	run := objectOf(preview["run_ref"])
	for _, v := range listOf(objectOf(result["payload"])["variants"]) {
		for _, e := range listOf(objectOf(v)["execution_evidence"]) {
			evidence := objectOf(e)
			actual := objectOf(evidence["run_ref"])
			if str(run["service_id"]) != str(actual["service_id"]) || str(run["run_id"]) != str(actual["run_id"]) {
				continue
			}
			for _, output := range listOf(evidence["output_refs"]) {
				if curationIdentity(learningSourceRef(objectOf(output))) == wanted {
					return true // The existing variant gate still verifies every claim.
				}
			}
		}
	}
	return false
}

func validPreviewMedia(m ProducerMedia) bool {
	return producerHashPattern.MatchString(m.SHA256) && m.Size > 0 && m.MIME != ""
}

// Order is not part of the preview reference contract; multiplicity still is.
func samePreviewInputs(expected, actual []ProducerMedia) bool {
	if len(expected) != len(actual) {
		return false
	}
	counts := map[ProducerMedia]int{}
	for _, m := range expected {
		if !validPreviewMedia(m) {
			return false
		}
		counts[m]++
	}
	for _, m := range actual {
		if counts[m] == 0 {
			return false
		}
		counts[m]--
	}
	return true
}

func matchCurationPreviewProof(run RunRef, project string, proof ProducerProof, in []ProducerMedia, output ProducerMedia) error {
	if proof.Version != 1 || proof.ServiceID != run.ServiceID || proof.RunID != run.RunID || proof.ProjectID != project || proof.State != "succeeded" || !samePreviewInputs(in, proof.Inputs) || !validPreviewMedia(output) {
		return ErrNotReady
	}
	for _, m := range proof.Outputs {
		if m == output {
			return nil
		}
	}
	return ErrNotReady
}

// ProducerProof proves media provenance only. It does not validate a prompt
// variant's parameters, model compatibility, or the generated learning document.
func (s *Service) checkCurationPreviewExecution(ctx context.Context, p Principal, preview contentObject, inputs *draftInputs) (RunRef, error) {
	ref := objectOf(preview["run_ref"])
	run := RunRef{ServiceID: str(ref["service_id"]), RunID: str(ref["run_id"])}
	if s.producer == nil || s.content == nil {
		return run, ErrNotReady
	}
	outputRef := learningSourceRef(objectOf(preview["file_ref"]))
	project := outputRef.ProjectID
	resolve := func(ref DraftSourceRef) (ProducerMedia, error) {
		if ref.Kind != "project" || ref.ObjectKey != "" || !ValidID(project) || ref.ProjectID != project || !ValidID(ref.RevisionID) {
			return ProducerMedia{}, ErrNotReady
		}
		file, ok := inputs.Files[ref]
		if !ok || !file.Content.Valid() || file.ID != ref.FileID {
			return ProducerMedia{}, ErrNotReady
		}
		version, err := s.content.Metadata(ctx, p, file.Content, Access{ProjectID: project, RevisionID: ref.RevisionID})
		if err != nil {
			return ProducerMedia{}, err
		}
		media := ProducerMedia{SHA256: version.SHA256, Size: version.Size, MIME: version.MIME}
		if version.ContentRef != file.Content || version.OrganizationID != p.OrganizationID || !validPreviewMedia(media) {
			return ProducerMedia{}, ErrNotReady
		}
		return media, nil
	}
	output, err := resolve(outputRef)
	if err != nil {
		return run, err
	}
	inputMedia := []ProducerMedia{}
	for _, v := range listOf(preview["input_refs"]) {
		media, err := resolve(learningSourceRef(objectOf(v)))
		if err != nil {
			return run, err
		}
		inputMedia = append(inputMedia, media)
	}
	proof, err := s.producer.Read(ctx, p, run, project)
	if err != nil {
		return run, err
	}
	return run, matchCurationPreviewProof(run, project, proof, inputMedia, output)
}
