// Package labelui is the box marking tool: a human accepts or rejects what the
// detector proposed, and draws what it missed.
//
// # Why this exists and what it is for
//
// A false-positive rate is a claim about what a human would have dismissed. Only a
// human can say that — no arithmetic over a detector's own output produces it, and
// this package is the only place that claim can come from.
//
// The second half matters at least as much. Without a way to DRAW an object the
// detector missed, recall is not measurable: a frame with no detection carries no
// information, because "the detector found nothing" and "there was nothing" are the
// same row. A drawn box is the only thing that tells them apart, which is why this
// has a drag gesture and not only a verdict key.
//
// # Why a local page and not a desktop tool
//
// A reviewer sits next to the footage. A reviewer has to be able to accept, reject,
// draw, and go back — thousands of times, over an afternoon. That is a keyboard and
// a mouse and a big picture, not a form.
//
// It is served on loopback by `pocctl label --addr`. The alternative — writing the
// judgements to a file for import afterwards — is how they get written to a file for
// import afterwards and never done, and the rate stays NOT RUN forever.
//
// # The boundary
//
// This package holds storage and a page. It does not know what a detector is: it
// reads `box_observations` and it draws what it finds. That is what lets it be
// tested without a model, a session, or a database — the three things a reviewer
// does not have.
package labelui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// Store is the storage this tool needs, as an interface.
//
// It is an interface so a test can drive the whole page without a database, and so
// the boundary is stated in the type rather than only in a comment. The three
// methods are exactly the storage the tool uses; anything else a caller wants is
// something this tool should not be doing.
type Store interface {
	BoxObservations(ctx context.Context, runID string) ([]store.BoxObservation, error)
	PutLabel(ctx context.Context, l store.Label) error
	PutLabelObject(ctx context.Context, o store.LabelObject) error
	Labels(ctx context.Context, runID string) ([]store.Label, error)
	LabelObjects(ctx context.Context, runID string) ([]store.LabelObject, error)
	LabelTally(ctx context.Context, runID string) (store.LabelCounts, error)
	ProposedBoxCount(ctx context.Context, runID string) (int, error)
}

// Options configure the tool.
type Options struct {
	// RunID is whose boxes are being marked. It is required and there is no default:
	// marking the wrong run's boxes produces a rate about material nobody measured.
	RunID string
	// Store is the storage.
	Store Store
	// Addr is where to listen. Loopback only, and the listener REFUSES a non-loopback
	// address.
	Addr string
	// By is who is marking, recorded on every verdict. A false-positive rate with no
	// name against it is a number from nowhere.
	By string
	// Logf is optional.
	Logf func(string, ...any)
}

// Frame is one frame for the page: the boxes to judge and the marks already made.
type Frame struct {
	Seq       int64                  `json:"seq"`
	Boxes     []store.BoxObservation `json:"boxes"`
	Labels    []store.Label          `json:"labels"`
	Objects   []store.LabelObject    `json:"objects"`
	Measured  bool                   `json:"measured"`
	FrameSkip bool                   `json:"frame_skipped"`
}

// Session is everything the page needs for a run, and it is computed once so the
// page does not re-read the database per keystroke.
type Session struct {
	RunID    string  `json:"run_id"`
	By       string  `json:"by"`
	Frames   []Frame `json:"frames"`
	Proposed int     `json:"proposed"`
	Marked   int     `json:"marked"`
	Accept   int     `json:"accepted"`
	Reject   int     `json:"reject"`
	// Unmarked is printed on the page at all times, because a reviewer who cannot
	// see how much is left will stop, and a stopped session is indistinguishable
	// from a finished one.
	Unmarked int `json:"unmarked"`
}

