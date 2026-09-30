package ui

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// The renderers. They read the same Data the JSON feed does, so the page and the
// machine-readable summary can never disagree about a number.
func page(title, body string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + esc(title) + `</title><style>` + style + `</style></head><body>` +
		body + `</body></html>`
}

func header(d Data, verdict string, on string) string {
	l := d.Live
	age := time.Since(l.At)
	dot := "livedot"
	switch {
	case len(l.Frame) == 0:
		dot += " dead"
	case age > 30*time.Second:
		dot += " stale"
	}
	var b strings.Builder
	b.WriteString(`<header><div class="hrow">`)
	b.WriteString(`<span class="livedot-wrap"><span class="` + dot + `"></span></span>`)
	b.WriteString(`<span class="brand">active monitoring</span><span class="sep"></span>`)
	fmt.Fprintf(&b, `<span class="truth">%s</span>`, truthLine(d))
	fmt.Fprintf(&b, `<span class="sep"></span><span class="truth"><b>source:</b> %s &nbsp;·&nbsp; <b>mode:</b> %s &nbsp;·&nbsp; <b>clock:</b> %s</span>`,
		esc(orNone(l.Source, "not stated")), esc(orNone(l.Mode, "not stated")), esc(orNone(l.ClockSource, "not stated")))
	b.WriteString(`<nav><a href="/" class="` + onClass(on, "live") + `">live</a><a href="/board" class="` + onClass(on, "board") + `">7-day board</a></nav>`)
	b.WriteString(`</div></header>`)
	return b.String()
}

func onClass(on, want string) string {
	if on == want {
		return "on"
	}
	return ""
}

func onClassBool(b bool) string {
	if b {
		return "on"
	}
	return ""
}

// truthLine is the first thing on the page. Everything else is subordinate to it:
// what this run is connected to, and what it is not connected to.
func truthLine(d Data) string {
	l := d.Live
	push := pushSentence(l, d)
	prov := orNone(l.Provenance, "provenance NOT STATED")
	return fmt.Sprintf(`<b>push:</b> %s &nbsp;·&nbsp; <b>footage:</b> %s &nbsp;·&nbsp; `+
		`<span class="warn">no Milestone server was reached</span>`, esc(push), esc(prov))
}

func kpi(k, v, s, cls string) string {
	return fmt.Sprintf(`<div class="kpi %s"><div class="k">%s</div><div class="v">%s</div>`+
		`<div class="s">%s</div></div>`, cls, esc(k), esc(v), esc(s))
}

func kpiRow(d Data, verdict string, provisional bool) string {
	c := d.Counts
	total, notDone, done := sumNonDone(c), sumNonDone(c), c["done"]
	vclass := "notrun"
	switch verdict {
	case "PASS":
		vclass = "pass"
	case "FAIL":
		vclass = "fail"
	}
	vd := verdict
	if provisional {
		vd = "PROVISIONAL"
	}
	totalTxt := countOr(c)
	if total == 0 {
		totalTxt = "0"
	}
	var b strings.Builder
	b.WriteString(`<div class="kpis">`)
	b.WriteString(kpi("verdict", vd, provisionalSuffix(provisional), vclass))
	b.WriteString(kpi("detections", totalTxt, fmt.Sprintf("%d terminal · %d not", done, notDone), ""))
	b.WriteString(kpi("clips verified", itoa(d.Evidence.Verified), fmt.Sprintf("%d stored · %d mismatched", d.Evidence.Stored, d.Evidence.Mismatched), ""))
	b.WriteString(kpi("pushes", itoa(d.Push.WithResult), fmt.Sprintf("%d attempts · %d unattributed", d.Push.Total, d.Push.Unattributed), ""))
	b.WriteString(kpi("heartbeats", itoa(d.Heartbeats), fmt.Sprintf("%d gap(s) over 30s", len(d.Gaps)), ""))
	b.WriteString(kpi("faults", itoa(len(d.Faults)), "recorded", ""))
	b.WriteString(kpi("elapsed", orNone(d.Elapsed, "—"), targetText(d), ""))
	b.WriteString(kpi("rss peak", humanBytes(d.Peaks.RSSBytes), "process", ""))
	b.WriteString(`</div>`)
	return b.String()
}

