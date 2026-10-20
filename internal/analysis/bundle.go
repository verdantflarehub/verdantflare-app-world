package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const MaxBundleBytes = 16 << 20

var ErrObservationsIncomplete = errors.New("analysis observations incomplete")
var ErrBundleTooLarge = errors.New("analysis evidence bundle exceeds supported size")

type EvidenceNode struct {
	Key      string              `json:"key"`
	Kind     string              `json:"kind"`
	StepKey  string              `json:"step_key"`
	CallID   string              `json:"call_id"`
	Pointer  string              `json:"json_pointer"`
	LocalKey string              `json:"local_key,omitempty"`
	Scope    *Range              `json:"scope,omitempty"`
	Links    map[string][]string `json:"links"`
	Value    json.RawMessage     `json:"value"`
}
type ObservationReceipt struct {
	Step        Step     `json:"step"`
	CallID      string   `json:"call_id"`
	ModelID     string   `json:"model_id"`
	RequestHash string   `json:"request_sha256"`
	ResultHash  string   `json:"native_result_sha256"`
	Prepared    Prepared `json:"prepared"`
}
type ShotBoundary struct {
	Left           string `json:"left_shot"`
	Right          string `json:"right_shot"`
	AtMS           int64  `json:"at_ms"`
	LeftContinues  bool   `json:"left_continues"`
	RightContinues bool   `json:"right_continues"`
	State          string `json:"state"`
}
type Bundle struct {
	Version    int                  `json:"schema_version"`
	Stage      string               `json:"stage"`
	TaskID     string               `json:"task_id"`
	DraftID    string               `json:"draft_id"`
	RevisionID string               `json:"revision_id"`
	FileID     string               `json:"file_id"`
	Source     Source               `json:"source"`
	Receipts   []ObservationReceipt `json:"receipts"`
	Nodes      []EvidenceNode       `json:"nodes"`
	Boundaries []ShotBoundary       `json:"shot_boundaries"`
	SHA256     string               `json:"sha256,omitempty"`
}

