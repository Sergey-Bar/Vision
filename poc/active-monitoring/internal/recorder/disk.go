package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ErrDiskBudget is the reason a run stops rather than continuing. It is a
// refusal, not a warning: a full disk on day three of a seven-day run is a run
// that never concluded, and deleting stored evidence to keep going is how the
// only copy of an incident disappears.
type ErrDiskBudget struct {
	Ceiling     int64
	Root        string
	RollingDir  string
	InUse       int64
	RollingUse  int64
	EvidenceUse int64
	Deleted     int
}

func (e *ErrDiskBudget) Error() string {
	return fmt.Sprintf("disk_budget_exceeded: %s holds %d bytes (rolling %d, stored evidence %d) against a ceiling of %d bytes, after deleting %d rolling segment(s); stored evidence is never deleted to make room, so the run stops here and the report says why",
		e.Root, e.InUse, e.RollingUse, e.EvidenceUse, e.Ceiling, e.Deleted)
}

// Disk is the rolling budget.
//
// For a LOOP the rolling recording is the working set and a ceiling is the right
// bound. For a LIVE camera it is something else: the only reason to record at all
// is to have the seconds AROUND a crossing, because the moment of detection
// arrives after the pixels are gone. Six seconds are used. The other 86,394 are
// kept because nobody asked them to be deleted.
//
// So there are two bounds and the first one is age. `Keep` is how long a rolling
// segment may live at all; the ceiling is the backstop for when even that is too
// much. With a 4 Mbit/s source and a 30-second ring that is about 15 MB,
// constant, for as long as the run lasts — instead of 40 GB a day growing until
// something fills the disk.
type Disk struct {
	Root       string
	RollingDir string
	Ceiling    int64
	// Keep is the retention age. Zero means the ceiling is the only bound, which
	// is the loop's behaviour and NOT what a live camera should run with.
	Keep time.Duration
	// SegmentSeconds is the muxer's segment length, and the ring must exceed it.
	SegmentSeconds int
	now            func() time.Time
}

// SweepResult says what a sweep did and, more importantly, what it refused to do.
type SweepResult struct {
	Deleted    []string `json:"deleted"`
	BytesFreed int64    `json:"bytes_freed"`
	// AgedOut is how many segments the AGE rule removed, as against the ceiling.
	AgedOut     int       `json:"aged_out"`
	InUse       int64     `json:"in_use_after"`
	RollingUse  int64     `json:"rolling_use_after"`
	EvidenceUse int64     `json:"evidence_use_after"`
	Ceiling     int64     `json:"ceiling"`
	OldestKept  time.Time `json:"oldest_kept"`
	// Keep is the age rule in force, printed so a reader can see that the
	// recording is bounded by DESIGN and not by a disk sweep losing a race.
	Keep string `json:"keep,omitempty"`
}

func (d Disk) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// Used is the total size of a directory tree.
func Used(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("recorder: cannot measure %s: %w", dir, err)
	}
	return total, nil
}

// EvidenceBytes is the size of the stored evidence tree, reported separately so
// the refusal can say how much of the pressure is evidence that is staying put.
func (d Disk) EvidenceBytes() (int64, error) {
	ev := filepath.Join(d.Root, "evidence")
	if _, err := os.Stat(ev); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return Used(ev)
}

// Used is the total size of the data root.
func (d Disk) Used() (int64, error) { return Used(d.Root) }

// Free is the free space of the volume holding the data root. Preflight uses it
// to enforce the headroom rule.
//
// The path is resolved to its nearest EXISTING ancestor first. A data root that
// has not been created yet is the normal case at preflight time — the run
// creates it — and asking the OS about a path inside a directory that does not
// exist returns zero with no error. A zero then fails the headroom rule with
// "you do not have enough disk" on a machine with 900 GB free, which is the most
// confusing way this gate could possibly fail.
func Free(path string) (int64, error) {
	p := existingAncestor(path)
	var f uint64
	ep, err := filepath.EvalSymlinks(p)
	if err != nil {
		ep = p
	}
	if err := getDiskFree(ep, &f); err != nil {
		return 0, fmt.Errorf("recorder: cannot read free space for %s: %w", ep, err)
	}
	if f == 0 {
		return 0, fmt.Errorf("recorder: the OS reported zero free space for %s. That is not a measurement, and a headroom rule checked against zero refuses a run for no reason", ep)
	}
	return int64(f), nil
}

// existingAncestor walks up until it finds a directory that exists, and returns
// the path it would have created's volume.
func existingAncestor(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for p := abs; ; {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return abs
		}
		p = parent
	}
}

