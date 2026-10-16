// The chief page renders whatever /api/chief returns. No second truth: every
// number shown here is the byte /api/chief printed, rendered and escaped.

const statusEl = document.getElementById("chief-status");
const resultEl = document.getElementById("chief-result");

function esc(v) {
  return String(v == null ? "" : v)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}
function h(tag, attrs, children) {
  const e = document.createElement(tag);
  if (attrs) for (const k in attrs) {
    if (k === "class") e.className = attrs[k];
    else if (k === "text") e.textContent = attrs[k];
    else e.setAttribute(k, attrs[k]);
  }
  if (children) for (const c of children) {
    if (c == null) continue;
    e.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return e;
}

async function load() {
  statusEl.textContent = "reading the records…";
  try {
    const res = await fetch("/api/chief");
    const b = await res.json();
    statusEl.textContent = "";
    render(b);
  } catch (err) {
    statusEl.textContent = "the chief did not answer: " + err;
  }
}

function render(b) {
  resultEl.innerHTML = "";
  const wrap = h("div", { class: "ask-result" });
  wrap.appendChild(h("header", { class: "hero ask-hero" }, [
    h("div", { class: "eyebrow", text: "read at " + (b.generatedAt || "") })
  ]));

  const card = h("section", { class: "card ask-answer" });
  card.appendChild(h("h2", { class: "ask-headline", style: "font-size:22px", text: b.read }));
  card.appendChild(h("p", { class: "sub", text: b.headline }));

  const srow = h("div", { class: "split" });
  // baseline
  const bl = h("section", { class: "card" });
  bl.appendChild(h("h2", { text: "Baseline — restricted-zone alerts per minute" }));
  const bs = b.baseline;
  if (!bs || !bs.ran) {
    bl.appendChild(h("p", { class: "sub notrun", text: (bs && bs.reason) || "NOT RUN" }));
  } else {
    bl.appendChild(h("p", { class: "sub", text: "minutes " + bs.minutes + " · median " + bs.median.toFixed(1) + " · deviations " + bs.deviations.length }));
    if (bs.deviations.length === 0) {
      bl.appendChild(h("p", { class: "sub", text: "no minute exceeded 3x the median by more than 2: on this record the surprise is the silent minute, not a spike." }));
    }
    bs.deviations.forEach(function (dv) {
      bl.appendChild(h("p", { class: "sub", text: dv.minute + ": " + dv.value + " (" + dv.times.toFixed(1) + "x the median of " + dv.median.toFixed(1) + ")" }));
    });
  }
  srow.appendChild(bl);

  // dark minutes
  const dk = h("section", { class: "card" });
  dk.appendChild(h("h2", { text: "Minutes that saw nothing" }));
  if (!b.dark || !b.dark.length) {
    dk.appendChild(h("p", { class: "sub", text: "none in this record." }));
  } else {
    (b.dark || []).forEach(function (d) {
      dk.appendChild(h("p", { class: "sub", text: d.minute + " — " + d.frames + " frames, zero people" }));
    });
  }
  srow.appendChild(dk);
  card.appendChild(srow);

  // product
  const pr = b.product || {};
  const pc = h("section", { class: "card" });
  pc.appendChild(h("h2", { text: "The product's record" }));
  if (!pr.ran) {
    pc.appendChild(h("p", { class: "sub notrun", text: pr.reason || "NOT RUN" }));
  } else {
    const t = h("table", { class: "tbl" });
    [["runs", pr.runs], ["span", (pr.spanFrom || "") + " → " + (pr.spanTo || "")],
      ["detections", pr.detections], ["with detections", pr.withDetections],
      ["run with events", pr.run], ["events in it", pr.events], ["with clip", pr.withClip],
      ["judged", pr.judged], ["unjudged", pr.unjudged],
      ["classes", (pr.classes || []).join(", ")], ["source", pr.synthetic ? "fixture (synthetic)" : (pr.source || "")]]
      .forEach(function (row) {
        const tr = h("tr", {});
        tr.appendChild(h("td", { text: row[0] }));
        tr.appendChild(h("td", { text: row[1] }));
        t.appendChild(tr);
      });
    pc.appendChild(t);
  }
  card.appendChild(pc);

  // lab
  const lb = b.lab || {};
  const lc = h("section", { class: "card" });
  lc.appendChild(h("h2", { text: "The lab's record" }));
  if (!lb.available) {
    lc.appendChild(h("p", { class: "sub notrun", text: lb.reason || "NOT RUN" }));
  } else {
    const t = h("table", { class: "tbl" });
    [["video", lb.videoId], ["minutes", (lb.series || []).length],
      ["frames", lb.frames], ["restricted-zone alerts", Object.values(lb.alertRules || {}).reduce(function (a, c) { return a + c; }, 0)],
      ["dark minutes", (b.dark || []).length], ["clips", lb.clips],
      ["entities tracked", lb.entities], ["relations", lb.relations],
      ["learner rows", Object.values((lb.learner || {}).rows || {}).reduce(function (a, c) { return a + c; }, 0)]]
      .forEach(function (row) {
        const tr = h("tr", {});
        tr.appendChild(h("td", { text: row[0] }));
        tr.appendChild(h("td", { text: row[1] }));
        t.appendChild(tr);
      });
    lc.appendChild(t);
  }
  card.appendChild(lc);

  // findings
  if (b.findings && b.findings.length) {
    card.appendChild(h("h3", { text: "Findings" }));
    b.findings.forEach(function (f) {
      const row = h("div", { class: "finding finding-" + f.severity });
      row.appendChild(h("div", { class: "eyebrow", text: f.severity }));
      row.appendChild(h("h4", { text: f.title }));
      row.appendChild(h("p", { class: "sub", text: f.detail }));
      row.appendChild(h("p", { class: "sub mono", text: "source: " + f.source }));
      card.appendChild(row);
    });
  }

  // recommendations
  if (b.recommendations && b.recommendations.length) {
    card.appendChild(h("h3", { text: "Recommendations" }));
    b.recommendations.forEach(function (rc) {
      const row = h("div", { class: "rec" });
      row.appendChild(h("h4", { text: rc.title }));
      row.appendChild(h("p", { class: "sub", text: rc.detail }));
      if (rc.command) row.appendChild(h("code", { class: "mono", text: rc.command }));
      card.appendChild(row);
    });
  }

  // sources
  if (b.sources && b.sources.length) {
    const sbox = h("div", { class: "src-box" });
    sbox.appendChild(h("div", { class: "eyebrow", text: "sources" }));
    const ul = h("ul", { class: "src-list" });
    b.sources.forEach(function (s) {
      const li = h("li", {});
      li.appendChild(h("b", { text: s.label }));
      if (s.detail) li.appendChild(h("span", { class: "sub", text: " — " + s.detail }));
      ul.appendChild(li);
    });
    sbox.appendChild(ul);
    card.appendChild(sbox);
  }

  // perimeter summary
  if (b.perimeter) {
    const p = b.perimeter;
    card.appendChild(h("h3", { text: "Perimeter — " + (p.cameras || []).length + " cameras, " + p.clearedCount + " cleared for external use" }));
  }

  wrap.appendChild(card);
  resultEl.appendChild(wrap);
}

load();