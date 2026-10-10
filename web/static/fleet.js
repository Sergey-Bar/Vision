// Fleet Dashboard JavaScript
const els = {
  // Summary
  fleetStatus: document.getElementById('fleetStatus'),
  totalCameras: document.getElementById('totalCameras'),
  healthyCameras: document.getElementById('healthyCameras'),
  degradedCameras: document.getElementById('degradedCameras'),
  unreachableCameras: document.getElementById('unreachableCameras'),
  disabledCameras: document.getElementById('disabledCameras'),
  activeWorkers: document.getElementById('activeWorkers'),
  totalFrames: document.getElementById('totalFrames'),
  refreshFleetBtn: document.getElementById('refreshFleetBtn'),
  addCameraBtn: document.getElementById('addCameraBtn'),
  fleetFilter: document.getElementById('fleetFilter'),
  
  // Camera grid
  gridContainer: document.getElementById('gridContainer'),
  
  // Alerts
  alertList: document.getElementById('alertList'),
  alertLevelFilter: document.getElementById('alertLevelFilter'),
  alertTimeRange: document.getElementById('alertTimeRange'),
  refreshAlertsBtn: document.getElementById('refreshAlertsBtn'),
  
  // Operator panel
  tabBtns: document.querySelectorAll('.tab-btn'),
  tabPanes: document.querySelectorAll('.tab-pane'),
  decisionList: document.getElementById('decisionList'),
  operatorChatInput: document.getElementById('operatorChatInput'),
  operatorChatSend: document.getElementById('operatorChatSend'),
  chatMessages: document.getElementById('chatMessages'),
  anomalySummary: document.getElementById('anomalySummary'),
  
  // Conversational search
  searchInput: document.getElementById('searchInput'),
  searchSend: document.getElementById('searchSend'),
  searchMessages: document.getElementById('searchMessages'),
  searchSuggestions: document.getElementById('searchSuggestions'),
  
  // Modals
  addCameraModal: document.getElementById('addCameraModal'),
  cameraDetailModal: document.getElementById('cameraDetailModal'),
  alertDetailModal: document.getElementById('alertDetailModal'),
  camName: document.getElementById('camName'),
  camId: document.getElementById('camId'),
  camUrl: document.getElementById('camUrl'),
  camType: document.getElementById('camType'),
  camProfile: document.getElementById('camProfile'),
  camZones: document.getElementById('camZones'),
  submitCamera: document.getElementById('submitCamera'),
  detailTitle: document.getElementById('detailTitle'),
  detailContent: document.getElementById('detailContent'),
  alertDetailTitle: document.getElementById('alertDetailTitle'),
  alertDetailContent: document.getElementById('alertDetailContent'),
  ackAlertBtn: document.getElementById('ackAlertBtn'),
  resolveAlertBtn: document.getElementById('resolveAlertBtn')
};

let currentCameras = [];
let currentAlerts = [];
let currentDecisions = [];
let selectedAlertId = null;
let fleetFilter = 'all';
let alertFilterLevel = 'all';
let alertFilterTime = '6h';

// Initialize
document.addEventListener('DOMContentLoaded', () => {
  setupEventListeners();
  refreshFleet();
  refreshAlerts();
  refreshDecisions();
  loadSearchSuggestions();
  
  // Auto-refresh every 30 seconds
  setInterval(() => {
    refreshFleet();
    refreshAlerts();
    refreshDecisions();
  }, 30000);
});

