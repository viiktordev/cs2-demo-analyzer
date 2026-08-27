// Thin wrappers around the backend JSON API. The frontend is served by the same
// origin as the API, so paths are relative.

const BASE = "/api";

/** Reads a JSON error body, falling back to the HTTP status text. */
async function errorFrom(response) {
  try {
    const body = await response.json();
    if (body && body.error) return new Error(body.error);
  } catch {
    // Body wasn't JSON — fall through to the status.
  }
  return new Error(`${response.status} ${response.statusText}`);
}

async function getJSON(path) {
  const response = await fetch(`${BASE}${path}`);
  if (!response.ok) throw await errorFrom(response);
  return response.json();
}

/** GET /api/health */
export function getHealth() {
  return getJSON("/health");
}

/** GET /api/demos */
export async function listDemos() {
  const { demos } = await getJSON("/demos");
  return demos ?? [];
}

/** GET /api/demos/{id} — roster, plus the analysis once it has run. */
export function getDemo(id) {
  return getJSON(`/demos/${encodeURIComponent(id)}`);
}

/**
 * POST /api/analyses/{id}/coaching — generates (or returns) the AI report.
 * Slow by nature: it waits on the model.
 */
export async function generateCoaching(analysisId) {
  const response = await fetch(`${BASE}/analyses/${encodeURIComponent(analysisId)}/coaching`, {
    method: "POST",
  });

  if (!response.ok) throw await errorFrom(response);
  return response.json();
}

/** GET /api/players/{steamId} — every analysed match for a player. */
export function getPlayerHistory(steamId) {
  return getJSON(`/players/${encodeURIComponent(steamId)}`);
}

/**
 * POST /api/demos/{id}/players/{steamId}/analyze — runs the full parse for one
 * player. This consumes the upload: the server deletes the demo afterwards, so
 * analysing a second player means uploading again.
 */
export async function analyzePlayer(demoId, steamId) {
  const response = await fetch(
    `${BASE}/demos/${encodeURIComponent(demoId)}/players/${encodeURIComponent(steamId)}/analyze`,
    { method: "POST" }
  );

  if (!response.ok) throw await errorFrom(response);
  return response.json();
}

/**
 * POST /api/demos — uploads a .dem and resolves with its roster. The heavy
 * parse does not run here; it waits for a player to be chosen.
 *
 * Uses XMLHttpRequest rather than fetch because fetch gives no upload progress,
 * and demos are large enough that a progress bar matters.
 *
 * @param {File} file
 * @param {(percent: number) => void} [onProgress]
 * @returns {Promise<object>}
 */
export function uploadDemo(file, onProgress) {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append("demo", file);

    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${BASE}/demos`);
    xhr.responseType = "json";

    if (onProgress) {
      xhr.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable) {
          onProgress(Math.round((event.loaded / event.total) * 100));
        }
      });
    }

    xhr.addEventListener("load", () => {
      const body = xhr.response;
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body);
      } else {
        reject(new Error(body?.error ?? `${xhr.status} ${xhr.statusText}`));
      }
    });

    xhr.addEventListener("error", () => reject(new Error("falha de rede")));
    xhr.addEventListener("abort", () => reject(new Error("upload cancelado")));

    xhr.send(form);
  });
}
