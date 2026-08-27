import {
  analyzePlayer,
  generateCoaching,
  getDemo,
  getHealth,
  getPlayerHistory,
  listDemos,
  uploadDemo,
} from "./api.js";

const el = {
  status: document.getElementById("status"),
  form: document.getElementById("upload-form"),
  dropzone: document.getElementById("dropzone"),
  dropzoneText: document.getElementById("dropzone-text"),
  fileInput: document.getElementById("file-input"),
  submit: document.getElementById("submit-btn"),
  progress: document.getElementById("progress"),
  feedback: document.getElementById("feedback"),
  resultPanel: document.getElementById("result-panel"),
  result: document.getElementById("result"),
  rosterPanel: document.getElementById("roster-panel"),
  roster: document.getElementById("roster"),
  rosterHint: document.getElementById("roster-hint"),
  playerPanel: document.getElementById("player-panel"),
  playerDetail: document.getElementById("player-detail"),
  coachingPanel: document.getElementById("coaching-panel"),
  coaching: document.getElementById("coaching"),
  historyPanel: document.getElementById("history-panel"),
  history: document.getElementById("history"),
  demoList: document.getElementById("demo-list"),
};

/** Escapes text before it goes into innerHTML. */
function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[c]);
}

/** 3725 -> "62:05" */
function formatDuration(seconds) {
  if (!seconds) return "—";
  const total = Math.round(seconds);
  const mins = String(Math.floor(total / 60)).padStart(2, "0");
  const secs = String(total % 60).padStart(2, "0");
  return `${mins}:${secs}`;
}

// Compression suffixes demo providers wrap .dem in — FACEIT serves .dem.zst,
// Valve match downloads .dem.bz2. Mirrors isDemoFile in the backend.
const COMPRESSION_EXTS = [".zst", ".gz", ".bz2"];

function isDemoFile(name) {
  let base = name.toLowerCase();
  for (const ext of COMPRESSION_EXTS) {
    if (base.endsWith(ext)) base = base.slice(0, -ext.length);
  }
  return base.endsWith(".dem");
}

function showFeedback(message, kind) {
  el.feedback.textContent = message;
  el.feedback.dataset.kind = kind;
  el.feedback.hidden = false;
}

function clearFeedback() {
  el.feedback.hidden = true;
}

// ---------- health ----------

// Whether the server has an API key. Set from /api/health at boot.
let coachingAvailable = false;

async function refreshHealth() {
  try {
    const { status, version, coaching, provider } = await getHealth();
    coachingAvailable = Boolean(coaching);
    el.status.dataset.state = status === "ok" ? "ok" : "down";
    el.status.textContent = `API ${status} · ${version}${coaching ? ` · IA (${provider})` : ""}`;
  } catch {
    el.status.dataset.state = "down";
    el.status.textContent = "API indisponível";
  }
}

// ---------- rendering ----------

const STATUS_LABELS = {
  pending: "aguardando escolha",
  analyzing: "analisando…",
  analyzed: "analisada",
};

function renderSummary(summary) {
  const [first, second] = summary.score;

  const scoreboard = `
    <div class="scoreboard">
      <div class="team" data-side="${esc(first.side)}">
        <span class="team-name">${esc(first.name || first.side || "Time 1")}</span>
        <span class="team-score">${esc(first.score)}</span>
      </div>
      <span class="sep">×</span>
      <div class="team" data-side="${esc(second.side)}">
        <span class="team-name">${esc(second.name || second.side || "Time 2")}</span>
        <span class="team-score">${esc(second.score)}</span>
      </div>
    </div>`;

  const meta = `
    <dl class="meta">
      <div><dt>Mapa</dt><dd>${esc(summary.map || "desconhecido")}</dd></div>
      <div><dt>Rounds</dt><dd>${esc(summary.rounds)}</dd></div>
      <div><dt>Duração</dt><dd>${formatDuration(summary.durationSeconds)}</dd></div>
      <div><dt>Tick rate</dt><dd>${esc(Math.round(summary.tickRate) || "—")}</dd></div>
      <div><dt>Arquivo</dt><dd>${esc(summary.fileName)}</dd></div>
    </dl>`;

  const warning = summary.truncated
    ? `<p class="warning">⚠ Demo incompleta — o arquivo termina antes do fim da partida.</p>`
    : "";

  const rounds = summary.roundHistory?.length
    ? `<details>
         <summary>Histórico de rounds (${summary.roundHistory.length})</summary>
         <div class="table-wrap">
           <table>
             <thead><tr><th>#</th><th>Vencedor</th><th>Motivo</th><th>Tempo</th></tr></thead>
             <tbody>
               ${summary.roundHistory.map((round) => `
                 <tr>
                   <td class="mono">${esc(round.number)}</td>
                   <td class="side-${esc(round.winner)}">${esc(round.winner || "empate")}</td>
                   <td>${esc(round.reason)}</td>
                   <td class="mono">${formatDuration(round.elapsedSeconds)}</td>
                 </tr>`).join("")}
             </tbody>
           </table>
         </div>
       </details>`
    : "";

  el.result.innerHTML = scoreboard + meta + warning + rounds;
  el.resultPanel.hidden = false;
}

