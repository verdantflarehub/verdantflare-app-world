package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

// Review reads only the fixed, published asset. It never opens its source project.
func (s *Service) buildReviewTarget(ctx context.Context, p Principal, l ReviewLocator) (ReviewTarget, error) {
	t := ReviewTarget{SchemaVersion: 1, Locator: l, Evidence: []ReviewEvidence{}, Dependencies: []ReviewDependency{}, Coverage: ReviewCoverage{CameraIDs: []string{}, RegionIDs: []string{}, Cameras: []ReviewCamera{}}, Requirements: []ReviewRequirement{}}
	got, e := s.WorldGet(ctx, p, WorldGetRequest{AssetID: l.AssetID, VersionID: l.VersionID})
	if e != nil {
		return t, e
	}
	m := got.Manifest
	if m.AssetType != "scene" {
		return t, ErrInvalid
	}
	a := Access{AssetID: l.AssetID, AssetVersionID: l.VersionID}
	meta, e := s.reviewVerify(ctx, p, got.ManifestRef, a)
	if e != nil {
		return t, e
	}
	t.Source = ReviewFileSource{got.ManifestRef, meta.SHA256, meta.Size}
	// The source-project locator and unpublished source records are
	// deliberately not copied into the World production definition.
	t.Definition = map[string]any{"asset_id": m.AssetID, "asset_type": m.AssetType, "name": m.Name, "subjects": m.Subjects}
	productionFiles := []File{}
	for _, f := range m.Files {
		if f.ID == m.Source.ReviewFileID || f.Role == "set-review" || f.Role == "set-review-index" {
			continue
		}
		productionFiles = append(productionFiles, f)
		v, e := s.reviewVerify(ctx, p, f.Content, a)
		if e != nil {
			return t, e
		}
		role := f.Role
		switch role {
		case "source_engineering", "space_record", "lighting_record", "camera_reference", "render", "photograph", "generated", "composite", "set_package":
		default:
			role = "supporting"
		}
		t.Evidence = append(t.Evidence, ReviewEvidence{ID: f.ID, FileID: f.ID, ContentRef: f.Content, SHA256: v.SHA256, Size: v.Size, MIME: v.MIME, Role: role, CameraIDs: []string{}, RegionIDs: []string{}})
	}
	t.Definition["files"] = productionFiles
	if m.DomainDocuments != nil {
		t.Definition["domain_documents"] = *m.DomainDocuments
	}
	if len(m.Extensions) > 0 {
		t.Definition["extensions"] = m.Extensions
	}
	for _, d := range m.DependsOn {
		if _, e := assetRole(ctx, s.db, p, d.AssetID); e != nil {
			return t, e
		}
		_, ref, e := assetVersion(ctx, s.db, p.OrganizationID, d.AssetID, d.VersionID)
		if e != nil {
			return t, e
		}
		v, e := s.reviewVerify(ctx, p, ref, Access{AssetID: d.AssetID, AssetVersionID: d.VersionID})
		if e != nil {
			return t, e
		}
		sha := v.SHA256
		t.Dependencies = append(t.Dependencies, ReviewDependency{d.AssetID, d.VersionID, d.Purpose, &ref, &sha})
	}
	sort.Slice(t.Evidence, func(i, j int) bool { return t.Evidence[i].ID < t.Evidence[j].ID })
	sort.Slice(t.Dependencies, func(i, j int) bool {
		a, b := t.Dependencies[i], t.Dependencies[j]
		if a.AssetID != b.AssetID {
			return a.AssetID < b.AssetID
		}
		if a.VersionID != b.VersionID {
			return a.VersionID < b.VersionID
		}
		return a.Purpose < b.Purpose
	})
	if e = s.loadReviewMapping(ctx, p, &t); e != nil {
		return t, e
	}
	if e = s.verifyReviewCameras(ctx, p, &t); e != nil {
		return t, e
	}
	// File presence and producer declarations do not establish a recovered scene.
	for _, claim := range []string{"full_set", "source_recovery", "space_layout"} {
		ids := []string{}
		for _, v := range t.Evidence {
			if claim == "full_set" || v.Role == "set_package" || claim == "source_recovery" && v.Role == "source_engineering" || claim == "space_layout" && v.Role == "space_record" {
				ids = append(ids, v.ID)
			}
		}
		state, reason := "missing", "required_evidence_missing"
		if len(ids) > 0 && claim != "full_set" {
			state, reason = "unverified", "recovery_not_verified"
		}
		if claim == "full_set" {
			for _, v := range t.Evidence {
				if v.Role == "set_package" {
					state, reason = "unverified", "recovery_not_verified"
				}
			}
		}
		t.Requirements = append(t.Requirements, ReviewRequirement{claim, claim, ids, state, reason})
	}
	sort.Slice(t.Requirements, func(i, j int) bool { return t.Requirements[i].ID < t.Requirements[j].ID })
	return t, nil
}

