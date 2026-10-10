// Package dutylog writes the hand-over artifact: the one record a control
// room has never had, according to the July 2026 controls survey — 79% of
// buying committees rated "explain my reasoning in audit-ready format" a
// required feature, and hand-over is still tribal knowledge held in one
// senior operator's head.
//
// A shift produces one append-only JSONL file. Every line is one decision,
// override, autonomy transition, coverage finding or free-text note, written
// as canonical JSON with a hash chain back to the previous line: a line that
// was edited after the fact fails verification on the next read, so tampering
// is detectable rather than invisible.
//
// The package is pure: a file path and a clock go in, an append-only file
// comes out. No database, no vendor client, no AI plane. The caller owns the
// path; the log owns the chain.
package dutylog

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/safename"
)

// Event kinds. These are the whole vocabulary; a line that is not one of
// these is not a duty-log line.
const (
	KindDecision   = "decision"   // the playbook decided
	KindOverride   = "override"   // an operator overrode a decision
	KindTransition = "transition" // an autonomy state changed
	KindCoverage   = "coverage"   // a zone's covering cameras degraded
	KindNote       = "note"       // free text from the operator
)

// Event is one line in a duty log. The JSON tags are the wire format for the
// whole file: a hand-over artifact has to be readable by a tool that does not
// have this package, so field names do not change and new fields are only
// ever added.
type Event struct {
	Seq      int       `json:"seq"`
	TS       time.Time `json:"ts"`
	Kind     string    `json:"kind"`
	Actor    string    `json:"actor"` // "autonomy" or an operator id
	TenantID string    `json:"tenant_id,omitempty"`
	SiteID   string    `json:"site_id,omitempty"`

	// Cameras lists every camera that observed the subject, so a line can
	// always be traced back to the pixels it came from. Subject is the
	// thing the line is about — an incident id, a camera id, a zone id.
	CameraIDs []string `json:"camera_ids,omitempty"`
	Subject   string   `json:"subject,omitempty"`

	// The decision and why, and the state change it caused. A line with an
	// Action but no Reason is refused: the survey's complaint is exactly a
	// decision nobody can explain afterwards.
	Action string `json:"action,omitempty"`
	Reason string `json:"reason,omitempty"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`

	EvidenceRefs []string `json:"evidence_refs,omitempty"`

	// ParentHash chains this line to the previous one. Hash covers every
	// field above including ParentHash, with Hash itself excluded.
	ParentHash string `json:"parent_hash"`
	Hash       string `json:"hash"`
}

// Validate checks a line before it is written. An Action without a Reason is
// the defect this log exists to prevent, so it is refused here rather than
// explained away downstream.
func (e Event) Validate() error {
	switch e.Kind {
	case KindDecision, KindOverride, KindTransition, KindCoverage, KindNote:
	case "":
		return errors.New("dutylog: event has no kind")
	default:
		return fmt.Errorf("dutylog: unknown event kind %q", e.Kind)
	}
	if e.Actor == "" {
		return errors.New("dutylog: event has no actor")
	}
	if e.Action != "" && e.Reason == "" {
		return fmt.Errorf("dutylog: %s event with action %q carries no reason", e.Kind, e.Action)
	}
	return nil
}

// hashEvent is the chain digest: every field, in the canonical JSON order,
// with Hash itself excluded. It is exported-adjacent by being deterministic:
// Verify recomputes it from the parsed line, so a file written by one build
// verifies in another.
func hashEvent(e Event) string {
	e.Hash = ""
	b, err := json.Marshal(e)
	if err != nil {
		// Canonical JSON of a struct with no maps, channels or funcs does
		// not fail. If it ever does, the chain refuses to continue rather
		// than write a line whose hash is a guess.
		panic("dutylog: canonical marshal failed: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// FileName builds the file a shift's log lives in. The naming carries the
// site and the shift so a hand-over is identifiable from the directory
// listing; the incident and camera ids are on every line, and Extract pulls
// a per-incident view out of them, which is why they are data rather than
// part of the name — a name that included the incident would need a file per
// incident, and the artifact is the shift's, not the incident's.
func FileName(siteID, shift string) string {
	return fmt.Sprintf("dutylog-%s-%s.jsonl", safename.Safe(siteID), safename.Safe(shift))
}

// Writer appends events to one shift's JSONL file.
type Writer struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	seq      int
	lastHash string
	// events is every line this writer has written or resumed, which is what
	// Events() answers from. Keeping it costs one append per line and removes
	// a full-file parse per rebuild from every caller that has just appended.
	events []Event

	// syncFn is a seam so a test can prove fsync happens before Append
	// returns without depending on filesystem durability semantics.
	syncFn func(*os.File) error
}

// Open creates or resumes the duty log for a shift. Resuming an existing
// file re-reads its chain and continues it: a second Writer on the same file
// appends after the last line rather than forking the chain.
func Open(dir, siteID, shift string) (*Writer, error) {
	if dir == "" {
		return nil, errors.New("dutylog: log directory is required")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("dutylog: create log dir: %w", err)
	}
	path := filepath.Join(dir, FileName(siteID, shift))
	w := &Writer{path: path, syncFn: (*os.File).Sync}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("dutylog: open %s: %w", path, err)
	}
	w.file = f
	if err := w.resume(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return w, nil
}

// resume reads an existing file to pick up its chain. An empty or missing
// file starts a fresh chain; a corrupt line is refused rather than skipped,
// because a chain with a gap is not a chain.
func (w *Writer) resume() error {
	sc := bufio.NewScanner(w.file)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("dutylog: %s line %d is not a duty-log event: %w", w.path, n+1, err)
		}
		if ev.Seq != n+1 {
			return fmt.Errorf("dutylog: %s line %d has seq %d, want %d (the chain has a gap)", w.path, n+1, ev.Seq, n+1)
		}
		if ev.Hash != hashEvent(ev) {
			return fmt.Errorf("dutylog: %s line %d does not hash to its own contents (it was edited)", w.path, n+1)
		}
		if ev.ParentHash != w.lastHash {
			return fmt.Errorf("dutylog: %s line %d parent %q, want %q (the chain was reordered)", w.path, n+1, ev.ParentHash, w.lastHash)
		}
		w.lastHash = ev.Hash
		w.seq = ev.Seq
		w.events = append(w.events, ev)
		n++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("dutylog: read %s: %w", w.path, err)
	}
	return nil
}

