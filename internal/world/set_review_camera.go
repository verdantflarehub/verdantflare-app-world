package world

import (
	"context"
	"math"
	"strings"
)

type reviewCameraParameters struct {
	SchemaVersion    int    `json:"schema_version"`
	Kind             string `json:"kind"`
	SpaceBasisSHA256 string `json:"space_basis_sha256"`
	CoordinateSystem string `json:"coordinate_system"`
	Camera           struct {
		Projection   string    `json:"projection"`
		Location     []float64 `json:"location_blender_m"`
		Rotation     []float64 `json:"rotation_euler_xyz_rad"`
		Lens         float64   `json:"lens_mm"`
		SensorWidth  float64   `json:"sensor_width_mm"`
		SensorHeight float64   `json:"sensor_height_mm"`
		SensorFit    string    `json:"sensor_fit"`
		ClipStart    float64   `json:"clip_start_m"`
		ClipEnd      float64   `json:"clip_end_m"`
		OrthoScale   float64   `json:"ortho_scale"`
		ShiftX       float64   `json:"shift_x"`
		ShiftY       float64   `json:"shift_y"`
	} `json:"camera"`
	Render struct {
		Width        int64   `json:"resolution_x"`
		Height       int64   `json:"resolution_y"`
		PixelAspectX float64 `json:"pixel_aspect_x"`
		PixelAspectY float64 `json:"pixel_aspect_y"`
		Frame        int64   `json:"frame"`
	} `json:"render"`
}

// Camera verification establishes only fixed-file correspondence for comparison.
// It never changes the recovery requirements or represents a human conclusion.
func validateReviewCamera(data []byte, basis string) bool {
	var p reviewCameraParameters
	if decodeReview(data, &p) != nil || p.SchemaVersion != 1 || p.Kind != "set-review-camera" || !reviewSHA(basis) || p.SpaceBasisSHA256 != basis || p.CoordinateSystem != "blender_z_up_m" {
		return false
	}
	if _, _, e := reviewHash(p); e != nil {
		return false
	}
	c, r := p.Camera, p.Render
	if (c.Projection != "PERSP" && c.Projection != "ORTHO") || (c.SensorFit != "AUTO" && c.SensorFit != "HORIZONTAL" && c.SensorFit != "VERTICAL") || len(c.Location) != 3 || len(c.Rotation) != 3 || c.ClipEnd <= c.ClipStart {
		return false
	}
	for _, f := range []float64{c.Lens, c.SensorWidth, c.SensorHeight, c.ClipStart, c.ClipEnd, c.OrthoScale, r.PixelAspectX, r.PixelAspectY} {
		if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return r.Width > 0 && r.Width <= 1<<53-1 && r.Height > 0 && r.Height <= 1<<53-1 && r.Frame >= -(1<<53-1) && r.Frame <= 1<<53-1
}

func (s *Service) verifyReviewCameras(ctx context.Context, p Principal, t *ReviewTarget) error {
	if t.Coverage.SpaceBasis == nil {
		return nil
	}
	byID := map[string]ReviewEvidence{}
	for _, e := range t.Evidence {
		byID[e.ID] = e
	}
	basis, ok := byID[t.Coverage.SpaceBasis.EvidenceID]
	if !ok || basis.SHA256 != t.Coverage.SpaceBasis.SHA256 {
		return ErrInvalid
	}
	for i := range t.Coverage.Cameras {
		c := &t.Coverage.Cameras[i]
		c.Verified = false
		parameter, ok := byID[c.ParametersID]
		if !ok || parameter.MIME != "application/json" || parameter.SHA256 != c.ParametersSHA256 || parameter.Size > maxReviewRequest || len(c.EvidenceIDs) == 0 {
			continue
		}
		images := true
		for _, id := range c.EvidenceIDs {
			e, ok := byID[id]
			if !ok || !strings.HasPrefix(e.MIME, "image/") || e.FrameRange != nil || !containsReview(e.CameraIDs, c.CameraID) {
				images = false
			}
		}
		if !images {
			continue
		}
		b, e := s.content.Read(ctx, p, parameter.ContentRef, Access{AssetID: t.Locator.AssetID, AssetVersionID: t.Locator.VersionID}, maxReviewRequest)
		if e != nil {
			return e
		}
		if int64(len(b)) != parameter.Size || reviewDigest(b) != parameter.SHA256 {
			return ErrNotReady
		}
		c.Verified = validateReviewCamera(b, basis.SHA256)
	}
	return nil
}
