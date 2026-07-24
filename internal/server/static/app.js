// Словарь машинных значений, которыми браузер обменивается с Go-сервером.
// Константы не меняют JSON-контракт, но убирают разбросанные строковые литералы.
const SimulationMode = Object.freeze({
  SWISS: "swiss",
  LEGACY: "legacy",
  REAL: "real",
  THEORY: "theory",
});

const OperationKind = Object.freeze({
  INSERT: "insert",
  READ: "read",
  DELETE: "delete",
});

const ScenarioName = Object.freeze({
  SMALL_TO_TABLE: "small-to-table",
  TABLE_GROW: "table-grow",
  SPLIT: "split",
  DIRECTORY: "directory",
  EVACUATION: "evacuation",
  REAL_GROW: "real-grow",
});

/**
 * Изменяемое состояние браузерного приложения.
 *
 * @typedef {Object} ApplicationState
 * @property {string} mode Выбранный режим лаборатории.
 * @property {?Object} snapshot Последний снимок, полученный от сервера.
 * @property {boolean} busy Признак выполняющегося HTTP-запроса.
 * @property {number} nextKey Следующий ключ для автоматической записи.
 */

/** @type {ApplicationState} */
const app = {
  mode: SimulationMode.SWISS,
  snapshot: null,
  busy: false,
  nextKey: 1,
};

// Короткие помощники поиска одного или нескольких элементов интерфейса.
const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];

// Сценарий — это заранее подготовленный сервером учебный этап, до которого
// вручную пришлось бы выполнять много одинаковых операций.
const scenarios = {
  [SimulationMode.SWISS]: [
    [ScenarioName.SMALL_TO_TABLE, "9-я запись: small → table"],
    [ScenarioName.TABLE_GROW, "15-я запись: grow 16 → 32"],
    [ScenarioName.SPLIT, "29-я запись: split"],
    [ScenarioName.DIRECTORY, "Несколько split"],
  ],
  [SimulationMode.LEGACY]: [
    [ScenarioName.EVACUATION, "Поймать oldbuckets"],
  ],
  [SimulationMode.REAL]: [
    [ScenarioName.REAL_GROW, "Поймать реальный grow"],
  ],
};

document.addEventListener("DOMContentLoaded", () => {
  bindEvents();
  loadState(SimulationMode.SWISS);
  updateCalculator();
});

// bindEvents один раз связывает элементы страницы с действиями приложения.
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

// enterToStep позволяет выполнять выбранную операцию клавишей Enter.
function enterToStep(event) {
  if (event.key === "Enter") performStep();
}

// selectMode переключает вкладку и при необходимости загружает снимок сервера.
async function selectMode(mode) {
  app.mode = mode;
  $$(".mode-tab").forEach((button) => button.classList.toggle("active", button.dataset.mode === mode));
  const theory = mode === SimulationMode.THEORY;
  $("#lab-page").hidden = theory;
  $("#theory-page").hidden = !theory;
  if (!theory) {
    $("#scale-field").hidden = mode !== SimulationMode.SWISS;
    configureScenarios();
    await loadState(mode);
  }
}

// loadState получает текущее состояние выбранной реализации без изменения map.
async function loadState(mode) {
  await withBusy(async () => {
    const response = await fetch(`/api/state?mode=${encodeURIComponent(mode)}`);
    render(await readJSON(response));
  });
}

// performStep проверяет введённые числа, отправляет одну операцию и отображает
// получившийся снимок.
async function performStep() {
  if (app.busy || app.mode === SimulationMode.THEORY) return;

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

    if (kind === OperationKind.INSERT) {
      app.nextKey = Math.max(app.nextKey, key + 1);
      $("#key-input").value = app.nextKey;
      $("#value-input").value = app.nextKey * 10;
    }
  });
}