func targetText(d Data) string {
	if d.Run == nil {
		return "no run"
	}
	t := time.Duration(d.Run.TargetSeconds) * time.Second
	return "of a " + t.Round(time.Second).String() + " target"
}

// tile is one acceptance criterion. The verdict word is as prominent as the
// name, because on this surface the verdict is the thing being read.
func tile(c soak.Criterion) string {
	cls := "notrun"
	switch c.Verdict {
	case soak.VerdictPass:
		cls = "pass"
	case soak.VerdictFail:
		cls = "fail"
	}
	extra := ""
	if c.Missing != "" {
		extra = `<div class="ob">` + esc(clip(c.Missing, 150)) + `</div>`
	} else if c.Observed != "" {
		extra = `<div class="ob">` + esc(clip(c.Observed, 150)) + `</div>`
	}
	return fmt.Sprintf(`<div class="tile %s"><div class="id">%s</div><div class="nm">%s</div>`+
		`<div class="vd">%s</div>%s</div>`, cls, esc(c.ID), esc(c.Name), esc(string(c.Verdict)), extra)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func panel(title, tag, body string) string {
	t := ""
	if tag != "" {
		t = `<span class="tag">` + esc(tag) + `</span>`
	}
	return fmt.Sprintf(`<section class="panel"><h2>%s%s</h2><div class="body">%s</div></section>`, esc(title), t, body)
}

// faultCard is one recorded fault.
//
// `data-verdict` carries the verdict as a machine-readable attribute, not only
// as a colour and a phrase. The colour is what a person reads; the attribute is
// what a test reads, and a test that has to parse CSS classes to check a
// verdict is a test that breaks when the design changes.
func faultCard(f store.Fault) string {
	ok := f.Expected != "" && f.Expected == f.Observed
	verdict := "FAIL"
	note := "recorded"
	cls := "fault"
	if ok {
		verdict, note, cls = "PASS", "as rehearsed", "fault ok"
	} else if f.Expected != "" {
		note = "expected " + f.Expected + ", observed " + f.Observed
	}
	return fmt.Sprintf(`<div class="%s" data-verdict="%s"><div class="h"><span class="r">%s</span>`+
		`<span class="t">%s</span><span class="t">%s</span></div><div class="d">%s</div></div>`,
		cls, verdict, esc(f.Reason), f.At.Local().Format("15:04:05"), esc(note), esc(clip(f.Detail, 240)))
}

func handleLive(s *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		ctx := r.Context()
		d := s.Gather(ctx)
		verdict, provisional := s.verdictFor(ctx, d)

		var b strings.Builder
		b.WriteString(`<div class="wrap">`)
		b.WriteString(header(d, verdict, "live"))
		b.WriteString(kpiRow(d, verdict, provisional))

		b.WriteString(`<div class="cols">`)

		// ---- the live stage ------------------------------------------------
		var stage strings.Builder
		if len(d.Live.Frame) == 0 {
			// The SAME phrase the still endpoint uses, so "no frame" reads
			// identically wherever a viewer meets it. One phrase, one meaning:
			// the absence of a picture, not a black one.
			stage.WriteString(`<div class="stage"><div class="empty">` +
				`<div style="color:var(--notrun);font-size:15px;font-weight:600">no frame has been read yet</div>` +
				`<div style="color:var(--fg3);font-size:12.5px;margin-top:8px">` +
				`This is the absence of a frame, not a black picture. A black rectangle looks like a ` +
				`camera pointed at nothing, and the run's log says when the first frame arrived.</div></div></div>`)
		} else {
			// The stream, not a refresh. A browser renders
			// `multipart/x-mixed-replace` inside a plain <img> and updates it as
			// parts arrive, so this is a live picture with no JavaScript at all.
			stage.WriteString(`<div class="stage"><img src="/live.mjpeg" alt="the live frame from the recorder, with the model boxes and the crossing line drawn on the server"></div>`)
		}
		var foot strings.Builder
		fmt.Fprintf(&foot, `<span>last frame <b>%s</b></span>`, ageText(d.Live))
		fmt.Fprintf(&foot, `<span>sidecar <b>%s</b></span>`, orNone(d.Live.AIState, "—"))
		fmt.Fprintf(&foot, `<span>provenance <b>%s</b></span>`, orNone(d.Live.Provenance, "not stated"))
		fmt.Fprintf(&foot, `<span>tracked now <b>%d</b></span>`, len(d.Live.Tracks))
		if d.Live.Source == "" {
			foot.WriteString(`<span style="color:var(--notrun)">no live source line</span>`)
		}
		b.WriteString(`<section class="panel" style="margin:0">`)
		b.WriteString(stage.String())
		b.WriteString(`<div class="stagefoot">` + foot.String() + `</div>`)
		b.WriteString(`</section>`)

		// ---- evidence ------------------------------------------------------
		var clips strings.Builder
		if d.Evidence.Stored == 0 {
			clips.WriteString(noData("no clip has been stored"))
		} else {
			clips.WriteString(`<div class="clips">`)
			for _, ev := range d.Recent {
				if ev.ClipRef == "" {
					continue
				}
				sum := strings.SplitN(ev.ClipRef, "/", 2)
				clips.WriteString(`<div class="clip"><div class="m"><b>verified</b> ` + esc(sum[0]) + `</div></div>`)
			}
			clips.WriteString(`</div>`)
		}
		b.WriteString(panel("evidence", fmt.Sprintf("%d stored · %d verified", d.Evidence.Stored, d.Evidence.Verified), clips.String()))

		b.WriteString(`</div><div>`) // end the live column

		// ---- criteria ------------------------------------------------------
		crit := ""
		for _, c := range criteriaFor(d) {
			crit += tile(c)
		}
		b.WriteString(panel("acceptance criteria", "three verdicts only", `<div class="tiles">`+crit+`</div>`))

		// ---- faults --------------------------------------------------------
		var faults strings.Builder
		if len(d.Faults) == 0 {
			faults.WriteString(noData("no fault has been recorded"))
		} else {
			for i, f := range d.Faults {
				if i >= 8 {
					break
				}
				faults.WriteString(faultCard(f))
			}
		}
		b.WriteString(panel("faults", fmt.Sprintf("%d recorded", len(d.Faults)), faults.String()))

		// ---- detections ----------------------------------------------------
		b.WriteString(panel("last detections", fmt.Sprintf("%d total", sumNonDone(d.Counts)), detectionTable(d)))

		b.WriteString(footer(d))
		b.WriteString(`</div>`) // end the right column
		b.WriteString(`</div>`) // end .cols
		b.WriteString(`</div>`) // end .wrap

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page("Active monitoring — live", b.String())))
	}
}