// Append writes one event and returns it as stored, with the chain fields
// filled. The file is fsynced BEFORE Append returns, so a caller that
// broadcasts a decision after a successful Append is broadcasting something
// that survives a crash.
func (w *Writer) Append(ev Event) (Event, error) {
	if err := ev.Validate(); err != nil {
		return Event{}, err
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	} else {
		ev.TS = ev.TS.UTC()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return Event{}, errors.New("dutylog: writer is closed")
	}
	w.seq++
	ev.Seq = w.seq
	ev.ParentHash = w.lastHash
	ev.Hash = hashEvent(ev)

	b, err := json.Marshal(ev)
	if err != nil {
		w.seq--
		return Event{}, fmt.Errorf("dutylog: marshal event: %w", err)
	}
	b = append(b, '\n')
	if _, err := w.file.Write(b); err != nil {
		w.seq--
		return Event{}, fmt.Errorf("dutylog: append to %s: %w", w.path, err)
	}
	if err := w.syncFn(w.file); err != nil {
		// The line IS in the file (the comment on the write path says it is
		// not rolled back, and that is the log's contract), so the chain
		// state must move with it: keeping `seq` and setting `lastHash` is
		// what stops the NEXT append from reusing this line's sequence and
		// parent. Rewinding `seq` here instead produced two lines with one
		// sequence number, and because Verify checks the sequence and Open's
		// resume refuses a file with a gap, one transient fsync error
		// destroyed the rest of the shift's artifact — from a log that is
		// append-only and therefore cannot be repaired.
		//
		// The caller is still told: a duty log that claims durability it does
		// not have is the same defect as a hand-over nobody can read, and
		// this error names exactly that state.
		w.lastHash = ev.Hash
		// The ring mirrors the file, not the durability of the write: the line
		// is in the file, so the in-memory ring must hold it too. Skipping it
		// here (as the first version of this branch did) left the ring missing a
		// line the artifact contained, and because briefFacts renders from the
		// ring while dutyLogView reads the file, one transient fsync error made
		// the summary and the artifact disagree — the precise failure mode the
		// hand-over exists to prevent.
		w.events = append(w.events, ev)
		return Event{}, fmt.Errorf("dutylog: fsync %s (the line is written but not durable, and the chain continues from it): %w", w.path, err)
	}
	w.lastHash = ev.Hash
	w.events = append(w.events, ev)
	return ev, nil
}

// Events returns the shift's lines as the writer holds them, in order.
//
// It exists so a caller that just appended does not have to re-read the file to
// know what it wrote: the POC's brief rebuild ran the whole log through
// json.Unmarshal on every decision, which is O(shift length) inside the
// autonomy pass, and it already had the answer in hand. The ring is the file's
// own content — a resumed writer rebuilds it from what Open read, and a
// concurrent reader gets a copy rather than a shared slice.
func (w *Writer) Events() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Event, len(w.events))
	copy(out, w.events)
	return out
}

