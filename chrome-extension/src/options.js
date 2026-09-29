import { getServer, originPattern } from "./api.js";

const status = document.getElementById("status");
const input = document.getElementById("server");
getServer().then((s) => (input.value = s));

document.getElementById("save").onclick = async () => {
  let server;
  try {
    const u = new URL(input.value.trim());
    if (!/^https?:$/.test(u.protocol)) throw new Error();
    server = u.origin;
  } catch {
    status.textContent = "Enter a full URL, like https://knowpod.example.com";
    return;
  }
  // Must run from this click: asks the user to allow the extension to talk to the server.
  const granted = await chrome.permissions.request({ origins: [originPattern(server)] });
  if (!granted) {
    status.textContent = "Access to the server was not allowed.";
    return;
  }
  await chrome.storage.sync.set({ server });
  input.value = server;
  status.textContent = "Saved.";
};