function setupEventListeners() {
  // Fleet controls
  els.refreshFleetBtn.addEventListener('click', refreshFleet);
  els.addCameraBtn.addEventListener('click', () => els.addCameraModal.showModal());
  els.submitCamera.addEventListener('click', addCamera);
  els.fleetFilter.addEventListener('change', (e) => {
    fleetFilter = e.target.value;
    renderCameraGrid();
  });
  
  // Alert controls
  els.refreshAlertsBtn.addEventListener('click', refreshAlerts);
  els.alertLevelFilter.addEventListener('change', (e) => {
    alertFilterLevel = e.target.value;
    renderAlerts();
  });
  els.alertTimeRange.addEventListener('change', (e) => {
    alertFilterTime = e.target.value;
    refreshAlerts();
  });
  
  // Tab controls
  els.tabBtns.forEach(btn => {
    btn.addEventListener('click', () => {
      els.tabBtns.forEach(b => b.classList.remove('active'));
      els.tabPanes.forEach(p => p.classList.remove('active'));
      btn.classList.add('active');
      const tab = btn.dataset.tab;
      document.getElementById(`tab-${tab}`).classList.add('active');
    });
  });
  
  // Operator chat
  els.operatorChatSend.addEventListener('click', sendOperatorMessage);
  els.operatorChatInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') sendOperatorMessage();
  });
  
  // Conversational search
  els.searchSend.addEventListener('click', performSearch);
  els.searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') performSearch();
  });
  
  // Alert detail modal
  els.ackAlertBtn.addEventListener('click', () => {
    if (selectedAlertId) acknowledgeAlert(selectedAlertId);
    els.alertDetailModal.close();
  });
  els.resolveAlertBtn.addEventListener('click', () => {
    if (selectedAlertId) resolveAlert(selectedAlertId);
    els.alertDetailModal.close();
  });
}

// API Calls
async function refreshFleet() {
  try {
    const response = await fetch('/api/fleet/summary');
    if (!response.ok) throw new Error('Failed to fetch fleet summary');
    const summary = await response.json();
    updateFleetSummary(summary);
    
    const camsResponse = await fetch('/api/fleet/cameras');
    if (!camsResponse.ok) throw new Error('Failed to fetch cameras');
    currentCameras = await camsResponse.json();
    renderCameraGrid();
    
    els.fleetStatus.textContent = 'online';
    els.fleetStatus.className = 'status-badge healthy';
  } catch (error) {
    console.error('Failed to refresh fleet:', error);
    els.fleetStatus.textContent = 'error';
    els.fleetStatus.className = 'status-badge unreachable';
  }
}

function updateFleetSummary(summary) {
  els.totalCameras.textContent = summary.total_cameras || 0;
  els.healthyCameras.textContent = summary.healthy || 0;
  els.degradedCameras.textContent = summary.degraded || 0;
  els.unreachableCameras.textContent = summary.unreachable || 0;
  els.disabledCameras.textContent = summary.disabled || 0;
  els.activeWorkers.textContent = summary.active_workers || 0;
  els.totalFrames.textContent = (summary.total_frames_processed || 0).toLocaleString();
}

function renderCameraGrid() {
  let filtered = currentCameras;
  if (fleetFilter !== 'all') {
    filtered = currentCameras.filter(c => c.status === fleetFilter);
  }
  
  if (filtered.length === 0) {
    els.gridContainer.innerHTML = '<div class="loading muted">No cameras found</div>';
    return;
  }
  
  els.gridContainer.innerHTML = filtered.map(camera => `
    <div class="camera-card ${camera.status}">
      <div class="camera-card-header">
        <h3>${escapeHtml(camera.name)}</h3>
        <div class="camera-status">
          <div class="status-dot ${camera.status}"></div>
          <span>${camera.status}</span>
        </div>
      </div>
      <div class="camera-card-meta">
        <div><strong>ID</strong><span>${camera.id}</span></div>
        <div><strong>Type</strong><span>${camera.type}</span></div>
        <div><strong>Profile</strong><span>${camera.profile}</span></div>
        <div><strong>Failures</strong><span>${camera.consecutive_failures}</span></div>
        <div><strong>Last Frame</strong><span>${camera.last_frame_at ? new Date(camera.last_frame_at).toLocaleTimeString() : 'never'}</span></div>
        <div><strong>Updated</strong><span>${new Date(camera.updated_at).toLocaleTimeString()}</span></div>
      </div>
      <div class="camera-card-actions">
        <button onclick="viewCameraDetails('${camera.id}')">Details</button>
        <button onclick="restartCamera('${camera.id}')">Restart</button>
        <button onclick="removeCamera('${camera.id}')">Remove</button>
      </div>
    </div>
  `).join('');
}

async function refreshAlerts() {
  try {
    const response = await fetch('/api/operator/alerts?include_acknowledged=true&limit=100');
    if (!response.ok) throw new Error('Failed to fetch alerts');
    currentAlerts = await response.json();
    renderAlerts();
  } catch (error) {
    console.error('Failed to refresh alerts:', error);
    els.alertList.innerHTML = '<div class="loading muted">Failed to load alerts</div>';
  }
}

