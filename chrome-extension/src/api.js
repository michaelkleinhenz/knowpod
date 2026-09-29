// Talks to the knowpod server with the browser's signed-in session (the knowpod_session cookie).
// The server origin is stored by the options page; its host permission is requested there.

export const CLIPPED_FOLDER = "Clipped";
const MAX_TITLE = 200;
const MAX_MARKDOWN = 100000; // the server's limit for a text note

export async function getServer() {
  const { server } = await chrome.storage.sync.get("server");
  return server ? server.replace(/\/+$/, "") : "";
}

export function originPattern(server) {
  return new URL(server).origin + "/*";
}

export async function hasAccess(server) {
  return chrome.permissions.contains({ origins: [originPattern(server)] });
}

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

async function call(server, method, path, body) {
  let res;
  try {
    res = await fetch(`${server}/api/v1${path}`, {
      method,
      credentials: "include",
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, `Can't reach ${server}`);
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error || msg;
    } catch {}
    throw new ApiError(res.status, msg);
  }
  return res.status === 204 ? null : res.json();
}

// The top-level folder named "Clipped" that you own, created when missing.
async function clippedFolderId(server) {
  const folders = await call(server, "GET", "/folders");
  const found = folders.find(
    (f) => !f.parentId && f.access !== "viewer" && f.name.toLowerCase() === CLIPPED_FOLDER.toLowerCase()
  );
  if (found) return found.id;
  try {
    return (await call(server, "POST", "/folders", { name: CLIPPED_FOLDER })).id;
  } catch (e) {
    // Created by another clip in the meantime.
    if (e.status !== 409 && e.status !== 400) throw e;
    const again = await call(server, "GET", "/folders");
    const f = again.find((x) => !x.parentId && x.name.toLowerCase() === CLIPPED_FOLDER.toLowerCase());
    if (!f) throw e;
    return f.id;
  }
}

// Builds the note's Markdown: a link to the source, then the extracted text. No summarizing.
export function buildMarkdown({ url, title, text }, now = new Date()) {
  const head = `Source: [${title.replace(/[\[\]]/g, "")}](${url})\n\nClipped: ${now.toISOString().slice(0, 10)}\n\n---\n\n`;
  const room = MAX_MARKDOWN - head.length;
  let body = text;
  if (body.length > room) {
    const note = "\n\n_(Clip shortened: the page is longer than a note can hold.)_";
    body = body.slice(0, room - note.length).trimEnd() + note;
  }
  return head + body;
}

export async function clip(page) {
  const server = await getServer();
  if (!server) throw new ApiError(0, "Set your knowpod server in the extension options first.");
  const folderId = await clippedFolderId(server);
  const title = (page.title || page.url).trim().slice(0, MAX_TITLE);
  return call(server, "POST", "/recordings/text", {
    title,
    markdown: buildMarkdown({ ...page, title }),
    folderId,
  });
}
