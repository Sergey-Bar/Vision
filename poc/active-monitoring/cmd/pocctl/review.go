package main

import (
	"context"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// cmdReview writes an HTML contact sheet for a run: every pushed event, the clip
// that proves it, and the exact command that records a verdict on it.
//
// WHY IT EXISTS. The false-positive rate is the one number here that no amount of
// code can produce, because it needs a person to look at footage and say which
// events were wrong. Every step up to that point is built; this is the step where a
// person is handed ten tags and a folder of files, and the realistic outcome is that
// the window is abandoned halfway.
//
// So the window is one page: watch the clip, read the tag beside it, copy the
// command beside that. Nothing to remember. Every field comes from the STORE, so the
// sheet cannot disagree with the report about what happened.
//
// It is a static file. No server, no callback, no writes from a browser — the
// verdict still goes through `pocctl mark`, which refuses a tag the run never
// pushed, so the rate cannot be inflated by marking events nobody saw.
//
//	pocctl review --data-root <root> [--out <file.html>]
func cmdReview(args []string) error {
	flags, err := flagSet("review", map[string]string{"data-root": "", "run": "", "out": "", "limit": "200"}, args)
	if err != nil {
		return err
	}
	root := flags["data-root"]
	if root == "" {
		root = defaultDataRoot()
	}
	if root == "" {
		return fmt.Errorf("--data-root is required; the sheet is generated from the store and there is nowhere else to read it from")
	}
	st, err := store.Open(root)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	runID := strings.TrimSpace(flags["run"])
	if runID == "" {
		run, err := st.LatestRun(ctx)
		if err != nil {
			return err
		}
		if run == nil {
			return fmt.Errorf("this store has no runs, so there is nothing to review")
		}
		runID = run.ID
	}
	limit := 200
	fmt.Sscan(flags["limit"], &limit)
	dets, err := st.Reviewable(ctx, runID, limit)
	if err != nil {
		return err
	}
	if len(dets) == 0 {
		return fmt.Errorf("run %s recorded no pushed events; there is nothing to review and nothing to mark", runID)
	}

	out := flags["out"]
	if out == "" {
		out = filepath.Join(root, "review.html")
	}
	rows := make([]reviewRow, 0, len(dets))
	for i, d := range dets {
		r := reviewRow{
			Index: i + 1, ID: d.DetectionID, At: d.At.Format("15:04:05"),
			Class: d.Class, Family: d.Family, Score: fmt.Sprintf("%.3f", d.Score),
			Box: fmt.Sprintf("%.3f %.3f %.3f %.3f", d.X1, d.Y1, d.X2, d.Y2),
			Tag: d.Tag, Marked: d.Verdict,
		}
		if d.ClipRef != "" {
			r.ClipRef = d.ClipRef
			r.ClipPath = filepath.Join(root, "evidence", filepath.FromSlash(d.ClipRef))
			if fi, err := os.Stat(r.ClipPath); err == nil && !fi.IsDir() {
				r.ClipExists = true
				r.Bytes = fi.Size()
			}
		}
		rows = append(rows, r)
	}

	tmpl, err := template.New("review").Parse(reviewPage)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("the sheet could not be created at %s: %w", out, err)
	}
	defer f.Close()
	judged := 0
	for _, r := range rows {
		if r.Marked != "" {
			judged++
		}
	}
	data := struct {
		Run    string
		Root   string
		Rows   []reviewRow
		Stamp  string
		Judged int
	}{runID, root, rows, time.Now().UTC().Format(time.RFC3339), judged}
	if err := tmpl.Execute(f, data); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", out)
	fmt.Printf("  %d event(s) for run %s, %d already judged\n", len(rows), runID, judged)
	if judged < len(rows) {
		fmt.Printf("  %d to go. Open the page, watch each clip, copy the command beside it.\n", len(rows)-judged)
	}
	return nil
}

type reviewRow struct {
	Index      int
	ID         string
	Tag        string
	At         string
	Class      string
	Family     string
	Score      string
	Box        string
	ClipRef    string
	ClipPath   string
	ClipExists bool
	Bytes      int64
	// Marked is the human verdict already recorded, or "" — and it is rendered
	// rather than hidden, because a rate computed over the events somebody looked
	// at and the count of events there were is a number a judge will check.
	Marked string
}

const reviewCSS = `
body{font:14px/1.55 system-ui,-apple-system,Segoe UI,sans-serif;margin:0;padding:28px;background:#0f1115;color:#e6e8ec}
h1{font-size:20px;margin:0 0 6px}
.meta{color:#8b93a1;margin-bottom:22px;max-width:70ch}
.meta code{background:#1a1f27;padding:1px 5px;border-radius:3px}
table{border-collapse:collapse;width:100%}
th,td{border-bottom:1px solid #262b33;padding:10px;text-align:left;vertical-align:top}
th{color:#8b93a1;font-weight:600;font-size:11px;text-transform:uppercase;letter-spacing:.05em}
video{width:280px;border-radius:5px;background:#000;display:block}
code{background:#1a1f27;padding:2px 6px;border-radius:3px;font-size:12px;display:inline-block;word-break:break-all}
.marked{color:#4ade80;font-weight:600}
.unjudgeable{color:#fbbf24;font-weight:600}
.missing{color:#f87171}
.muted{color:#8b93a1}
.cmd{margin:2px 0}
`

const reviewPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Review {{.Run}}</title><style>` + reviewCSS + `</style></head><body>
<h1>Review &mdash; {{.Run}}</h1>
<div class="meta">
  {{len .Rows}} event(s), {{.Judged}} already judged. Generated {{.Stamp}} from the store's own rows, so
  this page cannot disagree with the report about what happened.<br>
  Every field here comes from a stored row. An event with no clip offers <b>no command and cannot be
  marked</b>: a judge asked to rule on an event they cannot see will rule on it wrongly, and the number
  that comes back will be a measurement of their guess rather than of the system.<br>
  The material in this run is labelled <code>synthetic</code>. Marking it measures the fixture, not
  the detector &mdash; say so when the number is quoted.
</div>
<table>
<tr><th>#</th><th>clip</th><th>at</th><th>class</th><th>score</th><th>box (0&ndash;1)</th><th>verdict</th></tr>
{{range .Rows}}<tr>
  <td>{{.Index}}</td>
  <td>{{if .ClipExists}}<video src="evidence/{{.ClipRef}}" controls preload="metadata"></video>
      {{else}}<span class="missing">no clip on disk</span>{{if .ClipRef}}<br><span class="muted">{{.ClipRef}}</span>{{end}}{{end}}</td>
  <td>{{.At}}<br><span class="muted">{{.ID}}</span><br><code>{{.Tag}}</code></td>
  <td>{{.Class}}<br><span class="muted">{{.Family}}</span></td>
  <td>{{.Score}}</td>
  <td class="muted">{{.Box}}</td>
  <td>{{if .Marked}}<span class="marked">{{.Marked}}</span>
      {{else if not .ClipExists}}<span class="unjudgeable">cannot be judged &mdash; no clip. Skip it.</span>
      {{else}}
      <div class="cmd"><code>pocctl mark {{.Tag}} --verdict true_positive --by YOUR-NAME --data-root {{$.Root}}</code></div>
      <div class="cmd"><code>pocctl mark {{.Tag}} --verdict false_positive --by YOUR-NAME --data-root {{$.Root}}</code></div>
      {{end}}</td>
</tr>
{{end}}</table>
</body></html>
`
