/* ==========================================================================
   The operations page's behaviour.
   --------------------------------------------------------------------------
   The rule this file follows is the same one the report follows: every number
   on this page is either a MEASUREMENT or it says so. A 0 that means "nothing
   happened" and a 0 that means "nobody measured" are different facts, and the
   second must never render as the first.

   So the renderers below carry the empty case in the same branch as the full
   one, and the phrase used for an absent measurement is "No incident in this
   window has both an `incident.opened` row and a disposition" — the honest
   answer `securityctl efficiency` prints, carried through unchanged rather than
   re-worded into a zero.
   ========================================================================== */

const $ = (id) => document.getElementById(id);

// The three answers are all on the page.
//
// They used to be tabs, and a tab HIDES the other two answers: a reader who
// never pressed the second tab never learned that the system had stayed
// quiet on purpose. The links below move the eye down the page instead, and
// nothing is hidden.
document.querySelectorAll('.jump a').forEach((a) => {
  a.addEventListener('click', (e) => {
    const target = document.querySelector(a.getAttribute('href'));
    if (!target) return;
    e.preventDefault();
    target.scrollIntoView({ behavior: 'smooth', block: 'start' });
    target.setAttribute('tabindex', '-1');
    target.focus({ preventScroll: true });
  });
});

/* ---------- formatting --------------------------------------------------- */

// A number is either an integer, one decimal, or the words NOT RUN. There is
// deliberately no "0%" for an unmeasured share anywhere in this file.
const n0 = (v) => (v === null || v === undefined || Number.isNaN(v)) ? 'NOT RUN' : Number(v).toLocaleString('en-GB', { maximumFractionDigits: 0 });
const n1 = (v) => (v === null || v === undefined || Number.isNaN(v)) ? 'NOT RUN' : Number(v).toFixed(1);
const pct = (v) => (v === null || v === undefined || Number.isNaN(v)) ? 'NOT RUN' : `${Math.round(v * 1000) / 10}%`;
const dur = (s) => {
  if (s === null || s === undefined || Number.isNaN(s)) return 'NOT RUN';
  if (s < 60) return `${n0(s)}s`;
  if (s < 3600) return `${n0(s / 60)}m`;
  if (s < 86400) return `${n1(s / 3600)}h`;
  return `${n1(s / 86400)}d`;
};
const clock = (t) => {
  if (!t) return '';
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return String(t);
  // The site's clock, not the reader's: a timestamp without an offset is a
  // timestamp that will be read in the wrong timezone.
  return d.toLocaleString('en-GB', { hour12: false }) + ' UTC';
};

// A 500-word escape hatch lives in the renderers below and not here, because
// the values rendered are our own numbers and API responses — the one place an
// operator-supplied string appears (the incident id, echoed back) is escaped
// where it is put into the DOM.
const esc = (s) => String(s ?? '')
  .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;').replace(/'/g, '&#39;');

function empty(title, body) {
  return `<div class="empty"><strong>${title}</strong>${body}</div>`;
}

function banner(kind, text) {
  return `<div class="banner ${kind}">${text}</div>`;
}

/* ---------- attention ---------------------------------------------------- */

function kpis(rows) {
  return `<div class="kpis">${rows.map((r) => `
    <div class="kpi">
      <div class="label">${esc(r.label)}</div>
      <div class="value">${r.value}</div>
      ${r.sub ? `<div class="sub">${r.sub}</div>` : ''}
    </div>`).join('')}</div>`;
}

// The honest-answer sentence, carried verbatim from the CLI that produced it.
const NO_ATTENTION = 'No incident in this window has both an <code>incident.opened</code> row and a disposition.';

