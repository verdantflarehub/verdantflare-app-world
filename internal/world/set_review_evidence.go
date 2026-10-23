package world

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

type reviewEvidenceMapping struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Coverage      struct {
		CameraIDs    []string `json:"camera_ids,omitempty"`
		RegionIDs    []string `json:"region_ids,omitempty"`
		SpaceBasisID *string  `json:"space_basis_file_id,omitempty"`
		Cameras      []struct {
			ID           string   `json:"camera_id"`
			ParametersID string   `json:"parameters_file_id"`
			EvidenceIDs  []string `json:"evidence_file_ids"`
			RegionIDs    []string `json:"region_ids"`
		} `json:"cameras,omitempty"`
	} `json:"coverage"`
	Evidence []struct {
		FileID     string            `json:"file_id"`
		Role       string            `json:"role"`
		CameraIDs  []string          `json:"camera_ids"`
		RegionIDs  []string          `json:"region_ids"`
		FrameRange *ReviewFrameRange `json:"frame_range"`
	} `json:"evidence,omitempty"`
	Checks []struct {
		Claim       string   `json:"claim"`
		EvidenceIDs []string `json:"evidence_file_ids"`
	} `json:"checks,omitempty"`
}

func reviewSubset(items, all []string) bool {
	for _, v := range items {
		if !containsReview(all, v) {
			return false
		}
	}
	return true
}
func reviewRole(role string) bool {
	switch role {
	case "source_engineering", "space_record", "lighting_record", "camera_reference", "render", "photograph", "generated", "composite", "set_package", "supporting":
		return true
	}
	return false
}
func applyReviewMapping(t *ReviewTarget, b []byte) error {
	var m reviewEvidenceMapping
	m.Coverage.CameraIDs = []string{}
	m.Coverage.RegionIDs = []string{}
	if decodeReview(b, &m) != nil || m.SchemaVersion != 1 || m.Kind != "set-review-evidence" || !reviewIDs(m.Coverage.CameraIDs) || !reviewIDs(m.Coverage.RegionIDs) {
		return ErrInvalid
	}
	byID := map[string]*ReviewEvidence{}
	for i := range t.Evidence {
		byID[t.Evidence[i].ID] = &t.Evidence[i]
	}
	seen := map[string]bool{}
	for _, v := range m.Evidence {
		e, ok := byID[v.FileID]
		if !ok || seen[v.FileID] || !reviewRole(v.Role) || !reviewIDs(v.CameraIDs) || !reviewIDs(v.RegionIDs) || !reviewSubset(v.CameraIDs, m.Coverage.CameraIDs) || !reviewSubset(v.RegionIDs, m.Coverage.RegionIDs) {
			return ErrInvalid
		}
		seen[v.FileID] = true
		if v.FrameRange != nil {
			r := v.FrameRange
			if !strings.HasPrefix(e.MIME, "video/") || r.Start < 0 || r.End < r.Start || r.End > 1<<53-1 || r.FPSNumerator <= 0 || r.FPSNumerator > 1<<53-1 || r.FPSDenominator <= 0 || r.FPSDenominator > 1<<53-1 {
				return ErrInvalid
			}
		}
		sort.Strings(v.CameraIDs)
		e.Role, e.CameraIDs, e.FrameRange = v.Role, v.CameraIDs, v.FrameRange
		sort.Strings(v.RegionIDs)
		e.RegionIDs = v.RegionIDs
	}
	sort.Strings(m.Coverage.CameraIDs)
	sort.Strings(m.Coverage.RegionIDs)
	t.Coverage.CameraIDs, t.Coverage.RegionIDs = m.Coverage.CameraIDs, m.Coverage.RegionIDs
	if m.Coverage.SpaceBasisID != nil {
		e, ok := byID[*m.Coverage.SpaceBasisID]
		if !ok {
			return ErrInvalid
		}
		t.Coverage.SpaceBasis = &ReviewSpaceBasis{e.ID, e.SHA256}
	}
	seen = map[string]bool{}
	for _, c := range m.Coverage.Cameras {
		parameter, ok := byID[c.ParametersID]
		if !ok || seen[c.ID] || !containsReview(m.Coverage.CameraIDs, c.ID) || !reviewIDs(c.EvidenceIDs) || !reviewIDs(c.RegionIDs) || !reviewSubset(c.RegionIDs, m.Coverage.RegionIDs) {
			return ErrInvalid
		}
		seen[c.ID] = true
		for _, id := range c.EvidenceIDs {
			e, ok := byID[id]
			if !ok || !containsReview(e.CameraIDs, c.ID) {
				return ErrInvalid
			}
		}
		sort.Strings(c.EvidenceIDs)
		sort.Strings(c.RegionIDs)
		// Presence alone does not establish correspondence. The supported parameter
		// validator runs separately against actual fixed parameter bytes.
		t.Coverage.Cameras = append(t.Coverage.Cameras, ReviewCamera{c.ID, parameter.ID, parameter.SHA256, c.EvidenceIDs, c.RegionIDs, false})
	}
	sort.Slice(t.Coverage.Cameras, func(i, j int) bool { return t.Coverage.Cameras[i].CameraID < t.Coverage.Cameras[j].CameraID })
	seen = map[string]bool{}
	for _, c := range m.Checks {
		if seen[c.Claim] || !reviewIDs(c.EvidenceIDs) || len(c.EvidenceIDs) == 0 {
			return ErrInvalid
		}
		seen[c.Claim] = true
		switch c.Claim {
		case "image_reading", "space_layout", "source_recovery", "new_camera", "full_set":
		default:
			return ErrInvalid
		}
		for _, id := range c.EvidenceIDs {
			if _, ok := byID[id]; !ok {
				return ErrInvalid
			}
		}
		sort.Strings(c.EvidenceIDs)
		t.Requirements = append(t.Requirements, ReviewRequirement{ID: "declared_" + c.Claim, Claim: c.Claim, EvidenceIDs: c.EvidenceIDs, State: "unverified", Reason: "technical_report_not_verified"})
	}
	return nil
}

func (s *Service) loadReviewMapping(ctx context.Context, p Principal, t *ReviewTarget) error {
	found := false
	for _, f := range t.Evidence {
		if f.Role != "space_record" || f.Size > maxReviewRequest {
			continue
		}
		b, e := s.content.Read(ctx, p, f.ContentRef, Access{AssetID: t.Locator.AssetID, AssetVersionID: t.Locator.VersionID}, maxReviewRequest)
		if e != nil {
			return e
		}
		if int64(len(b)) != f.Size || reviewDigest(b) != f.SHA256 {
			return ErrNotReady
		}
		var marker struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(b, &marker) != nil || marker.Kind != "set-review-evidence" {
			continue
		}
		if found {
			return ErrInvalid
		}
		if e = applyReviewMapping(t, b); e != nil {
			return e
		}
		found = true
	}
	return nil
}
