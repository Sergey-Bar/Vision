package camerahealth

import (
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
}

// TestStateMachineOrdering is the precedence rule: silence outranks blindness,
// a late but alive camera is stale, and a camera with no frame yet is silent.
func TestStateMachineOrdering(t *testing.T) {
	now := fixedNow()
	w := Windows{Fresh: 3 * time.Second, Silent: 10 * time.Second}
	cases := []struct {
		name  string
		last  time.Time
		blind bool
		want  string
	}{
		{"no frame yet", time.Time{}, false, Silent},
		{"a second old", now.Add(-time.Second), false, Fresh},
		{"five seconds old with a 3s window", now.Add(-5 * time.Second), false, Stale},
		{"a fresh blind frame", now.Add(-time.Second), true, Blind},
		{"a blind frame gone silent", now.Add(-30 * time.Second), true, Silent},
	}
	for _, c := range cases {
		if got := State(c.last, c.blind, now, w); got != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestDegradedIsOneSet is the contract that ended the disagreement between the
// brief's headline and its coverage section: a stale camera is degraded for
// everything, and nothing else is.
func TestDegradedIsOneSet(t *testing.T) {
	for _, s := range []string{Stale, Silent, Blind} {
		if !Degraded(s) {
			t.Errorf("%s should read as degraded", s)
		}
		if Live(s) {
			t.Errorf("%s should not read as live", s)
		}
	}
	if Degraded(Fresh) {
		t.Error("a fresh camera should not read as degraded")
	}
	if !Live(Fresh) {
		t.Error("a fresh camera should read as live")
	}
}

// TestWindowsScaleWithCaptureRate: a 4 fps camera is not called silent for the
// two seconds a 1 fps camera is routinely quiet, and a quarter-fps camera gets
// windows longer than the frame interval itself.
func TestWindowsScaleWithCaptureRate(t *testing.T) {
	slow := WindowsFor(0.25)
	if slow.Fresh <= 4*time.Second {
		t.Errorf("0.25 fps fresh window = %v, want > 4s", slow.Fresh)
	}
	if slow.Silent < 16*time.Second {
		t.Errorf("0.25 fps silent window = %v, want >= 16s", slow.Silent)
	}
	fast := WindowsFor(4)
	if fast.Fresh >= slow.Fresh || fast.Silent >= slow.Silent {
		t.Errorf("4 fps windows (%v/%v) should be tighter than 0.25 fps (%v/%v)", fast.Fresh, fast.Silent, slow.Fresh, slow.Silent)
	}
	if zero := WindowsFor(0); zero.Fresh <= 0 || zero.Silent <= 0 {
		t.Errorf("a zero fps must still yield positive windows, got %+v", zero)
	}
}
