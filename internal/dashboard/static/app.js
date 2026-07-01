const state = {
  queue: [],
  uploads: [],
  downloads: [],
  labels: [],
};

const els = {
  status: document.getElementById("connectionStatus"),
  totalUploaded: document.getElementById("totalUploaded"),
  totalDownloaded: document.getElementById("totalDownloaded"),
  uploadedRate: document.getElementById("uploadedRate"),
  readCalls: document.getElementById("readCalls"),
  queueDepth: document.getElementById("queueDepth"),
  queueBytes: document.getElementById("queueBytes"),
  pressureLevel: document.getElementById("pressureLevel"),
  stagingUsage: document.getElementById("stagingUsage"),
  dirtyCount: document.getElementById("dirtyCount"),
  syncingCount: document.getElementById("syncingCount"),
  syncedCount: document.getElementById("syncedCount"),
  lastUpdated: document.getElementById("lastUpdated"),
  capacityPercent: document.getElementById("capacityPercent"),
  pendingFiles: document.getElementById("pendingFiles"),
  syncedFiles: document.getElementById("syncedFiles"),
};

const historyCanvas = document.getElementById("historyChart");
const capacityCanvas = document.getElementById("capacityChart");
const transferCanvas = document.getElementById("transferChart");

function formatBytes(bytes) {
  const value = Number(bytes || 0);
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const power = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const scaled = value / Math.pow(1024, power);
  return `${scaled >= 10 || power === 0 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[power]}`;
}

function formatNumber(value) {
  return new Intl.NumberFormat().format(Number(value || 0));
}

function formatAge(seconds) {
  if (!Number.isFinite(Number(seconds))) return "now";
  if (seconds < 60) return `${Math.max(0, Math.round(seconds))}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${Math.round(seconds / 3600)}h`;
}

function formatTime(timestamp) {
  if (!timestamp) return "";
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(timestamp));
}

function setStatus(kind, text) {
  els.status.className = `status-pill ${kind}`;
  els.status.lastChild.nodeValue = ` ${text}`;
}

async function loadData() {
  const response = await fetch("/debug/dashboard/data", { cache: "no-store" });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return response.json();
}

function pushHistory(data) {
  const sync = data.sync || {};
  const transfers = data.transfers || {};
  const now = new Date();
  state.queue.push(Number(sync.sync_queue_bytes || 0));
  state.uploads.push(Number(sync.total_uploaded_bytes || 0));
  state.downloads.push(Number(transfers.bytes_read_total || 0));
  state.labels.push(now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }));

  const maxPoints = 38;
  for (const key of ["queue", "uploads", "downloads", "labels"]) {
    if (state[key].length > maxPoints) state[key].shift();
  }
}

function renderSummary(data) {
  const sync = data.sync || {};
  const perf = data.performance || {};
  const transfers = data.transfers || perf.transfers || {};
  const cos = perf.cos_operations || {};
  const lastSync = sync.last_sync || {};
  const used = Number(sync.staging_used_bytes || 0);
  const available = Number(sync.staging_available_bytes || 0);
  const totalCapacity = used + available;
  const capacityPct = totalCapacity > 0 ? Math.round((used / totalCapacity) * 100) : 0;

  els.totalUploaded.textContent = formatBytes(sync.total_uploaded_bytes);
  els.totalDownloaded.textContent = formatBytes(transfers.bytes_read_total);
  els.uploadedRate.textContent = `${Number(lastSync.upload_mib_per_second || 0).toFixed(1)} MiB/s last upload`;
  els.readCalls.textContent = `${formatNumber(cos.get_object)} COS reads`;
  els.queueDepth.textContent = `${formatNumber(sync.sync_queue_depth)} files`;
  els.queueBytes.textContent = `${formatBytes(sync.sync_queue_bytes)} waiting`;
  els.pressureLevel.textContent = String(sync.staging_pressure_level || "normal");
  els.stagingUsage.textContent = `${formatBytes(used)} staged`;
  els.dirtyCount.textContent = formatNumber(sync.sync_queue_depth);
  els.syncingCount.textContent = formatNumber(sync.syncing_files);
  els.syncedCount.textContent = formatNumber(sync.total_synced_files);
  els.capacityPercent.textContent = `${capacityPct}%`;
  els.lastUpdated.textContent = `Updated ${new Date().toLocaleTimeString()}`;

  drawHistoryChart();
  drawCapacityChart(capacityPct, sync.staging_pressure_level);
  drawTransferChart(Number(sync.total_uploaded_bytes || 0), Number(transfers.bytes_read_total || 0), Number(transfers.bytes_written_total || 0));
}

