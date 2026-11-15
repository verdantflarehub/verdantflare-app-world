package world

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

func curationUnitRatio(value any) (*big.Rat, bool) {
	n, ok := value.(json.Number)
	if !ok || len(n) > 1024 {
		return nil, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return nil, false
	}
	if f == 0 {
		mantissa := strings.SplitN(strings.ToLower(string(n)), "e", 2)[0]
		if strings.Trim(mantissa, "-+.0") == "" {
			return new(big.Rat), true
		}
		return nil, false
	}
	return new(big.Rat).SetString(string(n))
}

func curationIdentity(ref DraftSourceRef) DraftSourceRef {
	ref.Purpose = ""
	return ref
}

func curationKeys(value any) (map[string]bool, error) {
	out := map[string]bool{}
	for _, v := range listOf(value) {
		if out[str(v)] {
			return nil, learningError("curation", "duplicate key")
		}
		out[str(v)] = true
	}
	return out, nil
}

func validateLearningCuration(doc LearningContent) error {
	curation := objectOf(doc.Document["curation"])
	if curation == nil {
		return nil // v1 remains byte-for-byte compatible.
	}
	packages, err := learningIndex(curation["packages"], "curation.packages")
	if err != nil {
		return err
	}
	membership := map[string]map[string]bool{}
	for k := range doc.Results {
		membership[k] = map[string]bool{}
	}
	for k, p := range packages {
		members, err := curationKeys(p["result_keys"])
		if err != nil {
			return err
		}
		for member := range members {
			if membership[member] == nil {
				return learningError("curation.packages", "missing result")
			}
			membership[member][k] = true
		}
	}
	seen := map[string]bool{}
	merged := map[DraftSourceRef]bool{}
	for _, v := range listOf(curation["assessments"]) {
		a := objectOf(v)
		key := str(a["result_key"])
		if doc.Results[key] == nil || seen[key] {
			return learningError("curation.assessments", "missing or duplicate result")
		}
		seen[key] = true
		keys, err := curationKeys(a["package_keys"])
		if err != nil || len(keys) != len(membership[key]) {
			return learningError("curation.assessments", "package membership differs")
		}
		for k := range keys {
			if !membership[key][k] {
				return learningError("curation.assessments", "package membership differs")
			}
		}
		if _, err := curationKeys(a["reason_codes"]); err != nil {
			return err
		}
		for _, field := range []string{"evidence_refs", "merged_from_refs"} {
			refs := map[DraftSourceRef]bool{}
			for _, r := range listOf(a[field]) {
				ref := curationIdentity(learningSourceRef(objectOf(r)))
				if refs[ref] || field == "merged_from_refs" && (ref.ObjectKey == "" || merged[ref]) {
					return learningError("curation", "duplicate reference or invalid merged object")
				}
				refs[ref] = true
				if field == "merged_from_refs" {
					merged[ref] = true
				}
			}
		}
		if _, err := learningIndex(a["previews"], "curation.previews"); err != nil {
			return err
		}
		for _, item := range listOf(a["previews"]) {
			p := objectOf(item)
			if str(p["state"]) == "missing" {
				continue
			}
			if str(objectOf(p["file_ref"])["object_key"]) != "" {
				return learningError("curation.previews", "media must be a whole file")
			}
			loc := objectOf(p["locator"])
			kind, media := str(loc["kind"]), str(p["media_kind"])
			if kind != "whole" && !(kind == "time" && media != "image") && !(kind == "image_region" && media == "image") {
				return learningError("curation.previews", "locator does not match media")
			}
			if kind == "image_region" {
				for _, pair := range [][2]string{{"x", "width"}, {"y", "height"}} {
					a, ok1 := curationUnitRatio(loc[pair[0]])
					b, ok2 := curationUnitRatio(loc[pair[1]])
					if !ok1 || !ok2 || new(big.Rat).Add(a, b).Cmp(big.NewRat(1, 1)) > 0 {
						return learningError("curation.previews", "image region exceeds bounds")
					}
				}
			}
			refs := map[DraftSourceRef]bool{}
			for _, r := range listOf(p["input_refs"]) {
				ref := curationIdentity(learningSourceRef(objectOf(r)))
				if refs[ref] {
					return learningError("curation.previews", "duplicate input reference")
				}
				refs[ref] = true
			}
		}
	}
	if len(seen) != len(doc.Results) {
		return learningError("curation.assessments", "not every result has an assessment")
	}
	return nil
}

