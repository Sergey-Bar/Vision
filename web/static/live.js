const els = {
  url: document.getElementById('youtubeUrl'), search: document.getElementById('search'), profile: document.getElementById('profile'), start: document.getElementById('startBtn'),
  player: document.getElementById('player'), analysisFrame: document.getElementById('analysisFrame'), overlay: document.getElementById('overlay'), status: document.getElementById('status'), backend: document.getElementById('backend'), performance: document.getElementById('performance'), clock: document.getElementById('clock'),
  people: document.getElementById('people'), vehicles: document.getElementById('vehicles'), animals: document.getElementById('animals'), other: document.getElementById('other'), uniqueTracks: document.getElementById('uniqueTracks'), restrictedCount: document.getElementById('restrictedCount'), averageConfidence: document.getElementById('averageConfidence'), occupancy: document.getElementById('occupancy'),
  alerts: document.getElementById('alerts'), hits: document.getElementById('hits'), detections: document.getElementById('detections'),
  criticalStreams: document.getElementById('criticalStreams'), recentClips: document.getElementById('recentClips'), chatQuestion: document.getElementById('chatQuestion'), chatSend: document.getElementById('chatSend'), chatAnswer: document.getElementById('chatAnswer')
};

let ws, generation = 0, pendingFrame, renderPending = false, currentVideoID = '';
let lastCriticalRefresh = 0;
const recentDetections = new Map();
const alertHistory = new Map();
const trackTrails = new Map();

function videoId(raw) {
  const m = raw.match(/(?:youtube\.com\/watch\?v=|youtu\.be\/|youtube\.com\/embed\/|youtube\.com\/live\/)([A-Za-z0-9_-]{11})/) || raw.match(/^([A-Za-z0-9_-]{11})$/);
  return m ? m[1] : '';
}

function start(reset = true) {
  generation++;
  if (ws) ws.close();
  const id = videoId(els.url.value.trim());
  if (!id) { els.status.textContent = 'invalid YouTube URL'; return; }
  if (id !== currentVideoID) {
    currentVideoID = id;
    els.player.src = `https://www.youtube.com/embed/${id}?autoplay=1&mute=1&playsinline=1`;
  }
  if (reset) {
    recentDetections.clear();
    alertHistory.clear();
    trackTrails.clear();
  }
  connect(generation);
}

function connect(token) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(`${proto}//${location.host}/ws/live?${new URLSearchParams({ url: els.url.value.trim(), profile: els.profile.value, protocol: '2' })}`);
  els.status.textContent = 'connecting detector';
  ws.onopen = () => { if (token === generation) els.status.textContent = 'warming CUDA model'; };
  ws.onerror = () => { if (token === generation) els.status.textContent = 'detector connection error'; };
  ws.onclose = ev => {
    if (token !== generation) return;
    if (ev.code === 4001) {
      els.status.textContent = 'analysis moved to another dashboard tab';
      return;
    }
    if (ev.code === 4002) {
      els.status.textContent = 'dashboard update required · reload this page';
      return;
    }
    els.status.textContent = 'disconnected · press Start Real Analysis';
  };
  ws.onmessage = ev => {
    if (token !== generation) return;
    const frame = JSON.parse(ev.data);
    if (frame.error) { els.status.textContent = frame.error; return; }
    pendingFrame = frame;
    if (!renderPending) {
      renderPending = true;
      requestAnimationFrame(() => { renderPending = false; if (pendingFrame) render(pendingFrame); });
    }
  };
}

