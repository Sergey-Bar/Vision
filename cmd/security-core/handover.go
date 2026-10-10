package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/api"
	"github.com/Sergey-Bar/Ai-Captain/internal/brief"
	"github.com/Sergey-Bar/Ai-Captain/internal/dutylog"
)

// The console hand-over page's reader, wired to the site's duty-log directory.
//
// It implements `api.HandoverReader` from the same packages the capture path
// writes the artifact with - `internal/dutylog` for the chain and
// `internal/brief` for the rendering - so the console page and the POC are one
// implementation of the hand-over rather than two. The brief's mapping is
// `brief.FactsFromEvents`, the same function the POC calls, so one artifact
// cannot render two different briefs.
//
// A site with no `SP_HANDOVER_DIR` returns a named error, and the page renders
// it: "the hand-over artifact is not configured for this site". That is the
// honest answer, and strictly better than a page that renders empty - an empty
// hand-over reads as "nothing happened", the one sentence this product must
// never put in front of an operator.

// DutyLogReader reads the shift's hand-over artifact for the console.
type DutyLogReader struct {
	// Dir is where the shift logs live: one hash-chained JSONL per shift.
	Dir string
	// DeployedSite names the site, which is part of the file name. It is
	// named DeployedSite rather than SiteID because the interface method
	// SiteID() reports it and Go forbids a field and method with one name.
	DeployedSite string
	// Now is the clock, injected so tests are not time-dependent.
	Now func() time.Time
}

// NewDutyLogReader builds the reader the console hand-over page reads.
func NewDutyLogReader(dir, siteID string, now func() time.Time) *DutyLogReader {
	return &DutyLogReader{Dir: dir, DeployedSite: siteID, Now: now}
}

// SiteID is the site whose artifact this reader serves: the deployment's
// configured site, which is what the console's authorization decision has to
// be made against — the principal's own scope says nothing about which site's
// artifact is about to be read.
func (r *DutyLogReader) SiteID() string { return r.DeployedSite }

// shift is the current shift's day label. The capture path appends its run mode
// to it, and this reader deliberately does not guess the mode.
func (r *DutyLogReader) shift() string {
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	return now.Format("2006-01-02")
}

// shiftFile resolves the current shift's file: the newest `dutylog-<site>-<date>*.jsonl`
// in the directory.
//
// Globbing rather than computing a suffix is not laziness - the writer's label
// is `<date>-<mode>` and the reader cannot know the mode. A reader that
// hardcodes `<date>-core` renders "no artifact" against a directory holding a
// perfectly good one, which is exactly what this path did first.
func (r *DutyLogReader) shiftFile() string {
	if r.Dir == "" {
		return ""
	}
	// The writer's name is `dutylog-<site>-<date>-<mode>.jsonl`, and the reader
	// does not know the mode. So the prefix is the dated name without a mode
	// suffix, and the fallback is a dated name that does not exist - which
	// ReadAll reports as "no artifact", the named absence.
	prefix := strings.TrimSuffix(dutylog.FileName(r.DeployedSite, r.shift()), ".jsonl") + "-"
	byName := filepath.Join(r.Dir, dutylog.FileName(r.DeployedSite, r.shift()+"-unknown"))
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return byName
	}
	newest, newestMod := "", time.Time{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if info.ModTime().After(newestMod) {
			newest, newestMod = filepath.Join(r.Dir, e.Name()), info.ModTime()
		}
	}
	if newest == "" {
		return byName
	}
	return newest
}

// Read returns the brief and the artifact's own state for one shift.
//
// One file read: the previous shape called two reader methods that each opened
// and parsed the whole log, so a page render was four full passes - two of them
// re-hashing every line - for a dump capped at 200 events.
func (r *DutyLogReader) Read(_ context.Context, _, _ string) (api.HandoverView, error) {
	if r.Dir == "" {
		return api.HandoverView{}, errors.New("the hand-over directory is not configured for this site")
	}
	path := r.shiftFile()
	events, err := dutylog.ReadAll(path)
	if err != nil {
		return api.HandoverView{}, r.configured(err)
	}
	if len(events) == 0 {
		return api.HandoverView{}, fmt.Errorf("no hand-over artifact at %s for this shift", path)
	}

	// The shift label is the file name minus the "dutylog-<safe(site)>-"
	// prefix — with the SAME mapping that built the name. Trimming the raw
	// site id instead worked only while the site id contained nothing
	// safename changes, and produced a label that still carried the site, so
	// the verification step below opened (and O_CREATEd) a file that is not
	// the artifact on screen. A read-only page must not write one.
	shift := dutylog.ShiftFromName(filepath.Base(path), r.DeployedSite)

	// The chain is verified over the events already parsed, not by opening
	// the file a second time. `dutylog.Open` takes O_CREATE, so verifying by
	// re-opening is how a GET acquires a file in the operator's append-only
	// artifact directory; the writer's own resume/verify is the same
	// computation over the same bytes.
	verification := "ok"
	if err := dutylog.VerifyChain(events); err != nil {
		verification = err.Error()
	}

	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	facts := brief.FactsFromEvents(events)
	facts.Shift = shift
	facts.SiteID = r.DeployedSite
	facts.GeneratedAt = now
	rendered := brief.Render(facts)
	dump, note := dutylog.DumpTail(events)

	return api.HandoverView{
		Brief: &rendered,
		State: api.DutyLogState{
			Path:         path,
			Shift:        shift,
			SiteID:       r.DeployedSite,
			Lines:        len(events),
			HeadHash:     events[len(events)-1].Hash,
			Verification: verification,
			Dump:         dump,
			DumpNote:     note,
		},
	}, nil
}

// configured turns "nothing configured" and "nothing written yet" into the
// named absences the page renders.
func (r *DutyLogReader) configured(err error) error {
	if r.Dir == "" {
		return errors.New("the hand-over directory is not configured for this site")
	}
	if os.IsNotExist(err) {
		return fmt.Errorf("no hand-over artifact in %s", r.Dir)
	}
	return err
}
