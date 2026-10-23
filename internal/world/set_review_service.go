package world

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

type reviewHead struct {
	Target   ReviewTarget
	Hash     string
	ID       *string
	Sequence int64
}

func loadReviewHead(ctx context.Context, q queryer, p Principal, l ReviewLocator, lock bool) (reviewHead, error) {
	h := reviewHead{}
	var raw []byte
	sql := `SELECT target_sha256,target_bytes,head_record_id::text,record_count FROM studio.world_set_review_heads WHERE organization_id=$1 AND asset_id=$2 AND version_id=$3`
	if lock {
		sql += " FOR UPDATE"
	}
	e := q.QueryRow(ctx, sql, p.OrganizationID, l.AssetID, l.VersionID).Scan(&h.Hash, &raw, &h.ID, &h.Sequence)
	if errors.Is(e, pgx.ErrNoRows) {
		return h, ErrNotFound
	}
	if e != nil {
		return h, e
	}
	if reviewDigest(raw) != h.Hash || json.Unmarshal(raw, &h.Target) != nil || h.Target.Locator != l || h.Sequence < 0 || (h.Sequence == 0) != (h.ID == nil) {
		return h, ErrNotReady
	}
	return h, nil
}

type reviewQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadReviewRecords(ctx context.Context, q reviewQuerier, p Principal, l ReviewLocator, seq int64) ([]ReviewRecord, error) {
	rows, e := q.Query(ctx, `SELECT record_bytes,record_sha256,record_id::text,target_sha256 FROM studio.world_set_review_records WHERE organization_id=$1 AND asset_id=$2 AND version_id=$3 AND sequence<=$4 ORDER BY sequence`, p.OrganizationID, l.AssetID, l.VersionID, seq)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ReviewRecord{}
	var prior *string
	for rows.Next() {
		var b []byte
		var h, id, targetHash string
		var r ReviewRecord
		if e = rows.Scan(&b, &h, &id, &targetHash); e != nil {
			return nil, e
		}
		if reviewDigest(b) != h || json.Unmarshal(b, &r) != nil || !reviewSameID(prior, r.PreviousID) || r.ID != id || r.TargetHash != targetHash || r.Target.Locator != l || r.Actor.OrganizationID != p.OrganizationID || r.SchemaVersion != 1 {
			return nil, ErrNotReady
		}
		out = append(out, r)
		canonicalHash, _, e := reviewHash(r.Target)
		if e != nil || canonicalHash != targetHash {
			return nil, ErrNotReady
		}
		id = r.ID
		prior = &id
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if int64(len(out)) != seq {
		return nil, ErrNotReady
	}
	return out, nil
}
func reviewSameID(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// lockReviewAccess holds the same durable revocation barrier used by grant
// changes until the short append transaction commits. No remote work occurs here.
func lockReviewAccess(ctx context.Context, tx pgx.Tx, p Principal, t ReviewTarget) (string, error) {
	ids := []string{t.Locator.AssetID}
	for _, d := range t.Dependencies {
		ids = append(ids, d.AssetID)
	}
	sort.Strings(ids)
	role := ""
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, e := tx.Exec(ctx, `INSERT INTO studio.world_coord_barriers(organization_id,resource_id,subject_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, p.OrganizationID, id, p.SubjectID); e != nil {
			return "", e
		}
		var blocked bool
		if e := tx.QueryRow(ctx, `SELECT blocked FROM studio.world_coord_barriers WHERE organization_id=$1 AND resource_id=$2 AND subject_id=$3 FOR SHARE`, p.OrganizationID, id, p.SubjectID).Scan(&blocked); e != nil {
			return "", e
		}
		if blocked {
			return "", ErrForbidden
		}
		var member string
		e := tx.QueryRow(ctx, `SELECT role FROM studio.world_asset_grants WHERE organization_id=$1 AND asset_id=$2 AND subject_id=$3 FOR SHARE`, p.OrganizationID, id, p.SubjectID).Scan(&member)
		if errors.Is(e, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		if e != nil {
			return "", e
		}
		if id == t.Locator.AssetID {
			role = member
		}
	}
	return role, nil
}

func (s *Service) ReviewOpen(ctx context.Context, p Principal, r ReviewOpenRequest) (ReviewOpenResult, error) {
	out := ReviewOpenResult{Locator: r.Target, Records: []any{}, Decisions: []ReviewDecision{}, Availability: []ReviewAvailability{}, Missing: []ReviewAvailability{}, MappingState: "mapped"}
	if !p.Valid() {
		return out, ErrForbidden
	}
	if !r.Target.valid() || r.ExpectedHash != "" && !reviewSHA(r.ExpectedHash) || r.Cursor != "" && !ValidID(r.Cursor) {
		return out, ErrInvalid
	}
	role, e := assetRole(ctx, s.db, p, r.Target.AssetID)
	if e != nil {
		return out, e
	}
	h, e := loadReviewHead(ctx, s.db, p, r.Target, false)
	if errors.Is(e, ErrNotFound) {
		if r.Cursor != "" {
			return out, ErrInvalid
		}
		h.Target, e = s.buildReviewTarget(ctx, p, r.Target)
		if e != nil {
			if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotReady) || errors.Is(e, ErrDependency) {
				if _, err := assetRole(ctx, s.db, p, r.Target.AssetID); err != nil {
					return out, err
				}
				out.View = ReviewTarget{SchemaVersion: 1, Locator: r.Target, Definition: map[string]any{}, Evidence: []ReviewEvidence{}, Dependencies: []ReviewDependency{}, Coverage: ReviewCoverage{CameraIDs: []string{}, RegionIDs: []string{}, Cameras: []ReviewCamera{}}, Requirements: []ReviewRequirement{}}
				out.State = "unreviewed"
				out.Capabilities.ReasonCodes = []string{"target_incomplete"}
				out.Availability = []ReviewAvailability{{Code: "target_incomplete"}}
				out.Missing = out.Availability
				return out, nil
			}
			return out, e
		}
		h.Hash, _, e = reviewHash(h.Target)
	}
	if e != nil {
		return out, e
	}
	if r.ExpectedHash != "" && r.ExpectedHash != h.Hash {
		return out, ErrTargetMismatch
	}
	offset := 0
	if r.Cursor != "" {
		var hash string
		var seq int64
		var head *string
		e = s.db.QueryRow(ctx, `SELECT target_sha256,snapshot_sequence,snapshot_head::text,page_offset FROM studio.world_set_review_cursors WHERE cursor_id=$1 AND organization_id=$2 AND asset_id=$3 AND version_id=$4 AND subject_id=$5 AND expires_at>now()`, r.Cursor, p.OrganizationID, r.Target.AssetID, r.Target.VersionID, p.SubjectID).Scan(&hash, &seq, &head, &offset)
		if errors.Is(e, pgx.ErrNoRows) {
			return out, ErrInvalid
		}
		if e != nil {
			return out, e
		}
		if hash != h.Hash || seq > h.Sequence {
			return out, ErrInvalid
		}
		h.Sequence, h.ID = seq, head
	}
	all, e := loadReviewRecords(ctx, s.db, p, r.Target, h.Sequence)
	if e != nil {
		return out, e
	}
	view, availability, e := s.reviewProjection(ctx, p, h.Target)
	if e != nil {
		return out, e
	}
	out.View, out.Availability, out.TargetHash, out.Head = view, availability, h.Hash, h.ID
	out.State, out.Decisions = reviewAggregate(h.Target, all)
	out.Capabilities = ReviewCapabilities{CanComment: role == "owner" || role == "editor", CanDecide: (role == "owner" || role == "editor") && reviewOrigin(p) == "human_interactive", ReasonCodes: []string{}}
	if len(availability) == 0 {
		out.Manifest = &h.Target
	} else {
		out.Capabilities.CanComment = false
		out.Capabilities.CanDecide = false
		out.Capabilities.ReasonCodes = append(out.Capabilities.ReasonCodes, "restricted_or_unavailable_evidence")
	}
	if !out.Capabilities.CanDecide && reviewOrigin(p) != "human_interactive" {
		out.Capabilities.ReasonCodes = append(out.Capabilities.ReasonCodes, "agent_advisory_only")
	}
	for _, v := range h.Target.Requirements {
		if v.State != "satisfied" {
			out.Missing = append(out.Missing, ReviewAvailability{Code: v.Reason})
		}
	}
	if offset > len(all) {
		return out, ErrInvalid
	}
	end := offset + 50
	if end > len(all) {
		end = len(all)
	}
	for _, record := range all[offset:end] {
		out.Records = append(out.Records, record)
	}
	if len(availability) > 0 {
		out.Records = []any{}
		out.Decisions = []ReviewDecision{}
		for _, record := range all[offset:end] {
			out.Records = append(out.Records, ReviewRecordView{ID: record.ID, Visibility: "restricted"})
		}
	}
	if end < len(all) {
		out.NextCursor = newID()
		_, e = s.db.Exec(ctx, `INSERT INTO studio.world_set_review_cursors(cursor_id,organization_id,asset_id,version_id,subject_id,target_sha256,snapshot_sequence,snapshot_head,page_offset,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now()+interval '24 hours')`, out.NextCursor, p.OrganizationID, r.Target.AssetID, r.Target.VersionID, p.SubjectID, h.Hash, h.Sequence, h.ID, end)
		if e != nil {
			return out, e
		}
	}
	role, e = s.reviewCurrentAccess(ctx, p, view)
	if e != nil {
		return ReviewOpenResult{}, e
	}
	if role != "owner" && role != "editor" {
		out.Capabilities.CanComment = false
		out.Capabilities.CanDecide = false
	}
	return out, nil
}

func reviewRequestHash(_ Principal, r ReviewAppendRequest) (string, error) {
	payload := r.input
	if len(payload) == 0 {
		payload, _ = json.Marshal(r)
	}
	h, _, e := reviewHash(payload)
	return h, e
}
func loadReviewRequest(ctx context.Context, q queryer, p Principal, asset, commit string) (ReviewStatusResult, string, error) {
	var out ReviewStatusResult
	var actor string
	var raw []byte
	out.target = ReviewLocator{Kind: "world", AssetID: asset}
	e := q.QueryRow(ctx, `SELECT created_by::text,request_sha256,state,result,COALESCE(error_code,''),version_id::text,origin FROM studio.world_set_review_requests WHERE organization_id=$1 AND asset_id=$2 AND commit_id=$3`, p.OrganizationID, asset, commit).Scan(&actor, &out.RequestHash, &out.State, &raw, &out.ErrorCode, &out.target.VersionID, &out.origin)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, actor, ErrNotFound
	}
	if e != nil {
		return out, actor, e
	}
	if len(raw) > 0 {
		out.Result = &ReviewAppendResult{}
		if json.Unmarshal(raw, out.Result) != nil {
			return out, actor, ErrNotReady
		}
	}
	if out.State == "conflict" {
		out.State = "rejected"
	}
	return out, actor, nil
}
func reviewError(code string) error {
	for _, e := range []error{ErrReviewConflict, ErrTargetMismatch, ErrReviewScope, ErrInvalid, ErrForbidden, ErrIdempotency} {
		if e.Error() == code {
			return e
		}
	}
	return ErrNotReady
}
func (s *Service) ReviewAppend(ctx context.Context, p Principal, r ReviewAppendRequest) (ReviewAppendResult, error) {
	out := ReviewAppendResult{}
	if e := validateReviewAppend(r, p); e != nil {
		return out, e
	}
	if len(r.input) == 0 {
		r.input, _ = json.Marshal(r)
	}
	var e error
	r.Scope, e = normalizeScope(r.Scope)
	if e != nil {
		return out, e
	}
	r.TechnicalRefs = append([]string{}, r.TechnicalRefs...)
	sort.Strings(r.TechnicalRefs)
	if e = assetWritable(ctx, s.db, p, r.Target.AssetID); e != nil {
		return out, e
	}
	fingerprint, e := reviewRequestHash(p, r)
	if e != nil {
		return out, e
	}
	// Resolve and verify actual immutable content before taking any database locks.
	h, e := loadReviewHead(ctx, s.db, p, r.Target, false)
	if errors.Is(e, ErrNotFound) {
		h.Target, e = s.buildReviewTarget(ctx, p, r.Target)
		if e == nil {
			h.Hash, _, e = reviewHash(h.Target)
		}
	}
	if e != nil {
		return out, e
	}
	_, availability, e := s.reviewProjection(ctx, p, h.Target)
	if e != nil {
		return out, e
	}
	if len(availability) > 0 {
		return out, ErrForbidden
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(context.Background())
	role, e := lockReviewAccess(ctx, tx, p, h.Target)
	if e != nil {
		return out, e
	}
	if role != "owner" && role != "editor" {
		return out, ErrForbidden
	}
	if e = advisory(ctx, tx, "world-commit:"+p.OrganizationID+":"+r.Target.AssetID+":"+r.CommitID); e != nil {
		return out, e
	}
	cached, actor, e := loadReviewRequest(ctx, tx, p, r.Target.AssetID, r.CommitID)
	if e == nil {
		if actor != p.SubjectID {
			return out, ErrForbidden
		}
		if cached.RequestHash != fingerprint || cached.origin != reviewOrigin(p) {
			return out, ErrIdempotency
		}
		if cached.Result != nil {
			return *cached.Result, nil
		}
		return out, reviewError(cached.ErrorCode)
	}
	if !errors.Is(e, ErrNotFound) {
		return out, e
	}
	var collision bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM studio.world_commits WHERE organization_id=$1 AND asset_id=$2 AND commit_id=$3)`, p.OrganizationID, r.Target.AssetID, r.CommitID).Scan(&collision); e != nil {
		return out, e
	}
	if collision {
		return out, ErrIdempotency
	}
	if e = advisory(ctx, tx, "world-review:"+p.OrganizationID+":"+r.Target.AssetID+":"+r.Target.VersionID); e != nil {
		return out, e
	}
	saved, e := loadReviewHead(ctx, tx, p, r.Target, true)
	if e == nil {
		if saved.Hash != h.Hash {
			return out, ErrTargetMismatch
		}
		h = saved
	} else if !errors.Is(e, ErrNotFound) {
		return out, e
	}
	finish := func(result *ReviewAppendResult, cause error) (ReviewAppendResult, error) {
		state, code := "committed", any(nil)
		var b any
		if cause != nil {
			state = "conflict"
			code = cause.Error()
		} else {
			raw, _ := json.Marshal(result)
			b = raw
		}
		_, err := tx.Exec(ctx, `INSERT INTO studio.world_set_review_requests(organization_id,asset_id,commit_id,version_id,created_by,origin,request_sha256,state,result,error_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, p.OrganizationID, r.Target.AssetID, r.CommitID, r.Target.VersionID, p.SubjectID, reviewOrigin(p), fingerprint, state, b, code)
		if err != nil {
			return out, err
		}
		if err = tx.Commit(ctx); err != nil {
			return out, err
		}
		if result != nil {
			return *result, nil
		}
		return out, cause
	}
	if r.ExpectedHash != h.Hash {
		return finish(nil, ErrTargetMismatch)
	}
	if !reviewSameID(r.ExpectedRecordID, h.ID) {
		return finish(nil, ErrReviewConflict)
	}
	if e = validateReviewScope(h.Target, r.Scope, r.Action); e != nil {
		return finish(nil, e)
	}
	for _, ref := range r.TechnicalRefs {
		found := false
		for _, v := range h.Target.Evidence {
			if v.ID == ref {
				found = true
			}
		}
		if !found {
			return finish(nil, ErrInvalid)
		}
	}
	records, e := loadReviewRecords(ctx, tx, p, r.Target, h.Sequence)
	if e != nil {
		return out, e
	}
	if e = validateReviewDecision(records, r); e != nil {
		return finish(nil, e)
	}
	record := ReviewRecord{1, newID(), h.Hash, h.Target, h.ID, r.Action, r.Scope, r.Body, ReviewActor{p.SubjectID, p.OrganizationID, reviewOrigin(p)}, time.Now().UTC(), r.Supersedes, r.TechnicalRefs}
	rh, rb, e := reviewHash(record)
	if e != nil {
		return out, e
	}
	_, tb, e := reviewHash(h.Target)
	if e != nil {
		return out, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO studio.world_set_review_heads(organization_id,asset_id,version_id,target_sha256,target_bytes) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, p.OrganizationID, r.Target.AssetID, r.Target.VersionID, h.Hash, tb); e != nil {
		return out, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO studio.world_set_review_records(organization_id,asset_id,version_id,record_id,sequence,target_sha256,previous_record_id,record_sha256,record_bytes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.OrganizationID, r.Target.AssetID, r.Target.VersionID, record.ID, h.Sequence+1, h.Hash, h.ID, rh, rb); e != nil {
		return out, e
	}
	tag, e := tx.Exec(ctx, `UPDATE studio.world_set_review_heads SET head_record_id=$4,record_count=record_count+1 WHERE organization_id=$1 AND asset_id=$2 AND version_id=$3 AND head_record_id IS NOT DISTINCT FROM $5::uuid`, p.OrganizationID, r.Target.AssetID, r.Target.VersionID, record.ID, h.ID)
	if e != nil {
		return out, e
	}
	if tag.RowsAffected() != 1 {
		return out, ErrReviewConflict
	}
	state, _ := reviewAggregate(h.Target, append(records, record))
	out = ReviewAppendResult{"committed", r.CommitID, record.ID, h.Hash, state, record.ID}
	if e = audit(ctx, tx, p, "world.review_append", record.ID); e != nil {
		return out, e
	}
	return finish(&out, nil)
}

func (s *Service) ReviewStatus(ctx context.Context, p Principal, r ReviewStatusRequest) (ReviewStatusResult, error) {
	out := ReviewStatusResult{}
	if !p.Valid() {
		return out, ErrForbidden
	}
	if !ValidID(r.AssetID) || !ValidID(r.CommitID) {
		return out, ErrInvalid
	}
	role, e := assetRole(ctx, s.db, p, r.AssetID)
	if e != nil {
		return out, e
	}
	out, actor, e := loadReviewRequest(ctx, s.db, p, r.AssetID, r.CommitID)
	if e != nil {
		return out, e
	}
	if actor != p.SubjectID && role != "owner" {
		return ReviewStatusResult{}, ErrForbidden
	}
	m, _, e := assetVersion(ctx, s.db, p.OrganizationID, out.target.AssetID, out.target.VersionID)
	if e != nil {
		return ReviewStatusResult{}, e
	}
	t := ReviewTarget{Locator: out.target}
	for _, d := range m.DependsOn {
		t.Dependencies = append(t.Dependencies, ReviewDependency{AssetID: d.AssetID, VersionID: d.VersionID})
	}
	role, e = s.reviewCurrentAccess(ctx, p, t)
	if e != nil {
		return ReviewStatusResult{}, e
	}
	if actor != p.SubjectID && role != "owner" {
		return ReviewStatusResult{}, ErrForbidden
	}
	return out, nil
}

func (s *Service) reviewCurrentAccess(ctx context.Context, p Principal, t ReviewTarget) (string, error) {
	role, e := assetRole(ctx, s.db, p, t.Locator.AssetID)
	if e != nil {
		return "", e
	}
	for _, d := range t.Dependencies {
		if _, e := assetRole(ctx, s.db, p, d.AssetID); e != nil {
			return "", e
		}
	}
	return role, nil
}
