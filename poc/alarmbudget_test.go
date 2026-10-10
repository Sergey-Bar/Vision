package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestAlarmBandIsPureNumbers is the band contract: two numbers in, one named
// band out. The boundary is the one an operator needs to be exact about — at
// the budget is not over it, but it is no longer "within".
func TestAlarmBandIsPureNumbers(t *testing.T) {
	cases := []struct {
		esc, budget int
		want        string
	}{
		{0, 10, alarmWithin},
		{4, 10, alarmWithin},
		{9, 10, alarmWithin},
		{10, 10, alarmApproaching},
		{11, 10, alarmOver},
		{5, 0, alarmUnbounded},
		{5, -1, alarmUnbounded},
	}
	for _, c := range cases {
		if got := alarmBand(c.esc, c.budget); got != c.want {
			t.Errorf("alarmBand(%d, %d) = %q, want %q", c.esc, c.budget, got, c.want)
		}
	}
}

// TestEscalationRingPrunesWithinTheWindow checks the ring that feeds the band:
// an escalation inside the hour counts and one outside it does not, and the
// ring cannot grow without bound on a shift that escalates all night.
func TestEscalationRingPrunesWithinTheWindow(t *testing.T) {
	s := &StreamIngest{}
	now := time.Now().UTC()
	s.recordEscalation(now.Add(-3 * time.Hour))
	s.recordEscalation(now.Add(-30 * time.Minute))
	s.recordEscalation(now.Add(-1 * time.Minute))

	if got := s.escalationsWithin(now); got != 2 {
		t.Errorf("escalations in the last hour = %d, want 2", got)
	}
	// The hour-old entry is gone after a read, so the ring stays bounded.
	if got := s.escalationsWithin(now.Add(2 * time.Hour)); got != 0 {
		t.Errorf("escalations two hours later = %d, want 0", got)
	}
}

// TestRecordEscalationIsConcurrencySafe: the autonomy pass writes and the
// status snapshot reads, and the snapshot must not race the pass. Run with
// -race to prove it.
func TestRecordEscalationIsConcurrencySafe(t *testing.T) {
	s := &StreamIngest{}
	var writes int64
	stop := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				s.recordEscalation(time.Now())
				atomic.AddInt64(&writes, 1)
				// The goroutine is running before the reader asserts anything:
				// without the barrier, a scheduler that only starts it after the
				// loop ends proves nothing about concurrent access.
				select {
				case <-started:
				default:
					close(started)
				}
			}
		}
	}()
	<-started
	for i := 0; i < 50; i++ {
		if n := s.escalationsWithin(time.Now()); n < 0 {
			t.Fatalf("escalationsWithin returned %d, want a whole count", n)
		}
		if band := alarmBand(1, 10); band != alarmWithin {
			t.Fatalf("alarmBand = %q, want %q", band, alarmWithin)
		}
	}
	close(stop)
	<-done

	// The writer really wrote, and the ring really holds what it wrote: those
	// timestamps are recent, so all of them are inside the hour.
	if atomic.LoadInt64(&writes) == 0 {
		t.Fatal("the writer goroutine recorded nothing; the concurrency under test did not happen")
	}
	if n := s.escalationsWithin(time.Now()); n < 1 {
		t.Errorf("after the writer stopped the ring holds %d escalations for %d writes", n, atomic.LoadInt64(&writes))
	}
	// And a read two hours later prunes everything, which is what keeps the
	// ring bounded rather than accumulating a history nobody reads.
	if later := s.escalationsWithin(time.Now().Add(2 * time.Hour)); later != 0 {
		t.Errorf("a ring read two hours later holds %d escalations, want 0 — the window is an hour", later)
	}
}
