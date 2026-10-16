// The free-text console's rendering. Every number shown here is the same byte
// that /api/ask printed: we render and escape, we never recompute, and a value
// that is absent is drawn as absent rather than as zero.

const resultEl = document.getElementById("ask-result");
const statusEl = document.getElementById("ask-status");
const formEl = document.getElementById("ask-form");
const inputEl = document.getElementById("ask-q");

formEl.addEventListener("submit", function (e) {
  e.preventDefault();
  resolve(inputEl.value);
});

document.querySelectorAll("[data-q]").forEach(function (el) {
  el.style.cursor = "pointer";
  el.addEventListener("click", function () {
    inputEl.value = el.dataset.q;
    resolve(el.dataset.q);
  });
});

function esc(v) {
  return String(v == null ? "" : v)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

async function resolve(q) {
  q = (q || "").trim();
  if (!q) return;
  statusEl.textContent = "reading the record…";
  resultEl.hidden = true;
  resultEl.innerHTML = "";
  try {
    const res = await fetch("/api/ask", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ q: q }),
    });
    const data = await res.json();
    statusEl.textContent = "";
    renderAsk(data);
    resultEl.hidden = false;
  } catch (err) {
    statusEl.textContent = "the console did not answer: " + err;
  }
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

function renderAsk(d) {
  const sec = h("section", { class: "card ask-answer" });
  const head = h("div", { class: "ask-head" });
  const badge = h("span", { class: "intent-chip intent-" + esc(d.intent) }, [esc(d.intent)]);
  const headline = h("h2", { class: "ask-headline", text: d.headline });
  head.appendChild(badge);
  head.appendChild(headline);
  sec.appendChild(head);
  if (d.detail) sec.appendChild(h("p", { class: "sub ask-detail", text: d.detail }));
  if (d.matched && d.matched.length) {
    const m = h("p", { class: "sub ask-matched" }, ["matched words: ", h("code", { text: d.matched.join(", ") })]);
    sec.appendChild(m);
  }

  // Render payload cards in the order the intent produced them.
  if (d.replay) sec.appendChild(renderReplay(d.replay));
  if (d.rule) sec.appendChild(renderRule(d.rule));
  if (d.record) sec.appendChild(renderRecord(d.record));
  if (d.ledger) sec.appendChild(renderLedger(d.ledger));
  if (d.perimeter) sec.appendChild(renderPerimeter(d.perimeter));
  if (d.brief) sec.appendChild(renderBrief(d.brief));

  const srcs = d.sources || [];
  if (srcs.length) {
    const sbox = h("div", { class: "src-box" });
    sbox.appendChild(h("div", { class: "eyebrow", text: "sources" }));
    const ul = h("ul", { class: "src-list" });
    srcs.forEach(function (s) {
      const li = h("li", {});
      li.appendChild(h("b", { text: s.label }));
      if (s.detail) li.appendChild(h("span", { class: "sub", text: " — " + s.detail }));
      ul.appendChild(li);
    });
    sbox.appendChild(ul);
    sec.appendChild(sbox);
  }

  resultEl.innerHTML = "";
  resultEl.appendChild(sec);
}

function renderReplay(r) {
  const wrap = h("div", { class: "ask-block" });
  wrap.appendChild(h("h3", { text: "Replay — the record, not a file the request named" }));
  if (!r.ran) { wrap.appendChild(h("p", { class: "sub notrun", text: r.reason || "NOT RUN" })); return wrap; }
  if (!r.run) { wrap.appendChild(h("p", { class: "sub", text: r.note || "no run" })); return wrap; }
  wrap.appendChild(h("p", { class: "sub mono", text: r.run + (r.synthetic ? " · synthetic fixture" : "") }));
  if (r.note) wrap.appendChild(h("p", { class: "sub", text: r.note }));

  const list = h("div", { class: "clip-row" });
  (r.rows || []).forEach(function (row) {
    const card = h("div", { class: "clip-card" });
    const kind = row.clipKind || "video";
    if (row.clipUrl) {
      let media;
      if (kind === "image") {
        media = h("img", { src: row.clipUrl, alt: "detection " + row.detectionId });
      } else {
        media = h("video", { controls: "", preload: "metadata" });
        const src = h("source", { src: row.clipUrl, type: kind === "video" ? "video/mp4" : "video/mp4" });
        media.appendChild(src);
      }
      card.appendChild(media);
    } else {
      card.appendChild(h("div", { class: "clip-none", text: "no clip" }));
    }
    const meta = h("div", { class: "clip-meta" });
    meta.appendChild(h("span", { class: "mono", text: row.detectionId }));
    meta.appendChild(h("span", { class: "sub", text: " " + (row.at || "").slice(0, 19).replace("T", " ") }));
    if (row.verdict) meta.appendChild(h("span", { class: "chip", text: row.verdict }));
    card.appendChild(meta);
    list.appendChild(card);
  });
  wrap.appendChild(list);
  if (r.total > (r.rows || []).length) {
    wrap.appendChild(h("p", { class: "sub", text: "showing " + (r.rows || []).length + " of " + r.total + " stored events" }));
  }
  return wrap;
}

