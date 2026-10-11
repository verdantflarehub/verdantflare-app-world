package world

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Generated from the design repository by scripts/contracts/generate_project_content.py.
//
//go:embed project_content_schema.generated.json
var contentSchemaBundle []byte
var contentSchema = sync.OnceValues(func() (*jsonschema.Schema, error) { return compileDramaSchema("project-content") })
var publicationSchema = sync.OnceValues(func() (*jsonschema.Schema, error) { return compileDramaSchema("short-drama-publication") })

func compileDramaSchema(name string) (*jsonschema.Schema, error) {
	var bundle map[string]any
	if err := json.Unmarshal(contentSchemaBundle, &bundle); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		re.MatchTimeout = 100 * time.Millisecond
		return schemaRegexp{re}, nil
	})
	// Some v2 schemas use filename references as well as canonical IDs.
	for id, schema := range bundle {
		if err := compiler.AddResource(id, schema); err != nil {
			return nil, err
		}
		if err := compiler.AddResource(id+".schema.json", schema); err != nil {
			return nil, err
		}
	}
	return compiler.Compile("station://contracts/drafts/" + name)
}

type schemaRegexp struct{ *regexp2.Regexp }

func (r schemaRegexp) MatchString(value string) bool {
	ok, err := r.Regexp.MatchString(value)
	return err == nil && ok
}

type contentObject = map[string]any

func objectOf(v any) contentObject { o, _ := v.(map[string]any); return o }
func listOf(v any) []any           { a, _ := v.([]any); return a }
func str(v any) string             { s, _ := v.(string); return s }

type contentValidationError struct{ Path, Reason string }

