package analysis

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const LeaseDuration = 25 * time.Minute
const StepTimeout = 20 * time.Minute
const PollInterval = 5 * time.Second

type Work struct {
	OrganizationID, OwnerID, TaskID, StepKey, LeaseID string
	ConsecutiveErrors                                 int
}

// Claim selects one task, then one step in its original plan. A task with a
// failed observation cannot spend on new steps, but sent calls still recover.
// No transaction/connection is held during media preparation or channel I/O.
func (s PGStore) Claim(ctx context.Context, dispatch bool) (Work, error) {
	var w Work
	if s.DB == nil {
		return w, ErrInvalid
	}
	w.LeaseID = uuid.NewString()
	err := s.DB.QueryRow(ctx, `WITH candidate AS (
 SELECT q.organization_id,q.owner_id,q.task_id,step.step_key
 FROM studio.world_analysis_schedule q
 JOIN studio.world_analysis_tasks t USING(organization_id,owner_id,task_id)
 CROSS JOIN LATERAL (
  SELECT s.step_key FROM jsonb_array_elements(t.plan->'steps') WITH ORDINALITY p(item,position)
  JOIN studio.world_analysis_steps s ON s.organization_id=q.organization_id AND s.owner_id=q.owner_id AND s.task_id=q.task_id AND s.step_key=p.item->>'key'
  WHERE s.state IN ('dispatching','running','acceptance_unknown') OR (
   $1 AND s.state='planned' AND NOT EXISTS (
    SELECT 1 FROM studio.world_analysis_steps other
    WHERE other.organization_id=q.organization_id AND other.owner_id=q.owner_id AND other.task_id=q.task_id
    AND other.state IN ('dispatching','running','acceptance_unknown','failed')))
  ORDER BY CASE WHEN s.state='planned' THEN 1 ELSE 0 END,p.position LIMIT 1
 ) step
 WHERE q.next_attempt_at<=now() AND (q.lease_until IS NULL OR q.lease_until<=now())
 ORDER BY q.next_attempt_at,q.task_id FOR UPDATE OF q SKIP LOCKED LIMIT 1
 ) UPDATE studio.world_analysis_schedule q
 SET lease_id=$2,lease_until=now()+make_interval(secs=>$3)
 FROM candidate c WHERE q.organization_id=c.organization_id AND q.owner_id=c.owner_id AND q.task_id=c.task_id
 RETURNING q.organization_id::text,q.owner_id::text,q.task_id::text,c.step_key,q.consecutive_errors`, dispatch, w.LeaseID, LeaseDuration.Seconds()).Scan(&w.OrganizationID, &w.OwnerID, &w.TaskID, &w.StepKey, &w.ConsecutiveErrors)
	if errors.Is(err, pgx.ErrNoRows) {
		return Work{}, ErrMissing
	}
	return w, err
}

func scheduleDelay(code string, failures int) time.Duration {
	if code == "" {
		return PollInterval
	}
	return min(time.Minute, PollInterval*time.Duration(1<<min(max(failures, 0), 4)))
}

// Release is fenced by the claim token, including its expiry. A late worker
// cannot reset the next attempt or diagnostics of a new lease holder.
func (s PGStore) Release(ctx context.Context, w Work, code string) error {
	if s.DB == nil || !validUUID(w.OrganizationID) || !validUUID(w.OwnerID) || !validUUID(w.TaskID) || !validUUID(w.LeaseID) {
		return ErrInvalid
	}
	switch code {
	case "", "dependency_unavailable", "acceptance_unknown", "source_unavailable", "invalid_observation", "step_failed", "interrupted":
	default:
		return ErrInvalid
	}
	n := 0
	if code != "" {
		n = min(max(w.ConsecutiveErrors, 0)+1, 16)
	}
	tag, err := s.DB.Exec(ctx, `UPDATE studio.world_analysis_schedule SET lease_id=NULL,lease_until=NULL,next_attempt_at=now()+make_interval(secs=>$1),consecutive_errors=$2,last_error_code=$3 WHERE organization_id=$4 AND owner_id=$5 AND task_id=$6 AND lease_id=$7 AND lease_until>now()`, scheduleDelay(code, n).Seconds(), n, code, w.OrganizationID, w.OwnerID, w.TaskID, w.LeaseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrChanged
	}
	return nil
}