function render(frame) {
  els.status.textContent = 'live';
  els.backend.textContent = `backend: ${frame.profile || frame.backend}`;
  els.performance.textContent = `${(frame.inferenceMs || 0).toFixed(0)} ms infer · ${(frame.encodeMs || 0).toFixed(0)} ms JPEG · ${(frame.analysisFps || 0).toFixed(1)} FPS · ${(frame.transportKB || 0).toFixed(0)} KB`;
  els.clock.textContent = `${frame.sourceWidth || '?'}×${frame.sourceHeight || '?'} · ${new Date(frame.at).toLocaleTimeString()}`;
  if (frame.imageData) els.analysisFrame.src = frame.imageData;
  els.people.textContent = frame.people || 0;
  els.vehicles.textContent = frame.vehicles || 0;
  els.animals.textContent = frame.animals || 0;
  els.other.textContent = frame.other || 0;
  els.uniqueTracks.textContent = frame.uniqueTracks || 0;
  els.restrictedCount.textContent = frame.restrictedCount || 0;
  els.averageConfidence.textContent = `${((frame.averageConfidence || 0) * 100).toFixed(0)}%`;
  els.occupancy.textContent = `${(frame.occupancyPercent || 0).toFixed(1)}%`;

  const now = Date.now();
  for (const d of frame.detections || []) {
    recentDetections.set(d.id, { ...d, lastSeen: now });
    const [x,y,w,h] = d.bbox;
    const trail = trackTrails.get(d.id) || [];
    trail.push([x + w / 2, y + h]);
    if (trail.length > 24) trail.shift();
    trackTrails.set(d.id, trail);
  }
  for (const [id, d] of recentDetections) if (now - d.lastSeen > 60000) recentDetections.delete(id);
  for (const id of trackTrails.keys()) if (!recentDetections.has(id)) trackTrails.delete(id);
  while (recentDetections.size > 5000) recentDetections.delete(recentDetections.keys().next().value);
  for (const alert of frame.alerts || []) alertHistory.set(alert.id, alert);
  while (alertHistory.size > 50) alertHistory.delete(alertHistory.keys().next().value);

  drawOverlay(frame);
  drawList(els.alerts, [...alertHistory.values()].reverse(), a => `<div class="item bad"><b>${escapeHtml(a.severity.toUpperCase())}</b> ${escapeHtml(a.message)}<small>${escapeHtml(a.rule)} · ${new Date(a.at).toLocaleTimeString()}</small></div>`, 'No zone-entry alerts.');
  renderSearch();
  drawList(els.detections, (frame.detections || []).slice(0, 100), d => `<div class="pill ${escapeHtml(d.category)}">${escapeHtml(d.label)} · ${(d.confidence * 100).toFixed(0)}% · track ${escapeHtml(d.id)} · ${escapeHtml(d.zone)}</div>`, 'No detections in this frame.');
  if (now - lastCriticalRefresh > 5000) { lastCriticalRefresh = now; refreshCritical(); refreshClips(); }
}

async function refreshClips() {
  try {
    const query = currentVideoID ? `?videoId=${encodeURIComponent(currentVideoID)}` : '';
    const response = await fetch(`/api/clips${query}`, { cache: 'no-store' });
    if (!response.ok) throw new Error(await response.text());
    const body = await response.json();
    drawList(els.recentClips, (body.clips || []).slice(0, 10), clip => `<a class="item clipLink" href="${escapeHtml(clip.uri)}" target="_blank" rel="noopener"><b>${new Date(clip.observedAt).toLocaleString()}</b><small>${escapeHtml(clip.videoId)} · ${Number(clip.metadata.durationSeconds || 0).toFixed(1)}s · SHA-256 ${escapeHtml(clip.sha256.slice(0, 12))}…</small></a>`, 'No completed alert clips yet.');
  } catch (error) {
    els.recentClips.textContent = `Clip catalog unavailable: ${error.message}`;
  }
}

async function refreshCritical() {
  try {
    const response = await fetch('/api/critical?limit=5', { cache: 'no-store' });
    if (!response.ok) throw new Error(await response.text());
    const body = await response.json();
    drawList(els.criticalStreams, body.streams || [], item => `<div class="item"><b>${escapeHtml(item.videoId)}</b> <span class="criticalScore">${Number(item.score).toFixed(1)}/100</span><small>${escapeHtml((item.reasons || []).join(' · '))}</small><button class="openCritical" data-video="${escapeHtml(item.videoId)}">Open stream</button></div>`, 'No recent stored streams.');
    els.criticalStreams.querySelectorAll('.openCritical').forEach(button => button.addEventListener('click', () => {
      els.url.value = `https://www.youtube.com/watch?v=${button.dataset.video}`;
      start(true);
    }));
  } catch (error) {
    els.criticalStreams.textContent = `Critical ranking unavailable: ${error.message}`;
  }
}

