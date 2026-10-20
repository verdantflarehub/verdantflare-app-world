package analysis

// LearningMaterial is an internal, fully checked snapshot for deterministic
// document assembly. It is not a saved asset or a public request body.
type LearningMaterial struct {
	Bundle      Bundle         `json:"bundle"`
	Association Association    `json:"association"`
	Prompts     []PromptDraft  `json:"prompts"`
	Results     []ResultDraft  `json:"results"`
	Calls       []LearningCall `json:"calls"`
}

// LearningCall retains the original native identity without copying account
// configuration, credentials, or the repeated full-context request payload.
type LearningCall struct {
	Stage       string `json:"stage"`
	CallID      string `json:"call_id"`
	ModelID     string `json:"model_id"`
	RequestHash string `json:"request_sha256"`
	ResultHash  string `json:"native_result_sha256"`
}

// AssembleLearningMaterial requires a bundle revalidated from stored native
// observations. The caller must authorize the pinned source before calling it.
// Every configured batch is checked against its full plan, not just row count.
func AssembleLearningMaterial(t Task, b Bundle, ar AssociationRecord, prompts []PromptRecord, results []ResultRecord) (LearningMaterial, error) {
	out := LearningMaterial{Bundle: b, Prompts: []PromptDraft{}, Results: []ResultDraft{}, Calls: []LearningCall{}}
	if !t.IncludeTypedResults || t.TaskID != b.TaskID || t.DraftID != b.DraftID || t.RevisionID != b.RevisionID || t.FileID != b.FileID || t.Plan.Source != b.Source || ar.Validate() != nil || ar.OrganizationID != t.OrganizationID || ar.OwnerID != t.OwnerID || ar.Request.TaskID != t.TaskID {
		return LearningMaterial{}, ErrInvalid
	}
	if ar.State != "association_ready" {
		return LearningMaterial{}, ErrObservationsIncomplete
	}
	a, err := VerifyAssociationNative(b, ar.Request, ar.CallID, ar.Result)
	if err != nil {
		return LearningMaterial{}, err
	}
	out.Association = a
	out.Calls = append(out.Calls, LearningCall{AssociationStepKey, ar.CallID, ar.Request.ModelID, ar.RequestHash, hashBytes(ar.Result)})
	seenCalls := map[string]bool{ar.CallID: true}
	for _, r := range b.Receipts {
		if seenCalls[r.CallID] {
			return LearningMaterial{}, ErrInvalid
		}
		seenCalls[r.CallID] = true
	}
	plan := []PromptBatch{}
	if len(t.PromptTargets) > 0 {
		plan, err = PlanPrompts(b, a, AllPromptScopes(a), t.PromptTargets)
		if err != nil {
			return LearningMaterial{}, err
		}
	}
	if len(prompts) != len(plan) {
		return LearningMaterial{}, ErrObservationsIncomplete
	}
	for i, r := range prompts {
		if r.Validate() != nil || r.OrganizationID != t.OrganizationID || r.OwnerID != t.OwnerID || canonicalHash(r.Request.Batch) != canonicalHash(plan[i]) || r.Request.ModelID != ar.Request.ModelID || r.Request.AccountID != ar.Request.AccountID || r.Request.BudgetLimit != ar.Request.BudgetLimit || seenCalls[r.CallID] {
			return LearningMaterial{}, ErrInvalid
		}
		if r.State != "prompt_ready" {
			return LearningMaterial{}, ErrObservationsIncomplete
		}
		drafts, err := PromptDraftsFromNative(b, a, r.Request, r.CallID, r.Result)
		if err != nil {
			return LearningMaterial{}, err
		}
		seenCalls[r.CallID] = true
		out.Prompts = append(out.Prompts, drafts...)
		out.Calls = append(out.Calls, LearningCall{r.Request.Batch.Key, r.CallID, r.Request.ModelID, r.RequestHash, hashBytes(r.Result)})
	}
	resultPlan, err := PlanResults(b, a)
	if err != nil {
		return LearningMaterial{}, err
	}
	if len(results) != len(resultPlan) {
		return LearningMaterial{}, ErrObservationsIncomplete
	}
	for i, r := range results {
		if r.Validate() != nil || r.OrganizationID != t.OrganizationID || r.OwnerID != t.OwnerID || canonicalHash(r.Request.Batch) != canonicalHash(resultPlan[i]) || r.Request.ModelID != ar.Request.ModelID || r.Request.AccountID != ar.Request.AccountID || r.Request.BudgetLimit != ar.Request.BudgetLimit || seenCalls[r.CallID] {
			return LearningMaterial{}, ErrInvalid
		}
		if r.State != "result_ready" {
			return LearningMaterial{}, ErrObservationsIncomplete
		}
		drafts, err := ResultDraftsFromNative(b, a, r.Request, r.CallID, r.Result)
		if err != nil {
			return LearningMaterial{}, err
		}
		seenCalls[r.CallID] = true
		out.Results = append(out.Results, drafts...)
		out.Calls = append(out.Calls, LearningCall{r.Request.Batch.Key, r.CallID, r.Request.ModelID, r.RequestHash, hashBytes(r.Result)})
	}
	return out, nil
}