function renderRule(d) {
  const wrap = h("div", { class: "ask-block" });
  wrap.appendChild(h("h3", { text: "Rule draft — born disabled" }));
  wrap.appendChild(h("p", { class: "sub", text: "slug: " + d.slug + " · title: " + d.title }));
  const pre = h("pre", { class: "code" });
  pre.textContent = d.yaml;
  wrap.appendChild(pre);

  if (d.mapped && d.mapped.length) {
    const mb = h("div", { class: "term-block" });
    mb.appendChild(h("div", { class: "eyebrow", text: "mapped" }));
    d.mapped.forEach(function (t) {
      const chip = h("span", { class: "term-chip term-mapped" });
      chip.appendChild(h("b", { text: t.term }));
      chip.appendChild(document.createTextNode(" → "));
      chip.appendChild(h("code", { text: t.became }));
      mb.appendChild(chip);
    });
    wrap.appendChild(mb);
  }
  if (d.dropped && d.dropped.length) {
    const db = h("div", { class: "term-block" });
    db.appendChild(h("div", { class: "eyebrow", text: "dropped — the rule language has no condition for these" }));
    d.dropped.forEach(function (t) {
      const chip = h("span", { class: "term-chip term-dropped" });
      chip.appendChild(h("b", { text: t.term }));
      chip.appendChild(h("span", { class: "sub", text: " — " + t.became }));
      db.appendChild(chip);
    });
    wrap.appendChild(db);
  }
  if (d.problems && d.problems.length) {
    const pb = h("div", { class: "term-block" });
    pb.appendChild(h("div", { class: "eyebrow", text: "compiler (in-process)" }));
    d.problems.forEach(function (p) {
      const line = h("div", { class: "problem problem-" + p.severity });
      line.appendChild(h("span", { class: "chip", text: p.severity.toUpperCase() + " " + p.code }));
      line.appendChild(h("span", { class: "sub", text: " " + p.message }));
      if (p.path) line.appendChild(h("span", { class: "sub mono", text: " at " + p.path }));
      pb.appendChild(line);
    });
    wrap.appendChild(pb);
  } else {
    wrap.appendChild(h("div", { class: "term-block" }, [h("span", { class: "chip ok", text: "0 errors · " + d.warnings + " warnings" })]));
  }
  wrap.appendChild(h("p", { class: "sub ask-apply", text: d.apply }));
  return wrap;
}

function renderRecord(d) {
  const wrap = h("div", { class: "ask-block" });
  wrap.appendChild(h("h3", { text: "The run record" }));
  if (!d.ran) { wrap.appendChild(h("p", { class: "sub notrun", text: d.reason || "NOT RUN" })); return wrap; }
  const t = h("table", { class: "tbl" });
  const thead = h("tr", {});
  ["run", "started", "mode", "source", "detections"].forEach(function (x) { thead.appendChild(h("th", { text: x })); });
  t.appendChild(thead);
  (d.table || []).forEach(function (row) {
    const tr = h("tr", {});
    tr.appendChild(h("td", { class: "mono", text: row.id }));
    tr.appendChild(h("td", { text: (row.startedAt || "").replace("T", " ").slice(0, 19) }));
    tr.appendChild(h("td", { text: row.mode }));
    tr.appendChild(h("td", { class: "sub", text: row.source || "—" }));
    tr.appendChild(h("td", { text: row.detections }));
    t.appendChild(tr);
  });
  wrap.appendChild(t);
  return wrap;
}

