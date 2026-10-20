package analysis

import "encoding/json"

// ResultDraft preserves every disposition, including unsupported conclusions.
// It is an internal candidate, not a saved asset, engineering file or approval.
type ResultDraft struct {
	Key                  string   `json:"key"`
	Type                 string   `json:"type_key"`
	ContextUncertainties []string `json:"context_uncertainties"`
	ResultCandidate
}

func ResultDraftsFromNative(b Bundle, a Association, f ResultRequest, callID string, data []byte) ([]ResultDraft, error) {
	output, err := VerifyResultsNative(b, a, f, callID, data)
	if err != nil {
		return nil, err
	}
	context := append([]string{}, a.Uncertainties...)
	for _, n := range b.Nodes {
		if n.Kind == "uncertainties" {
			var note string
			if json.Unmarshal(n.Value, &note) != nil {
				return nil, ErrInvalid
			}
			context = append(context, note)
		}
	}
	for _, groups := range [][]AssociationGroup{a.Entities, a.Voices} {
		for _, group := range groups {
			if group.State == "inferred" {
				context = append(context, group.Key+": "+group.Reason)
			}
		}
	}
	for _, link := range a.VoiceLinks {
		context = append(context, link.Voice+" → "+link.Entity+": "+link.State+"; "+link.Reason)
	}
	for _, shot := range a.Shots {
		if shot.State == "inferred" {
			context = append(context, shot.Key+": "+shot.Reason)
		}
	}
	for _, boundary := range a.Boundaries {
		if boundary.Decision == "undetermined" {
			context = append(context, boundary.Left+" → "+boundary.Right+": "+boundary.Reason)
		}
	}
	byScope := map[string]ResultCandidate{}
	for _, item := range output.Items {
		byScope[item.Scope] = item
	}
	drafts := []ResultDraft{}
	for _, scope := range f.Batch.Scopes {
		key := "result." + canonicalHash(struct{ Plan, Type, Scope string }{f.Batch.PlanHash, f.Batch.Type, scope.Key})[:32]
		drafts = append(drafts, ResultDraft{key, f.Batch.Type, append([]string{}, context...), byScope[scope.Key]})
	}
	return drafts, nil
}