async function askChat() {
  const question = els.chatQuestion.value.trim();
  if (!question) return;
  els.chatSend.disabled = true;
  els.chatAnswer.textContent = 'Searching stored observations…';
  try {
    const response = await fetch('/api/chat', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question, videoId: currentVideoID || undefined })
    });
    if (!response.ok) throw new Error(await response.text());
    const body = await response.json();
    const citations = (body.citations || []).join(', ');
    els.chatAnswer.classList.toggle('muted', body.state !== 'answered');
    els.chatAnswer.textContent = `${body.answer}${citations ? `\nSources: ${citations}` : ''}`;
  } catch (error) {
    els.chatAnswer.classList.add('muted');
    els.chatAnswer.textContent = `Chat unavailable: ${error.message}`;
  } finally {
    els.chatSend.disabled = false;
  }
}

function renderSearch() {
  const tokens = els.search.value.toLowerCase().trim().split(/\s+/).filter(Boolean);
  if (!tokens.length) { drawList(els.hits, [], detectionHTML, 'Search the last 60 seconds without restarting analysis.'); return; }
  const hits = [...recentDetections.values()].filter(d => {
    const text = `${d.label} ${d.category} ${d.color || ''} ${d.vehicleType || ''} ${d.zone}`.toLowerCase();
    return tokens.every(token => text.includes(token));
  }).sort((a, b) => b.lastSeen - a.lastSeen);
  drawList(els.hits, hits.slice(0, 50), detectionHTML, 'No matching tracked objects in the last 60 seconds.');
}

function drawOverlay(frame) {
  const zones = (frame.zones || []).map(z => {
    const points = (z.points || []).map(p => `${p[0]*1000},${p[1]*562}`).join(' ');
    const color = z.type === 'restricted' ? '#fb7185' : '#38bdf8';
    return `<polygon points="${points}" fill="${color}22" stroke="${color}" stroke-width="2"/><text x="${z.points[0][0]*1000+8}" y="${z.points[0][1]*562+18}" fill="${color}" font-size="16">${escapeHtml(z.name)}</text>`;
  }).join('');
  const boxes = (frame.detections || []).map(d => {
    const [x,y,w,h] = d.bbox;
    const color = d.violation ? '#fb7185' : d.category === 'vehicle' ? '#38bdf8' : d.category === 'biological' ? '#34d399' : '#fbbf24';
    return `<rect x="${x*1000}" y="${y*562}" width="${w*1000}" height="${h*562}" fill="none" stroke="${color}" stroke-width="2"/><text x="${x*1000}" y="${Math.max(14, y*562-4)}" fill="${color}" font-size="14">${escapeHtml(d.label)} #${escapeHtml(d.id.split('-').pop())}</text>`;
  }).join('');
  const trails = (frame.detections || []).map(d => {
    const points = (trackTrails.get(d.id) || []).map(p => `${p[0]*1000},${p[1]*562}`).join(' ');
    if (!points) return '';
    const color = d.category === 'vehicle' ? '#38bdf8' : d.category === 'biological' ? '#34d399' : '#fbbf24';
    return `<polyline points="${points}" fill="none" stroke="${color}" stroke-width="2" stroke-opacity=".75"/>`;
  }).join('');
  els.overlay.innerHTML = zones + trails + boxes;
}

function drawList(root, items, fn, empty) {
  root.classList.toggle('muted', !items.length);
  root.innerHTML = items.length ? items.map(fn).join('') : empty;
}

function detectionHTML(d) {
  return `<div class="item"><b>${escapeHtml(d.label)}</b><small>${escapeHtml(d.category)} · ${escapeHtml(d.color || d.vehicleType || 'n/a')} · ${(d.confidence * 100).toFixed(0)}% · ${escapeHtml(d.zone)} · track ${escapeHtml(d.id)}</small></div>`;
}

function escapeHtml(s) { return String(s).replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c])); }

els.start.addEventListener('click', () => start(true));
els.search.addEventListener('input', renderSearch);
els.chatSend.addEventListener('click', askChat);
els.chatQuestion.addEventListener('keydown', event => { if (event.key === 'Enter') askChat(); });
refreshCritical();
refreshClips();
start(true);