type Queue interface {
	Claim(context.Context, bool) (Work, error)
	Release(context.Context, Work, string) error
}
type Scheduler struct {
	Queue    Queue
	Dispatch bool
	Advance  func(context.Context, Work) (StepRecord, error)
}

func outcome(r StepRecord, err error) string {
	if r.State == "failed" {
		return "step_failed"
	}
	if r.State == "acceptance_unknown" {
		return "acceptance_unknown"
	}
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	if errors.Is(err, ErrMediaInvalid) || errors.Is(err, ErrMediaUnsupported) {
		return "source_unavailable"
	}
	if errors.Is(err, ErrInvalid) {
		return "invalid_observation"
	}
	if err != nil {
		return "dependency_unavailable"
	}
	return ""
}

func (s Scheduler) Tick(ctx context.Context) (bool, error) {
	if s.Queue == nil || s.Advance == nil {
		return false, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	w, err := s.Queue.Claim(ctx, s.Dispatch)
	if errors.Is(err, ErrMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stepCtx, cancel := context.WithTimeout(ctx, StepTimeout)
	r, advanceErr := s.Advance(stepCtx, w)
	cancel()
	// A canceled request must not leave a healthy process holding the lease.
	// On process death the database expiry provides the same recovery path.
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer releaseCancel()
	err = s.Queue.Release(releaseCtx, w, outcome(r, advanceErr))
	return true, errors.Join(advanceErr, err)
}

func (s Scheduler) Run(ctx context.Context, report func(error)) {
	for ctx.Err() == nil {
		worked, err := s.Tick(ctx)
		if err != nil && report != nil && ctx.Err() == nil {
			report(err)
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type Progress struct {
	TaskID        string `json:"task_id"`
	Stage         string `json:"stage"`
	Planned       int    `json:"planned"`
	Running       int    `json:"running"`
	Unknown       int    `json:"acceptance_unknown"`
	Ready         int    `json:"observation_ready"`
	Failed        int    `json:"failed"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

func (s PGStore) Progress(ctx context.Context, org, owner, task string) (Progress, error) {
	p := Progress{TaskID: task}
	t, err := s.Task(ctx, org, owner, task)
	if err != nil {
		return p, err
	}
	err = s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='planned'),count(*) FILTER(WHERE state IN ('dispatching','running')),count(*) FILTER(WHERE state='acceptance_unknown'),count(*) FILTER(WHERE state='observation_ready'),count(*) FILTER(WHERE state='failed') FROM studio.world_analysis_steps WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task).Scan(&p.Planned, &p.Running, &p.Unknown, &p.Ready, &p.Failed)
	if err != nil {
		return p, err
	}
	if p.Planned+p.Running+p.Unknown+p.Ready+p.Failed != len(t.Plan.Steps) {
		return p, ErrInvalid
	}
	err = s.DB.QueryRow(ctx, `SELECT last_error_code FROM studio.world_analysis_schedule WHERE organization_id=$1 AND owner_id=$2 AND task_id=$3`, org, owner, task).Scan(&p.LastErrorCode)
	if err != nil {
		return p, err
	}
	switch {
	case p.Failed > 0 || p.Unknown > 0 || p.LastErrorCode != "":
		p.Stage = "needs_attention"
	case p.Ready == len(t.Plan.Steps):
		p.Stage = "observations_ready"
	case p.Running > 0 || p.Ready > 0:
		p.Stage = "observing"
	default:
		p.Stage = "planned"
	}
	return p, nil
}