func ageText(l Live) string {
	if len(l.Frame) == 0 {
		return "none"
	}
	d := time.Since(l.At)
	switch {
	case d < 2*time.Second:
		return fmt.Sprintf("%.1fs ago", d.Seconds())
	case d < 2*time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
}

func detectionTable(d Data) string {
	if len(d.Recent) == 0 {
		return noData("no detection has been recorded")
	}
	var b strings.Builder
	b.WriteString(`<table><thead><tr><th>time</th><th>class</th><th>dir</th><th>status</th><th>clip</th><th>provenance</th></tr></thead><tbody>`)
	for i, det := range d.Recent {
		if i >= 14 {
			break
		}
		cls := ""
		switch det.Status {
		case "done":
			cls = "pass"
		case "accepted", "clip_ok", "push_sent":
			cls = "live"
		default:
			cls = "fail"
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td><td class="%s">%s</td><td>%s</td><td>%s</td></tr>`,
			det.At.Local().Format("15:04:05"), esc(det.Class), esc(det.Direction), cls, esc(det.Status),
			esc(clip(orNone(det.ClipRef, "—"), 20)), esc(det.Provenance))
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func handleBoard(s *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/board" {
			http.NotFound(w, r)
			return
		}
		ctx := r.Context()
		d := s.Gather(ctx)
		verdict, provisional := s.verdictFor(ctx, d)

		var b strings.Builder
		b.WriteString(`<div class="wrap">`)
		b.WriteString(header(d, verdict, "board"))
		b.WriteString(kpiRow(d, verdict, provisional))

		b.WriteString(`<div class="cols">`)

		// ---- availability --------------------------------------------------
		var avail strings.Builder
		if d.Heartbeats == 0 {
			avail.WriteString(noData("no heartbeat has been recorded, so availability is not computed"))
		} else {
			fmt.Fprintf(&avail, `<div style="font-size:13px;color:var(--fg2);margin-bottom:12px">`+
				`%d heartbeats recorded. A gap over 30s is a dropout and fails AC1 absolutely, `+
				`not on a rate.</div>`, d.Heartbeats)
			if len(d.Gaps) == 0 {
				avail.WriteString(`<div style="color:var(--pass);font-size:13px">no gap over 30 seconds</div>`)
			} else {
				for _, g := range d.Gaps {
					fmt.Fprintf(&avail, `<div class="fault"><div class="h"><span class="r">gap</span>`+
						`<span class="t">%s</span></div><div class="d">%.1f seconds of silence</div></div>`,
						g.From.Local().Format("15:04:05"), g.Seconds)
				}
			}
		}
		b.WriteString(panel("availability", "AC1", avail.String()))

		// ---- rehearsed faults ------------------------------------------------
		var reh strings.Builder
		if len(d.Faults) == 0 {
			reh.WriteString(noData("no fault has been recorded"))
		} else {
			byReason := map[string]int{}
			for _, f := range d.Faults {
				byReason[f.Reason]++
			}
			reh.WriteString(`<table><thead><tr><th>reason</th><th>count</th></tr></thead><tbody>`)
			for _, k := range sortedIntKeys(byReason) {
				fmt.Fprintf(&reh, `<tr><td>%s</td><td class="num">%d</td></tr>`, esc(k), byReason[k])
			}
			reh.WriteString(`</tbody></table>`)
		}
		b.WriteString(panel("fault histogram", "by reason", reh.String()))

		// ---- detections histogram ------------------------------------------
		var hist strings.Builder
		if len(d.Counts) == 0 {
			hist.WriteString(noData("no detection has been recorded"))
		} else {
			hist.WriteString(`<table><thead><tr><th>status</th><th>count</th></tr></thead><tbody>`)
			for _, k := range sortedIntKeys(d.Counts) {
				fmt.Fprintf(&hist, `<tr><td>%s</td><td class="num">%d</td></tr>`, esc(k), d.Counts[k])
			}
			hist.WriteString(`</tbody></table>`)
		}
		b.WriteString(panel("detections", "terminal state", hist.String()))

		b.WriteString(`</div><div>`)

		crit := ""
		for _, c := range criteriaFor(d) {
			crit += tile(c)
		}
		b.WriteString(panel("acceptance criteria", "three verdicts only", `<div class="tiles">`+crit+`</div>`))

		b.WriteString(footer(d))
		b.WriteString(`</div></div></div>`)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page("Active monitoring — 7-day board", b.String())))
	}
}

func footer(d Data) string {
	var b strings.Builder
	b.WriteString(`<footer>`)
	fmt.Fprintf(&b, `Every number on this page was read from a row in <code>%s</code>. `, esc(d.DBPath))
	b.WriteString(`There is no second source of numbers, no editing, and no JavaScript. `)
	if len(d.Unavailable) > 0 {
		fmt.Fprintf(&b, `<br><span style="color:var(--notrun)">This page could not read: %s. `+
			`The sections above show <b>no data</b> there, never a zero.</span>`, esc(strings.Join(d.Unavailable, "; ")))
	}
	b.WriteString(`</footer>`)
	return b.String()
}

var _ = html.EscapeString
var _ = strconv.Itoa
