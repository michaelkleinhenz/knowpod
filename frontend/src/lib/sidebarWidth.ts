import { useCallback, useState } from 'react';

// The width of the notes sidebar in pixels, set by dragging its edge and remembered in the browser.
const WIDTH_KEY = 'knowpod.sidebarWidth';
export const DEFAULT_SIDEBAR_WIDTH = 320;
const MIN_WIDTH = 224;
const MAX_WIDTH = 720;

// clampSidebarWidth keeps the sidebar usable and leaves the open note at least 40% of the window.
export function clampSidebarWidth(width: number): number {
  const max = Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, window.innerWidth * 0.6));
  return Math.round(Math.min(max, Math.max(MIN_WIDTH, width)));
}

function load(): number {
  try {
    const stored = Number(localStorage.getItem(WIDTH_KEY));
    return stored > 0 ? stored : DEFAULT_SIDEBAR_WIDTH;
  } catch {
    return DEFAULT_SIDEBAR_WIDTH;
  }
}

// useSidebarWidth returns the sidebar width and a setter that clamps and remembers it.
export function useSidebarWidth(): [number, (width: number) => void] {
  const [width, setWidth] = useState(load);
  const set = useCallback((next: number) => {
    const clamped = clampSidebarWidth(next);
    setWidth(clamped);
    try {
      localStorage.setItem(WIDTH_KEY, String(clamped));
    } catch {
      // Private mode or storage full: the sidebar just starts at its default width next time.
    }
  }, []);
  return [width, set];
}