function renderAttention(doc) {
  const win = doc && doc.hours ? doc.hours : 0;
  $('window-label').textContent = win ? `last ${win} hours` : 'no window';

  // The wire is snake_case all the way down. It was not always: the report's top
  // level carried JSON tags and the structs behind it carried none, so Go
  // marshalled `Incidents` and `ByState` beside a document whose own keys were
  // `verification_states`. A console reading `attention.incidents` therefore got
  // `undefined`, rendered the honest "nothing measured" empty state, and looked
  // correct over a window that had plenty. The tags are fixed now and
  // `internal/operatormetrics` has a test that keeps the document one language;
  // this page reads the shape the CLI actually emits.
  const a = (doc && doc.attention) || null;
  const mtti = (doc && doc.mt_ti) || null;
  const mttv = (doc && doc.mt_t_v) || null;
  const states = (doc && doc.verification_states) || null;

  const incidents = (a && a.incidents) || [];
  const dispositioned = incidents.filter((i) => i.dispositioned);

  if (!incidents.length) {
    $('attention-body').innerHTML = empty(
      'No attention measured in this window',
      NO_ATTENTION + ' That is a fact about the window, not about the operators.',
    );
  } else {
    // The uncapped figure is the one known to be wrong and the capped one is
    // reported beside it rather than instead of it: a reader shown only the
    // capped number cannot tell that data exists beyond it.
    const capped = incidents.reduce((s, i) => s + (i.capped_seconds || 0), 0);
    const uncapped = incidents.reduce((s, i) => s + (i.uncapped_seconds || 0), 0);
    const cappedCount = incidents.filter((i) => i.capped).length;
    const per = dispositioned.map((i) => i.capped_seconds || 0).sort((x, y) => x - y);
    const median = per.length ? per[Math.floor(per.length / 2)] : null;

    $('attention-body').innerHTML = kpis([
      { label: 'Incidents a human read', value: n0(incidents.length),
        sub: dispositioned.length < incidents.length
          ? `${n0(dispositioned.length)} already dispositioned, ${n0(incidents.length - dispositioned.length)} still open`
          : 'every one dispositioned' },
      { label: 'Attention, capped', value: dur(capped),
        sub: uncapped > capped
          ? `${dur(uncapped)} uncapped — the cap removed ${dur(uncapped - capped)} across ${n0(cappedCount)} open-ended read(s)`
          : '' },
      { label: 'Median per incident', value: dur(median) },
      { label: 'Open with no verdict', value: n0(incidents.length - dispositioned.length),
        sub: 'still holding attention, which is not the same as nobody looked' },
    ]);
  }

  // The three parts of time-to-investigate, per rule class. The median is the
  // headline and the two parts sit beside it, because long detection latency is
  // a pipeline problem and long investigation latency is an interface problem,
  // and a single number has neither remedy.
  if (mtti && (mtti.classes || []).length) {
    $('attention-body').insertAdjacentHTML('beforeend', `
      <div class="eyebrow section-label">Time to investigate, per rule class</div>
      <table><thead><tr><th>Rule class</th><th class="num">Median</th><th class="num">Mean</th>
        <th class="num">Detection</th><th class="num">Investigation</th><th class="num">Samples</th></tr></thead>
        <tbody>${mtti.classes.map((c) => `<tr>
          <td><code>${esc(c.rule_id)}</code></td>
          <td class="num">${dur(c.median_mtti_seconds)}</td>
          <td class="num">${dur(c.mean_mtti_seconds)}</td>
          <td class="num">${dur(c.median_detection_seconds)}</td>
          <td class="num">${dur(c.median_investigation_seconds)}</td>
          <td class="num">${n0(c.samples)}</td>
        </tr>`).join('')}</tbody></table>`);
  }

  if (mttv && mttv.samples > 0) {
    $('attention-body').insertAdjacentHTML('beforeend', `
      <div class="kpi kpi-spaced">
        <div class="label">Time to verdict — PROPOSED definition</div>
        <div class="value">${dur(mttv.median_seconds)}</div>
        <div class="sub">median over ${n0(mttv.samples)} verdict(s), p95 ${dur(mttv.p95_seconds)}.
        The name itself is a proposal the metrics doc says must be confirmed before M2 opens,
        and this page labels it as one rather than promoting it to a fact.</div>
      </div>`);
  }

  if (states && states.total > 0) {
    renderStates(states);
  } else {
    $('states-body').innerHTML = empty('No decision in this window',
      'The verification-state distribution counts decisions the pipeline made. An empty window is an empty window, and it is not a 0% silent share.');
  }
}