// AgeOut removes rolling segments older than the retention age, oldest first,
// and returns what it removed. It runs BEFORE the ceiling sweep and it does not
// consult the ceiling at all: a ring is a ring because of its age, not because
// the disk is filling.
func (d Disk) AgeOut(now time.Time) (SweepResult, error) {
	res := SweepResult{Ceiling: d.Ceiling, Keep: d.Keep.String()}
	if d.Keep <= 0 {
		return res, nil
	}
	// The ring must be LONGER THAN ONE SEGMENT.
	//
	// A 30 second ring over 60 second segments deletes the file ffmpeg is writing
	// into, and ffmpeg then stops recording without saying why. This is not a
	// hypothetical: the first version of the test used exactly those numbers and
	// the ring ate every segment including the live one.
	if d.SegmentSeconds > 0 && d.Keep <= time.Duration(d.SegmentSeconds)*time.Second {
		return res, fmt.Errorf(
			"recorder: the ring is %s but a segment is %ds, so the ring would delete the file ffmpeg is writing into and the recording would stop without an error.\n"+
				"  Set the ring to at least the segment length plus one, and to the clip window you need: %s of pre-detection video is what a clip is cut from",
			d.Keep.Round(time.Second), d.SegmentSeconds, d.Keep.Round(time.Second))
	}
	idx, _, err := ScanSegmentIndex(d.RollingDir)
	if err != nil {
		return res, err
	}
	cutoff := now.Add(-d.Keep)
	// Age comes from the FILE, not from the name.
	//
	// The segment filename is a counter — seg-000000.mp4 — and a counter carries
	// no time. The recorder knows each segment's wall clock because it measured it
	// from the frame stream, but that measurement lives in memory and is gone after
	// a restart, and the ring has to work after one. So the age is the file's
	// modification time, which for a segment ffmpeg is writing is when that
	// segment was produced.
	segs := idx.Segments()
	type aged struct {
		path string
		mod  time.Time
		size int64
	}
	var cand []aged
	for _, s := range segs {
		info, err := os.Stat(s.Path)
		if err != nil {
			continue
		}
		cand = append(cand, aged{path: s.Path, mod: info.ModTime(), size: info.Size()})
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].mod.Before(cand[j].mod) })

	for _, a := range cand {
		if a.mod.After(cutoff) {
			break
		}
		// The segment ffmpeg is writing right now. Its mtime is the wall clock,
		// so it is never past the cutoff — but the check is written out anyway,
		// because deleting a file a process has open is how a recorder stops
		// recording without saying why.
		if a.mod.After(now.Add(-time.Second)) {
			break
		}
		if evidenceExempt(a.path) {
			return res, fmt.Errorf("recorder: refusing to age out %s: it is under the evidence root, and stored evidence is never deleted to satisfy a retention rule", a.path)
		}
		if err := os.Remove(a.path); err != nil {
			return res, fmt.Errorf("recorder: cannot age out %s: %w", a.path, err)
		}
		res.Deleted = append(res.Deleted, a.path)
		res.BytesFreed += a.size
		res.AgedOut++
	}
	if r, err := Used(d.RollingDir); err == nil {
		res.RollingUse = r
	}
	return res, nil
}

// Sweep deletes the oldest rolling segments until the data root is under the
// ceiling. It never touches evidence, and it stops with ErrDiskBudget if
// deleting every rolling segment is still not enough.
func (d Disk) Sweep() (SweepResult, error) {
	// The age rule runs first: a ring is a ring because of its age, and the
	// ceiling is the backstop for when even that is too much.
	if aged, err := d.AgeOut(d.clock()); err == nil && aged.AgedOut > 0 {
		res := SweepResult{Ceiling: d.Ceiling, Keep: d.Keep.String()}
		if inUse, err := Used(d.Root); err == nil {
			res.InUse = inUse
		}
		return res, nil
	} else if err != nil {
		return SweepResult{Ceiling: d.Ceiling}, err
	}

	res := SweepResult{Ceiling: d.Ceiling, Keep: d.Keep.String()}
	inUse, err := Used(d.Root)
	if err != nil {
		return res, err
	}
	rolling, err := Used(d.RollingDir)
	if err != nil && !os.IsNotExist(err) {
		return res, err
	}
	evidence, err := d.EvidenceBytes()
	if err != nil {
		return res, err
	}

	if inUse <= d.Ceiling {
		res.InUse, res.RollingUse, res.EvidenceUse = inUse, rolling, evidence
		if idx, _, err := ScanSegmentIndex(d.RollingDir); err == nil && idx.Len() > 0 {
			res.OldestKept = idx.Segments()[0].Start
		}
		return res, nil
	}

	idx, _, err := ScanSegmentIndex(d.RollingDir)
	if err != nil {
		return res, err
	}
	segs := idx.Segments()
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start.Before(segs[j].Start) })
	for _, s := range segs {
		if inUse <= d.Ceiling {
			break
		}
		if evidenceExempt(s.Path) {
			// Unreachable while the roots are separate, and that is the point:
			// a future change that mixed them fails here instead of deleting an
			// incident.
			return res, fmt.Errorf("recorder: refusing to sweep %s: it is under the evidence root, and stored evidence is never deleted to satisfy a disk budget", s.Path)
		}
		if err := os.Remove(s.Path); err != nil {
			return res, fmt.Errorf("recorder: cannot delete the oldest segment %s: %w", s.Path, err)
		}
		res.Deleted = append(res.Deleted, s.Path)
		res.BytesFreed += s.Size
		inUse -= s.Size
	}
	res.InUse = inUse
	res.EvidenceUse = evidence
	if rollingAfter, err := Used(d.RollingDir); err == nil {
		res.RollingUse = rollingAfter
	}
	if inUse > d.Ceiling {
		return res, &ErrDiskBudget{
			Ceiling: d.Ceiling, Root: d.Root, RollingDir: d.RollingDir,
			InUse: inUse, RollingUse: res.RollingUse, EvidenceUse: evidence, Deleted: len(res.Deleted),
		}
	}
	if after, _, err := ScanSegmentIndex(d.RollingDir); err == nil && after.Len() > 0 {
		res.OldestKept = after.Segments()[0].Start
	}
	return res, nil
}

// Check is the preflight rule: refuse to start when free space is below
// headroom times the measured budget. The consequence is spelled out because
// the alternative is a run that dies on day three.
func Check(free, measuredBudget int64, headroom float64, days int) error {
	need := int64(float64(measuredBudget) * headroom)
	if free >= need {
		return nil
	}
	return fmt.Errorf("disk: %d bytes free on the data volume, and the measured %.2f Mbit/s source needs %d bytes for %d days plus %.2fx headroom = %d bytes; starting now means the run fills the disk and stops with disk_budget_exceeded, so free space or lower the bit rate first",
		free, float64(measuredBudget)*8/1e6, measuredBudget, days, headroom, need)
}