// Use only source files already resolved and authorized by the commit path.
func (s *Service) checkLearningCurationBindings(ctx context.Context, p Principal, doc LearningContent, m DraftManifest, inputs *draftInputs) error {
	curation := objectOf(doc.Document["curation"])
	if curation == nil {
		return nil
	}
	fileFor := func(ref contentObject) (File, bool) {
		if str(ref["kind"]) == "file" {
			for _, f := range m.Files {
				if f.ID == str(ref["file_id"]) {
					return f, true
				}
			}
			return File{}, false
		}
		f, ok := inputs.Files[learningSourceRef(ref)]
		return f, ok
	}
	for _, v := range listOf(curation["assessments"]) {
		a := objectOf(v)
		result := doc.Results[str(a["result_key"])]
		currentSources := map[DraftSourceRef]bool{}
		for _, v := range listOf(result["source_refs"]) {
			ref := objectOf(v)
			if str(ref["kind"]) != "file" && str(ref["kind"]) != "source" && str(ref["kind"]) != "result" {
				currentSources[curationIdentity(learningSourceRef(ref))] = true
			}
		}
		for _, v := range listOf(a["merged_from_refs"]) {
			ref := learningSourceRef(objectOf(v))
			old := inputs.Objects[ref]
			if old == nil || str(old["type_key"]) != str(result["type_key"]) || str(old["domain"]) != str(result["domain"]) || learningJSONKey(old["type_version"]) != learningJSONKey(result["type_version"]) {
				return learningError("curation.merged_from_refs", "fixed result type differs or is unresolved")
			}
			for _, source := range listOf(old["source_refs"]) {
				r := objectOf(source)
				fixed := learningSourceRef(r)
				switch str(r["kind"]) {
				case "source", "result":
					fixed = ref
					fixed.ObjectKey = str(r["key"])
				case "file":
					fixed = ref
					fixed.ObjectKey, fixed.FileID = "", str(r["file_id"])
				}
				if !currentSources[curationIdentity(fixed)] {
					return learningError("curation.merged_from_refs", "original source provenance was dropped")
				}
			}
		}
		for _, v := range listOf(a["previews"]) {
			preview := objectOf(v)
			if str(preview["state"]) == "missing" {
				continue
			}
			file, ok := fileFor(objectOf(preview["file_ref"]))
			if !ok {
				return ErrNotReady
			}
			matched := false
			for _, v := range listOf(result["source_refs"]) {
				ref := objectOf(v)
				if str(ref["kind"]) == "source" {
					ref = objectOf(doc.Sources[str(ref["key"])]["content"])
				}
				if source, ok := fileFor(ref); ok && source.Content == file.Content {
					matched = true
				}
			}
			if str(preview["role"]) == "original_reference" && !matched {
				return learningError("curation.previews", "media does not match original source")
			}
			access, ok := inputs.Access[file.Content]
			if !ok || s.content == nil {
				return ErrNotReady
			}
			metadata, err := s.content.Metadata(ctx, p, file.Content, access)
			if err != nil {
				return err
			}
			if metadata.ContentRef != file.Content || metadata.OrganizationID != p.OrganizationID || metadata.Size <= 0 || metadata.SHA256 != str(preview["sha256"]) || !strings.HasPrefix(metadata.MIME, str(preview["media_kind"])+"/") {
				return learningError("curation.previews", "media metadata differs")
			}
		}
	}
	return nil
}
