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
  // pocketBluetooth is missing in older desktop apps.
  pocketBluetooth?: (request: PocketBluetoothRequest) => Promise<PocketBluetoothResult>;
}

// The desktop app's Bluetooth connection to a Pocket recorder (desktop/src/pocket-bluetooth.js),
// which switches its USB drive on so the app can copy from it. Its settings stay in the
// desktop app; the session key is never handed back.
export type PocketBluetoothRequest =
  | { action: 'settings' | 'check' | 'usb-on' | 'state' | 'sync' | 'eject' }
  | { action: 'save'; address?: string; sessionKey?: string };

export interface PocketBluetoothResult {
  ok: boolean;
  // Why it failed: not-configured, not-found, auth, unsupported, usb-refused, busy, timeout,
  // invalid-address, invalid-key, disconnected, no-answer or failed.
  error?: string;
  message?: string;
  // settings and save
  address?: string;
  sessionKeySet?: boolean;
  // check
  battery?: number | null;
  firmware?: string | null;
  storage?: { usedKB: number; totalKB: number } | null;
  // check and usb-on
  usb?: boolean | null;
  // state: whether the Bluetooth connection is set up (configured) or in use (busy), and the
  // USB copying (desktop/src/pocket.js): turned on (enabled), a recorder mounted
  // (connected), copying (syncing); phase is '', checking, copying (current of total), done
  // (copied this time), signed-out or failed.
  configured?: boolean;
  busy?: boolean;
  enabled?: boolean;
  connected?: boolean;
  syncing?: boolean;
  phase?: '' | 'checking' | 'copying' | 'done' | 'signed-out' | 'failed';
  current?: number;
  total?: number;
  copied?: number;
}

// pocketBluetooth returns the desktop app's Pocket Bluetooth call, or undefined outside the
// desktop app (and in older ones).
export const pocketBluetooth = () => bridge()?.pocketBluetooth;

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
