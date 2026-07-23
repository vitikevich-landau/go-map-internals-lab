const app = {
  mode: "swiss",
  snapshot: null,
  busy: false,
  nextKey: 1,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];

const scenarios = {
  swiss: [
    ["small-to-table", "9-я запись: small → table"],
    ["table-grow", "15-я запись: grow 16 → 32"],
    ["split", "29-я запись: split"],
    ["directory", "Несколько split"],
  ],
  legacy: [
    ["evacuation", "Поймать oldbuckets"],
  ],
  real: [
    ["real-grow", "Поймать реальный grow"],
  ],
};

document.addEventListener("DOMContentLoaded", () => {
  bindEvents();
  loadState("swiss");
  updateCalculator();
});

function bindEvents() {
  $$(".mode-tab").forEach((button) => button.addEventListener("click", () => selectMode(button.dataset.mode)));
  $("#step-button").addEventListener("click", performStep);
  $("#reset-button").addEventListener("click", reset);
  $("#auto-button").addEventListener("click", autoInsert);
  $("#operation-kind").addEventListener("change", syncOperationForm);
  $("#key-input").addEventListener("keydown", enterToStep);
  $("#value-input").addEventListener("keydown", enterToStep);
  $("#expert-toggle").addEventListener("change", (event) => document.body.classList.toggle("expert", event.target.checked));
  $("#bucket-calculator").addEventListener("input", updateCalculator);
}

function enterToStep(event) {
  if (event.key === "Enter") performStep();
}

async function selectMode(mode) {
  app.mode = mode;
  $$(".mode-tab").forEach((button) => button.classList.toggle("active", button.dataset.mode === mode));
  const theory = mode === "theory";
  $("#lab-page").hidden = theory;
  $("#theory-page").hidden = !theory;
  if (!theory) {
    $("#scale-field").hidden = mode !== "swiss";
    configureScenarios();
    await loadState(mode);
  }
}

async function loadState(mode) {
  await withBusy(async () => {
    const response = await fetch(`/api/state?mode=${encodeURIComponent(mode)}`);
    render(await readJSON(response));
  });
}

async function performStep() {
  if (app.busy || app.mode === "theory") return;
  const kind = $("#operation-kind").value;
  const key = Number($("#key-input").value);
  const value = Number($("#value-input").value);
  if (!Number.isInteger(key) || !Number.isInteger(value)) {
    showError("Ключ и значение должны быть целыми числами.");
    return;
  }
  await withBusy(async () => {
    const response = await fetch("/api/operation", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode: app.mode, kind, key, value }),
    });
    render(await readJSON(response));
    if (kind === "insert") {
      app.nextKey = Math.max(app.nextKey, key + 1);
      $("#key-input").value = app.nextKey;
      $("#value-input").value = app.nextKey * 10;
    }
  });
}

async function reset() {
  await withBusy(async () => {
    const payload = { mode: app.mode };
    if (app.mode === "swiss") payload.maxTableCapacity = Number($("#scale-select").value);
    const response = await fetch("/api/reset", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    app.nextKey = 1;
    $("#key-input").value = 1;
    $("#value-input").value = 10;
    render(await readJSON(response));
  });
}

async function runScenario(name) {
  await withBusy(async () => {
    const response = await fetch("/api/scenario", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode: app.mode, scenario: name }),
    });
    const snapshot = await readJSON(response);
    render(snapshot);
    const count = Number(snapshot.stats?.find((stat) => /Элементы|count|Map.used|hmap.count/.test(stat.label))?.value?.match(/\d+/)?.[0] || 0);
    app.nextKey = count + 1;
    $("#key-input").value = app.nextKey;
    $("#value-input").value = app.nextKey * 10;
  });
}

async function autoInsert() {
  if (app.busy) return;
  setBusy(true);
  try {
    for (let i = 0; i < 10; i++) {
      const key = app.nextKey++;
      const response = await fetch("/api/operation", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ mode: app.mode, kind: "insert", key, value: key * 10 }),
      });
      render(await readJSON(response));
      await new Promise((resolve) => setTimeout(resolve, 120));
    }
    $("#key-input").value = app.nextKey;
    $("#value-input").value = app.nextKey * 10;
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

