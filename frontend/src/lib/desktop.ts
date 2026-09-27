// The knowpod desktop app (Electron, see desktop/): what its preload script
// (desktop/src/preload.js) offers the web app, and the live notifications it shows.
import { useEffect, useRef } from 'react';

// DesktopNotification is a notification as the server sends it (see
// backend/internal/service/notifications.go).
export interface DesktopNotification {
  title: string;
  body?: string;
  url?: string;
  tag?: string;
}

interface DesktopBridge {
  platform: string;
  version: string;
  // notify and onOpen are missing in older desktop apps.
  notify?: (n: DesktopNotification) => void;
  onOpen?: (listener: (url: string) => void) => () => void;
}

const bridge = (): DesktopBridge | undefined =>
  typeof window !== 'undefined' ? (window as { knowpodDesktop?: DesktopBridge }).knowpodDesktop : undefined;

// desktopNotifications says whether this is a desktop app that shows notifications (older
// ones can't).
export const desktopNotifications = () => typeof bridge()?.notify === 'function';

// retryAfter is how long to wait before listening again when the server refused the stream
// (EventSource only retries by itself after network errors).
const retryAfter = 30_000;

// useDesktopNotifications makes the desktop app show the user's notifications while it
// runs (also in the background): Web Push can't reach it, so it listens to the server's
// live stream instead. Clicked notifications open their page through open.
export function useDesktopNotifications(active: boolean, open: (path: string) => void) {
  // open changes with every navigation (react-router's navigate); the stream shouldn't.
  const openRef = useRef(open);
  openRef.current = open;
  useEffect(() => {
    const desktop = bridge();
    if (!active || !desktop?.notify) return;
    let source: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    const listen = () => {
      source = new EventSource('/api/v1/me/notifications/stream');
      source.addEventListener('notification', (e) => {
        try {
          desktop.notify!(JSON.parse((e as MessageEvent<string>).data) as DesktopNotification);
        } catch {
          // not a notification
        }
      });
      source.onerror = () => {
        if (source?.readyState !== EventSource.CLOSED) return;
        retry = setTimeout(listen, retryAfter);
      };
    };
    listen();
    const stopOpen = desktop.onOpen?.((target) => {
      const url = new URL(target, window.location.origin);
      if (url.origin === window.location.origin) openRef.current(url.pathname + url.search);
    });
    return () => {
      clearTimeout(retry);
      source?.close();
      stopOpen?.();
    };
  }, [active]);
}