/* ---------- the five states ---------------------------------------------- */

// Each state carries its own colour and a plain-language meaning. The two
// silent ones are marked, because they are the two a reader is looking for.
const STATE_TEXT = {
  corroborated: 'the evidence agrees with itself',
  duplicate: 'ingest working correctly — the same event, seen twice',
  suppressed: 'a decision the system made deliberately, and audited',
  unverified: 'nothing contradicts it, nothing supports it',
  contradicted: 'the evidence disagrees with itself',
};

function renderStates(states) {
  // `by_state` is the counter as the query produced it, and `total` is the
  // window's own half. The share is computed here rather than asked of the
  // query, because a share the query computed would be a share over a
  // denominator nobody rendered.
  const counts = states.by_state ?? {};
  const total = states.total ?? Object.values(counts).reduce((a, b) => a + Number(b || 0), 0);
  if (!total) {
    $('states-body').innerHTML = empty('No decision in this window', '');
    return;
  }
  $('states-body').innerHTML = `<table><thead><tr>
      <th>State</th><th class="num">Count</th><th class="num">Share</th><th>What it means</th>
    </tr></thead><tbody>${Object.entries(counts).sort((a, b) => b[1] - a[1]).map(([k, v]) => {
    const silent = k === 'duplicate' || k === 'suppressed';
    return `<tr>
      <td><span class="chip ${esc(k)} ${silent ? 'silent' : ''}">${esc(k)}</span></td>
      <td class="num">${n0(v)}</td>
      <td class="num">${pct(v / total)}</td>
      <td class="muted">${esc(STATE_TEXT[k] ?? '')}</td>
    </tr>`;
  }).join('')}</tbody></table>`;
}

/* ---------- the slice ---------------------------------------------------- */