function configureScenarios() {
  const holder = $("#scenario-buttons");
  holder.innerHTML = "";
  for (const [id, label] of scenarios[app.mode] || []) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = label;
    button.addEventListener("click", () => runScenario(id));
    holder.append(button);
  }
}

function syncOperationForm() {
  const insert = $("#operation-kind").value === "insert";
  $("#value-field").style.visibility = insert ? "visible" : "hidden";
}

function render(snapshot) {
  app.snapshot = snapshot;
  $("#mode-title").textContent = snapshot.title;
  $("#mode-subtitle").textContent = snapshot.subtitle;
  $("#notice").textContent = snapshot.notice;
  $("#scale-note").textContent = snapshot.scaleNote || "";
  $("#event-counter").textContent = plural(snapshot.eventCount || 0, "шаг", "шага", "шагов");
  $("#mode-eyebrow").textContent = snapshot.mode === "real" ? "ФАКТИЧЕСКАЯ ПАМЯТЬ RUNTIME" : "ДЕТЕРМИНИРОВАННАЯ МОДЕЛЬ";
  renderStats(snapshot.stats || []);
  renderMemory(snapshot);
  renderTrace(snapshot.trace || []);
  configureScenarios();
}

function renderStats(stats) {
  $("#stats").innerHTML = stats.map((stat) => `
    <div class="stat-card">
      <span class="stat-label">${escapeHTML(stat.label)}</span>
      <strong class="stat-value" title="${escapeHTML(stat.value)}">${escapeHTML(stat.value)}</strong>
      <span class="stat-hint">${escapeHTML(stat.hint)}</span>
    </div>
  `).join("");
}

function renderMemory(snapshot) {
  const holder = $("#memory-view");
  if (snapshot.legacy) {
    holder.innerHTML = renderLegacy(snapshot.legacy);
    return;
  }
  if (!snapshot.tables?.length) {
    holder.innerHTML = `<div class="empty-memory"><div><b>Физическая память ещё не выделена</b><p>Сделайте первую запись — появится малая группа.</p></div></div>`;
    return;
  }
  let html = "";
  if (snapshot.directory?.length) {
    html += `
      <div class="directory-block" id="directory">
        <div class="subhead"><h3>Directory</h3><span>верхние globalDepth бит → table</span></div>
        <div class="directory-row">${snapshot.directory.map((entry) => `
          <div class="dir-cell ${entry.shared ? "shared" : ""}" id="dir-${entry.index}">
            <small>${escapeHTML(entry.bits)}</small><b>${escapeHTML(entry.table)}</b>
          </div>`).join("")}
        </div>
      </div>`;
  }
  html += snapshot.tables.map(renderTable).join("");
  holder.innerHTML = html;
  revealTraceTarget(snapshot.trace);
}

function renderTable(table) {
  return `
    <section class="table-card" id="${safeID(table.id)}">
      <header class="table-head">
        <span class="table-id">${escapeHTML(table.id)}</span>
        <strong>${table.used} / ${table.capacity} слотов</strong>
        <span class="meta">growthLeft ${table.growthLeft} · tomb ${table.tombstones} · localDepth ${table.localDepth}</span>
      </header>
      <div class="groups-row">
        ${table.groups.map((group) => renderGroup(table.id, group)).join("")}
      </div>
    </section>`;
}

function renderGroup(tableID, group) {
  return `
    <div class="group-card" id="${safeID(`${tableID}-g${group.index}`)}">
      <div class="group-head"><span>GROUP ${group.index}</span><span class="control-word">${escapeHTML(group.controlWord)}</span></div>
      <div class="slots">${group.slots.map(renderSlot).join("")}</div>
    </div>`;
}