func (s *Service) reviewVerify(ctx context.Context, p Principal, ref ContentRef, a Access) (ContentVersion, error) {
	v, e := s.content.Metadata(ctx, p, ref, a)
	if e != nil {
		return v, e
	}
	if v.OrganizationID != p.OrganizationID || v.ContentRef != ref || !reviewSHA(v.SHA256) || v.Size < 0 || v.Size > 1<<50 {
		return v, ErrNotReady
	}
	if stream, ok := s.content.(interface {
		Download(context.Context, Principal, ContentRef, Access, io.Writer, int64) error
	}); ok {
		h := sha256.New()
		counter := &reviewCountWriter{Writer: h}
		e = stream.Download(ctx, p, ref, a, counter, v.Size)
		if e == nil && (counter.Count != v.Size || hex.EncodeToString(h.Sum(nil)) != v.SHA256) {
			e = ErrNotReady
		}
	} else if v.Size <= MaxManifestBytes {
		var b []byte
		b, e = s.content.Read(ctx, p, ref, a, MaxManifestBytes)
		if e == nil && (int64(len(b)) != v.Size || reviewDigest(b) != v.SHA256) {
			e = ErrNotReady
		}
	} else {
		e = ErrNotReady
	}
	return v, e
}

type reviewCountWriter struct {
	io.Writer
	Count int64
}

func (w *reviewCountWriter) Write(b []byte) (int, error) {
	n, e := w.Writer.Write(b)
	w.Count += int64(n)
	return n, e
}

func (s *Service) reviewProjection(ctx context.Context, p Principal, t ReviewTarget) (ReviewTarget, []ReviewAvailability, error) {
	raw, _ := json.Marshal(t)
	var view ReviewTarget
	_ = json.Unmarshal(raw, &view)
	availability := []ReviewAvailability{}
	source, e := s.reviewVerify(ctx, p, t.Source.ContentRef, Access{AssetID: t.Locator.AssetID, AssetVersionID: t.Locator.VersionID})
	if e != nil {
		availability = append(availability, ReviewAvailability{Code: "source_unavailable"})
		view.Source = ReviewFileSource{}
	} else if source.SHA256 != t.Source.SHA256 || source.Size != t.Source.Size {
		return view, nil, ErrTargetMismatch
	}
	view.Dependencies = []ReviewDependency{}
	for _, d := range t.Dependencies {
		_, e := assetRole(ctx, s.db, p, d.AssetID)
		if errors.Is(e, ErrForbidden) {
			availability = append(availability, ReviewAvailability{Code: "restricted_dependency"})
			continue
		}
		if e != nil {
			return view, nil, e
		}
		if d.ManifestRef != nil && d.SHA256 != nil {
			v, e := s.reviewVerify(ctx, p, *d.ManifestRef, Access{AssetID: d.AssetID, AssetVersionID: d.VersionID})
			if e != nil {
				availability = append(availability, ReviewAvailability{Code: "dependency_unavailable"})
				continue
			}
			if v.SHA256 != *d.SHA256 {
				return view, nil, ErrTargetMismatch
			}
		}
		view.Dependencies = append(view.Dependencies, d)
	}
	view.Evidence = []ReviewEvidence{}
	for _, v := range t.Evidence {
		m, e := s.reviewVerify(ctx, p, v.ContentRef, Access{AssetID: t.Locator.AssetID, AssetVersionID: t.Locator.VersionID})
		if e != nil {
			code := "unavailable"
			if errors.Is(e, ErrForbidden) {
				code = "restricted"
			}
			if errors.Is(e, ErrNotFound) {
				code = "deleted"
			}
			id := v.ID
			if code == "restricted" {
				id = ""
			}
			availability = append(availability, ReviewAvailability{code, id})
			continue
		}
		if m.SHA256 != v.SHA256 || m.Size != v.Size || m.MIME != v.MIME {
			return view, nil, ErrTargetMismatch
		}
		view.Evidence = append(view.Evidence, v)
	}
	if len(availability) > 0 {
		// Requirements may identify hidden evidence; the readable projection carries
		// only availability codes. The frozen canonical target remains unchanged.
		view.Requirements = []ReviewRequirement{}
		view.Definition = map[string]any{}
		view.Coverage = ReviewCoverage{CameraIDs: []string{}, RegionIDs: []string{}, Cameras: []ReviewCamera{}}
	}
	return view, availability, nil
}
