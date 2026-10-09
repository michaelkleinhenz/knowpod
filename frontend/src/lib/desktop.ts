// The knowpod desktop app (Electron, see desktop/) and Android and iOS apps (Capacitor, see
// mobile/): what they offer the web app (desktop/src/preload.js as window.knowpodDesktop,
// mobile/android/…/AppBridge.java as window.knowpodAndroid, mobile/ios/App/App/AppBridge.swift
// as window.knowpodIOS), and the live notifications they show.
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
  // the OS: 'android' and 'ios' in the mobile apps, process.platform in the desktop app
  platform: string;
  version: string;
  // notify and onOpen are missing in older desktop apps.
  notify?: (n: DesktopNotification) => void;
  onOpen?: (listener: (url: string) => void) => () => void;
  // pocketBluetooth is missing in older desktop and iOS apps.
  pocketBluetooth?: (request: PocketBluetoothRequest) => Promise<PocketBluetoothResult>;
  // systemAudio and onSystemAudio record what the computer plays (desktop app on Linux only,
  // desktop/src/system-audio.js; missing elsewhere and in older desktop apps).
  systemAudio?: (request: SystemAudioRequest) => Promise<SystemAudioResult>;
  onSystemAudio?: (onData: (id: number, chunk: Uint8Array) => void, onEnd: (id: number) => void) => () => void;
  // recorderBluetooth is missing in older apps.
  recorderBluetooth?: (request: RecorderBluetoothRequest) => Promise<RecorderBluetoothState>;
  // showSetup shows the mobile app's page for the server's address (mobile apps only).
  showSetup?: () => void;
}

// The desktop app's Bluetooth connection to a Pocket recorder (desktop/src/pocket-bluetooth.js),
// which switches its USB drive on so the app can copy from it, and drives the copy over the
// Pocket's WiFi (desktop/src/pocket-wifi-sync.js). The Android and iOS apps answer the same
// calls (mobile/android/…/pocket/PocketController.java, mobile/ios/App/App/Pocket/
// PocketController.swift), copying over Bluetooth and the WiFi, not by USB. Its settings
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
  // downloading: bytes of totalBytes of the current recording, at rate bytes per second, over
  // the Pocket’s WiFi or Bluetooth (the mobile apps copy short recordings over Bluetooth;
  // missing in the desktop app, which copies over WiFi only)
  bytes: number;
  totalBytes: number;
  rate: number;
  via?: '' | 'bluetooth' | 'wifi';
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
  // settings and state: false where there is no copying by USB (the mobile apps)
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

// What the desktop app records of the computer's outputs (desktop/src/system-audio.js): list
// names the outputs (sinks), start records one's monitor as float mono samples at rate,
// streamed to onSystemAudio by id, stop ends it.
export type SystemAudioRequest = { action: 'list' } | { action: 'start'; sink: string; rate: number } | { action: 'stop'; id: number };

export interface SystemAudioSink {
  name: string;
  description: string;
  default: boolean;
}

export interface SystemAudioResult {
  ok: boolean;
  // Why it failed: unsupported, missing-tools, no-server or failed.
  error?: string;
  message?: string;
  sinks?: SystemAudioSink[];
  id?: number;
}

// systemAudioBridge returns the desktop app's calls to record what the computer plays, or
// undefined where it can't (outside the desktop app on Linux).
export function systemAudioBridge() {
  const b = bridge();
  return b?.systemAudio && b.onSystemAudio ? { call: b.systemAudio, listen: b.onSystemAudio } : undefined;
}

// The app's Bluetooth link to the knowpod recorder (the ESP32 gadget, esp32/): where the recorder
// has no Wi-Fi, it hands its recordings to the app, which uploads them with the recorder's device
// token (docs/ble-transfer.md; desktop/src/recorder-bluetooth.js, mobile/android/…/recorder/
// RecorderController.java, mobile/ios/App/App/Recorder/RecorderController.swift). Pairing is asked
// for here once; afterwards the app looks for the recorder by itself.
export type RecorderBluetoothRequest =
  | { action: 'state' | 'sync' | 'cancel' | 'forget' }
  | { action: 'pair'; name?: string }
  | { action: 'pin'; pin: string }
  | { action: 'enable'; enabled: boolean };

// RecorderBluetoothPhase is how pairing or copying goes: '' (idle), searching, choose (several
// recorders are in pairing mode: devices), connecting, pin (the app waits for the passkey the
// recorder shows), listing, preparing, uploading (recording current of total, bytes of
// totalBytes), done (copied, failed) or failed (error, message).
export type RecorderBluetoothPhase =
  | ''
  | 'searching'
  | 'choose'
  | 'connecting'
  | 'pin'
  | 'listing'
  | 'preparing'
  | 'uploading'
  | 'done'
  | 'failed';

export interface RecorderBluetoothState {
  ok: boolean;
  // Why a call or the last run failed: not-found, unsupported, permission, auth, busy, timeout,
  // disconnected, no-token, other-server, token, recorder, http, no-server, invalid-pin,
  // not-paired or failed.
  error?: string;
  message?: string;
  paired?: boolean;
  name?: string;
  enabled?: boolean;
  busy?: boolean;
  phase?: RecorderBluetoothPhase;
  current?: number;
  total?: number;
  title?: string;
  bytes?: number;
  totalBytes?: number;
  copied?: number;
  failed?: number;
  devices?: string[];
  // The system asks for the passkey itself (the mobile apps): no 'pin' call.
  systemPin?: boolean;
}

// recorderBluetooth returns the app's knowpod recorder call, or undefined outside the apps (and
// in older ones).
export const recorderBluetooth = () => bridge()?.recorderBluetooth;

// pocketBluetooth returns the app's Pocket Bluetooth call, or undefined outside the desktop
// and mobile apps (and in older desktop and iOS apps).
export const pocketBluetooth = () => bridge()?.pocketBluetooth;

type AppWindow = { knowpodDesktop?: DesktopBridge; knowpodAndroid?: DesktopBridge; knowpodIOS?: DesktopBridge };

const bridge = (): DesktopBridge | undefined =>
  typeof window !== 'undefined'
    ? ((window as AppWindow).knowpodDesktop ?? (window as AppWindow).knowpodAndroid ?? (window as AppWindow).knowpodIOS)
    : undefined;

// isAndroidApp says whether this is the knowpod Android app (see mobile/android/).
export const isAndroidApp = () => typeof window !== 'undefined' && !!(window as AppWindow).knowpodAndroid;

// isIOSApp says whether this is the knowpod iOS app (see mobile/ios/).
export const isIOSApp = () => typeof window !== 'undefined' && !!(window as AppWindow).knowpodIOS;

// isMobileApp says whether this is the knowpod Android or iOS app.
export const isMobileApp = () => isAndroidApp() || isIOSApp();

// appContext is the i18next context of texts that differ in the mobile apps ('phone': "this
// phone" rather than "this computer"); see the *_phone keys in i18n/en.ts.
export const appContext = (): 'phone' | undefined => (isMobileApp() ? 'phone' : undefined);

// deviceContext is appContext for the few texts that differ between the Android and iOS apps
// too ('ios'; every *_ios key needs a *_phone one beside it).
export const deviceContext = (): 'ios' | 'phone' | undefined => (isIOSApp() ? 'ios' : appContext());

// showAppSetup shows the mobile app's page for the server's address; false outside it.
export function showAppSetup(): boolean {
  const show = bridge()?.showSetup;
  if (!show) return false;
  show();
  return true;
}

// desktopNotifications says whether this is a desktop or mobile app that shows
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