function renderSlot(slot) {
  const pair = slot.key === undefined || slot.key === null ? "·" : `${slot.key}→${slot.value}`;
  return `
    <div class="slot ${safeClass(slot.state)} ${slot.highlight ? "changed" : ""}" id="${safeID(slot.physicalId || "")}" title="slot ${slot.index}; ${escapeHTML(slot.state)}; control ${escapeHTML(slot.control)}">
      <small>${slot.index}</small>
      <span class="pair">${escapeHTML(pair)}</span>
      <span class="ctrl">${escapeHTML(slot.control)}</span>
    </div>`;
}

function renderLegacy(legacy) {
  const progress = legacy.growing
    ? `<div class="evac-progress">Эвакуация активна: <b>nevacuate = ${legacy.nevacuate}</b> · старых бакетов ${legacy.oldBucketCount}. Бакеты левее nevacuate гарантированно готовы.</div>`
    : `<div class="evac-progress">Эвакуации сейчас нет: <b>oldbuckets = nil</b>.</div>`;
  const oldArray = legacy.growing ? `
    <section class="legacy-array old-array">
      <div class="subhead"><h3>oldbuckets · источник</h3><span>чтение отсюда, пока бакет не evacuated</span></div>
      <div class="bucket-grid">${legacy.oldBuckets.map((bucket) => renderBucket(bucket, "old")).join("")}</div>
    </section>` : "";
  return `
    ${progress}
    <div class="legacy-arrays" id="legacy-arrays">
      ${oldArray}
      <section class="legacy-array">
        <div class="subhead"><h3>buckets · ${legacy.growing ? "назначение" : "текущий массив"}</h3><span>2^B = ${2 ** legacy.B}</span></div>
        <div class="bucket-grid">${legacy.newBuckets.map((bucket) => renderBucket(bucket, "new")).join("")}</div>
      </section>
    </div>`;
}

function renderBucket(bucket, generation) {
  return `
    <div class="bucket-card ${bucket.evacuated ? "evacuated" : ""}" id="${generation}-b${bucket.index}">
      <div class="bucket-head"><b>BUCKET ${bucket.index}</b><span>${bucket.evacuated ? "EVACUATED" : "ACTIVE"}</span></div>
      ${bucket.chain.map((slots, index) => `
        ${index > 0 ? `<div class="overflow-label">overflow → bmap #${index}</div>` : ""}
        <div class="slots">${slots.map(renderSlot).join("")}</div>
      `).join("")}
    </div>`;
}

function renderTrace(trace) {
  $("#trace").innerHTML = trace.map((step) => `
    <li class="trace-item ${safeClass(step.tone || "info")}">
      <h3>${escapeHTML(step.title)}</h3>
      <p>${escapeHTML(step.detail)}</p>
      ${step.formula ? `<code class="formula advanced-only">${escapeHTML(step.formula)}</code>` : ""}
    </li>
  `).join("");
}

function revealTraceTarget(trace) {
  const target = [...trace].reverse().find((step) => step.target)?.target;
  if (!target) return;
  const element = document.getElementById(safeID(target));
  if (element?.classList.contains("group-card")) element.classList.add("highlight");
}

function updateCalculator() {
  const raw = Math.max(1, Number($("#bucket-calculator").value) || 1);
  $("#calc-min").textContent = Math.ceil(raw / 2);
  $("#calc-max").textContent = raw;
}

async function withBusy(action) {
  if (app.busy) return;
  setBusy(true);
  try {
    await action();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

function setBusy(value) {
  app.busy = value;
  document.body.classList.toggle("loading", value);
}

async function readJSON(response) {
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || "Не удалось выполнить запрос.");
  return payload;
}

function showError(message) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.classList.add("show");
  setTimeout(() => toast.classList.remove("show"), 3200);
}

function plural(value, one, few, many) {
  const mod10 = value % 10;
  const mod100 = value % 100;
  const word = mod10 === 1 && mod100 !== 11 ? one : mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) ? few : many;
  return `${value} ${word}`;
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[char]));
}

function safeClass(value) {
  return String(value || "").replace(/[^a-z0-9_-]/gi, "");
}

function safeID(value) {
  return String(value || "").replace(/[^a-z0-9_@-]/gi, "-");
}