function renderTables(data) {
  const sync = data.sync || {};
  const pending = sync.pending_files || [];
  const synced = sync.recent_synced_files || [];

  els.pendingFiles.innerHTML = pending.length
    ? pending.map((file) => `
      <tr>
        <td class="path-cell" title="${escapeHtml(file.path)}">${escapeHtml(file.path)}</td>
        <td><span class="badge">${escapeHtml(file.status || "queued")}</span></td>
        <td>${formatBytes(file.size_bytes)}</td>
        <td>
          <div class="progress" title="${Math.round(file.progress_percent || 0)}%">
            <span style="width:${Math.max(5, Math.min(100, Number(file.progress_percent || 0)))}%"></span>
          </div>
          <small>${formatAge(file.age_seconds)} old</small>
        </td>
      </tr>
    `).join("")
    : `<tr><td class="empty" colspan="4">No files are waiting for sync.</td></tr>`;

  els.syncedFiles.innerHTML = synced.length
    ? synced.map((file) => `
      <tr>
        <td class="path-cell" title="${escapeHtml(file.path)}">${escapeHtml(file.path)}</td>
        <td>${formatBytes(file.bytes)}</td>
        <td>${Number(file.upload_mib_per_second || 0).toFixed(1)} MiB/s</td>
        <td><span class="badge synced">${formatTime(file.completed_at)}</span></td>
      </tr>
    `).join("")
    : `<tr><td class="empty" colspan="4">Synced files will appear here after the first completed upload.</td></tr>`;
}

