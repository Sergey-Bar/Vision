package soak

import (
	"sync"
	"time"
)

// CPUSampler measures the CPU a set of child processes consume, as a percentage
// of one core over a wall-clock interval.
//
// It samples DELTAS of each child's own kernel+user time, divided by the wall
// clock since the previous sample. That is the number AC5's clause means by
// "cpu(ffmpeg+python) <= declared": the two processes, as a share of the
// machine, measured rather than assumed.
//
// A child that cannot be read is a MISSING measurement and is reported as such.
// It is never counted as zero, because zero is a ceiling that passes on any
// load at all — and a seven-day run scored against a zero would be a run whose
// CPU clause was never checked.
type CPUSampler struct {
	mu      sync.Mutex
	last    map[int]float64
	lastAt  time.Time
	pids    func() []int
	missing map[string]bool
}

// NewCPUSampler builds a sampler over a function that returns the current child
// PIDs. The function is called per sample because the set changes when a child
// is restarted, and a stale PID would be a process that is no longer ours.
func NewCPUSampler(pids func() []int) *CPUSampler {
	return &CPUSampler{pids: pids, last: map[int]float64{}, missing: map[string]bool{}}
}

// Percent returns the CPU percent across the named children, or ok=false when
// none of them could be read. The second return is the reason, because a
// missing measurement with no reason is a shrug.
func (s *CPUSampler) Percent(now time.Time) (float64, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastAt.IsZero() {
		s.lastAt = now
		// Prime the counters: the first sample has no interval to divide by, and
		// dividing by zero would produce the largest number the type can hold.
		s.readInto(now)
		return 0, false, "first sample: CPU is measured from the second one onwards, because the first has no interval to divide by"
	}
	elapsed := now.Sub(s.lastAt).Seconds()
	s.lastAt = now
	cur := s.readInto(now)
	var total float64
	read := 0
	var reasons []string
	for pid, secs := range cur {
		prev, ok := s.last[pid]
		if !ok {
			// A child that appeared since the last sample has no baseline yet.
			reasons = append(reasons, "pid "+itoa(pid)+" appeared since the last sample")
			continue
		}
		total += secs - prev
		read++
	}
	s.last = cur
	if read == 0 || elapsed <= 0 {
		return 0, false, "no child's CPU time could be read"
	}
	pct := total / elapsed * 100
	for _, r := range reasons {
		s.missing[r] = true
	}
	return pct, true, ""
}

// Unmeasured lists the reasons children could not be read for, so the report
// can print them instead of a clean zero.
func (s *CPUSampler) Unmeasured() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.missing))
	for r := range s.missing {
		out = append(out, r)
	}
	return out
}

func (s *CPUSampler) readInto(now time.Time) map[int]float64 {
	cur := map[int]float64{}
	for _, pid := range s.pids() {
		if pid <= 0 {
			continue
		}
		secs, err := processCPUTime(pid)
		if err != nil {
			s.missing["pid "+itoa(pid)+": "+err.Error()] = true
			continue
		}
		cur[pid] = secs
	}
	return cur
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
