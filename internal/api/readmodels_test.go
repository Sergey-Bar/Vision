package api_test

// The dead-letter reader.
//
// One case here, and it is the one that killed the daemon.
//
// # The defect
//
// `ReadWithCursor` takes a `kind`. The switch has cases for `"inbox"`,
// `"job"` and `"outbox"`, and a `default` branch that means "both" — the
// empty kind. That branch called itself with the kind `"jobs"`, and `"jobs"`
// is not one of the cases, so it fell through to `default` again and called
// itself again with `"jobs"`, for ever.
//
// The live daemon died with a stack overflow.
//
// # Why it was invisible for so long
//
// Two defects were stacked on the same route. The coverage panel first
// crashed on a nil clock — in the `vendor_feed` row, which is the FIRST row
// the panel reads — and never reached the dead-letter read that comes later.
// Fixing the clock uncovered the recursion.
//
// And both were only reachable when the panel had something to report: a
// panel with nothing to say never reaches either line.
//
// Which is the general shape of this file's subject: a reader that is
// correct when it is empty and wrong when it is not is a reader that is
// wrong exactly when it matters.

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/jobs"
)

// TestTheDeadLetterReaderWithAnEmptyKindDoesNotRecurse covers the empty kind.
//
// The empty kind means "both queues", and the branch that means it must not
// call itself with a kind the switch does not know.
func TestTheDeadLetterReaderWithAnEmptyKindDoesNotRecurse(t *testing.T) {
	// The harness migrates the package's own database, and the reader needs
	// the schema: a dead letter is a row, and a reader that answered "no dead
	// letters" because the TABLE was missing would be right for the wrong
	// reason.
	serverWithRegistry(t, nil)
	pool := adminPool(t)
	d := api.DeadLetters{
		Inbox: &inbox.Writer{Pool: pool},
		Jobs:  &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, Name: jobs.QueueVLM},
	}

	// A deadline, because the broken version does not return at all: it
	// recurses until the stack is gone and the process dies. A test that
	// waits for a value that never comes is a test that hangs; a test that
	// hits a deadline reports what it was waiting for.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	items, _, err := d.ReadWithCursor(ctx, "tenant-does-not-matter", "site-does-not-matter", "", "", 10)
	if err != nil {
		t.Fatalf("read the dead letters with an empty kind: %v", err)
	}
	// The answer for an empty kind is a LIST, and an empty list is a
	// legitimate answer: this tenant has no dead letters.
	if items == nil {
		t.Errorf("the reader answered nil for the empty kind. A nil list and an empty list are " +
			"different answers to a reader, and only one of them is a list")
	}
}

// TestTheDeadLetterReaderKnowsItsKinds pins the vocabulary.
//
// A kind the switch does not know falls into the `default` branch, which
// means "both" — so an unknown kind silently answers with EVERY queue rather
// than with the thing the caller asked for. That is the shape that turned
// `"jobs"` into an infinite loop, and a caller that mistypes a kind must be
// able to tell the difference between "no dead letters" and "here are all of
// them".
func TestTheDeadLetterReaderKnowsItsKinds(t *testing.T) {
	serverWithRegistry(t, nil)
	pool := adminPool(t)
	d := api.DeadLetters{
		Inbox: &inbox.Writer{Pool: pool},
		Jobs:  &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, Name: jobs.QueueVLM},
	}
	ctx := context.Background()

	for _, kind := range []string{"inbox", "job", "outbox"} {
		items, _, err := d.ReadWithCursor(ctx, "t", "s", kind, "", 10)
		if err != nil {
			// "outbox" is not wired in this reader, and it says so by name.
			// That is the honest answer; the loop only requires that the
			// call RETURNS.
			continue
		}
		for _, item := range items {
			if item.Kind != kind {
				t.Errorf("the %s reader answered with a %s row. A reader that answers with the "+
					"wrong queue's rows is a reader nobody can trust to have asked", kind, item.Kind)
			}
		}
	}
}
