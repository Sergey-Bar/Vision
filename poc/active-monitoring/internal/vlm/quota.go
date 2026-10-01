package vlm

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// callCounter counts every HTTP request the client makes.
//
// It is how "a replay made zero provider calls" stops being a claim about the
// code's shape and becomes a measurement.
type callCounter struct {
	n atomic.Int64
}

func (c *callCounter) bump()      { c.n.Add(1) }
func (c *callCounter) get() int64 { return c.n.Load() }

// quota is the per-site daily cap on inference calls.
//
// Two properties matter and both are about being honest rather than careful:
//
//   - Exhaustion is a **state**, not an error and not a silent throttle. A
//     silently skipped call is indistinguishable from a detector that did not
//     fire, and the operator's alarm list would be shorter for a reason nobody
//     could see.
//   - The counter is per **UTC day**, because a cap that resets on a different
//     boundary than the bill is not a cap.
type quota struct {
	mu    sync.Mutex
	limit int
	day   string
	used  int
	now   func() time.Time
}

func newQuota(limit int, now func() time.Time) *quota {
	q := &quota{limit: limit, now: now}
	q.day = q.dayKey(now())
	return q
}

func (q *quota) dayKey(at time.Time) string { return at.UTC().Format("2006-01-02") }

// QuotaStatus is what a caller is allowed to report about spend.
type QuotaStatus struct {
	// Limit is the cap, or zero when no local cap is configured.
	Limit int
	// Used is how many calls have been made today, UTC.
	Used int
	// Remaining is what is left, or -1 when there is no cap. -1 rather than a
	// large number, because "unlimited" and "a lot" must not read alike on a
	// console.
	Remaining int
	// Exhausted is true once the cap has been reached and a call was refused.
	Exhausted bool
	// Day is the UTC date the counters are for. It travels with the number
	// because a "used" figure without its date is not a fact.
	Day string
}

// ErrQuotaExhausted is returned when the site's daily cap is reached.
//
// A first-class outcome, not a transient failure: retrying does not help until
// the UTC day rolls over, and a caller that treats it as retryable will spin.
var ErrQuotaExhausted = fmt.Errorf("vlm: the site's daily query cap is reached")

func (q *quota) status(at time.Time) QuotaStatus {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.roll(at)
	st := QuotaStatus{Limit: q.limit, Used: q.used, Day: q.day}
	if q.limit <= 0 {
		st.Remaining = -1
		return st
	}
	st.Remaining = q.limit - q.used
	if st.Remaining < 0 {
		st.Remaining = 0
	}
	st.Exhausted = q.used >= q.limit
	return st
}

// take reserves one call, refusing when the cap is reached.
//
// It reserves *before* the call rather than counting after it, because the call
// is paid for whether or not it succeeds, and counting afterwards would let a
// failed call be free.
func (q *quota) take() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.roll(q.now())
	if q.limit > 0 && q.used >= q.limit {
		return fmt.Errorf("%w: %d of %d used on %s", ErrQuotaExhausted, q.used, q.limit, q.day)
	}
	q.used++
	return nil
}

func (q *quota) roll(at time.Time) {
	if d := q.dayKey(at); d != q.day {
		q.day = d
		q.used = 0
	}
}

// QuotaExhausted is a convenience for a caller that wants to branch on the
// reason without comparing errors by string.
func QuotaExhausted(err error) bool { return errors.Is(err, ErrQuotaExhausted) }

func elapsedMS(start, end time.Time) int64 {
	ms := end.Sub(start).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}