// ---------- roster ----------

// The demo whose roster is on screen, so a pick knows what to analyze.
let currentDemoId = null;
// Guards against a second click while a parse is in flight.
let analyzing = false;

function renderRoster(view) {
  currentDemoId = view.id;

  if (!view.players?.length) {
    el.roster.innerHTML = `<p class="empty">Nenhum jogador encontrado nesta demo.</p>`;
    el.rosterHint.textContent = "";
    el.rosterPanel.hidden = false;
    return;
  }

  el.rosterHint.innerHTML =
    `<strong>${esc(view.map || "mapa desconhecido")}</strong> · ` +
    "escolha um jogador para iniciar a análise. " +
    "Só um por envio: a demo é apagada do servidor depois de analisada.";

  // Keep teammates together, preserving the order the API sent.
  const teams = new Map();
  for (const player of view.players) {
    const key = player.team || player.side || "—";
    if (!teams.has(key)) teams.set(key, []);
    teams.get(key).push(player);
  }

  el.roster.innerHTML = [...teams]
    .map(([team, members]) => `
      <div class="team-block">
        <h3 class="team-heading">${esc(team)}</h3>
        <div class="player-picks">
          ${members.map((p) => `
            <button type="button" class="player-pick" data-steam-id="${esc(p.steamId)}">
              <span class="pick-name">${esc(p.name)}</span>
              <span class="pick-side side-${esc(p.side)}">${esc(p.side)}</span>
            </button>`).join("")}
        </div>
      </div>`)
    .join("");

  el.rosterPanel.hidden = false;
}

/** A labelled figure in the player detail grid. Pass isText for values that read
 * as a phrase rather than a number, so they don't get the large numeric style.
 */
function statCard(label, value, hint, isText) {
  return `
    <div class="stat">
      <span class="stat-label">${esc(label)}</span>
      <span class="stat-value${isText ? " stat-value-text" : ""}">${esc(value)}</span>
      ${hint ? `<span class="stat-hint">${esc(hint)}</span>` : ""}
    </div>`;
}

