// The app's color scheme: the user's saved choice ("light", "dark"), or "" to follow the
// system's. The resolved scheme is set as data-theme on <html>, which styles.css keys its
// colors on. The choice is also kept on this device, so public/appearance.js can apply it
// before the page is drawn.

export const APPEARANCES = ['', 'light', 'dark'] as const;
export type Appearance = (typeof APPEARANCES)[number];

const STORAGE_KEY = 'knowpod.appearance';
const systemDark = window.matchMedia?.('(prefers-color-scheme: dark)');

let current: Appearance = (() => {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === 'light' || saved === 'dark') return saved;
  } catch {
    // storage unavailable
  }
  return '';
})();

function render() {
  const dark = current === 'dark' || (current === '' && !!systemDark?.matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
}

render();
systemDark?.addEventListener?.('change', render);

// currentAppearance is the choice in effect on this device.
export function currentAppearance(): Appearance {
  return current;
}

// applyAppearance switches to a user's saved color scheme (absent or "" follows the system)
// and remembers it on this device.
export function applyAppearance(a?: string) {
  current = a === 'light' || a === 'dark' ? a : '';
  try {
    if (current) localStorage.setItem(STORAGE_KEY, current);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    // storage unavailable
  }
  render();
}