function renderAlerts() {
  let filtered = currentAlerts;
  
  // Filter by level
  if (alertFilterLevel !== 'all') {
    filtered = filtered.filter(a => a.level === alertFilterLevel);
  }
  
  // Filter by time
  const now = new Date();
  let cutoff = new Date();
  if (alertFilterTime === '1h') cutoff.setHours(now.getHours() - 1);
  else if (alertFilterTime === '6h') cutoff.setHours(now.getHours() - 6);
  else if (alertFilterTime === '24h') cutoff.setHours(now.getHours() - 24);
  else if (alertFilterTime === '7d') cutoff.setDate(now.getDate() - 7);
  filtered = filtered.filter(a => new Date(a.timestamp) > cutoff);
  
  // Sort by timestamp descending
  filtered.sort((a, b) => new Date(b.timestamp) - new Date(a.timestamp));
  
  if (filtered.length === 0) {
    els.alertList.innerHTML = '<div class="loading muted">No alerts in this time range</div>';
    return;
  }
  
  els.alertList.innerHTML = filtered.map(alert => `
    <div class="alert-item ${alert.level}" onclick="viewAlertDetails('${alert.id}')">
      <div class="alert-header">
        <div class="alert-title">${escapeHtml(alert.title)}</div>
        <div class="alert-meta">
          <span>${alert.level.toUpperCase()}</span>
          <span>${new Date(alert.timestamp).toLocaleString()}</span>
        </div>
      </div>
      <div class="alert-description">${escapeHtml(alert.description)}</div>
      <div class="alert-actions">
        ${!alert.acknowledged ? `<button onclick="event.stopPropagation(); acknowledgeAlert('${alert.id}')">Acknowledge</button>` : ''}
        ${!alert.resolved ? `<button onclick="event.stopPropagation(); resolveAlert('${alert.id}')">Resolve</button>` : ''}
      </div>
    </div>
  `).join('');
}

async function refreshDecisions() {
  try {
    const response = await fetch('/api/operator/decisions?limit=50');
    if (!response.ok) throw new Error('Failed to fetch decisions');
    currentDecisions = await response.json();
    renderDecisions();
  } catch (error) {
    console.error('Failed to refresh decisions:', error);
    els.decisionList.innerHTML = '<div class="loading muted">Failed to load decisions</div>';
  }
}

function renderDecisions() {
  if (currentDecisions.length === 0) {
    els.decisionList.innerHTML = '<div class="loading muted">No autonomous decisions yet</div>';
    return;
  }
  
  els.decisionList.innerHTML = currentDecisions.map(decision => {
    const priorityClass = decision.priority >= 8 ? 'high' : decision.priority >= 6 ? 'medium' : 'low';
    return `
      <div class="decision-item">
        <div class="decision-header">
          <div class="decision-type">${escapeHtml(decision.action_type.replace(/_/g, ' '))}</div>
          <span class="decision-priority ${priorityClass}">Priority ${decision.priority}</span>
        </div>
        <div class="alert-description">${escapeHtml(decision.reason)}</div>
        <div class="alert-meta">
          <span>Camera: ${escapeHtml(decision.camera_id)}</span>
          <span>${new Date(decision.timestamp).toLocaleString()}</span>
          <span>${decision.executed ? 'Executed' : 'Pending'}</span>
        </div>
      </div>
    `;
  }).join('');
}

async function sendOperatorMessage() {
  const message = els.operatorChatInput.value.trim();
  if (!message) return;
  
  // Add user message
  addChatMessage('user', message, els.chatMessages);
  els.operatorChatInput.value = '';
  els.operatorChatSend.disabled = true;
  
  try {
    const response = await fetch('/api/operator/command', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ command: message })
    });
    if (!response.ok) throw new Error('Failed to send command');
    const result = await response.json();
    addChatMessage('system', result.answer, els.chatMessages, new Date().toISOString());
  } catch (error) {
    addChatMessage('system', `Error: ${error.message}`, els.chatMessages);
  } finally {
    els.operatorChatSend.disabled = false;
  }
}