// reset создаёт новый пустой экземпляр выбранной модели.
async function reset() {
  await withBusy(async () => {
    const payload = { mode: app.mode };
    if (app.mode === SimulationMode.SWISS) {
      payload.maxTableCapacity = Number($("#scale-select").value);
    }

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

// runScenario просит сервер подготовить выбранный учебный этап.
async function runScenario(name) {
  await withBusy(async () => {
    const response = await fetch("/api/scenario", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode: app.mode, scenario: name }),
    });
    const snapshot = await readJSON(response);
    render(snapshot);

    const count = Number(
      snapshot.stats
        ?.find((stat) => /Элементы|count|Map.used|hmap.count/.test(stat.label))
        ?.value
        ?.match(/\d+/)?.[0] || 0,
    );
    app.nextKey = count + 1;
    $("#key-input").value = app.nextKey;
    $("#value-input").value = app.nextKey * 10;
  });
}

// autoInsert выполняет десять обычных записей с небольшой паузой, чтобы зритель
// успевал увидеть промежуточные состояния.
async function autoInsert() {
  if (app.busy) return;
  setBusy(true);
  try {
    for (let operationIndex = 0; operationIndex < 10; operationIndex++) {
      const key = app.nextKey++;
      const response = await fetch("/api/operation", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          mode: app.mode,
          kind: OperationKind.INSERT,
          key,
          value: key * 10,
        }),
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

// configureScenarios заново строит набор кнопок для текущего режима.
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

// syncOperationForm скрывает значение для чтения и удаления, не меняя JSON-форму.
function syncOperationForm() {
  const insert = $("#operation-kind").value === OperationKind.INSERT;
  $("#value-field").style.visibility = insert ? "visible" : "hidden";
}

// render распределяет один снимок по независимым областям интерфейса.
function render(snapshot) {
  app.snapshot = snapshot;
  $("#mode-title").textContent = snapshot.title;
  $("#mode-subtitle").textContent = snapshot.subtitle;
  $("#notice").textContent = snapshot.notice;
  $("#scale-note").textContent = snapshot.scaleNote || "";
  $("#event-counter").textContent = plural(snapshot.eventCount || 0, "шаг", "шага", "шагов");
  $("#mode-eyebrow").textContent = snapshot.mode === SimulationMode.REAL
    ? "ФАКТИЧЕСКАЯ ПАМЯТЬ RUNTIME"
    : "ДЕТЕРМИНИРОВАННАЯ МОДЕЛЬ";
  renderStats(snapshot.stats || []);
  renderMemory(snapshot);
  renderTrace(snapshot.trace || []);
  configureScenarios();
}

// renderStats строит карточки служебных полей и счётчиков.
function renderStats(stats) {
  $("#stats").innerHTML = stats.map((stat) => `
    <div class="stat-card">
      <span class="stat-label">${escapeHTML(stat.label)}</span>
      <strong class="stat-value" title="${escapeHTML(stat.value)}">${escapeHTML(stat.value)}</strong>
      <span class="stat-hint">${escapeHTML(stat.hint)}</span>
    </div>
  `).join("");
}

// renderMemory выбирает современное или классическое представление памяти.
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
        <div class="subhead"><h3>Directory</h3><span>старшие globalDepth бит → table</span></div>
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

// renderTable строит карточку одной независимо растущей Swiss-таблицы.
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

// renderGroup строит одну группу и передаёт слоты общему визуализатору.
function renderGroup(tableID, group) {
  return `
    <div class="group-card" id="${safeID(`${tableID}-g${group.index}`)}">
      <div class="group-head"><span>ГРУППА ${group.index}</span><span class="control-word">${escapeHTML(group.controlWord)}</span></div>
      <div class="slots">${group.slots.map(renderSlot).join("")}</div>
    </div>`;
}

// renderSlot отображает состояние, пару и управляющий байт физического слота.
function renderSlot(slot) {
  const pair = slot.key === undefined || slot.key === null ? "·" : `${slot.key}→${slot.value}`;
  return `
    <div class="slot ${safeClass(slot.state)} ${slot.highlight ? "changed" : ""}" id="${safeID(slot.physicalId || "")}" title="слот ${slot.index}; ${escapeHTML(slot.state)}; управляющий байт ${escapeHTML(slot.control)}">
      <small>${slot.index}</small>
      <span class="pair">${escapeHTML(pair)}</span>
      <span class="ctrl">${escapeHTML(slot.control)}</span>
    </div>`;
}

// renderLegacy строит одновременно новый и старый массивы во время эвакуации.
function renderLegacy(legacy) {
  const progress = legacy.growing
    ? `<div class="evac-progress">Эвакуация активна: <b>nevacuate = ${legacy.nevacuate}</b> · старых бакетов ${legacy.oldBucketCount}. Бакеты левее nevacuate гарантированно готовы.</div>`
    : `<div class="evac-progress">Эвакуации сейчас нет: <b>oldbuckets = nil</b>.</div>`;
  const oldArray = legacy.growing ? `
    <section class="legacy-array old-array">
      <div class="subhead"><h3>oldbuckets · источник</h3><span>чтение отсюда, пока бакет не эвакуирован</span></div>
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

// renderBucket отображает основной bmap и его цепочку переполнения.
function renderBucket(bucket, generation) {
  return `
    <div class="bucket-card ${bucket.evacuated ? "evacuated" : ""}" id="${generation}-b${bucket.index}">
      <div class="bucket-head"><b>БАКЕТ ${bucket.index}</b><span>${bucket.evacuated ? "ЭВАКУИРОВАН" : "АКТИВЕН"}</span></div>
      ${bucket.chain.map((slots, index) => `
        ${index > 0 ? `<div class="overflow-label">переполнение → bmap #${index}</div>` : ""}
        <div class="slots">${slots.map(renderSlot).join("")}</div>
      `).join("")}
    </div>`;
}

// renderTrace строит человекочитаемую последовательность внутренних шагов.
function renderTrace(trace) {
  $("#trace").innerHTML = trace.map((step) => `
    <li class="trace-item ${safeClass(step.tone || "info")}">
      <h3>${escapeHTML(step.title)}</h3>
      <p>${escapeHTML(step.detail)}</p>
      ${step.formula ? `<code class="formula advanced-only">${escapeHTML(step.formula)}</code>` : ""}
    </li>
  `).join("");
}

// revealTraceTarget подсвечивает последний физический объект, указанный трассой.
function revealTraceTarget(trace) {
  const target = [...trace].reverse().find((step) => step.target)?.target;
  if (!target) return;
  const element = document.getElementById(safeID(target));
  if (element?.classList.contains("group-card")) element.classList.add("highlight");
}

// updateCalculator показывает нижнюю и верхнюю оценки числа изменяющих операций.
function updateCalculator() {
  const raw = Math.max(1, Number($("#bucket-calculator").value) || 1);
  $("#calc-min").textContent = Math.ceil(raw / 2);
  $("#calc-max").textContent = raw;
}

// withBusy обеспечивает единый порядок блокировки интерфейса и показа ошибок.
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

// setBusy синхронизирует состояние приложения с визуальным индикатором загрузки.
function setBusy(value) {
  app.busy = value;
  document.body.classList.toggle("loading", value);
}

// readJSON преобразует успешный ответ либо выбрасывает серверное сообщение об
// ошибке, которое затем покажет withBusy.
async function readJSON(response) {
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || "Не удалось выполнить запрос.");
  return payload;
}

// showError ненадолго показывает всплывающее сообщение.
function showError(message) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.classList.add("show");
  setTimeout(() => toast.classList.remove("show"), 3200);
}

// plural выбирает русскую форму существительного по числу.
function plural(value, one, few, many) {
  const mod10 = value % 10;
  const mod100 = value % 100;
  const word = mod10 === 1 && mod100 !== 11
    ? one
    : mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)
      ? few
      : many;
  return `${value} ${word}`;
}

// escapeHTML экранирует произвольные серверные значения перед вставкой в разметку.
function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

// safeClass оставляет только символы, допустимые в машинных именах CSS-классов.
function safeClass(value) {
  return String(value || "").replace(/[^a-z0-9_-]/gi, "");
}

// safeID преобразует серверный идентификатор в безопасный идентификатор DOM.
function safeID(value) {
  return String(value || "").replace(/[^a-z0-9_@-]/gi, "-");
}
