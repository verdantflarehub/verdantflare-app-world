package world

import (
	"bytes"
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var learningOutputTypes = sync.OnceValues(func() (map[string]bool, error) {
	var bundle map[string]any
	if json.Unmarshal(learningSchemaBundle, &bundle) != nil {
		return nil, ErrDependency
	}
	root := objectOf(bundle["station://contracts/v2/reference-learning"])
	result := objectOf(objectOf(root["$defs"])["result"])
	types := listOf(objectOf(objectOf(result["properties"])["type_key"])["enum"])
	if len(types) == 0 {
		return nil, ErrDependency
	}
	out := map[string]bool{}
	for _, kind := range types {
		out[str(kind)] = true
	}
	return out, nil
})

// LearningContent is validated data, not an instruction or an execution plan.
// RemoteRefs/RunRefs still require authorization and native-service resolution.
type LearningContent struct {
	Document   contentObject
	Sources    map[string]contentObject
	Results    map[string]contentObject
	RemoteRefs []DraftSourceRef
	RunRefs    []RunRef
}
type learningValidationError struct{ Path, Reason string }

func (e *learningValidationError) Error() string {
	return ErrInvalid.Error() + ": " + e.Path + ": " + e.Reason
}
func (e *learningValidationError) Unwrap() error { return ErrInvalid }
func learningError(path, reason string) error    { return &learningValidationError{path, reason} }
func learningIndex(value any, path string) (map[string]contentObject, error) {
	out := map[string]contentObject{}
	for _, v := range listOf(value) {
		o := objectOf(v)
		k := str(o["key"])
		if _, ok := out[k]; ok {
			return nil, learningError(path, "duplicate local key")
		}
		out[k] = o
	}
	return out, nil
}

// Opaque model/provider parameters are never traversed as VF references.
func walkLearning(value any, visit func(contentObject) error) error {
	switch v := value.(type) {
	case map[string]any:
		if err := visit(v); err != nil {
			return err
		}
		for key, child := range v {
			if key == "parameters" || key == "parameter_overrides" || (key == "default" && str(v["value_type"]) != "ref") {
				continue
			}
			if err := walkLearning(child, visit); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := walkLearning(child, visit); err != nil {
				return err
			}
		}
	}
	return nil
}
func learningDAG(nodes map[string]contentObject, edges [][2]string, path string) error {
	degrees := map[string]int{}
	followers := map[string][]string{}
	seen := map[[2]string]bool{}
	for k := range nodes {
		degrees[k] = 0
	}
	for _, edge := range edges {
		if nodes[edge[0]] == nil || nodes[edge[1]] == nil {
			return learningError(path, "missing dependency")
		}
		if seen[edge] {
			continue
		}
		seen[edge] = true
		degrees[edge[1]]++
		followers[edge[0]] = append(followers[edge[0]], edge[1])
	}
	todo := []string{}
	for k, n := range degrees {
		if n == 0 {
			todo = append(todo, k)
		}
	}
	count := 0
	for len(todo) > 0 {
		k := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		count++
		for _, child := range followers[k] {
			degrees[child]--
			if degrees[child] == 0 {
				todo = append(todo, child)
			}
		}
	}
	if count != len(nodes) {
		return learningError(path, "cyclic dependency")
	}
	return nil
}
func learningInteger(v any) (*big.Rat, error) {
	n, ok := v.(json.Number)
	if !ok {
		return nil, ErrInvalid
	}
	// The bounded JSON reader rejects overflow. Handle zero before rational
	// parsing so a zero with a hostile exponent cannot allocate huge integers.
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return nil, ErrInvalid
	}
	if f == 0 {
		mantissa := strings.SplitN(strings.ToLower(string(n)), "e", 2)[0]
		if strings.Trim(mantissa, "-+.0") == "" {
			return new(big.Rat), nil
		}
		return nil, ErrInvalid // nonzero underflow is not an integer
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || !r.IsInt() {
		return nil, ErrInvalid
	}
	return r, nil
}
func learningRange(o contentObject) (*big.Rat, *big.Rat, error) {
	start, err := learningInteger(o["start_tick"])
	if err != nil {
		return nil, nil, err
	}
	end, err := learningInteger(o["end_tick"])
	if err != nil {
		return nil, nil, err
	}
	num, err := learningInteger(o["timebase_num"])
	if err != nil {
		return nil, nil, err
	}
	den, err := learningInteger(o["timebase_den"])
	if err != nil || den.Sign() <= 0 {
		return nil, nil, ErrInvalid
	}
	scale := new(big.Rat).Quo(num, den)
	return start.Mul(start, scale), end.Mul(end, scale), nil
}
func learningJSONKey(v any) string { b, _ := json.Marshal(v); return string(b) }

func ValidateLearningContent(data []byte, files []File) (LearningContent, error) {
	out := LearningContent{}
	if err := validateLearningJSON(data, "reference-learning"); err != nil {
		return out, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&out.Document) != nil {
		return out, ErrInvalid
	}
	var err error
	out.Sources, err = learningIndex(out.Document["sources"], "sources")
	if err != nil {
		return out, err
	}
	out.Results, err = learningIndex(out.Document["results"], "results")
	if err != nil {
		return out, err
	}
	for _, field := range []string{"relations", "uses"} {
		if _, err = learningIndex(out.Document[field], field); err != nil {
			return out, err
		}
	}
	for k := range out.Sources {
		if out.Results[k] != nil {
			return out, learningError("sources", "source and result keys overlap")
		}
	}
	inventory := map[string]bool{}
	for _, f := range files {
		inventory[f.ID] = true
	}
	remote := map[DraftSourceRef]bool{}
	runs := map[RunRef]bool{}
	err = walkLearning(out.Document, func(node contentObject) error {
		kind := str(node["kind"])
		switch kind {
		case "source":
			if out.Sources[str(node["key"])] == nil {
				return learningError("references", "missing local source")
			}
		case "result":
			if out.Results[str(node["key"])] == nil {
				return learningError("references", "missing local result")
			}
		case "file":
			if !inventory[str(node["file_id"])] {
				return learningError("references", "missing inventoried file")
			}
		case "world_draft", "project", "asset":
			remote[learningSourceRef(node)] = true
		case "time":
			a, b, err := learningRange(node)
			if err != nil || b.Cmp(a) <= 0 {
				return learningError("locator", "time interval must increase")
			}
		case "lines":
			a, e1 := learningInteger(node["start_line"])
			b, e2 := learningInteger(node["end_line"])
			if e1 != nil || e2 != nil || b.Cmp(a) < 0 {
				return learningError("locator", "line interval must increase")
			}
		}
		// Native run objects have an exact two-field shape in the central schema.
		if len(node) == 2 && node["service_id"] != nil && node["run_id"] != nil {
			runs[RunRef{ServiceID: str(node["service_id"]), RunID: str(node["run_id"])}] = true
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	parents := [][2]string{}
	for key, r := range out.Results {
		if parent := str(r["parent_key"]); parent != "" {
			parents = append(parents, [2]string{parent, key})
		}
		if _, err = learningIndex(r["evidence"], "evidence"); err != nil {
			return out, err
		}
		conditions := map[[2]string]bool{}
		for _, v := range listOf(r["use_conditions"]) {
			c := objectOf(v)
			identity := [2]string{str(c["mode"]), str(c["purpose"])}
			if conditions[identity] {
				return out, learningError("use_conditions", "duplicate mode and purpose")
			}
			conditions[identity] = true
		}
		payload := objectOf(r["payload"])
		switch str(r["type_key"]) {
		case "prompt":
			for _, field := range []string{"variables", "variants"} {
				if _, err = learningIndex(payload[field], field); err != nil {
					return out, err
				}
			}
			for _, v := range listOf(payload["variants"]) {
				if _, err = learningIndex(objectOf(v)["reference_bindings"], "reference_bindings"); err != nil {
					return out, err
				}
			}
		case "method", "skill_candidate":
			if err = validateLearningMethod(payload); err != nil {
				return out, err
			}
		case "creative_system":
			if err = validateLearningSystem(payload, out.Results); err != nil {
				return out, err
			}
		case "scene", "blender_scene":
			if c := objectOf(payload["coordinate_system"]); c != nil && strings.TrimPrefix(str(c["up_axis"]), "-") == strings.TrimPrefix(str(c["forward_axis"]), "-") {
				return out, learningError("coordinate_system", "up and forward axes must differ")
			}
		case "camera_animation":
			a, e1 := learningInteger(payload["frame_start"])
			b, e2 := learningInteger(payload["frame_end"])
			if e1 != nil || e2 != nil || b.Cmp(a) <= 0 {
				return out, learningError("camera_animation", "frame interval must increase")
			}
		case "style":
			if _, err = learningIndex(payload["rules"], "rules"); err != nil {
				return out, err
			}
		}
	}
	if err = learningDAG(out.Results, parents, "parents"); err != nil {
		return out, err
	}
	for _, basis := range []string{"presentation", "story_time", "media_time"} {
		edges := [][2]string{}
		for _, v := range listOf(out.Document["relations"]) {
			e := objectOf(v)
			if str(e["relation_type"]) != "precedes" || str(objectOf(e["attributes"])["order_basis"]) != basis {
				continue
			}
			a, b := objectOf(e["from_ref"]), objectOf(e["to_ref"])
			if str(a["kind"]) == "result" && str(b["kind"]) == "result" {
				edges = append(edges, [2]string{str(a["key"]), str(b["key"])})
			}
		}
		if err = learningDAG(out.Results, edges, "precedence"); err != nil {
			return out, err
		}
	}
	if err = validateLearningCoverage(out.Document, out.Sources); err != nil {
		return out, err
	}
	for _, v := range listOf(out.Document["relations"]) {
		e := objectOf(v)
		if str(e["relation_type"]) == "constrains" {
			if err = out.checkRule(objectOf(e["from_ref"]), str(objectOf(e["attributes"])["rule_key"])); err != nil {
				return out, err
			}
		}
	}
	for _, v := range listOf(out.Document["uses"]) {
		use := objectOf(v)
		outputs := map[string]bool{}
		for _, ref := range listOf(use["output_refs"]) {
			outputs[learningJSONKey(ref)] = true
		}
		for _, item := range listOf(use["rule_effects"]) {
			e := objectOf(item)
			if err = out.checkRule(objectOf(e["method_ref"]), str(e["rule_key"])); err != nil {
				return out, err
			}
			if str(e["disposition"]) == "applied" {
				for _, ref := range listOf(e["output_refs"]) {
					if !outputs[learningJSONKey(ref)] {
						return out, learningError("rule_effects", "applied rule output absent from use outputs")
					}
				}
			}
		}
	}
	for ref := range remote {
		out.RemoteRefs = append(out.RemoteRefs, ref)
	}
	sort.Slice(out.RemoteRefs, func(i, j int) bool { return learningJSONKey(out.RemoteRefs[i]) < learningJSONKey(out.RemoteRefs[j]) })
	for run := range runs {
		out.RunRefs = append(out.RunRefs, run)
	}
	sort.Slice(out.RunRefs, func(i, j int) bool { return learningJSONKey(out.RunRefs[i]) < learningJSONKey(out.RunRefs[j]) })
	return out, nil
}
func validateLearningMethod(p contentObject) error {
	inputs, err := learningIndex(p["inputs"], "inputs")
	if err != nil {
		return err
	}
	if _, err = learningIndex(p["steps"], "steps"); err != nil {
		return err
	}
	outputField := "expected_outputs"
	if p[outputField] == nil {
		outputField = "outputs"
	}
	outputs, err := learningIndex(p[outputField], outputField)
	if err != nil {
		return err
	}
	declared, produced := map[string]bool{}, map[string]bool{}
	for key := range inputs {
		declared[key] = true
	}
	for _, v := range listOf(p["steps"]) {
		step := objectOf(v)
		for _, key := range listOf(step["input_keys"]) {
			if !declared[str(key)] {
				return learningError("steps", "undeclared or future input")
			}
		}
		for _, value := range listOf(step["output_keys"]) {
			key := str(value)
			if produced[key] || inputs[key] != nil {
				return learningError("steps", "output duplicate or overwrites input")
			}
			produced[key] = true
			declared[key] = true
		}
	}
	types, err := learningOutputTypes()
	if err != nil {
		return err
	}
	for key, output := range outputs {
		if !types[str(output["type_key"])] {
			return learningError(outputField, "unknown expected output type")
		}
		if !produced[key] {
			return learningError(outputField, "output has no producing step")
		}
	}
	return nil
}
func validateLearningSystem(p contentObject, results map[string]contentObject) error {
	if len(listOf(p["research_subjects"])) > 0 && len(listOf(p["corpus_refs"])) == 0 {
		return learningError("creative_system", "research requires a source corpus")
	}
	modules, err := learningIndex(p["modules"], "modules")
	if err != nil {
		return err
	}
	if _, err = learningIndex(p["coordination_rules"], "coordination_rules"); err != nil {
		return err
	}
	edges := [][2]string{}
	for key, m := range modules {
		for _, dep := range listOf(m["depends_on_keys"]) {
			edges = append(edges, [2]string{str(dep), key})
		}
		ref := objectOf(m["method_ref"])
		if str(ref["kind"]) == "result" {
			kind := str(results[str(ref["key"])]["type_key"])
			if kind != "method" && kind != "style" {
				return learningError("modules", "module must reference method or style")
			}
		}
	}
	if err = learningDAG(modules, edges, "modules"); err != nil {
		return err
	}
	for _, v := range listOf(p["coordination_rules"]) {
		for _, key := range listOf(objectOf(v)["target_module_keys"]) {
			if modules[str(key)] == nil {
				return learningError("coordination_rules", "missing target module")
			}
		}
	}
	return nil
}
func (c LearningContent) checkRule(ref contentObject, key string) error {
	if str(ref["kind"]) != "result" {
		return nil
	} // the server resolves fixed external refs separately
	r := c.Results[str(ref["key"])]
	field := map[string]string{"method": "constraints", "style": "rules", "creative_system": "coordination_rules"}[str(r["type_key"])]
	if field != "" {
		for _, v := range listOf(objectOf(r["payload"])[field]) {
			if str(objectOf(v)["key"]) == key {
				return nil
			}
		}
	}
	return learningError("rule_key", "rule does not resolve")
}
func validateLearningCoverage(d contentObject, sources map[string]contentObject) error {
	seen := map[string]bool{}
	for _, v := range listOf(d["coverage"]) {
		c := objectOf(v)
		source := sources[str(c["source_key"])]
		if source == nil {
			return learningError("coverage", "missing source")
		}
		requested := objectOf(c["requested_scope"])
		id := learningJSONKey([]any{c["source_key"], c["dimension"], requested})
		if seen[id] {
			return learningError("coverage", "duplicate dimension and scope")
		}
		seen[id] = true
		complete := str(c["state"]) == "complete"
		if complete && str(source["acquisition_state"]) != "ready" {
			return learningError("coverage", "unacquired source cannot be complete")
		}
		kind := str(requested["kind"])
		if kind == "time" || (complete && kind == "lines") {
			var start, end *big.Rat
			var err error
			if kind == "time" {
				start, end, err = learningRange(requested)
			} else {
				start, err = learningInteger(requested["start_line"])
				if err == nil {
					end, err = learningInteger(requested["end_line"])
				}
				if err == nil {
					end.Add(end, big.NewRat(1, 1))
				}
			}
			if err != nil {
				return err
			}
			ranges := [][2]*big.Rat{}
			for _, item := range listOf(c["covered_scopes"]) {
				o := objectOf(item)
				if str(o["kind"]) != kind {
					return learningError("coverage", "locator kind mismatch")
				}
				var a, b *big.Rat
				if kind == "time" {
					a, b, err = learningRange(o)
				} else {
					a, err = learningInteger(o["start_line"])
					if err == nil {
						b, err = learningInteger(o["end_line"])
					}
					if err == nil {
						b.Add(b, big.NewRat(1, 1))
					}
				}
				if err != nil {
					return err
				}
				if a.Cmp(start) < 0 || b.Cmp(end) > 0 {
					return learningError("coverage", "observation outside requested interval")
				}
				ranges = append(ranges, [2]*big.Rat{a, b})
			}
			if complete {
				sort.Slice(ranges, func(i, j int) bool { return ranges[i][0].Cmp(ranges[j][0]) < 0 })
				cursor := new(big.Rat).Set(start)
				for _, span := range ranges {
					if span[0].Cmp(cursor) > 0 {
						return learningError("coverage", "complete coverage contains a gap")
					}
					if span[1].Cmp(cursor) > 0 {
						cursor.Set(span[1])
					}
				}
				if cursor.Cmp(end) < 0 {
					return learningError("coverage", "complete coverage stops early")
				}
			}
		} else if complete && (kind == "whole" || kind == "cell") {
			found := false
			for _, item := range listOf(c["covered_scopes"]) {
				o := objectOf(item)
				if (kind == "whole" && str(o["kind"]) == "whole") || (kind == "cell" && learningJSONKey(o) == learningJSONKey(requested)) {
					found = true
				}
			}
			if !found {
				return learningError("coverage", "complete scope lacks matching observation")
			}
		}
	}
	return nil
}