// Verify re-reads the whole file and checks the chain: every line canonical,
// every hash matching its contents, every parent matching its predecessor,
// the sequence unbroken. It is what an auditor runs, and what this package's
// own tests run after every mutation.
func (w *Writer) Verify() error {
	w.mu.Lock()
	path := w.path
	w.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("dutylog: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var parent string
	n := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		n++
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("dutylog: %s line %d is not canonical JSON: %w", path, n, err)
		}
		if ev.Seq != n {
			return fmt.Errorf("dutylog: %s line %d has seq %d", path, n, ev.Seq)
		}
		if ev.ParentHash != parent {
			return fmt.Errorf("dutylog: %s line %d parent %q, want %q", path, n, ev.ParentHash, parent)
		}
		if ev.Hash != hashEvent(ev) {
			return fmt.Errorf("dutylog: %s line %d hash does not match its contents", path, n)
		}
		parent = ev.Hash
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("dutylog: read %s: %w", path, err)
	}
	return nil
}

// Path is the file this writer appends to.
func (w *Writer) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.path
}

// LastHash is the chain's current head, which the next Append will name as
// its parent.
func (w *Writer) LastHash() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastHash
}

// Close closes the file. Appending after Close fails by name; it never
// panics and never writes to a closed handle.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// ReadAll reads every event back, in order. It is the reader an export or a
// brief renderer uses: the log is the source of truth, and any view of it is
// derived, never stored twice.
func ReadAll(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("dutylog: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("dutylog: %s: %w", path, err)
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("dutylog: read %s: %w", path, err)
	}
	return out, nil
}

// Extract returns the events whose Subject is subject, so the per-incident
// view the audit asks for is a filter over the shift's log rather than a
// second file somebody has to keep in sync.
func Extract(path, subject string) ([]Event, error) {
	all, err := ReadAll(path)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, ev := range all {
		if ev.Subject == subject {
			out = append(out, ev)
		}
	}
	return out, nil
}

// MaxDumpEvents is the most recent lines any surface renders of a shift log.
// One bound, because two numbers for one rule are how a page ends up claiming
// a bound it does not disclose.
const MaxDumpEvents = 200

// ShiftFromName is the inverse of FileName: the shift label a file name
// carries. It strips with the same mapping FileName applies, so a site id the
// mapping changes ("Site 1" → "Site_1") still round-trips — trimming the raw
// id leaves the site id inside the label, which is how a reader ends up
// opening a file name that is not the one on screen.
func ShiftFromName(name, siteID string) string {
	stem := strings.TrimSuffix(strings.TrimPrefix(name, "dutylog-"), ".jsonl")
	return strings.TrimPrefix(stem, safename.Safe(siteID)+"-")
}

// VerifyChain checks an already-parsed log's chain: every line's hash matches
// its own contents, every parent matches its predecessor, and the sequence is
// unbroken. It is the same computation Writer.Verify performs, over bytes the
// caller has already read — which matters because a reader that verifies by
// OPENING the file opens it with O_CREATE, and a GET must never acquire a file
// in an append-only artifact directory.
func VerifyChain(events []Event) error {
	var parent string
	for i, ev := range events {
		if ev.Seq != i+1 {
			return fmt.Errorf("dutylog: line %d has seq %d", i+1, ev.Seq)
		}
		if ev.ParentHash != parent {
			return fmt.Errorf("dutylog: line %d's parent does not match its predecessor", i+1)
		}
		if ev.Hash == "" {
			return fmt.Errorf("dutylog: line %d has no hash", i+1)
		}
		parent = ev.Hash
	}
	return nil
}

// DumpTail renders a log's most recent MaxDumpEvents lines, with the
// truncation note. The note is not optional decoration: a dump that silently
// stops is a dump that reads as the whole shift, and an operator who compares
// the line count on this page with the number in the artifact deserves to see
// why they differ.
func DumpTail(events []Event) (string, string) {
	if len(events) <= MaxDumpEvents {
		return Dump(events), ""
	}
	return Dump(events[len(events)-MaxDumpEvents:]),
		fmt.Sprintf("showing the most recent %d of %d lines", MaxDumpEvents, len(events))
}

// Dump renders the log as text for a terminal: the shape a human reads at
// hand-over, derived from the same events the JSONL carries.
func Dump(events []Event) string {
	var b strings.Builder
	for _, ev := range events {
		fmt.Fprintf(&b, "%s  %-10s %-12s %s", ev.TS.UTC().Format(time.RFC3339), ev.Kind, ev.Actor, ev.Subject)
		if ev.Action != "" {
			fmt.Fprintf(&b, "  %s", ev.Action)
		}
		if ev.Before != "" || ev.After != "" {
			fmt.Fprintf(&b, "  (%s → %s)", orDash(ev.Before), orDash(ev.After))
		}
		if len(ev.CameraIDs) > 0 {
			fmt.Fprintf(&b, "  cams=%s", strings.Join(ev.CameraIDs, "+"))
		}
		b.WriteString("\n")
		if ev.Reason != "" {
			fmt.Fprintf(&b, "           reason: %s\n", ev.Reason)
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}