// Assemble revalidates original native results, not just a saved pass flag.
// It deliberately preserves observation fragments and identity uncertainty.
// This function is internal: callers must supply owner-authorized stored rows.
func Assemble(task Task, records []StepRecord) (Bundle, error) {
	b := Bundle{Version: 1, Stage: "association_input", TaskID: task.TaskID, DraftID: task.DraftID, RevisionID: task.RevisionID, FileID: task.FileID, Source: task.Plan.Source, Receipts: []ObservationReceipt{}, Nodes: []EvidenceNode{}, Boundaries: []ShotBoundary{}}
	for _, id := range []string{task.OrganizationID, task.OwnerID, task.TaskID, task.DraftID, task.RevisionID, task.FileID} {
		if !validUUID(id) {
			return Bundle{}, ErrInvalid
		}
	}
	plan, err := PlanVideo(task.Plan.Source)
	if err != nil || canonicalHash(task.Plan) != canonicalHash(plan) || len(records) != len(plan.Steps) {
		return Bundle{}, ErrInvalid
	}
	byKey := map[string]StepRecord{}
	for _, r := range records {
		if r.Validate() != nil || r.OrganizationID != task.OrganizationID || r.OwnerID != task.OwnerID || r.Request.TaskID != task.TaskID {
			return Bundle{}, ErrInvalid
		}
		if r.State != "observation_ready" {
			return Bundle{}, ErrObservationsIncomplete
		}
		if _, exists := byKey[r.Request.Step.Key]; exists {
			return Bundle{}, ErrInvalid
		}
		byKey[r.Request.Step.Key] = r
	}
	seen := map[string]bool{}
	seenCalls := map[string]bool{}
	var lastVisual *EvidenceNode
	var lastContinues bool
	total := 0
	for _, step := range plan.Steps {
		r, ok := byKey[step.Key]
		if !ok || r.Request.Step != step || !VerifyNative(step, r.Request.Prepared, Binding{CallID: r.CallID, TaskID: task.TaskID, ModelID: r.Request.ModelID}, r.Result).Passed {
			return Bundle{}, ErrInvalid
		}
		if seenCalls[r.CallID] {
			return Bundle{}, ErrInvalid
		}
		seenCalls[r.CallID] = true
		b.Receipts = append(b.Receipts, ObservationReceipt{Step: step, CallID: r.CallID, ModelID: r.Request.ModelID, RequestHash: r.RequestHash, ResultHash: canonicalHash(r.Result), Prepared: r.Request.Prepared})
		var envelope struct {
			Analysis struct {
				Text string `json:"text"`
			} `json:"analysis"`
		}
		if json.Unmarshal(r.Result, &envelope) != nil {
			return Bundle{}, ErrInvalid
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal([]byte(envelope.Analysis.Text), &doc) != nil {
			return Bundle{}, ErrInvalid
		}
		groups := map[string][]json.RawMessage{}
		for key, value := range doc {
			if key == "start_ms" || key == "end_ms" {
				continue
			}
			var items []json.RawMessage
			if json.Unmarshal(value, &items) != nil {
				return Bundle{}, ErrInvalid
			}
			groups[key] = items
		}
		keys := map[string]map[string]string{}
		for _, group := range []string{"entities", "shots", "speakers"} {
			keys[group] = map[string]string{}
			for i, raw := range groups[group] {
				var value struct {
					Key string `json:"key"`
				}
				if json.Unmarshal(raw, &value) != nil {
					return Bundle{}, ErrInvalid
				}
				keys[group][value.Key] = evidenceKey(task.TaskID, step.Key, group, fmt.Sprintf("/%s/%d", group, i))
			}
		}
		add := func(group, pointer string, raw json.RawMessage, links map[string][]string) (EvidenceNode, error) {
			value, err := canonicalJSON(raw)
			if err != nil {
				return EvidenceNode{}, ErrInvalid
			}
			total += len(value)
			if total > MaxBundleBytes {
				return EvidenceNode{}, ErrBundleTooLarge
			}
			n := EvidenceNode{Key: evidenceKey(task.TaskID, step.Key, group, pointer), Kind: group, StepKey: step.Key, CallID: r.CallID, Pointer: pointer, Links: links, Value: value}
			if seen[n.Key] {
				return n, ErrInvalid
			}
			seen[n.Key] = true
			if group != "uncertainties" {
				var meta struct {
					Key   string `json:"key"`
					Start *int64 `json:"start_ms"`
					End   *int64 `json:"end_ms"`
				}
				if json.Unmarshal(raw, &meta) != nil {
					return n, ErrInvalid
				}
				n.LocalKey = meta.Key
				if meta.Start != nil && meta.End != nil {
					n.Scope = &Range{*meta.Start, *meta.End}
				}
			}
			b.Nodes = append(b.Nodes, n)
			return n, nil
		}
		resolve := func(group string, local []string) ([]string, error) {
			out := []string{}
			for _, k := range local {
				id, ok := keys[group][k]
				if !ok {
					return nil, ErrInvalid
				}
				out = append(out, id)
			}
			return out, nil
		}
		ordered := []string{"entities", "shots", "findings", "speakers", "intervals", "speech", "music", "sound_effects", "uncertainties"}
		for _, group := range ordered {
			for i, raw := range groups[group] {
				pointer := fmt.Sprintf("/%s/%d", group, i)
				links := map[string][]string{}
				switch group {
				case "shots":
					var shot Shot
					if json.Unmarshal(raw, &shot) != nil {
						return Bundle{}, ErrInvalid
					}
					links["entities"], err = resolve("entities", shot.EntityKeys)
				case "findings":
					var f struct {
						Keys []string `json:"shot_keys"`
					}
					if json.Unmarshal(raw, &f) != nil {
						return Bundle{}, ErrInvalid
					}
					links["shots"], err = resolve("shots", f.Keys)
				case "speech":
					var s struct {
						Key string `json:"speaker_key"`
					}
					if json.Unmarshal(raw, &s) != nil {
						return Bundle{}, ErrInvalid
					}
					links["speakers"], err = resolve("speakers", []string{s.Key})
				}
				if err != nil {
					return Bundle{}, err
				}
				n, err := add(group, pointer, raw, links)
				if err != nil {
					return Bundle{}, err
				}
				if group == "shots" {
					var shot Shot
					_ = json.Unmarshal(raw, &shot)
					if i == 0 && lastVisual != nil {
						if lastVisual.Scope == nil || lastVisual.Scope.EndMS != shot.StartMS {
							return Bundle{}, ErrInvalid
						}
						b.Boundaries = append(b.Boundaries, ShotBoundary{Left: lastVisual.Key, Right: n.Key, AtMS: shot.StartMS, LeftContinues: lastContinues, RightContinues: shot.ContinuesBefore, State: "unreviewed"})
					}
					copyNode := n
					lastVisual = &copyNode
					lastContinues = shot.ContinuesAfter
					var children map[string]json.RawMessage
					_ = json.Unmarshal(raw, &children)
					for _, field := range []string{"beats", "visible_text"} {
						var items []json.RawMessage
						_ = json.Unmarshal(children[field], &items)
						for j, child := range items {
							if _, err = add(field, fmt.Sprintf("%s/%s/%d", pointer, field, j), child, map[string][]string{"shot": {n.Key}}); err != nil {
								return Bundle{}, err
							}
						}
					}
				}
			}
		}
	}
	for _, n := range b.Nodes {
		for _, ids := range n.Links {
			for _, id := range ids {
				if !seen[id] {
					return Bundle{}, ErrInvalid
				}
			}
		}
	}
	b.SHA256 = canonicalHash(b)
	encoded, err := json.Marshal(b)
	if err != nil {
		return Bundle{}, err
	}
	if len(encoded) > MaxBundleBytes {
		return Bundle{}, ErrBundleTooLarge
	}
	return b, nil
}

func evidenceKey(task, step, kind, pointer string) string {
	return "obs-" + hashBytes([]byte(task+"\x00"+step+"\x00"+kind+"\x00"+pointer))
}
func canonicalJSON(raw []byte) (json.RawMessage, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// Bundle reads immutable terminal rows in plan order. No partial packet is
// returned, and an already stored success flag never substitutes native audit.
func (s PGStore) Bundle(ctx context.Context, org, owner, id string) (Bundle, error) {
	task, err := s.Task(ctx, org, owner, id)
	if err != nil {
		return Bundle{}, err
	}
	rows := make([]StepRecord, 0, len(task.Plan.Steps))
	for _, step := range task.Plan.Steps {
		r, err := s.Load(ctx, org, owner, id, step.Key)
		if err != nil {
			return Bundle{}, err
		}
		rows = append(rows, r)
	}
	return Assemble(task, rows)
}
