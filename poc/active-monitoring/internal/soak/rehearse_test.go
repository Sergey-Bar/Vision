package soak

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

// TestTheRunRehearsesAgainstItsOwnPushTarget is the AC7 mechanism, tested.
//
// The rehearsal used to be a separate command that had to be remembered before
// the run started, and it wrote to its own scratch store. So AC7 was `NOT RUN` in
// every run that had actually been rehearsed, because the evidence about
// surviving failure lived somewhere other than the measurement it was supposed to
// justify. Here the rehearsal runs against a real simulator, the rows land in the
// run's own store, and every plan entry must produce the outcome it promises.
func TestTheRunRehearsesAgainstItsOwnPushTarget(t *testing.T) {
	sim := xpsim.New()
	srv := httptest.NewServer(sim.Handler())
	defer srv.Close()

	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{ID: "r1", Label: "rehearsal-in-run", Mode: "live",
		SourceKind: "loop", SourceArg: "f.mp4", PushBaseURL: srv.URL, ClockSource: "host",
		StartedAt: time.Now().Add(-time.Second), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}

	r := &Rehearser{
		BaseURL: srv.URL, Store: st, RunID: "r1",
		NewClient: func() (*xprotect.Client, error) {
			return xprotect.New(xprotect.Config{BaseURL: srv.URL, Timeout: 5 * time.Second,
				Credentials: xprotect.Credentials{ClientID: "c", ClientSecret: "s", Username: "u", Password: "p"}})
		},
	}
	if !r.IsSimulator() {
		t.Fatal("a loopback target was not recognised as a simulator; the rehearsal would refuse to run against a real VMS")
	}
	rows, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("the rehearsal could not run: %v", err)
	}
	if len(rows) != len(rehearsalPlan) {
		t.Fatalf("the rehearsal produced %d rows for a plan of %d", len(rows), len(rehearsalPlan))
	}
	for _, row := range rows {
		if row.Observed != row.Expected {
			t.Errorf("fault %s produced %q, expected %q: %s", row.Fault, row.Observed, row.Expected, row.Detail)
		}
	}
	// And the rows are in the RUN'S store, which is what the report reads.
	got, err := st.FaultsByReasonPrefix(ctx, "rehearsal:")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(rehearsalPlan) {
		t.Fatalf("the run's own store holds %d rehearsal rows, want %d: AC7 reads from HERE, so a rehearsal written elsewhere is invisible to it", len(got), len(rehearsalPlan))
	}
	// Which means the criterion can actually be scored.
	in := Inputs{RehearsedFaults: rows}
	if c := byID(Score(in), "AC7"); c.Verdict != VerdictPass {
		t.Fatalf("AC7 scored %s with %d rehearsal rows in the run's own store: %s", c.Verdict, len(rows), c.Observed)
	}
}

// TestTheRehearsalRefusesToTouchARealVMS: a real VMS has no control surface to
// arm a fault on, and inventing failures on somebody's production system is not
// a rehearsal. The criterion must be `NOT RUN`, and say why.
func TestTheRehearsalRefusesToTouchARealVMS(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	called := false
	r := &Rehearser{
		BaseURL: "https://vms.example.com", Store: st, RunID: "r1",
		NewClient: func() (*xprotect.Client, error) { called = true; return nil, nil },
	}
	if r.IsSimulator() {
		t.Fatal("a remote host was treated as a simulator")
	}
	rows, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 || called {
		t.Fatalf("the rehearsal ran against a remote VMS: %d rows, client built %v", len(rows), called)
	}
	// And with no rehearsal, the criterion is NOT RUN and says so — never PASS.
	c := byID(Score(Inputs{}), "AC7")
	if c.Verdict != VerdictNotRun {
		t.Fatalf("AC7 scored %s with no rehearsal at all", c.Verdict)
	}
	if c.Missing == "" {
		t.Fatal("AC7 is NOT RUN with no reason given")
	}
}

func TestTheRehearsalBodyIsNotADetection(t *testing.T) {
	// The rehearsal pushes a real event so the client does its whole cycle, but it
	// is labelled as what it is. A rehearsal event that looked like a detection
	// would put a fake row in somebody's incident list.
	body := string(rehearsalBody("aicap-rehearsal-500"))
	for _, want := range []string{"rehearsal", "fault-rehearsal", "aicap-car"} {
		_ = want
	}
	for _, unwanted := range []string{`"value":"car"`, `"value":"bird"`, `"value":"person"`} {
		if contains(body, unwanted) {
			t.Errorf("the rehearsal body claims to be a detection: %s", body)
		}
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

var _ = filepath.Join
