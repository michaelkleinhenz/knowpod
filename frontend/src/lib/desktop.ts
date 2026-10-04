// The knowpod desktop app (Electron, see desktop/) and Android app (Capacitor, see mobile/):
// what they offer the web app (desktop/src/preload.js as window.knowpodDesktop,
// mobile/android/…/AppBridge.java as window.knowpodAndroid), and the live notifications they
// show.
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
  // the OS: 'android' in the Android app, process.platform in the desktop app
  platform: string;
  version: string;
  // notify and onOpen are missing in older desktop apps.
  notify?: (n: DesktopNotification) => void;
  onOpen?: (listener: (url: string) => void) => () => void;
  // pocketBluetooth is missing in older desktop apps.
  pocketBluetooth?: (request: PocketBluetoothRequest) => Promise<PocketBluetoothResult>;
  // showSetup shows the Android app's page for the server's address (Android app only).
  showSetup?: () => void;
}

// The desktop app's Bluetooth connection to a Pocket recorder (desktop/src/pocket-bluetooth.js),
// which switches its USB drive on so the app can copy from it, and drives the copy over the
// Pocket's WiFi (desktop/src/pocket-wifi-sync.js). The Android app answers the same calls
// (mobile/android/…/pocket/PocketController.java), copying over the WiFi only. Its settings
// stay in the app; the session key is never handed back.
export type PocketBluetoothRequest =
  | { action: 'settings' | 'check' | 'usb-on' | 'state' | 'sync' | 'eject' | 'wifi-sync' | 'wifi-cancel' }
  | { action: 'save'; address?: string; sessionKey?: string };

// PocketWifiPhase is how the copy over the Pocket's WiFi goes: '' (not run yet), connecting
// (Bluetooth), listing (the Pocket's recordings), checking (which are new), wifi-starting
// (raising the Pocket's WiFi and joining it), downloading, wifi-restarting (the Pocket serves
// two files per WiFi session), reconnecting (back to the usual network), uploading, done or
// failed.
export type PocketWifiPhase =
  | ''
  | 'connecting'
  | 'listing'
  | 'checking'
  | 'wifi-starting'
  | 'downloading'
  | 'wifi-restarting'
  | 'reconnecting'
  | 'uploading'
  | 'done'
  | 'failed';

export interface PocketWifiState {
  running: boolean;
  cancelling: boolean;
  phase: PocketWifiPhase;
  // downloading and uploading: recording current of total
  current: number;
  total: number;
  // downloading: bytes of totalBytes of the current recording, at rate bytes per second
  bytes: number;
  totalBytes: number;
  rate: number;
  // done: copied into knowpod, failed to transfer
  copied: number;
  failed: number;
  // the recordings listed on the Pocket; incomplete when its listing came back short (not
  // reported by desktop apps before 0.9.2)
  found?: number;
  incomplete?: boolean;
  // failed (and done with failed > 0): why, see PocketUsbSync.tsx
  error: string;
  message: string;
}

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
  // settings and state: false where there is no copying by USB (the Android app)
  usbSupported?: boolean;
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
  // state: whether this computer can copy over the Pocket's WiFi, and how that goes (missing
  // in older desktop apps)
  wifiSupported?: boolean;
  wifi?: PocketWifiState | null;
}

// pocketBluetooth returns the app's Pocket Bluetooth call, or undefined outside the desktop
// and Android apps (and in older desktop apps).
export const pocketBluetooth = () => bridge()?.pocketBluetooth;

type AppWindow = { knowpodDesktop?: DesktopBridge; knowpodAndroid?: DesktopBridge };

const bridge = (): DesktopBridge | undefined =>
  typeof window !== 'undefined' ? ((window as AppWindow).knowpodDesktop ?? (window as AppWindow).knowpodAndroid) : undefined;

// isAndroidApp says whether this is the knowpod Android app (see mobile/).
export const isAndroidApp = () => typeof window !== 'undefined' && !!(window as AppWindow).knowpodAndroid;

// appContext is the i18next context of texts that differ in the Android app ('phone': "this
// phone" rather than "this computer"); see the *_phone keys in i18n/en.ts.
export const appContext = (): 'phone' | undefined => (isAndroidApp() ? 'phone' : undefined);

// showAppSetup shows the Android app's page for the server's address; false outside it.
export function showAppSetup(): boolean {
  const show = bridge()?.showSetup;
  if (!show) return false;
  show();
  return true;
}

// desktopNotifications says whether this is a desktop or Android app that shows
// notifications itself (older desktop apps can't).
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