function renderLedger(d) {
  const wrap = h("div", { class: "ask-block" });
  wrap.appendChild(h("h3", { text: "The ledger" }));
  if (!d.ran) { wrap.appendChild(h("p", { class: "sub notrun", text: d.reason || "NOT RUN" })); return wrap; }
  const status = h("span", { class: "chip " + (d.state === "intact" ? "ok" : "warn"), text: d.state });
  wrap.appendChild(h("p", {}, [h("b", { text: "state: " }), status, h("span", { class: "sub", text: " · " + d.runId })]));
  const pre = h("pre", { class: "code" });
  pre.textContent = d.output || "";
  wrap.appendChild(pre);
  return wrap;
}

function renderPerimeter(d) {
  const wrap = h("div", { class: "ask-block" });
  wrap.appendChild(h("h3", { text: "The perimeter — " + (d.cameras || []).length + " cameras, " + d.clearedCount + " cleared for external use" }));
  if (!d.ran) { wrap.appendChild(h("p", { class: "sub notrun", text: d.reason || "NOT RUN" })); return wrap; }
  const t = h("table", { class: "tbl" });
  const thead = h("tr", {});
  ["camera", "place", "operator", "verified", "cleared"].forEach(function (x) { thead.appendChild(h("th", { text: x })); });
  t.appendChild(thead);
  (d.cameras || []).forEach(function (c) {
    const tr = h("tr", {});
    tr.appendChild(h("td", { text: c.name }));
    tr.appendChild(h("td", { class: "sub", text: c.place }));
    tr.appendChild(h("td", { class: "sub", text: c.operator }));
    tr.appendChild(h("td", { class: "sub", text: c.verified || "—" }));
    tr.appendChild(h("td", { text: c.clearedForExternalUse ? "YES" : "no" }));
    t.appendChild(tr);
  });
  wrap.appendChild(t);
  return wrap;
}

function renderBrief(b) {
  const wrap = h("div", { class: "ask-block" });
  wrap.appendChild(h("h2", { text: "Chief of Security" }));
  wrap.appendChild(h("p", { class: "ask-headline", style: "font-size:20px", text: b.read }));
  wrap.appendChild(h("p", { class: "sub", text: b.headline }));

  if (b.findings && b.findings.length) {
    wrap.appendChild(h("h3", { text: "Findings" }));
    b.findings.forEach(function (f) {
      const row = h("div", { class: "finding finding-" + f.severity });
      row.appendChild(h("div", { class: "eyebrow", text: f.severity }));
      row.appendChild(h("h4", { text: f.title }));
      row.appendChild(h("p", { class: "sub", text: f.detail }));
      row.appendChild(h("p", { class: "sub mono", text: "source: " + f.source }));
      wrap.appendChild(row);
    });
  }
  if (b.baseline) {
    wrap.appendChild(h("h3", { text: "Baseline — restricted-zone alerts per minute" }));
    const bs = b.baseline;
    if (!bs.ran) {
      wrap.appendChild(h("p", { class: "sub notrun", text: bs.reason || "NOT RUN" }));
    } else {
      wrap.appendChild(h("p", { class: "sub", text: "minutes: " + bs.minutes + " · median: " + bs.median.toFixed(1) + " · deviations: " + bs.deviations.length }));
      bs.deviations.forEach(function (dv) {
        wrap.appendChild(h("p", { class: "sub", text: dv.minute + ": " + dv.value + " (" + dv.times.toFixed(1) + "x the median of " + dv.median.toFixed(1) + ")" }));
      });
    }
  }
  if (b.dark && b.dark.length) {
    const db = h("div", {});
    db.appendChild(h("h3", { text: "Minutes that saw nothing" }));
    b.dark.forEach(function (d) { db.appendChild(h("p", { class: "sub", text: d.minute + " — " + d.frames + " frames, zero people" })); });
    wrap.appendChild(db);
  }
  if (b.recommendations && b.recommendations.length) {
    wrap.appendChild(h("h3", { text: "Recommendations" }));
    b.recommendations.forEach(function (rc) {
      const row = h("div", { class: "rec" });
      row.appendChild(h("h4", { text: rc.title }));
      row.appendChild(h("p", { class: "sub", text: rc.detail }));
      if (rc.command) row.appendChild(h("code", { class: "mono", text: rc.command }));
      wrap.appendChild(row);
    });
  }
  const link = h("p", { class: "sub", style: "margin-top:18px" });
  link.innerHTML = 'full reading with sources: <a href="/chief">/chief</a>';
  wrap.appendChild(link);
  return wrap;
}