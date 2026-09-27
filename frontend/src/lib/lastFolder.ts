// The folder last opened in the sidebar's folder view, remembered in the browser: new notes
// and boards made while the folder view is shown go into it.
const LAST_FOLDER_KEY = 'knowpod.lastFolder';

function load(): string {
  try {
    return localStorage.getItem(LAST_FOLDER_KEY) ?? '';
  } catch {
    return '';
  }
}

let last = load();

// lastFolder returns the folder last opened; '' for the top level.
export function lastFolder(): string {
  return last;
}

// setLastFolder remembers the folder last opened; '' for the top level.
export function setLastFolder(id: string) {
  last = id;
  try {
    if (id) localStorage.setItem(LAST_FOLDER_KEY, id);
    else localStorage.removeItem(LAST_FOLDER_KEY);
  } catch {
    // Private mode or storage full: remembered until the page is reloaded.
  }
}
