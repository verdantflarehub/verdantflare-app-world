package world

import (
	"context"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

type draftInputs struct {
	Files        map[DraftSourceRef]File
	VerifiedRuns map[RunRef]bool
	Access       map[ContentRef]Access
	Dependencies []ContentRef
	Grants       coordination.Plan
	Objects      map[DraftSourceRef]contentObject
}

func draftValidatorsReady(m DraftManifest) error {
	for _, document := range m.DomainDocuments {
		if document.Type != "reference-learning" {
			return ErrNotReady
		}
	}
	return nil
}

// Resolve only explicit fixed source files. Merely mentioning a source never
// imports its private siblings or retains its entire workspace.
func (s *Service) resolveDraftInputs(ctx context.Context, p Principal, c draftCommit) (draftInputs, error) {
	out := draftInputs{Files: map[DraftSourceRef]File{}, Access: map[ContentRef]Access{}, Objects: map[DraftSourceRef]contentObject{}, Dependencies: []ContentRef{}, Grants: coordination.Plan{"world": {{ResourceID: c.DraftID, Write: true}}}}
	if c.Expected != "" {
		previous, err := draftRevision(ctx, s.db, p, c.DraftID, c.Expected)
		if err != nil {
			return out, err
		}
		for _, f := range previous.Manifest.Files {
			out.Access[f.Content] = Access{WorldDraftID: c.DraftID, WorldDraftRevisionID: c.Expected}
		}
	}
	for _, r := range c.Plan.Manifest.SourceRefs {
		var files []File
		var access Access
		switch r.Kind {
		case "world_draft":
			source, err := draftRevision(ctx, s.db, p, r.DraftID, r.RevisionID)
			if err != nil {
				return out, err
			}
			files = source.Manifest.Files
			access = Access{WorldDraftID: r.DraftID, WorldDraftRevisionID: r.RevisionID}
			out.Grants["world"] = append(out.Grants["world"], coordination.Grant{ResourceID: r.DraftID})
		case "project":
			source, err := s.Open(ctx, p, r.ProjectID, r.RevisionID)
			if err != nil {
				return out, err
			}
			if source.ProjectID != r.ProjectID || source.RevisionID != r.RevisionID {
				return out, ErrNotReady
			}
			files = source.Manifest.Files
			access = Access{ProjectID: r.ProjectID, RevisionID: r.RevisionID}
			out.Grants["project"] = append(out.Grants["project"], coordination.Grant{ResourceID: r.ProjectID})
		case "asset":
			source, err := s.WorldGet(ctx, p, WorldGetRequest{AssetID: r.AssetID, VersionID: r.AssetVersionID})
			if err != nil {
				return out, err
			}
			files = source.Manifest.Files
			access = Access{AssetID: r.AssetID, AssetVersionID: r.AssetVersionID}
			out.Grants["world"] = append(out.Grants["world"], coordination.Grant{ResourceID: r.AssetID})
		default:
			return out, ErrInvalid
		}
		found := false
		for _, f := range files {
			if f.ID == r.FileID {
				out.Files[r] = f
				v, err := s.content.Metadata(ctx, p, f.Content, access)
				if err != nil {
					return out, err
				}
				if v.ContentRef != f.Content || v.OrganizationID != p.OrganizationID {
					return out, ErrNotReady
				}
				if r.ObjectKey != "" {
					object, err := s.readLearningSourceObject(ctx, p, r, f, files, access)
					if err != nil {
						return out, err
					}
					out.Objects[r] = object
				}
				if _, ok := out.Access[f.Content]; !ok {
					out.Access[f.Content] = access
				}
				found = true
				break
			}
		}
		if !found {
			return out, ErrInvalid
		}
	}
	for _, a := range c.Plan.Manifest.AssetRefs {
		closure, err := s.ReferencesWithGrants(ctx, p, a)
		if err != nil {
			return out, err
		}
		// The root access only includes its own selected files; each dependency
		// version is independently authorized by the closure/retention resolver.
		m, _, err := assetVersion(ctx, s.db, p.OrganizationID, a.AssetID, a.VersionID)
		if err != nil {
			return out, err
		}
		for _, f := range m.Files {
			if _, ok := out.Access[f.Content]; !ok {
				out.Access[f.Content] = Access{AssetID: a.AssetID, AssetVersionID: a.VersionID}
			}
		}
		out.Dependencies = append(out.Dependencies, closure.Refs...)
		out.Grants["world"] = append(out.Grants["world"], closure.Grants...)
	}
	var err error
	out.Grants, err = coordination.Normalize(out.Grants)
	if err != nil {
		return out, ErrInvalid
	}
	return out, nil
}
func (s *Service) checkDraftContents(ctx context.Context, p Principal, c draftCommit) ([]ContentRef, coordination.Plan, error) {
	m := c.Plan.Manifest
	if m.Validate() != nil || m.DraftID != c.DraftID {
		return nil, nil, ErrInvalid
	}
	// Unknown domains fail closed. Native origins are attested only by authorized,
	// selected Artifact outputs, not by claims in the submitted document.
	if err := draftValidatorsReady(m); err != nil {
		return nil, nil, err
	}
	inputs, err := s.resolveDraftInputs(ctx, p, c)
	if err != nil {
		return nil, nil, err
	}
	refs := append([]ContentRef{}, inputs.Dependencies...)
	outputs := map[RunRef]bool{}
	for _, f := range m.Files {
		access := inputs.Access[f.Content]
		v, err := s.content.Metadata(ctx, p, f.Content, access)
		if err != nil {
			return nil, nil, err
		}
		if v.ContentRef != f.Content || v.OrganizationID != p.OrganizationID {
			return nil, nil, ErrNotReady
		}
		if access == (Access{}) && !draftUserSource(v.Source, c.DraftID) {
			if err := s.checkAnalysisOutput(ctx, p, v); err != nil {
				return nil, nil, err
			}
		}
		if run, ok := draftNativeOutput(v.Source); ok {
			outputs[run] = true
		}
		refs = append(refs, f.Content)
	}
	if err = s.checkDraftDocuments(ctx, p, m, &inputs); err != nil {
		return nil, nil, err
	}
	for _, run := range m.RunRefs {
		if !outputs[run] && !inputs.VerifiedRuns[run] {
			return nil, nil, ErrNotReady
		}
	}
	inputs.Grants, err = coordination.Normalize(inputs.Grants)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	refs = uniqueRefs(refs)
	if len(refs) > MaxFiles {
		return nil, nil, ErrInvalid
	}
	return refs, inputs.Grants, nil
}

// A source tag classifies the output; it does not attest execution state,
// model, prompt, inputs or current permission. The caller verifies these.
// TaskID and RunID are deliberately distinct and are never substituted.
func draftNativeOutput(source Source) (RunRef, bool) {
	run := RunRef{ServiceID: source.ServiceID, RunID: source.RunID}
	valid := source.Kind == "task_output" && source.WorldDraftID == "" && source.WorldDraftRevisionID == "" &&
		source.AssetID == "" && source.AssetVersionID == "" && source.Original == nil &&
		(source.ProjectID == "" || ValidID(source.ProjectID)) && tokenPattern.MatchString(run.ServiceID) && validText(run.RunID)
	return run, valid
}
func draftUserSource(source Source, id string) bool {
	return (source.Kind == "user_edit" || source.Kind == "user_import") && source.WorldDraftID == id && ValidID(id) && source.WorldDraftRevisionID == "" && source.ProjectID == "" && source.AssetID == "" && source.AssetVersionID == "" && source.ServiceID == "" && source.RunID == "" && source.TaskID == "" && source.Original == nil
}
func draftManifestSource(source Source, id, revision string) bool {
	return source.Kind == "world_draft_manifest" && ValidID(id) && ValidID(revision) && source.WorldDraftID == id && source.WorldDraftRevisionID == revision && source.ProjectID == "" && source.AssetID == "" && source.AssetVersionID == "" && source.ServiceID == "" && source.RunID == "" && source.TaskID == "" && source.Original == nil
}
func draftAccess(a Access) bool {
	return ValidID(a.WorldDraftID) && ValidID(a.WorldDraftRevisionID) && a.ProjectID == "" && a.RevisionID == "" && a.AssetID == "" && a.AssetVersionID == ""
}