function addChatMessage(role, content, container, timestamp = null) {
  const msg = document.createElement('div');
  msg.className = `chat-message ${role}`;
  const timeStr = timestamp ? new Date(timestamp).toLocaleString() : 'now';
  msg.innerHTML = `
    <div class="meta">${role === 'user' ? 'You' : 'Operator'} • ${timeStr}</div>
    <div class="content">${escapeHtml(content)}</div>
  `;
  container.appendChild(msg);
  container.scrollTop = container.scrollHeight;
}

async function performSearch() {
  const question = els.searchInput.value.trim();
  if (!question) return;
  
  addChatMessage('user', question, els.searchMessages);
  els.searchInput.value = '';
  els.searchSend.disabled = true;
  
  try {
    const response = await fetch('/api/search/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question, limit: 20 })
    });
    if (!response.ok) throw new Error('Failed to perform search');
    const result = await response.json();
    let answer = result.answer;
    if (result.citations && result.citations.length > 0) {
      answer += `\n\nSources: ${result.citations.join(', ')}`;
    }
    addChatMessage('system', answer, els.searchMessages, new Date().toISOString());
    
    // Show results if available
    if (result.results && result.results.length > 0) {
      const resultsText = result.results.map(r => 
        `  • ${r.label} in ${r.zone} on ${r.videoId} at ${r.observedAt} (conf: ${Math.round(r.confidence * 100)}%)`
      ).join('\n');
      addChatMessage('system', `Results:\n${resultsText}`, els.searchMessages);
    }
  } catch (error) {
    addChatMessage('system', `Error: ${error.message}`, els.searchMessages);
  } finally {
    els.searchSend.disabled = false;
  }
}

function loadSearchSuggestions() {
  const suggestions = [
    "How many people were detected today?",
    "Show me vehicles in the parking lot yesterday",
    "Any incidents in the last hour?",
    "What cameras have the most activity?",
    "Show me all anomalies in the restricted zone",
    "How many vehicles entered in the last 24 hours?",
    "Show me critical alerts from the last 6 hours",
    "Which cameras are unhealthy?"
  ];
  
  els.searchSuggestions.innerHTML = suggestions.map(s => 
    `<div class="suggestion-chip" onclick="els.searchInput.value='${s}'; performSearch()">${escapeHtml(s)}</div>`
  ).join('');
}

async function addCamera() {
  try {
    let zones = null;
    if (els.camZones.value.trim()) {
      zones = JSON.parse(els.camZones.value.trim());
    }
    
    const response = await fetch('/api/fleet/cameras', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: els.camName.value,
        camera_id: els.camId.value || undefined,
        url: els.camUrl.value,
        type: els.camType.value,
        profile: els.camProfile.value,
        zone_config: zones
      })
    });
    if (!response.ok) throw new Error('Failed to add camera');
    
    els.addCameraModal.close();
    els.camName.value = '';
    els.camId.value = '';
    els.camUrl.value = '';
    els.camZones.value = '';
    refreshFleet();
  } catch (error) {
    alert(`Failed to add camera: ${error.message}`);
  }
}

async function restartCamera(cameraId) {
  if (!confirm(`Restart camera ${cameraId}?`)) return;
  try {
    const response = await fetch(`/api/fleet/cameras/${cameraId}/restart`, {
      method: 'POST'
    });
    if (!response.ok) throw new Error('Failed to restart camera');
    refreshFleet();
  } catch (error) {
    alert(`Failed to restart camera: ${error.message}`);
  }
}

async function removeCamera(cameraId) {
  if (!confirm(`Remove camera ${cameraId}? This cannot be undone.`)) return;
  try {
    const response = await fetch(`/api/fleet/cameras/${cameraId}`, {
      method: 'DELETE'
    });
    if (!response.ok) throw new Error('Failed to remove camera');
    refreshFleet();
  } catch (error) {
    alert(`Failed to remove camera: ${error.message}`);
  }
}

