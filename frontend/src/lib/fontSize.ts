// The app's text size: the user's saved choice (see FONT_SIZES), or "" for the default.
// It is set as data-font-size on <html>, where styles.css scales the root font size, and
// with it every rem-based size in the app. The choice is also kept on this device, so
// public/appearance.js can apply it before the page is drawn.

export const FONT_SIZES = ['xsmall', 'small', '', 'large', 'xlarge'] as const;
export type FontSize = (typeof FONT_SIZES)[number];

const STORAGE_KEY = 'knowpod.fontSize';

function valid(f?: string | null): FontSize {
  return (FONT_SIZES as readonly string[]).includes(f ?? '') ? (f as FontSize) : '';
}

let current: FontSize = (() => {
  try {
    return valid(localStorage.getItem(STORAGE_KEY));
  } catch {
    return ''; // storage unavailable
  }
})();

function render() {
  if (current) document.documentElement.dataset.fontSize = current;
  else delete document.documentElement.dataset.fontSize;
}

render();

// currentFontSize is the text size in effect on this device.
export function currentFontSize(): FontSize {
  return current;
}

// applyFontSize switches to a user's saved text size (absent or "" is the default) and
// remembers it on this device.
export function applyFontSize(f?: string) {
  current = valid(f);
  try {
    if (current) localStorage.setItem(STORAGE_KEY, current);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    // storage unavailable
  }
  render();
}
