package labelui

// The page.
//
// # Why the JS is inline and why the CSS is a string
//
// Because this is a tool that runs on a reviewer's machine for an afternoon and is
// never deployed. Splitting it into three files would add a build step, a
// static-file route, and a content-type decision, to serve one page to one person
// who is already looking at it.
//
// # Why there is NO framework
//
// It must work when the network does not, on a laptop somebody has just woken up,
// and it must not be able to phone home. No CDN, no import, no fetch of anything
// but this process's own two endpoints.
//
// # What the reviewer sees
//
// One frame at a time, the boxes drawn to scale, the verdict per box, and a
// running count of what is left. The count is always visible: a reviewer who
// cannot see how much is left will stop, and a stopped session is
// indistinguishable from a finished one — which is how a false-positive rate stays
// NOT RUN forever while the log shows a folder full of data.
//
// # The drag gesture
//
// Drawing a box over something the detector MISSED is the whole reason this tool
// exists rather than a CSV. Without it, a frame with no detection carries no
// information: "the detector found nothing" and "there was nothing" are the same
// row, and no arithmetic over the table can tell them apart.
//
// So the canvas takes a drag, asks for a class, and posts it. That is the only
// interaction the page has that is not a keypress, and it is the one that makes
// recall measurable at all.

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>box marking</title>
<style>
  :root {
    /* Dark because a reviewer's eyes are already tired from the footage, and a
       white page next to a night-time camera is a second light source in the room. */
    --bg: #101214; --panel: #191c1f; --line: #2c3034; --fg: #d8dde1;
    --dim: #8b949c; --ok: #3fb950; --no: #f85149; --skip: #6e7681;
    --mark: #58a6ff;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--fg);
    font: 14px/1.5 ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  }
  header {
    display: flex; gap: 20px; align-items: baseline; padding: 10px 16px;
    border-bottom: 1px solid var(--line); background: var(--panel);
    position: sticky; top: 0; z-index: 2;
  }
  h1 { font-size: 14px; font-weight: 600; margin: 0; letter-spacing: .04em; }
  .stat { color: var(--dim); }
  .stat b { color: var(--fg); font-weight: 600; }
  .left { margin-left: auto; color: var(--ok); }
  main { display: flex; gap: 0; height: calc(100vh - 45px); }
  #stage { flex: 1; display: flex; flex-direction: column; min-width: 0; }
  canvas { width: 100%; flex: 1; display: block; background: #000; cursor: crosshair; }
  aside {
    width: 300px; border-left: 1px solid var(--line); background: var(--panel);
    overflow-y: auto; padding: 12px;
  }
  aside h2 { font-size: 12px; text-transform: uppercase; letter-spacing: .08em;
             color: var(--dim); margin: 16px 0 6px; font-weight: 600; }
  aside h2:first-child { margin-top: 0; }
  button {
    font: inherit; color: var(--fg); background: #23272b; border: 1px solid var(--line);
    border-radius: 5px; padding: 5px 10px; cursor: pointer;
  }
  button:hover { border-color: #444; }
  .box { display: flex; align-items: center; gap: 6px; padding: 3px 0;
         border-bottom: 1px solid #22262a; font-size: 13px; }
  .swatch { width: 10px; height: 10px; border-radius: 2px; flex: none; }
  .v-accept { color: var(--ok); }
  .v-reject { color: var(--no); }
  .v-none { color: var(--dim); }
  .skip { color: var(--skip); }
  kbd {
    background: #23272b; border: 1px solid var(--line); border-bottom-width: 2px;
    border-radius: 3px; padding: 0 5px; font-size: 12px;
  }
  #err { color: var(--no); padding: 0 16px; min-height: 20px; font-size: 13px; }
  input, select {
    font: inherit; color: var(--fg); background: #0c0e10; border: 1px solid var(--line);
    border-radius: 4px; padding: 4px 6px;
  }
  .hint { color: var(--dim); font-size: 12px; margin: 6px 0 0; }
  a { color: var(--mark); }
</style>
</head>
<body>
<header>
  <h1>BOX MARKING</h1>
  <span class="stat">run <b id="run">…</b></span>
  <span class="stat">frame <b id="fseq">–</b> / <b id="ftotal">–</b></span>
  <span class="stat">marked <b id="marked">–</b> of <b id="proposed">–</b></span>
  <span class="stat">accept <b id="accept">–</b> · reject <b id="reject">–</b></span>
  <span class="left" id="unmarked">– left</span>
</header>
<div id="err"></div>
<main>
  <div id="stage"><canvas id="cv"></canvas></div>
  <aside>
    <h2>proposed</h2>
    <div id="boxes"><p class="hint">loading…</p></div>
    <h2>drawn</h2>
    <div id="objs"><p class="hint">nothing drawn on this frame</p></div>
    <h2>keys</h2>
    <p class="hint">
      <kbd>←</kbd> <kbd>→</kbd> frame · <kbd>A</kbd> accept all ·
      <kbd>R</kbd> reject all<br>
      drag on the picture to mark something the detector MISSED, then pick its class<br>
      <kbd>0</kbd>–<kbd>9</kbd> pick a class for the drawn box
    </p>
    <h2>draw</h2>
    <select id="dclass">
      <option>car</option><option>truck</option><option>bus</option>
      <option>motorcycle</option><option>bicycle</option><option>person</option>
      <option>traffic light</option><option>stop sign</option>
      <option>bird</option><option>cat</option><option>dog</option>
      <option>other</option>
    </select>
    <p class="hint">
      A box you draw on a frame the detector found nothing on is what makes recall
      measurable at all. Without it, "found nothing" and "there was nothing" are the
      same row.
    </p>
  </aside>
</main>
<script>
"use strict";
const S = window.__SESSION__ || { frames: [], proposed: 0, marked: 0 };
const cv = document.getElementById("cv"), ctx = cv.getContext("2d");
let idx = -1, dragging = null, pendingClass = "car";

// COCO-80 colours, picked to stay apart on a dark background and to survive the
// most common forms of colour blindness — which matters because a reviewer
// telling a blue box from a green one is a reviewer guessing.
const COLOR = {
  car:"#58a6ff", truck:"#d29922", bus:"#a371f7", motorcycle:"#3fb950",
  bicycle:"#2ea043", person:"#f0883e", train:"#8b949c", boat:"#39c5cf",
  "traffic light":"#ff7b72", "stop sign":"#f85149", bird:"#bc8cff",
  cat:"#ffa657", dog:"#7ee787", horse:"#79c0ff", sheep:"#a5d6ff",
  other:"#6e7681"
};

function boxKey(b) { return b.top + "," + b.left + "," + b.bottom + "," + b.right; }
function frame() { return S.frames[idx]; }

function markedKeys() {
  const f = frame();
  if (!f) return {};
  const m = {};
  for (const l of f.labels || []) m[l.box_key] = l.verdict;
  return m;
}

function resize() {
  const f = frame();
  const w = 960, h = f ? 540 : 540;
  cv.width = w; cv.height = h;
}

function draw() {
  const f = frame();
  ctx.fillStyle = "#000"; ctx.fillRect(0, 0, cv.width, cv.height);
  if (!f) return;
  const marks = markedKeys();
  const W = cv.width, H = cv.height;

  if (f.frame_skipped && !f.measured) {
    ctx.fillStyle = "#6e7681";
    ctx.font = "20px ui-monospace, monospace";
    ctx.textAlign = "center";
    ctx.fillText("this frame was never measured — the pipeline skipped it", W / 2, H / 2);
    ctx.textAlign = "left";
    return;
  }

  // Drawn objects first, so a proposal sits on top of them: the question a
  // reviewer is answering is "did the detector find this", and the proposal is
  // the answer being tested.
  ctx.lineWidth = 2;
  for (const o of f.objects || []) {
    ctx.strokeStyle = COLOR[o.class] || COLOR.other;
    ctx.setLineDash([6, 4]);
    ctx.strokeRect(o.left / 1000 * W, o.top / 1000 * H,
                   (o.right - o.left) / 1000 * W, (o.bottom - o.top) / 1000 * H);
    ctx.setLineDash([]);
    ctx.fillStyle = ctx.strokeStyle;
    ctx.font = "12px ui-monospace, monospace";
    ctx.fillText("drawn: " + o.class, o.left / 1000 * W + 4, o.top / 1000 * H - 4);
  }

  for (const b of f.boxes || []) {
    const v = marks[boxKey(b)];
    const x = b.left / 1000 * W, y = b.top / 1000 * H;
    const w = (b.right - b.left) / 1000 * W, h = (b.bottom - b.top) / 1000 * H;
    const col = v === "accept" ? "#3fb950" : v === "reject" ? "#f85149" : (COLOR[b.class] || "#d8dde1");
    ctx.strokeStyle = col;
    ctx.globalAlpha = v ? 0.95 : 0.8;
    ctx.strokeRect(x, y, w, h);
    if (v) { ctx.fillStyle = col + "22"; ctx.fillRect(x, y, w, h); }
    ctx.globalAlpha = 1;
    ctx.fillStyle = col;
    ctx.font = "12px ui-monospace, monospace";
    ctx.fillText(b.class + " " + b.score.toFixed(2), x + 3, y - 4);
  }

  if (dragging) {
    ctx.strokeStyle = "#58a6ff"; ctx.setLineDash([4, 3]);
    const x = Math.min(dragging.x0, dragging.x1), y = Math.min(dragging.y0, dragging.y1);
    ctx.strokeRect(x, y, Math.abs(dragging.x1 - dragging.x0), Math.abs(dragging.y1 - dragging.y0));
    ctx.setLineDash([]);
  }
}

function renderAside() {
  const f = frame(), box = document.getElementById("boxes"),
        objs = document.getElementById("objs");
  if (!f) { box.innerHTML = '<p class="hint">no frame</p>'; return; }
  const marks = markedKeys();
  if (!f.boxes || !f.boxes.length) {
    box.innerHTML = '<p class="hint">the detector proposed nothing on this frame. ' +
      'If something IS there, drag a box below.</p>';
  } else {
    box.innerHTML = f.boxes.map(function (b) {
      const v = marks[boxKey(b)];
      const cls = v === "accept" ? "v-accept" : v === "reject" ? "v-reject" : "v-none";
      const sw = COLOR[b.class] || "#6e7681";
      return '<div class="box"><span class="swatch" style="background:' + sw + '"></span>' +
        '<span style="flex:1">' + b.class + '</span>' +
        '<span class="' + cls + '">' + (v || "unmarked") + '</span></div>';
    }).join("");
  }
  objs.innerHTML = (f.objects && f.objects.length)
    ? f.objects.map(function (o) {
        return '<div class="box"><span class="swatch" style="background:' +
          (COLOR[o.class] || "#6e7681") + '"></span><span style="flex:1">' + o.class +
          '</span></div>';
      }).join("")
    : '<p class="hint">nothing drawn on this frame</p>';
}

function renderHeader() {
  document.getElementById("run").textContent = S.run_id || "–";
  document.getElementById("fseq").textContent = idx < 0 ? "–" : idx + 1;
  document.getElementById("ftotal").textContent = S.frames.length;
  document.getElementById("marked").textContent = S.marked;
  document.getElementById("proposed").textContent = S.proposed;
  document.getElementById("accept").textContent = S.accept;
  document.getElementById("reject").textContent = S.reject;
  const u = document.getElementById("unmarked");
  u.textContent = S.unmarked + " left";
  u.style.color = S.unmarked ? "var(--fg)" : "var(--ok)";
}

function show(msg, bad) {
  const e = document.getElementById("err");
  e.textContent = msg || "";
  e.style.color = bad ? "var(--no)" : "var(--ok)";
}

async function post(url, body) {
  const r = await fetch(url, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body)
  });
  if (!r.ok) { show(await r.text(), true); return false; }
  return true;
}

async function judge(key, verdict) {
  const f = frame();
  if (!f || !key) return;
  if (!await post("/api/label", {
    frame_seq: f.seq, box_key: key, verdict: verdict, class: ""
  })) return;
  show("marked " + verdict, false);
  await refresh();
}

async function judgeAll(verdict) {
  const f = frame();
  if (!f || !f.boxes) return;
  for (const b of f.boxes) {
    const r = await fetch("/api/label", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ frame_seq: f.seq, box_key: boxKey(b), verdict: verdict, class: b.class })
    });
    if (!r.ok) { show(await r.text(), true); return; }
  }
  show("marked every box on this frame " + verdict, false);
  await refresh();
}