async function viewCameraDetails(cameraId) {
  try {
    const response = await fetch(`/api/fleet/cameras/${cameraId}`);
    if (!response.ok) throw new Error('Failed to get camera details');
    const camera = await response.json();
    
    els.detailTitle.textContent = `Camera: ${camera.name}`;
    els.detailContent.innerHTML = `
      <div><strong>ID</strong>${camera.id}</div>
      <div><strong>Name</strong>${camera.name}</div>
      <div><strong>Type</strong>${camera.type}</div>
      <div><strong>Status</strong>${camera.status}</div>
      <div><strong>URL</strong><span style="word-break: break-all">${escapeHtml(camera.url)}</span></div>
      <div><strong>Profile</strong>${camera.profile}</div>
      <div><strong>Consecutive Failures</strong>${camera.consecutive_failures}</div>
      <div><strong>Created At</strong>${new Date(camera.created_at).toLocaleString()}</div>
      <div><strong>Updated At</strong>${new Date(camera.updated_at).toLocaleString()}</div>
      <div><strong>Last Frame</strong>${camera.last_frame_at ? new Date(camera.last_frame_at).toLocaleString() : 'never'}</div>
      ${camera.zone_config ? `<div style="grid-column: 1 / -1"><strong>Zone Config</strong><pre style="margin-top: 0.5rem; white-space: pre-wrap; word-break: break-all; font-size: 0.75rem">${escapeHtml(JSON.stringify(camera.zone_config, null, 2))}</pre></div>` : ''}
    `;
    els.cameraDetailModal.showModal();
  } catch (error) {
    alert(`Failed to get camera details: ${error.message}`);
  }
}

function viewAlertDetails(alertId) {
  selectedAlertId = alertId;
  const alert = currentAlerts.find(a => a.id === alertId);
  if (!alert) return;
  
  els.alertDetailTitle.textContent = alert.title;
  els.alertDetailContent.innerHTML = `
    <div><strong>ID</strong>${alert.id}</div>
    <div><strong>Level</strong>${alert.level.toUpperCase()}</div>
    <div><strong>Camera</strong>${alert.camera_id || 'all'}</div>
    <div><strong>Type</strong>${alert.anomaly_type || 'system'}</div>
    <div><strong>Timestamp</strong>${new Date(alert.timestamp).toLocaleString()}</div>
    <div><strong>Acknowledged</strong>${alert.acknowledged ? 'Yes' : 'No'}</div>
    <div><strong>Resolved</strong>${alert.resolved ? 'Yes' : 'No'}</div>
    <div style="grid-column: 1 / -1"><strong>Description</strong>${escapeHtml(alert.description)}</div>
    ${alert.suggested_actions && alert.suggested_actions.length > 0 ? `<div style="grid-column: 1 / -1"><strong>Suggested Actions</strong>${alert.suggested_actions.map(a => a.replace(/_/g, ' ')).join(', ')}</div>` : ''}
    ${alert.metadata ? `<div style="grid-column: 1 / -1"><strong>Metadata</strong><pre style="margin-top: 0.5rem; white-space: pre-wrap; word-break: break-all; font-size: 0.75rem">${escapeHtml(JSON.stringify(alert.metadata, null, 2))}</pre></div>` : ''}
  `;
  els.alertDetailModal.showModal();
}

async function acknowledgeAlert(alertId) {
  try {
    const response = await fetch(`/api/operator/alerts/${alertId}/acknowledge`, {
      method: 'POST'
    });
    if (!response.ok) throw new Error('Failed to acknowledge alert');
    refreshAlerts();
  } catch (error) {
    alert(`Failed to acknowledge alert: ${error.message}`);
  }
}

async function resolveAlert(alertId) {
  try {
    const response = await fetch(`/api/operator/alerts/${alertId}/resolve`, {
      method: 'POST'
    });
    if (!response.ok) throw new Error('Failed to resolve alert');
    refreshAlerts();
  } catch (error) {
    alert(`Failed to resolve alert: ${error.message}`);
  }
}

function applyFilter() {
  fleetFilter = els.fleetFilter.value;
  renderCameraGrid();
}

function applyAlertFilter() {
  alertFilterLevel = els.alertLevelFilter.value;
  alertFilterTime = els.alertTimeRange.value;
  renderAlerts();
}

function escapeHtml(s) {
  return String(s).replace(/[&<>'"]/g, c => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    "'": '&#39;',
    '"': '&quot;'
  }[c]));
}