func (e *contentValidationError) Error() string {
	return fmt.Sprintf("%s: %s: %s", ErrInvalid, e.Path, e.Reason)
}
func (e *contentValidationError) Unwrap() error { return ErrInvalid }
func contentError(path, message string) error {
	return &contentValidationError{Path: path, Reason: message}
}
func indexObjects(v any, path string) (map[string]contentObject, error) {
	out := map[string]contentObject{}
	for _, value := range listOf(v) {
		o := objectOf(value)
		k := str(o["key"])
		if _, exists := out[k]; exists {
			return nil, contentError(path, "duplicate key")
		}
		out[k] = o
	}
	return out, nil
}
func contentEqual(a, b any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	if e != nil {
		return false
	}
	var left, right any
	if json.Unmarshal(x, &left) != nil || json.Unmarshal(y, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}
func sameReview(a, b any) bool {
	x, y := objectOf(a), objectOf(b)
	return str(x["state"]) == str(y["state"]) && str(x["record_file_id"]) == str(y["record_file_id"])
}

func hasString(values any, value string) bool {
	for _, v := range listOf(values) {
		if v == value {
			return true
		}
	}
	return false
}
func outputIDs(version contentObject, purpose string) []string {
	out := []string{}
	for _, value := range listOf(version["file_refs"]) {
		f := objectOf(value)
		if purpose == "" || str(f["purpose"]) == purpose {
			out = append(out, str(f["file_id"]))
		}
	}
	return out
}
func sameIDs(a any, b []string) bool {
	xs := listOf(a)
	if len(xs) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range xs {
		id := str(v)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	for _, id := range b {
		if !seen[id] {
			return false
		}
	}
	return true
}

// ValidateDramaContent validates new short-drama writes. Older categories keep their
// existing behavior. Files and record references are scoped to the supplied manifest.
// Human review truth and render quality are not inferred from client JSON.
func ValidateDramaContent(data []byte, m Manifest) error {
	_, err := decodeDramaContent(data, m)
	return err
}
func decodeDramaContent(data []byte, m Manifest) (contentObject, error) {
	if m.Category != "ai_short_drama" {
		return nil, nil
	}
	var root contentObject
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, contentError("project-content", "invalid JSON")
	}
	if root["category"] != m.Category {
		return nil, contentError("category", "does not match project")
	}
	schema, err := contentSchema()
	if err != nil {
		return nil, fmt.Errorf("compile project-content schema: %w", err)
	}
	if err = schema.Validate(root); err != nil {
		return nil, contentError("project-content", "schema validation failed")
	}
	files := map[string]File{}
	for _, f := range m.Files {
		files[f.ID] = f
	}
	if err = checkCurrentFiles(root, files, "project-content"); err != nil {
		return nil, err
	}
	d := objectOf(root["ai_short_drama"])
	globals, err := indexObjects(d["globals"], "globals")
	if err != nil {
		return nil, err
	}
	episodes, err := indexObjects(d["episodes"], "episodes")
	if err != nil {
		return nil, err
	}
	deliveries, err := indexObjects(d["deliverables"], "deliverables")
	if err != nil {
		return nil, err
	}
	continuity, err := indexObjects(d["continuity"], "continuity")
	if err != nil {
		return nil, err
	}
	for k, g := range globals {
		if err = checkVersions(g, "globals/"+k, "global"); err != nil {
			return nil, err
		}
		for _, v := range listOf(g["input_global_keys"]) {
			if globals[str(v)] == nil {
				return nil, contentError("globals/"+k, "missing input global")
			}
		}
	}
	if err = checkGraph(globals, "input_global_keys", "globals"); err != nil {
		return nil, err
	}
	for k, c := range continuity {
		for _, v := range listOf(c["global_keys"]) {
			if globals[str(v)] == nil {
				return nil, contentError("continuity/"+k, "missing global")
			}
		}
	}
	for ek, ep := range episodes {
		if k := str(ep["script_key"]); k != "" {
			g := globals[k]
			if g == nil || g["kind"] != "script" || str(ep["script_file_id"]) != "" && !hasString(g["file_ids"], str(ep["script_file_id"])) {
				return nil, contentError("episodes/"+ek, "invalid script source")
			}
		}
		scenes, e := indexObjects(ep["scenes"], "scenes")
		if e != nil {
			return nil, e
		}
		for sk, sc := range scenes {
			shots, e := indexObjects(sc["shots"], "shots")
			if e != nil {
				return nil, e
			}
			for hk, sh := range shots {
				path := "episodes/" + ek + "/scenes/" + sk + "/shots/" + hk
				if err = checkVersions(sh, path, "shot"); err != nil {
					return nil, err
				}
				if selected := str(sh["selected_result_file_id"]); selected != "" && !hasString(sh["result_file_ids"], selected) {
					return nil, contentError(path, "selected video not in results")
				}
				for _, k := range listOf(sh["continuity_keys"]) {
					if continuity[str(k)] == nil {
						return nil, contentError(path, "missing continuity")
					}
				}
				if r := objectOf(sh["run_ref"]); r != nil {
					found := false
					for _, run := range m.RunRefs {
						if run.ServiceID == str(r["service_id"]) && run.RunID == str(r["run_id"]) {
							found = true
						}
					}
					if !found {
						return nil, contentError(path, "run not in manifest")
					}
				}
			}
		}
	}
	usages, err := indexObjects(d["usages"], "usages")
	if err != nil {
		return nil, err
	}
	for k, u := range usages {
		path := "usages/" + k
		g := globals[str(u["global_key"])]
		if g == nil {
			return nil, contentError(path, "missing global")
		}
		target := objectOf(u["target"])
		ep := episodes[str(target["episode_key"])]
		if ep == nil {
			return nil, contentError(path, "missing episode")
		}
		var shot contentObject
		if sk := str(target["scene_key"]); sk != "" {
			scenes, _ := indexObjects(ep["scenes"], path)
			sc := scenes[sk]
			if sc == nil {
				return nil, contentError(path, "missing scene")
			}
			if hk := str(target["shot_key"]); hk != "" {
				shots, _ := indexObjects(sc["shots"], path)
				shot = shots[hk]
				if shot == nil {
					return nil, contentError(path, "missing shot")
				}
			}
		}
		state := str(u["state_key"])
		versions, _ := indexObjects(g["versions"], path)
		states, _ := indexObjects(g["states"], path)
		if state != "" && states[state] == nil {
			return nil, contentError(path, "missing state")
		}
		if vk := str(u["version_key"]); vk != "" {
			if state == "" || versions[vk] == nil || versions[vk]["state_key"] != state {
				return nil, contentError(path, "version/state mismatch")
			}
		}
		if seg := objectOf(u["segment"]); seg != nil {
			segments, _ := indexObjects(shot["segments"], path)
			if !contentEqual(segments[str(seg["key"])], seg) {
				return nil, contentError(path, "segment does not match target shot")
			}
		}
	}
	for k, d := range deliveries {
		if str(d["episode_key"]) != "" && episodes[str(d["episode_key"])] == nil {
			return nil, contentError("deliverables/"+k, "missing episode")
		}
		if err = checkVersions(d, "deliverables/"+k, "deliverable"); err != nil {
			return nil, err
		}
	}
	candidates, err := indexObjects(d["world_candidates"], "world_candidates")
	if err != nil {
		return nil, err
	}
	for k, c := range candidates {
		source := objectOf(c["source"])
		var item contentObject
		if source["kind"] == "global" {
			item = globals[str(source["key"])]
		} else {
			item = deliveries[str(source["key"])]
		}
		if item == nil {
			return nil, contentError("world_candidates/"+k, "missing source")
		}
		if req := objectOf(c["registration_request"]); req != nil && req["source_project_id"] != m.ProjectID {
			return nil, contentError("world_candidates/"+k, "source project mismatch")
		}
	}
	for _, collection := range []map[string]contentObject{globals, continuity} {
		for k, o := range collection {
			if asset := str(o["asset_id"]); asset != "" {
				found := false
				for _, a := range m.AssetRefs {
					if a.AssetID == asset && a.VersionID == str(o["asset_version_id"]) {
						found = true
					}
				}
				if !found {
					return nil, contentError(k, "asset version not in manifest")
				}
			}
		}
	}
	if err = checkDramaSelections(d, m); err != nil {
		return nil, err
	}
	return d, nil
}

func checkCurrentFiles(value any, files map[string]File, path string) error {
	switch node := value.(type) {
	case map[string]any:
		for k, v := range node {
			// These snapshots have their own fixed source revision, checked by World.
			if k == "registration_request" || k == "registration_status" {
				continue
			}
			if (k == "file_id" || strings.HasSuffix(k, "_file_id")) && v != nil {
				if _, ok := files[str(v)]; !ok {
					return contentError(path+"/"+k, "file not in current manifest")
				}
			}
			if k == "file_ids" || strings.HasSuffix(k, "_file_ids") {
				seen := map[string]bool{}
				for _, id := range listOf(v) {
					if _, ok := files[str(id)]; !ok || seen[str(id)] {
						return contentError(path+"/"+k, "missing or duplicate file")
					}
					seen[str(id)] = true
				}
			}
			if err := checkCurrentFiles(v, files, path+"/"+k); err != nil {
				return err
			}
		}
		if ref := objectOf(node["content_ref"]); ref != nil && node["file_id"] != nil {
			f := files[str(node["file_id"])]
			if f.Content != (ContentRef{StoreID: str(ref["store_id"]), ArtifactID: str(ref["artifact_id"]), VersionID: str(ref["version_id"])}) {
				return contentError(path, "fixed file content changed")
			}
		}
	case []any:
		for i, v := range node {
			if err := checkCurrentFiles(v, files, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
func checkGraph(objects map[string]contentObject, field, path string) error {
	color := map[string]int{}
	var visit func(string) error
	visit = func(k string) error {
		if color[k] == 1 {
			return contentError(path, "cyclic dependency")
		}
		if color[k] == 2 {
			return nil
		}
		color[k] = 1
		deps := listOf(objects[k][field])
		if field == "parent_version_key" && str(objects[k][field]) != "" {
			deps = []any{objects[k][field]}
		}
		for _, v := range deps {
			key := str(v)
			if objects[key] == nil {
				return contentError(path, "missing dependency")
			}
			if err := visit(key); err != nil {
				return err
			}
		}
		color[k] = 2
		return nil
	}
	for k := range objects {
		if err := visit(k); err != nil {
			return err
		}
	}
	return nil
}
func checkVersions(owner contentObject, path, kind string) error {
	versions, err := indexObjects(owner["versions"], path+"/versions")
	if err != nil {
		return err
	}
	states, err := indexObjects(owner["states"], path+"/states")
	if err != nil {
		return err
	}
	segments, err := indexObjects(owner["segments"], path+"/segments")
	if err != nil {
		return err
	}
	orders := map[any]bool{}
	for _, seg := range segments {
		if orders[seg["order"]] {
			return contentError(path, "duplicate segment order")
		}
		orders[seg["order"]] = true
	}
	for _, state := range states {
		if _, err := indexObjects(state["marks"], path+"/marks"); err != nil {
			return err
		}
	}
	fingerprints := map[string]bool{}
	for k, v := range versions {
		identity := contentObject{}
		for field, value := range v {
			switch field {
			case "key", "label", "parent_version_key", "change_note", "review":
			default:
				if value != nil {
					identity[field] = value
				}
			}
		}
		outputs := append([]any{}, listOf(v["file_refs"])...)
		sort.Slice(outputs, func(i, j int) bool {
			return str(objectOf(outputs[i])["file_id"]) < str(objectOf(outputs[j])["file_id"])
		})
		identity["file_refs"] = outputs
		records := append([]any{}, listOf(v["generation_record_file_ids"])...)
		sort.Slice(records, func(i, j int) bool { return str(records[i]) < str(records[j]) })
		identity["generation_record_file_ids"] = records
		fingerprint, _ := digest(identity)
		if fingerprints[fingerprint] {
			return contentError(path, "duplicate production result; reuse its version key")
		}
		fingerprints[fingerprint] = true
		if kind == "global" && states[str(v["state_key"])] == nil {
			return contentError(path+"/versions/"+k, "missing state")
		}
		if sk := str(v["segment_key"]); sk != "" && segments[sk] == nil {
			return contentError(path+"/versions/"+k, "missing segment")
		}
		purpose := str(v["purpose"])
		if purpose == "shot_video" && str(v["segment_key"]) != "" || purpose == "segment_video" && str(v["segment_key"]) == "" {
			return contentError(path, "video scope mismatch")
		}
		seen := map[string]bool{}
		whitebox, lighting := purpose == "whitebox", false
		for _, value := range listOf(v["file_refs"]) {
			f := objectOf(value)
			id := str(f["file_id"])
			if seen[id] {
				return contentError(path, "duplicate version output")
			}
			seen[id] = true
			whitebox = whitebox || f["purpose"] == "whitebox"
			lighting = lighting || f["purpose"] == "lighting"
		}
		if whitebox && str(v["spatial_record_file_id"]) == "" {
			return contentError(path, "whitebox requires spatial record")
		}
		if lighting && str(v["lighting_record_file_id"]) == "" {
			return contentError(path, "lighting requires resolved environment record")
		}
	}
	if err = checkGraph(versions, "parent_version_key", path); err != nil {
		return err
	}
	selections := map[string]bool{}
	for _, value := range listOf(owner["selected_versions"]) {
		sel := objectOf(value)
		v := versions[str(sel["version_key"])]
		purpose := str(sel["purpose"])
		scope := str(sel["state_key"]) + "/" + str(sel["segment_key"]) + "/" + purpose
		if v == nil || selections[scope] {
			return contentError(path, "missing or duplicate selected version")
		}
		selections[scope] = true
		if kind == "global" {
			if sel["state_key"] != v["state_key"] || len(outputIDs(v, purpose)) == 0 {
				return contentError(path, "selection state/purpose mismatch")
			}
		} else if purpose != str(v["purpose"]) || str(sel["segment_key"]) != str(v["segment_key"]) {
			return contentError(path, "selection purpose/segment mismatch")
		}
	}
	if len(versions) == 0 {
		if str(owner["selected_version_key"]) != "" {
			return contentError(path, "missing selected version")
		}
		return nil
	}
	if kind == "deliverable" {
		k := str(owner["selected_version_key"])
		if k != "" {
			v := versions[k]
			if v == nil || !sameIDs(owner["file_ids"], outputIDs(v, "")) || !sameReview(owner["review"], v["review"]) {
				return contentError(path, "delivery projection mismatch")
			}
		} else if len(listOf(owner["file_ids"])) > 0 {
			return contentError(path, "delivery files require explicit selected version")
		}
	}
	if kind == "shot" {
		videos := []string{}
		selected := ""
		var review any
		for _, v := range versions {
			if v["purpose"] == "shot_video" {
				ids := outputIDs(v, "video")
				if len(ids) != 1 {
					return contentError(path, "whole shot requires exactly one primary video")
				}
				videos = append(videos, ids...)
			}
		}
		for _, value := range listOf(owner["selected_versions"]) {
			sel := objectOf(value)
			if sel["purpose"] == "shot_video" {
				v := versions[str(sel["version_key"])]
				selected = outputIDs(v, "video")[0]
				review = v["review"]
			}
		}
		if !sameIDs(owner["result_file_ids"], videos) || str(owner["selected_result_file_id"]) != selected || selected != "" && !sameReview(owner["review"], review) {
			return contentError(path, "shot video projection mismatch")
		}
	}
	return nil
}

// Version locators include the complete parent path; SH001 in two episodes differs.
func dramaOwners(d contentObject) map[string]contentObject {
	out := map[string]contentObject{}
	for _, v := range listOf(d["globals"]) {
		o := objectOf(v)
		out["globals/"+str(o["key"])] = o
	}
	for _, v := range listOf(d["deliverables"]) {
		o := objectOf(v)
		out["deliverables/"+str(o["key"])] = o
	}
	for _, ev := range listOf(d["episodes"]) {
		ep := objectOf(ev)
		for _, sv := range listOf(ep["scenes"]) {
			sc := objectOf(sv)
			for _, hv := range listOf(sc["shots"]) {
				sh := objectOf(hv)
				out["episodes/"+str(ep["key"])+"/scenes/"+str(sc["key"])+"/shots/"+str(sh["key"])] = sh
			}
		}
	}
	return out
}
func checkVersionHistory(old, current contentObject, oldManifest, currentManifest Manifest) error {
	before, after := dramaOwners(old), dramaOwners(current)
	oldFiles, newFiles := map[string]File{}, map[string]File{}
	for _, f := range oldManifest.Files {
		oldFiles[f.ID] = f
	}
	for _, f := range currentManifest.Files {
		newFiles[f.ID] = f
	}
	for path, owner := range before {
		versions, _ := indexObjects(after[path]["versions"], path)
		for _, value := range listOf(owner["versions"]) {
			v := objectOf(value)
			next := versions[str(v["key"])]
			if next == nil {
				return contentError(path, "registered versions cannot be removed")
			}
			// Labels and new review decisions may change, but production facts cannot.
			for k, field := range v {
				if k != "label" && k != "review" && !contentEqual(field, next[k]) {
					return contentError(path+"/"+str(v["key"]), "registered version is immutable")
				}
			}
			for k := range next {
				if k != "label" && k != "review" {
					if _, ok := v[k]; !ok && next[k] != nil {
						return contentError(path, "new production facts require a new version")
					}
				}
			}
			// IDs in production/review records pin bytes even when they lack an inline content_ref.
			for _, id := range versionRecordIDs(v) {
				if oldFiles[id].Content != newFiles[id].Content {
					return contentError(path, "version record content changed or was removed")
				}
			}
		}
	}
	return nil
}
func versionRecordIDs(v contentObject) []string {
	out := []string{}
	for _, key := range []string{"change_record_file_id", "spatial_record_file_id", "lighting_record_file_id", "composition_file_id"} {
		if id := str(v[key]); id != "" {
			out = append(out, id)
		}
	}
	for _, id := range listOf(v["generation_record_file_ids"]) {
		out = append(out, str(id))
	}
	if id := str(objectOf(v["review"])["record_file_id"]); id != "" {
		out = append(out, id)
	}
	return out
}
func (s *Service) readDramaRevision(ctx context.Context, p Principal, m Manifest, revisionID string) (contentObject, error) {
	if m.Category != "ai_short_drama" {
		return nil, nil
	}
	for _, d := range m.DomainDocuments {
		if d.Type == "project-content" {
			for _, f := range m.Files {
				if f.ID == d.FileID {
					data, e := s.content.Read(ctx, p, f.Content, Access{ProjectID: m.ProjectID, RevisionID: revisionID}, MaxManifestBytes)
					if e != nil {
						return nil, e
					}
					var root contentObject
					if json.Unmarshal(data, &root) != nil {
						return nil, ErrInvalid
					}
					return objectOf(root["ai_short_drama"]), nil
				}
			}
		}
	}
	return nil, nil
}

// PublicationRecord is a small ordinary JSON file, included in the registered
// package. Source revision comes from WorldRegisterRequest, avoiding self-reference.
type dramaPublication struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Source        struct {
		Kind string `json:"kind"`
		Key  string `json:"key"`
	} `json:"source"`
	Versions []struct {
		VersionKey string                 `json:"version_key"`
		FileRefs   []dramaPublicationFile `json:"file_refs"`
	} `json:"versions"`
	Supporting []dramaPublicationFile `json:"supporting_file_refs"`
}
type dramaPublicationFile struct {
	FileID  string     `json:"file_id"`
	Content ContentRef `json:"content_ref"`
	Purpose string     `json:"purpose"`
}

func validateDramaPublication(data []byte, candidate, source contentObject, m Manifest, recordID string, request WorldRegisterRequest) error {
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return ErrInvalid
	}
	schema, err := publicationSchema()
	if err != nil {
		return err
	}
	if schema.Validate(raw) != nil {
		return ErrInvalid
	}
	var r dramaPublication
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil || r.SchemaVersion != 1 || r.Kind != "short-drama-publication" || len(r.Versions) == 0 || r.Supporting == nil {
		return ErrInvalid
	}
	expectedSource := objectOf(candidate["source"])
	if r.Source.Kind != str(expectedSource["kind"]) || r.Source.Key != str(expectedSource["key"]) {
		return ErrInvalid
	}
	files := map[string]File{}
	for _, f := range m.Files {
		files[f.ID] = f
	}
	if _, ok := files[recordID]; !ok {
		return ErrInvalid
	}
	versions, _ := indexObjects(source["versions"], "publication")
	selected := map[string]bool{recordID: true}
	seenVersions := map[string]bool{}
	allowedSupport := map[string]bool{}
	requiredSupport := map[string]bool{}
	for _, entry := range r.Versions {
		v := versions[entry.VersionKey]
		if v == nil || seenVersions[entry.VersionKey] || len(entry.FileRefs) == 0 {
			return ErrInvalid
		}
		seenVersions[entry.VersionKey] = true
		for _, file := range entry.FileRefs {
			if file.Purpose == "" || selected[file.FileID] || files[file.FileID].Content != file.Content {
				return ErrInvalid
			}
			found := false
			for _, item := range listOf(v["file_refs"]) {
				f := objectOf(item)
				if f["file_id"] == file.FileID && f["purpose"] == file.Purpose && contentEqual(f["content_ref"], file.Content) {
					found = true
				}
			}
			if !found {
				return ErrInvalid
			}
			selected[file.FileID] = true
			if file.Purpose == "lighting" {
				requiredSupport[str(v["lighting_record_file_id"])] = true
			}
			if file.Purpose == "whitebox" {
				requiredSupport[str(v["spatial_record_file_id"])] = true
			}
		}
		for _, id := range versionRecordIDs(v) {
			allowedSupport[id] = true
		}
		states, _ := indexObjects(source["states"], "publication")
		for _, item := range listOf(states[str(v["state_key"])]["definition_refs"]) {
			allowedSupport[str(objectOf(item)["file_id"])] = true
		}
	}
	for _, item := range listOf(source["definition_refs"]) {
		allowedSupport[str(objectOf(item)["file_id"])] = true
	}
	for _, file := range r.Supporting {
		if !allowedSupport[file.FileID] || selected[file.FileID] || files[file.FileID].Content != file.Content || file.Purpose == "" {
			return ErrInvalid
		}
		selected[file.FileID] = true
	}
	for id := range requiredSupport {
		if id == "" || !selected[id] {
			return ErrInvalid
		}
	}
	if len(selected) != len(request.FileIDs) {
		return ErrInvalid
	}
	for _, id := range request.FileIDs {
		if !selected[id] {
			return ErrInvalid
		}
	}
	return nil
}

// Manifest selections and domain selections are committed together, never inferred
// on read. Reserved purposes make cross-client consistency verifiable.
func checkDramaSelections(d contentObject, m Manifest) error {
	expected := map[string][]string{}
	versioned := false
	for path, owner := range dramaOwners(d) {
		versions, _ := indexObjects(owner["versions"], path)
		versioned = versioned || len(versions) > 0
		if strings.HasPrefix(path, "deliverables/") {
			if key := str(owner["selected_version_key"]); key != "" {
				expected["drama/"+path] = outputIDs(versions[key], "")
			}
			continue
		}
		for _, value := range listOf(owner["selected_versions"]) {
			sel := objectOf(value)
			v := versions[str(sel["version_key"])]
			purpose := str(sel["purpose"])
			if strings.HasPrefix(path, "globals/") {
				expected["drama/"+path+"/"+str(sel["state_key"])+"/"+purpose] = outputIDs(v, purpose)
			} else {
				segment := str(sel["segment_key"])
				if segment == "" {
					segment = "whole"
				} else {
					segment = "segment-" + segment
				}
				expected["drama/"+path+"/"+purpose+"/"+segment] = outputIDs(v, "")
			}
		}
	}
	if !versioned {
		return nil
	}
	for _, sel := range m.Selections {
		if !strings.HasPrefix(sel.Purpose, "drama/") {
			continue
		}
		ids, ok := expected[sel.Purpose]
		if !ok {
			return contentError("selections", "unknown drama purpose")
		}
		actual := make([]any, len(sel.FileIDs))
		for i, id := range sel.FileIDs {
			actual[i] = id
		}
		if !sameIDs(actual, ids) {
			return contentError("selections", "domain and manifest selection disagree")
		}
		delete(expected, sel.Purpose)
	}
	if len(expected) > 0 {
		return contentError("selections", "missing fixed drama selection")
	}
	return nil
}

// Both page and MCP world.register pass here before creating a new World version.
// Existing idempotent registrations resume their original fixed plan.
func (s *Service) checkWorldDramaPublication(ctx context.Context, p Principal, r WorldRegisterRequest) error {
	opened, err := s.Open(ctx, p, r.SourceProjectID, r.SourceRevisionID)
	m := opened.Manifest
	if err != nil {
		return err
	}
	if m.Category != "ai_short_drama" {
		return nil
	}
	d, err := s.readDramaRevision(ctx, p, m, r.SourceRevisionID)
	if err != nil {
		return err
	}
	versioned := false
	for _, owner := range dramaOwners(d) {
		if len(listOf(owner["versions"])) > 0 {
			versioned = true
			break
		}
	}
	if !versioned {
		return nil
	}
	for _, value := range listOf(d["world_candidates"]) {
		c := objectOf(value)
		recordID := str(c["publication_record_file_id"])
		selected := false
		for _, id := range r.FileIDs {
			if id == recordID {
				selected = true
			}
		}
		if !selected {
			continue
		}
		review := objectOf(c["review"])
		if review["state"] != "approved" || r.ReviewFileID == "" || r.ReviewFileID != str(review["record_file_id"]) {
			return ErrInvalid
		}
		source := objectOf(c["source"])
		prefix := "globals/"
		if source["kind"] == "deliverable" {
			prefix = "deliverables/"
		}
		owner := dramaOwners(d)[prefix+str(source["key"])]
		if owner == nil {
			return ErrInvalid
		}
		for _, f := range m.Files {
			if f.ID == recordID {
				data, e := s.content.Read(ctx, p, f.Content, Access{ProjectID: m.ProjectID, RevisionID: r.SourceRevisionID}, MaxManifestBytes)
				if e != nil {
					return e
				}
				return validateDramaPublication(data, c, owner, m, recordID, r)
			}
		}
		return ErrInvalid
	}
	return ErrInvalid
}