function renderPlayer(player) {
  const multi = player.multiKills
    .map((count, i) => (count ? `${count}× ${i + 1}k` : null))
    .filter(Boolean)
    .join(" · ") || "—";

  const cards = [
    statCard("Rating 1.0", player.rating.toFixed(2)),
    statCard("K / D / A", `${player.kills} / ${player.deaths} / ${player.assists}`),
    statCard("K/D", player.kd.toFixed(2)),
    statCard("ADR", player.adr.toFixed(1), `${player.damage} de dano`),
    statCard("KAST", `${player.kast.toFixed(1)}%`),
    statCard("Headshots", `${player.headshotPct.toFixed(0)}%`, `${player.headshots} de ${player.kills}`),
    statCard("Rounds", player.roundsPlayed),
    statCard("Multi-kills", multi, null, true),
    statCard("Entradas", `${player.openingKills} / ${player.openingDeaths}`, "abertas ganhas / perdidas"),
    statCard("Trades", player.tradeKills, "mortes vingadas"),
    statCard("Clutches", `${player.clutchesWon} / ${player.clutchesPlayed}`, "vencidos / disputados"),
    statCard("Utility", player.utilityDamage, "dano de granada"),
    statCard("Flashes", player.enemiesFlashed, `${player.flashAssists} assist. de flash`),
    statCard("Bomba", `${player.bombsPlanted} / ${player.bombsDefused}`, "plantadas / desarmadas"),
  ].join("");

  const rounds = player.rounds?.length
    ? `<div class="table-wrap">
         <table>
           <thead>
             <tr><th>#</th><th>K</th><th>A</th><th>D</th><th>Dano</th><th>Sobreviveu</th><th>KAST</th></tr>
           </thead>
           <tbody>
             ${player.rounds.map((r) => `
               <tr>
                 <td class="mono">${esc(r.round)}</td>
                 <td class="mono">${esc(r.kills)}</td>
                 <td class="mono">${esc(r.assists)}</td>
                 <td class="mono">${esc(r.deaths)}</td>
                 <td class="mono">${esc(r.damage)}</td>
                 <td>${r.survived ? "sim" : "não"}</td>
                 <td>${r.kast ? "✓" : "·"}</td>
               </tr>`).join("")}
           </tbody>
         </table>
       </div>`
    : "";

  el.playerDetail.innerHTML = `
    <div class="player-head">
      <span class="player-name">${esc(player.name)}</span>
      <span class="player-team">${esc(player.team)} · <span class="side-${esc(player.side)}">${esc(player.side)}</span></span>
    </div>
    <div class="stat-grid">${cards}</div>
    <details open>
      <summary>Round a round</summary>
      ${rounds}
    </details>`;

  el.playerPanel.hidden = false;
}

// ---------- coaching ----------

/** The analysis currently on screen, so the coaching button knows its target. */
let currentAnalysisId = null;

function renderFindings(items, kind) {
  if (!items?.length) return "";

  return `
    <ul class="findings findings-${kind}">
      ${items.map((f) => `
        <li>
          <span class="finding-title">${esc(f.title)}</span>
          <span class="finding-detail">${esc(f.detail)}</span>
          <span class="finding-evidence">${esc(f.evidence)}</span>
        </li>`).join("")}
    </ul>`;
}

function renderReport(coaching) {
  const report = typeof coaching.report === "string"
    ? JSON.parse(coaching.report)
    : coaching.report;

  const model = [coaching.provider, coaching.model].filter(Boolean).join(" · ");
  const cost = coaching.inputTokens
    ? `${model} · ${coaching.inputTokens} tokens de entrada, ${coaching.outputTokens} de saída` +
      (coaching.cachedTokens ? `, ${coaching.cachedTokens} em cache` : "")
    : model;

  el.coaching.innerHTML = `
    <p class="report-summary">${esc(report.summary)}</p>
    ${report.trend ? `<p class="report-trend"><strong>Evolução:</strong> ${esc(report.trend)}</p>` : ""}
    ${report.strengths?.length ? `<h3 class="report-heading">Pontos fortes</h3>${renderFindings(report.strengths, "good")}` : ""}
    ${report.mistakes?.length ? `<h3 class="report-heading">Erros recorrentes</h3>${renderFindings(report.mistakes, "bad")}` : ""}
    ${report.drills?.length ? `
      <h3 class="report-heading">O que treinar</h3>
      <ol class="drills">
        ${report.drills.map((d) => `
          <li>
            <span class="finding-title">${esc(d.title)}</span>
            <span class="finding-detail">${esc(d.why)}</span>
          </li>`).join("")}
      </ol>` : ""}
    <p class="report-meta">${esc(cost)}</p>`;

  el.coachingPanel.hidden = false;
}