function escapeHtml(value) {
  return String(value || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function canvasContext(canvas) {
  const frame = canvas.parentElement || canvas;
  const rect = frame.getBoundingClientRect();
  const ratio = window.devicePixelRatio || 1;
  const width = Math.max(1, rect.width);
  const height = Math.max(1, rect.height);
  canvas.width = Math.floor(width * ratio);
  canvas.height = Math.floor(height * ratio);
  canvas.style.width = `${width}px`;
  canvas.style.height = `${height}px`;
  const ctx = canvas.getContext("2d");
  ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
  return { ctx, width, height };
}

function drawHistoryChart() {
  const { ctx, width, height } = canvasContext(historyCanvas);
  ctx.clearRect(0, 0, width, height);
  drawGrid(ctx, width, height);
  drawAxisLabel(ctx, width, height, "Sync backlog");
  drawLine(ctx, width, height, state.queue, "#008c95", true);
}

function drawCapacityChart(percent, level) {
  const { ctx, width, height } = canvasContext(capacityCanvas);
  const size = Math.min(width, height);
  const cx = width / 2;
  const cy = height / 2;
  const radius = size * 0.36;
  const line = size * 0.09;
  const color = level === "critical" ? "#ce3d3d" : level === "high" ? "#d18a00" : "#16a36a";
  ctx.clearRect(0, 0, width, height);
  ctx.lineWidth = line;
  ctx.lineCap = "round";
  ctx.strokeStyle = "#dfe8ef";
  ctx.beginPath();
  ctx.arc(cx, cy, radius, 0, Math.PI * 2);
  ctx.stroke();
  ctx.strokeStyle = color;
  ctx.beginPath();
  ctx.arc(cx, cy, radius, -Math.PI / 2, -Math.PI / 2 + Math.PI * 2 * (percent / 100));
  ctx.stroke();
}

function drawTransferChart(uploaded, downloaded, written) {
  const { ctx, width, height } = canvasContext(transferCanvas);
  ctx.clearRect(0, 0, width, height);
  drawGrid(ctx, width, height);
  const values = [
    { label: "Uploaded", value: uploaded, color: "#008c95" },
    { label: "Downloaded", value: downloaded, color: "#266bd3" },
    { label: "Written", value: written, color: "#d18a00" },
  ];
  const max = Math.max(...values.map((item) => item.value), 1);
  const barWidth = Math.min(58, width / 6);
  const gap = (width - values.length * barWidth) / (values.length + 1);
  values.forEach((item, index) => {
    const x = gap + index * (barWidth + gap);
    const barHeight = Math.max(3, (height - 76) * (item.value / max));
    const y = height - 42 - barHeight;
    ctx.fillStyle = item.color;
    roundRect(ctx, x, y, barWidth, barHeight, 8);
    ctx.fill();
    ctx.fillStyle = "#17202a";
    ctx.font = "700 12px system-ui";
    ctx.textAlign = "center";
    ctx.fillText(item.label, x + barWidth / 2, height - 22);
    ctx.fillStyle = "#5c6876";
    ctx.fillText(formatBytes(item.value), x + barWidth / 2, Math.max(18, y - 8));
  });
}

function drawAxisLabel(ctx, width, height, label) {
  ctx.fillStyle = "#5c6876";
  ctx.font = "700 11px system-ui";
  ctx.textAlign = "left";
  ctx.fillText(label, 10, 18);
  ctx.textAlign = "right";
  const latest = state.queue[state.queue.length - 1] || 0;
  ctx.fillText(formatBytes(latest), width - 10, 18);
}

function drawGrid(ctx, width, height) {
  ctx.strokeStyle = "#e5edf3";
  ctx.lineWidth = 1;
  for (let i = 1; i < 4; i++) {
    const y = (height / 4) * i;
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(width, y);
    ctx.stroke();
  }
}

function drawLine(ctx, width, height, values, color, fill) {
  if (!values.length) return;
  const max = Math.max(...values, 1);
  const pad = 10;
  const topPad = 26;
  const bottomPad = 10;
  const points = values.map((value, index) => ({
    x: values.length === 1 ? pad : pad + (index / (values.length - 1)) * (width - pad * 2),
    y: height - bottomPad - (Number(value || 0) / max) * (height - topPad - bottomPad),
  }));
  ctx.strokeStyle = color;
  ctx.lineWidth = 3;
  ctx.beginPath();
  points.forEach((point, index) => {
    if (index === 0) ctx.moveTo(point.x, point.y);
    else ctx.lineTo(point.x, point.y);
  });
  ctx.stroke();
  if (!fill) return;
  ctx.lineTo(points[points.length - 1].x, height - bottomPad);
  ctx.lineTo(points[0].x, height - bottomPad);
  ctx.closePath();
  const gradient = ctx.createLinearGradient(0, 0, 0, height);
  gradient.addColorStop(0, "rgba(0, 140, 149, 0.22)");
  gradient.addColorStop(1, "rgba(0, 140, 149, 0.02)");
  ctx.fillStyle = gradient;
  ctx.fill();
}

function roundRect(ctx, x, y, width, height, radius) {
  const r = Math.min(radius, width / 2, height / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + width, y, x + width, y + height, r);
  ctx.arcTo(x + width, y + height, x, y + height, r);
  ctx.arcTo(x, y + height, x, y, r);
  ctx.arcTo(x, y, x + width, y, r);
  ctx.closePath();
}

async function tick() {
  try {
    const data = await loadData();
    pushHistory(data);
    renderSummary(data);
    renderTables(data);
    setStatus("online", "Live");
  } catch (error) {
    setStatus("offline", "Offline");
    els.lastUpdated.textContent = error.message;
  }
}

window.addEventListener("resize", () => {
  drawHistoryChart();
  drawCapacityChart(Number(els.capacityPercent.textContent.replace("%", "")) || 0, els.pressureLevel.textContent);
});

tick();
setInterval(tick, 2500);