function renderSilence(doc) {
  const slice = doc && doc.silence_by_site_and_class;
  const body = $('silence-body');

  // The whole document is snake_case, including the cells: `site_id`, `rule_id`,
  // `by_state`, `total`, `silent`. There is no `share` field, and no `lag`
  // field, and inventing them would render a number the query never produced.
  // The share is computed HERE, over the cell's own `total`, so a reader can
  // check it against the two counts on the same row.
  if (!slice) {
    body.innerHTML = empty('Where the system stays quiet — NOT RUN',
      'Nothing in this window was measured, so nothing here says the system was quiet. A run that produced no decisions and a run whose rules are eating incidents print the same <code>total 0</code>, and only one of them is a measurement.');
    return;
  }

  const cells = slice.cells ?? [];
  if (!cells.length) {
    body.innerHTML = empty('No decision in this window',
      'Every rule class at every site was measured, and none of them said nothing. The window has decisions in it, which is the difference between this sentence and an empty one.');
    return;
  }

  const share = (c) => (c.total > 0 ? c.silent / c.total : null);

  // Most silent first, because that is the row somebody should look at.
  const sorted = [...cells].sort((a, b) => (share(b) ?? 0) - (share(a) ?? 0));
  const loud = sorted.filter((c) => (share(c) ?? 0) > 0.5);

  const summary = slice.total_decided > 0
    ? `<div class="kpi kpi-gap">
         <div class="label">Window</div>
         <div class="value">${pct(slice.total_silent / slice.total_decided)}</div>
         <div class="sub">${n0(slice.total_silent)} of ${n0(slice.total_decided)} decision(s) said nothing. The aggregate says how often; the rows below say where.</div>
       </div>`
    : '';

  body.innerHTML = summary + (loud.length ? banner('warn',
      `${loud.length} rule class(es) at a site said nothing on more than half of their decisions. ` +
      'That is a configuration question about that site, not a number about the installation.') : banner('ok',
      'No rule class at any site stays silent on more than half of its decisions in this window.')) +
    `<table><thead><tr>
        <th>Site</th><th>Rule class</th>
        <th class="num">Silent</th><th class="num">Decided</th><th>Share</th><th>Per state</th>
      </tr></thead><tbody>${sorted.map((c) => {
        const s = share(c);
        const isLoud = s !== null && s > 0.5;
        const states = Object.entries(c.by_state ?? {})
          .sort((a, b) => b[1] - a[1])
          .map(([k, v]) => `<span class="chip ${esc(k)}">${esc(k)} ${n0(v)}</span>`)
          .join(' ');
        return `<tr class="${isLoud ? 'is-loud' : ''}" data-fill="${s === null ? 0 : Math.min(100, s * 100)}">
          <td class="site">${esc(c.site_id)}</td>
          <td><code>${esc(c.rule_id)}</code></td>
          <td class="num">${n0(c.silent)}</td>
          <td class="num">${n0(c.total)}</td>
          <td><div class="bar ${isLoud ? '' : 'is-ok'}">
              <div class="track"><div class="fill"></div></div>
              <span>${pct(s)}</span></div></td>
          <td>${states || '<span class="muted">none</span>'}</td>
        </tr>`;
      }).join('')}</tbody></table>`;
  // The widths, through the CSSOM.
  //
  // The Content Security Policy the server sends has `style-src 'self'` and
  // no `unsafe-inline`, and it drops a style ATTRIBUTE - custom properties
  // included. The CSSOM is not governed by the policy, so the measurement
  // goes in here, after the rows are in the DOM.
  for (const row of body.querySelectorAll('tr[data-fill]')) {
    const fill = row.querySelector('.fill');
    if (fill) fill.style.width = row.dataset.fill + '%';
  }
}
/* ==========================================================================
   Why an incident existed, or why nothing was raised.
   --------------------------------------------------------------------------
   A NOTE ON THE TWO CONVENTIONS, because a page that reads both has to say so
   rather than silently knowing. `securityctl efficiency --json` speaks
   snake_case, and until this pass it spoke it badly: the top level carried tags
   and the structs behind it carried none, so `Incidents` and `ByState` reached
   the wire beside a document whose own keys were `verification_states`.
   `securityctl incident explain --json` speaks camelCase and has always done so,
   consistently, on a wire that is already tagged.

   Each document is internally consistent now, and each has a control of its own
   (`internal/operatormetrics/wireshape_test.go` for one,
   `internal/incidents/replay_wire_test.go` for the other). What this page does
   is read each one as it is written — it does not translate, because a
   normalising layer between the CLI and the screen is a second implementation of
   the wire that can drift from the first one.
   ========================================================================== */