/** Shows the button that kicks off the analysis, or why it is unavailable. */
function renderCoachingPrompt() {
  if (!coachingAvailable) {
    el.coaching.innerHTML = `
      <p class="empty">
        Análise de IA desativada: o servidor está sem chave da API.
        Defina <code>OPENAI_API_KEY</code> ou <code>ANTHROPIC_API_KEY</code> no
        <code>.env</code>.
      </p>`;
    el.coachingPanel.hidden = false;
    return;
  }

  el.coaching.innerHTML = `
    <p class="hint">A IA lê as estatísticas desta partida e devolve o que treinar.</p>
    <button type="button" id="coach-btn">Gerar análise</button>`;
  el.coachingPanel.hidden = false;
}

async function requestCoaching() {
  const button = document.getElementById("coach-btn");
  if (!currentAnalysisId || !button) return;

  button.disabled = true;
  button.textContent = "Analisando…";

  try {
    renderReport(await generateCoaching(currentAnalysisId));
  } catch (err) {
    el.coaching.innerHTML = `<p class="empty">Não foi possível gerar: ${esc(err.message)}</p>`;
  }
}

el.coaching.addEventListener("click", (event) => {
  if (event.target.closest("#coach-btn")) requestCoaching();
});

// ---------- history ----------

async function renderHistory(steamId) {
  try {
    const { matches } = await getPlayerHistory(steamId);

    // A single match is just the one on screen — no history to show yet.
    if (!matches || matches.length < 2) {
      el.historyPanel.hidden = true;
      return;
    }

    el.history.innerHTML = `
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>Mapa</th><th>K/D</th><th>ADR</th><th>KAST</th><th>Rating</th><th>Quando</th></tr>
          </thead>
          <tbody>
            ${matches.map((m) => `
              <tr>
                <td>${esc(m.map || "—")}</td>
                <td class="mono">${esc(m.kills)}/${esc(m.deaths)}</td>
                <td class="mono">${m.adr.toFixed(1)}</td>
                <td class="mono">${m.kast.toFixed(1)}%</td>
                <td class="mono">${m.rating.toFixed(2)}</td>
                <td>${esc(new Date(m.analyzedAt).toLocaleDateString("pt-BR"))}</td>
              </tr>`).join("")}
          </tbody>
        </table>
      </div>`;

    el.historyPanel.hidden = false;
  } catch {
    el.historyPanel.hidden = true;
  }
}

/** Runs the analysis for the chosen player and shows the result. */
async function pickPlayer(steamId) {
  if (!currentDemoId || analyzing) return;

  const picks = [...el.roster.querySelectorAll(".player-pick")];
  const chosen = picks.find((b) => b.dataset.steamId === steamId);

  analyzing = true;
  picks.forEach((b) => {
    b.disabled = true;
    b.classList.toggle("selected", b === chosen);
  });

  showFeedback(`Analisando ${chosen?.querySelector(".pick-name")?.textContent ?? "jogador"}…`, "info");

  try {
    const view = await analyzePlayer(currentDemoId, steamId);
    showFeedback(`Análise concluída: ${view.analysis.player.name}`, "ok");
    showAnalysis(view);
    await refreshDemoList();
  } catch (err) {
    showFeedback(err.message, "error");
    // The upload survives a failed parse, so let the user try again.
    picks.forEach((b) => {
      b.disabled = false;
      b.classList.remove("selected");
    });
  } finally {
    analyzing = false;
  }
}

/** Shows a finished analysis and retires the picker: the demo is spent. */
function showAnalysis(view) {
  currentDemoId = view.id;
  currentAnalysisId = view.id;
  el.rosterPanel.hidden = true;
  renderSummary(view.analysis);
  renderPlayer(view.analysis.player);

  // The stored report doesn't ride along with the analysis, so the button is
  // always offered. Pressing it returns an already-generated report from the
  // server rather than paying for a second one.
  renderCoachingPrompt();
  renderHistory(view.analysis.player.steamId);
  el.resultPanel.scrollIntoView({ behavior: "smooth", block: "start" });
}

/** Opens a demo: the picker if it is still pending, the result if it ran. */
function showDemo(view) {
  el.playerPanel.hidden = true;
  el.resultPanel.hidden = true;
  el.rosterPanel.hidden = true;
  el.coachingPanel.hidden = true;
  el.historyPanel.hidden = true;

  if (view.analysis) {
    showAnalysis(view);
    return;
  }

  renderRoster(view);
  el.rosterPanel.scrollIntoView({ behavior: "smooth", block: "start" });
}