async function refresh() {
  const r = await fetch("/api/session");
  if (!r.ok) { show("the session could not be re-read: " + await r.text(), true); return; }
  const next = await r.json();
  const was = idx;
  S.run_id = next.run_id; S.frames = next.frames; S.proposed = next.proposed;
  S.marked = next.marked; S.accept = next.accept; S.reject = next.reject;
  S.unmarked = next.unmarked;
  if (S.frames.length === 0) {
    show("this run has no per-frame box observations. A marking session needs SP_POC_BOX_LOG_FRAMES above 0.",
         true);
    idx = -1;
  } else {
    idx = Math.min(Math.max(was, 0), S.frames.length - 1);
    show("", false);
  }
  renderHeader(); draw(); renderAside();
}

function move(d) {
  if (S.frames.length === 0) return;
  idx = Math.min(Math.max(idx + d, 0), S.frames.length - 1);
  renderHeader(); draw(); renderAside();
}

function pos(e) {
  const r = cv.getBoundingClientRect();
  return {
    x: (e.clientX - r.left) / r.width * cv.width,
    y: (e.clientY - r.top) / r.height * cv.height
  };
}

cv.addEventListener("mousedown", function (e) {
  const p = pos(e);
  dragging = { x0: p.x, y0: p.y, x1: p.x, y1: p.y };
});
cv.addEventListener("mousemove", function (e) {
  if (!dragging) return;
  const p = pos(e);
  dragging.x1 = p.x; dragging.y1 = p.y; draw();
});
window.addEventListener("mouseup", async function () {
  if (!dragging) return;
  const d = dragging; dragging = null; draw();
  const W = cv.width, H = cv.height;
  const left = Math.round(Math.min(d.x0, d.x1) / W * 1000);
  const right = Math.round(Math.max(d.x0, d.x1) / W * 1000);
  const top = Math.round(Math.min(d.y0, d.y1) / H * 1000);
  const bottom = Math.round(Math.max(d.y0, d.y1) / H * 1000);
  // A drag of a few pixels is a mis-click, not an object. It is refused with a
  // sentence rather than recorded as a box of no area, because a zero-area box is
  // a box nobody can see and the store refuses it anyway — with a message about
  // evidence naming no place, which is true and unhelpful.
  if (right - left < 8 || bottom - top < 8) {
    show("that drag was " + (right-left) + "x" + (bottom-top) +
         " thousandths of the frame — too small to be an object", true);
    return;
  }
  const f = frame();
  if (!f) return;
  if (!await post("/api/object", {
    frame_seq: f.seq, class: pendingClass,
    top: top, left: left, bottom: bottom, right: right
  })) return;
  show("drew a " + pendingClass + " the detector did not find", false);
  await refresh();
});

window.addEventListener("keydown", async function (e) {
  if (e.target.tagName === "SELECT" || e.target.tagName === "INPUT") return;
  const k = e.key.toLowerCase();
  if (k === "arrowright") { move(1); return; }
  if (k === "arrowleft") { move(-1); return; }
  if (k === "a") { await judgeAll("accept"); return; }
  if (k === "r") { await judgeAll("reject"); return; }
  if (/^[0-9]$/.test(k)) {
    const sel = document.getElementById("dclass");
    if (sel.options[k]) { sel.selectedIndex = +k; pendingClass = sel.options[k].value; }
    return;
  }
  const f = frame();
  if (!f || !f.boxes || !f.boxes.length) return;
  if (k === "y") { await judge(boxKey(f.boxes[0]), "accept"); return; }
  if (k === "n") { await judge(boxKey(f.boxes[0]), "reject"); return; }
});

resize();
(async function () { await refresh(); })();
</script>
</body>
</html>
`