function renderWhy(doc, id) {
  const b = $('why-body');
  if (!doc) { b.innerHTML = empty('Nothing asked yet', ''); return; }
  if (doc.error) {
    b.innerHTML = banner('bad', `The system could not explain <code>${esc(id)}</code>: ${esc(doc.error)}`) +
      empty('A reason nobody can read is a reason nobody can act on',
        'The lookup reached the ledger and the ledger said it does not have this incident. An id that is nearly right and an id that is wrong both land here.');
    return;
  }

  // `replayedFromStoredFacts` is a constant true, and it is the assertion this
  // whole product rests on: the decision was reconstructed from stored facts and
  // nothing was re-inferred. Rendering it as a facts row rather than assuming it
  // is the difference between a claim and evidence.
  const replayChip = doc.replayedFromStoredFacts
    ? '<span class="chip answered">replayed from stored facts</span>'
    : '<span class="chip cannot_tell">NOT replayed — this is not a reconstruction</span>';

  const matchLine = doc.matches === false
    ? banner('bad', `The replay does not reproduce the recorded outcome: ${esc(doc.mismatch)} ` +
        'A silent mismatch is how a replay becomes a fiction, so it is printed rather than resolved.')
    : banner('ok', 'Re-evaluating the stored facts reproduces the recorded outcome.');

  const rows = [
    ['Incident', `<code>${esc(doc.incidentId ?? id)}</code>`],
    ['Rule', doc.ruleId ? `<code>${esc(doc.ruleId)}</code>` : '<span class="muted">not recorded</span>'],
    ['State', doc.state ? `<span class="chip ${esc(doc.state)}">${esc(doc.state)}</span>` : '<span class="muted">not recorded</span>'],
    ['Bundle', doc.bundleVersion ? esc(doc.bundleVersion) : '<span class="muted">not recorded</span>'],
    ['Policy', doc.policyVersion ? esc(doc.policyVersion) : '<span class="muted">not recorded</span>'],
  ];
  if (doc.observationId) {
    rows.push(['Stored observation',
      `<code>${esc(doc.observationId)}</code> — model ${esc(doc.model)}, prompt ${esc(doc.promptTemplateId)} v${esc(doc.promptVersion)}`]);
  }

  b.innerHTML = matchLine +
    `<table><tbody>${rows.map(([k, v]) => `<tr><td class="site">${k}</td><td>${v}</td></tr>`).join('')}
       <tr><td class="site">Reconstruction</td><td>${replayChip}</td></tr></tbody></table>`;

  const steps = doc.steps ?? [];
  if (steps.length) {
    // The steps are the answer, and they are in the order they happened. A
    // reader asking "why did this fire" reads them top to bottom; a reader
    // asking "why did it not" reads the `action` step, which is the one that
    // carries the verification explanation.
    b.insertAdjacentHTML('beforeend', `
      <div class="eyebrow section-label">How the decision was reached</div>
      <table><thead><tr><th class="num">#</th><th>Kind</th><th>What happened</th></tr></thead><tbody>
      ${steps.map((s) => `<tr>
        <td class="num muted">${n0(s.order)}</td>
        <td><span class="chip unmeasured">${esc(s.kind)}</span></td>
        <td>${esc(s.description)}</td>
      </tr>`).join('')}</tbody></table>`);
  }
}
/* ---------- wire -------------------------------------------------------- */

async function post(url, body) {
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const text = await r.text();
  if (!r.ok) return { error: text || `${r.status} ${r.statusText}` };
  try { return JSON.parse(text); } catch { return { error: 'the answer was not JSON' }; }
}

function busy(btn, on) { btn.dataset.busy = on ? 'true' : 'false'; }

$('run-attention').addEventListener('click', async (e) => {
  const btn = e.currentTarget;
  busy(btn, true);
  try {
    const hours = Number($('hours').value) || 24;
    const doc = await post('/api/efficiency', { hours });
    if (doc.error) {
      $('attention-body').innerHTML = banner('bad', `The report could not be read: ${esc(doc.error)}`);
    } else {
      doc.hours = hours;
      renderAttention(doc);
      renderSilence(doc);
    }
  } finally { busy(btn, false); }
});

$('run-explain').addEventListener('click', async (e) => {
  const btn = e.currentTarget;
  const id = $('incident-id').value.trim();
  if (!id) { $('why-body').innerHTML = banner('warn', 'An incident id is required — the system explains a decision, not a search.'); return; }
  busy(btn, true);
  try { renderWhy(await post('/api/explain', { id }), id); }
  finally { busy(btn, false); }
});

$('run-incident').addEventListener('click', async (e) => {
  const btn = e.currentTarget;
  const id = $('incident-id').value.trim();
  if (!id) { $('why-body').innerHTML = banner('warn', 'An incident id is required.'); return; }
  busy(btn, true);
  try { renderWhy(await post('/api/incident', { id }), id); }
  finally { busy(btn, false); }
});