// Clicks are delegated, so re-rendering the picker keeps working.
el.roster.addEventListener("click", (event) => {
  const pick = event.target.closest(".player-pick");
  if (pick) pickPlayer(pick.dataset.steamId);
});

function renderDemoList(demos) {
  if (!demos.length) {
    el.demoList.innerHTML = `<p class="empty">Nenhuma demo enviada ainda.</p>`;
    return;
  }

  el.demoList.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr><th>Arquivo</th><th>Mapa</th><th>Status</th></tr></thead>
        <tbody>
          ${demos.map((demo) => `
            <tr class="demo-row" data-demo-id="${esc(demo.id)}" tabindex="0">
              <td>${esc(demo.fileName)}</td>
              <td>${esc(demo.map || "—")}</td>
              <td><span class="badge badge-${esc(demo.status)}">${esc(STATUS_LABELS[demo.status] ?? demo.status)}</span></td>
            </tr>`).join("")}
        </tbody>
      </table>
    </div>`;
}

el.demoList.addEventListener("click", async (event) => {
  const row = event.target.closest(".demo-row");
  if (!row) return;

  try {
    showDemo(await getDemo(row.dataset.demoId));
  } catch (err) {
    showFeedback(err.message, "error");
  }
});

async function refreshDemoList() {
  try {
    renderDemoList(await listDemos());
  } catch (err) {
    el.demoList.innerHTML = `<p class="empty">Não foi possível carregar: ${esc(err.message)}</p>`;
  }
}

// ---------- file selection ----------

function selectFile(file) {
  clearFeedback();

  if (!file) {
    el.dropzone.classList.remove("has-file");
    el.dropzoneText.innerHTML =
      "Arraste um arquivo <code>.dem</code> aqui ou clique para escolher" +
      "<small>aceita também <code>.dem.zst</code>, <code>.dem.gz</code> e <code>.dem.bz2</code></small>";
    el.submit.disabled = true;
    return;
  }

  if (!isDemoFile(file.name)) {
    showFeedback("Selecione um arquivo .dem (pode estar comprimido em .zst, .gz ou .bz2)", "error");
    el.submit.disabled = true;
    return;
  }

  const mb = (file.size / 1024 / 1024).toFixed(1);
  el.dropzone.classList.add("has-file");
  el.dropzoneText.textContent = `${file.name} · ${mb} MB`;
  el.submit.disabled = false;
}

el.fileInput.addEventListener("change", () => selectFile(el.fileInput.files[0]));

["dragenter", "dragover"].forEach((type) =>
  el.dropzone.addEventListener(type, (event) => {
    event.preventDefault();
    el.dropzone.classList.add("dragover");
  })
);

["dragleave", "drop"].forEach((type) =>
  el.dropzone.addEventListener(type, (event) => {
    event.preventDefault();
    el.dropzone.classList.remove("dragover");
  })
);

el.dropzone.addEventListener("drop", (event) => {
  const file = event.dataTransfer?.files?.[0];
  if (!file) return;

  // Keep the hidden input in sync so the form reflects what was dropped.
  el.fileInput.files = event.dataTransfer.files;
  selectFile(file);
});

// ---------- submit ----------

el.form.addEventListener("submit", async (event) => {
  event.preventDefault();

  const file = el.fileInput.files[0];
  if (!file) return;

  el.submit.disabled = true;
  el.progress.hidden = false;
  el.progress.value = 0;
  showFeedback("Enviando…", "info");

  try {
    const view = await uploadDemo(file, (percent) => {
      el.progress.value = percent;
      if (percent === 100) showFeedback("Lendo o elenco…", "info");
    });

    showFeedback(`Demo carregada: ${view.map || "mapa desconhecido"}`, "ok");
    showDemo(view);
    await refreshDemoList();
  } catch (err) {
    showFeedback(err.message, "error");
  } finally {
    el.progress.hidden = true;
    el.submit.disabled = false;
  }
});

// ---------- boot ----------

refreshHealth();
refreshDemoList();
