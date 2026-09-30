package hafnia

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// assetLedger tracks every asset this client uploaded and has not been told to
// delete.
//
// It exists because of the plan's P3.3: "asset deletion after the observation is
// recorded and on every failure path after upload; a sweep test proves a killed
// worker leaves no orphan." A killed worker is a process that vanishes between
// the upload and the delete, and nothing in Go can run a defer in that case - so
// the record of what was uploaded has to be durable somewhere the *next* process
// can read. This ledger is per-process, so it is the first half of that: it makes
// the sweep testable and it makes the failure visible. A durable ledger is
// Milestone 2's outbox work, and pretending this one is durable would be the
// exact kind of quiet gap this repository treats as a defect.
type assetLedger struct {
	mu      sync.Mutex
	pending map[string]time.Time
}

func newAssetLedger() *assetLedger {
	return &assetLedger{pending: map[string]time.Time{}}
}

// record notes an upload that has not yet been released.
func (l *assetLedger) record(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending[id] = time.Now().UTC()
}

// release drops an id after a confirmed delete.
func (l *assetLedger) release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, id)
}

func (l *assetLedger) ids() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.pending))
	for id := range l.pending {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SweepAssets deletes everything this process uploaded and did not release.
//
// It is the compensating action for a worker that died between upload and
// delete, and it is idempotent: deleting an id the provider has already forgotten
// is a 404 here, not a failure. A sweep that could not complete is reported with
// the ids it did not manage, because a sweep that reports success while leaving
// footage behind is worse than no sweep.
func (c *Client) SweepAssets(ctx context.Context) ([]string, error) {
	pending := c.assets.ids()
	if len(pending) == 0 {
		return nil, nil
	}
	var failed []string
	for _, id := range pending {
		err := c.DeleteAsset(ctx, id)
		switch {
		case err == nil, isGone(err):
			c.assets.release(id)
		default:
			failed = append(failed, id)
		}
	}
	if len(failed) > 0 {
		return failed, fmt.Errorf("hafnia: swept %d of %d outstanding assets; %d are still on the provider",
			len(pending)-len(failed), len(pending), len(failed))
	}
	return nil, nil
}
