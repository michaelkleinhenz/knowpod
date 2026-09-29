import { clip, getServer, hasAccess, ApiError, CLIPPED_FOLDER } from "./api.js";

const $ = (id) => document.getElementById(id);
const status = (msg, cls = "") => { $("status").textContent = msg; $("status").className = cls; };

$("options").onclick = (e) => { e.preventDefault(); chrome.runtime.openOptionsPage(); };

async function extract(tab) {
  const [res] = await chrome.scripting.executeScript({ target: { tabId: tab.id }, files: ["extract.js"] });
  return res?.result;
}

async function main() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  $("page").textContent = tab?.title || tab?.url || "";
  if (!tab?.url || !/^https?:/.test(tab.url)) {
    $("clip").disabled = true;
    return status("This page can't be clipped.", "error");
  }
  const server = await getServer();
  if (!server || !(await hasAccess(server))) {
    $("clip").disabled = true;
    return status("Set your knowpod server in the options first.", "error");
  }

  $("clip").onclick = async () => {
    $("clip").disabled = true;
    status("Clipping…");
    try {
      const page = await extract(tab);
      if (!page?.text) throw new ApiError(0, "No text found on this page.");
      const note = await clip(page);
      status(`${page.selection ? "Selection" : "Page"} saved to “${CLIPPED_FOLDER}”.`, "ok");
      const open = $("open");
      open.href = `${server}/n/${note.number}`;
      open.target = "_blank";
      open.hidden = false;
    } catch (e) {
      $("clip").disabled = false;
      if (e instanceof ApiError && e.status === 401) {
        status("You're not signed in to knowpod. Sign in in a tab, then try again.", "error");
        chrome.tabs.create({ url: server });
      } else {
        status(e.message || String(e), "error");
      }
    }
  };
}

main();