// Load reads a run's boxes and its existing marks.
func Load(ctx context.Context, s Store, runID string) (*Session, error) {
	rows, err := s.BoxObservations(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("label: the box log could not be read: %w", err)
	}
	labels, err := s.Labels(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("label: the labels could not be read: %w", err)
	}
	objects, err := s.LabelObjects(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("label: the drawn objects could not be read: %w", err)
	}
	tally, err := s.LabelTally(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("label: the label tally could not be read: %w", err)
	}
	proposed, err := s.ProposedBoxCount(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("label: the proposed-box count could not be read: %w", err)
	}

	byFrame := map[int64]*Frame{}
	order := []int64{}
	for _, r := range rows {
		if _, ok := byFrame[r.FrameSeq]; !ok {
			byFrame[r.FrameSeq] = &Frame{Seq: r.FrameSeq}
			order = append(order, r.FrameSeq)
		}
		f := byFrame[r.FrameSeq]
		f.Measured = f.Measured || r.Measured
		// A row that is NOT measured is a frame the pipeline skipped, and it is shown
		// as such rather than as an empty frame — otherwise a reviewer marks a frame
		// nobody asked about and the mark is about nothing.
		if !r.Measured {
			f.FrameSkip = true
			continue
		}
		f.Boxes = append(f.Boxes, r)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	for _, l := range labels {
		if f, ok := byFrame[l.FrameSeq]; ok {
			f.Labels = append(f.Labels, l)
		}
	}
	for _, o := range objects {
		if f, ok := byFrame[o.FrameSeq]; ok {
			f.Objects = append(f.Objects, o)
		}
	}

	sess := &Session{
		RunID: runID, Proposed: proposed,
		Marked: tally.Marked, Accept: tally.Accepted, Reject: tally.Rejected,
	}
	sess.Unmarked = proposed - tally.Marked
	if sess.Unmarked < 0 {
		sess.Unmarked = 0
	}
	for _, seq := range order {
		sess.Frames = append(sess.Frames, *byFrame[seq])
	}
	return sess, nil
}

// BoxKey identifies one box within a frame.
//
// It is the coordinates, rendered as a string, and that is deliberate: the key has
// to be stable across a reload so marking the same box twice is an UPDATE of one
// judgement rather than two rows that disagree. It uses the TRUNCATED thousandths
// the store writes, for the same reason — a key built from more precision than the
// store kept would not match on reload.
//
// And a box with no key is not markable, because there is nothing to attach the
// verdict to.
func BoxKey(o store.BoxObservation) string {
	return fmt.Sprintf("%d,%d,%d,%d", o.Top, o.Left, o.Bottom, o.Right)
}

// Judge records an accept or a reject.
//
// It refuses a verdict that is not one of the two, refuses an unnamed verdict, and
// refuses an empty box key — all three because a false-positive rate needs a
// denominator of decidable verdicts, and any of the three silently widens it.
func Judge(ctx context.Context, s Store, l store.Label) error {
	l.Verdict = strings.ToLower(strings.TrimSpace(l.Verdict))
	if l.Verdict != store.LabelAccept && l.Verdict != store.LabelReject {
		return fmt.Errorf("label: %q is not a verdict. It is %q or %q — a rate needs a denominator "+
			"of decidable verdicts, and 'maybe' widens it", l.Verdict, store.LabelAccept, store.LabelReject)
	}
	if strings.TrimSpace(l.By) == "" {
		return fmt.Errorf("label: a verdict needs a name. A false-positive rate with nobody against it " +
			"is a number from nowhere")
	}
	if strings.TrimSpace(l.BoxKey) == "" {
		return fmt.Errorf("label: a verdict needs the box it is about")
	}
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	return s.PutLabel(ctx, l)
}

// Draw records an object a human marked that the detector did not propose.
//
// This is the recall half. A frame with no detection and no drawn object says the
// detector found everything there was to find; a frame with no detection and a
// drawn object says it found nothing there was to find. Without this row the two
// are the same row and no arithmetic over the table can tell them apart.
func Draw(ctx context.Context, s Store, o store.LabelObject) error {
	if strings.TrimSpace(o.By) == "" {
		return fmt.Errorf("label: a drawn object needs a name, for the same reason a verdict does")
	}
	if strings.TrimSpace(o.Class) == "" {
		return fmt.Errorf("label: a drawn object needs a class. An unnamed box is a rectangle and a " +
			"reviewer cannot tell a mistake from a marking of something nobody has a word for")
	}
	if o.At.IsZero() {
		o.At = time.Now().UTC()
	}
	return s.PutLabelObject(ctx, o)
}

// Serve starts the page on loopback and blocks.
//
// The loopback refusal is structural and not advisory: a marking session serves
// unannotated footage and a human's judgements, and neither belongs on an
// interface somebody else can reach. A caller who wants that has to write it
// themselves, having thought about it.
func Serve(ctx context.Context, opt Options) error {
	if opt.RunID == "" {
		return fmt.Errorf("label: no run was named. Marking the wrong run's boxes produces a rate " +
			"about material nobody measured")
	}
	if opt.Store == nil {
		return fmt.Errorf("label: no store was supplied; a marking session writes judgements and " +
			"there is nothing to write them to")
	}
	if opt.By == "" {
		opt.By = os.Getenv("SP_POC_LABELLED_BY")
	}
	if opt.By == "" {
		return fmt.Errorf("label: no reviewer name. Pass --by or set SP_POC_LABELLED_BY: every verdict " +
			"is recorded against a name, and a rate with nobody against it is a number from nowhere")
	}
	if opt.Addr == "" {
		opt.Addr = "127.0.0.1:8096"
	}
	if err := checkLoopback(opt.Addr); err != nil {
		return err
	}
	mux := routes(opt)
	ln, err := net.Listen("tcp", opt.Addr)
	if err != nil {
		return fmt.Errorf("label: %s is not available: %w", opt.Addr, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if opt.Logf != nil {
		opt.Logf("label: serving http://%s — %s", ln.Addr(), opt.RunID)
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// checkLoopback refuses any address that is not on the loopback interface.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("label: %q is not an address: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("label: %q has no host, which means every interface. This page serves the "+
			"footage and the reviewer's judgements, so it binds loopback and you name a host", addr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// `localhost` is loopback and a caller will write it, so it is resolved rather
		// than refused. Refusing it would push every caller to write the IP, which is
		// a worse outcome than resolving one name that means 127.0.0.1 on every
		// platform this runs on — and the resolution is what makes the refusal
		// trustworthy, because `evil.example.com` does not resolve to a loopback
		// address and is refused below.
		if strings.EqualFold(host, "localhost") {
			return nil
		}
		return fmt.Errorf("label: %q is not an IP address; name one, and a loopback one", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("label: refusing to serve on %s. This page serves the footage and the "+
			"reviewer's judgements about it, and neither belongs on an interface somebody else can "+
			"reach. Bind 127.0.0.1.", host)
	}
	return nil
}

// routes builds the page's handlers.
func routes(opt Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		sess, err := Load(r.Context(), opt.Store, opt.RunID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw, err := json.Marshal(sess)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(pageHTML))
		_, _ = w.Write([]byte("<script>window.__SESSION__="))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte(";</script>"))
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		sess, err := Load(r.Context(), opt.Store, opt.RunID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, sess)
	})
	mux.HandleFunc("/api/label", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "this endpoint takes POST", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			FrameSeq int64  `json:"frame_seq"`
			BoxKey   string `json:"box_key"`
			Verdict  string `json:"verdict"`
			Class    string `json:"class"`
			Note     string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "the request body is not JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		err := Judge(r.Context(), opt.Store, store.Label{
			RunID: opt.RunID, FrameSeq: in.FrameSeq, BoxKey: in.BoxKey,
			Verdict: in.Verdict, Class: in.Class, By: opt.By, Note: in.Note,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/object", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "this endpoint takes POST", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			FrameSeq int64  `json:"frame_seq"`
			Class    string `json:"class"`
			// Four separate fields rather than one struct with four comma-joined
			// tags: a tag like `json:"top,left"` does not bind two fields to one
			// key, it binds both to the SAME key, and the second silently wins.
			Top    int `json:"top"`
			Left   int `json:"left"`
			Bottom int `json:"bottom"`
			Right  int `json:"right"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "the request body is not JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		next := 0
		existing, err := opt.Store.LabelObjects(r.Context(), opt.RunID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, o := range existing {
			if o.FrameSeq == in.FrameSeq && o.Idx >= next {
				next = o.Idx + 1
			}
		}
		err = Draw(r.Context(), opt.Store, store.LabelObject{
			RunID: opt.RunID, FrameSeq: in.FrameSeq, Idx: next, Class: in.Class,
			Top: in.Top, Left: in.Left, Bottom: in.Bottom, Right: in.Right, By: opt.By,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "idx": next})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	raw, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(raw)
}
