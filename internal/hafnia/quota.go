package hafnia

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// quota is the per-site daily cap on inference calls.
//
// The plan's cost argument is that the product pays per event rather than per
// camera-hour, and that argument only survives if the per-event number is
// bounded. A cap nobody enforces is a number in a slide.
//
// Two properties matter and both are about being honest rather than careful:
//
//   - Exhaustion is a **state**, not an error and not a silent throttle. The
//     caller gets [QuotaStatus] and a named reason, and the site sees a
//     degraded observation explaining that the cap was reached. A silently
//     skipped call is indistinguishable from a detector that did not fire.
//   - The counter is per **UTC day**, because a site's reporting is per UTC day
//     and a cap that resets on a different boundary than the bill is not a cap.
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
// It is a first-class outcome, not a transient failure: retrying does not help
// until the UTC day rolls over, and a caller that treats it as retryable will
// spin.
var ErrQuotaExhausted = fmt.Errorf("hafnia: the site's daily query cap is reached")

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
func QuotaExhausted(err error) bool { return is(err, ErrQuotaExhausted) }

// is reports whether target is anywhere in err's chain.
//
// It is errors.Is under a name that reads better at the call sites, and it earns
// its place because every reason this package returns is wrapped with its own
// context: comparing with == would quietly stop matching the moment somebody
// added a clause to a message, and the failure would be a call that is refused
// for no visible reason.
func is(err, target error) bool { return errors.Is(err, target) }
