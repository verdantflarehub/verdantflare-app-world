package analysis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSchedulerLeaseCanIdentifyWorldRequest(t *testing.T) {
	id, err := newLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	u, err := uuid.Parse(id)
	if err != nil || u.Version() != 7 || u.Variant() != uuid.RFC4122 {
		t.Fatal("lease cannot serve as a World request identity")
	}
}

type memoryQueue struct {
	work     Work
	claimErr error
	dispatch bool
	code     string
	released bool
}

func (q *memoryQueue) Claim(_ context.Context, dispatch bool) (Work, error) {
	q.dispatch = dispatch
	return q.work, q.claimErr
}
func (q *memoryQueue) Release(ctx context.Context, w Work, code string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	q.released = true
	q.code = code
	return nil
}
func TestSchedulerCancellationReleasesButDoesNotResubmit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	q := &memoryQueue{}
	s := Scheduler{Queue: q, Advance: func(ctx context.Context, w Work) (StepRecord, error) {
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) > StepTimeout {
			t.Fatal("unbounded step")
		}
		cancel()
		return StepRecord{State: "acceptance_unknown"}, ctx.Err()
	}}
	worked, err := s.Tick(ctx)
	if !worked || !errors.Is(err, context.Canceled) || !q.released || q.code != "acceptance_unknown" || q.dispatch {
		t.Fatal("cancellation lost claim/outcome", err)
	}
	q.released = false
	if worked, err = s.Tick(ctx); worked || !errors.Is(err, context.Canceled) || q.released {
		t.Fatal("canceled scheduler did more work")
	}
}
func TestSchedulerMissingWorkDoesNotAdvance(t *testing.T) {
	s := Scheduler{Queue: &memoryQueue{claimErr: ErrMissing}, Advance: func(context.Context, Work) (StepRecord, error) {
		t.Fatal("advanced absent work")
		return StepRecord{}, nil
	}}
	if worked, err := s.Tick(t.Context()); worked || err != nil {
		t.Fatal(worked, err)
	}
}
func TestSchedulerErrorSanitizationAndBoundedRetry(t *testing.T) {
	for _, tc := range []struct {
		record StepRecord
		err    error
		code   string
	}{
		{StepRecord{}, errors.New("sensitive upstream content"), "dependency_unavailable"},
		{StepRecord{State: "failed"}, nil, "step_failed"},
		{StepRecord{}, ErrMediaInvalid, "source_unavailable"},
		{StepRecord{}, ErrInvalid, "invalid_observation"},
		{StepRecord{State: "observation_ready"}, nil, ""},
	} {
		q := &memoryQueue{}
		s := Scheduler{Queue: q, Dispatch: true, Advance: func(context.Context, Work) (StepRecord, error) { return tc.record, tc.err }}
		_, _ = s.Tick(t.Context())
		if q.code != tc.code || !q.dispatch {
			t.Fatal("unsafe/wrong diagnostic", q.code)
		}
	}
	for n := -1; n < 100; n++ {
		d := scheduleDelay("acceptance_unknown", n)
		if d < PollInterval || d > time.Minute {
			t.Fatal("unbounded retry", d)
		}
	}
	if scheduleDelay("", 16) != PollInterval {
		t.Fatal("healthy retry did not reset")
	}
}
