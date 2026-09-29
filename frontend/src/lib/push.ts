// Web Push in the browser: whether this browser can receive notifications, and subscribing
// it with the server's key. The service worker shows them (public/push-sw.js).
import { api } from '../api/client';

// isDesktopApp says whether this is the knowpod desktop app (Electron, see desktop/).
export const isDesktopApp = () => typeof window !== 'undefined' && 'knowpodDesktop' in window;

// pushSupported says whether this browser can receive push notifications at all. The desktop
// app can't: Electron has the Push API but no push service behind it.
export const pushSupported = () =>
  typeof window !== 'undefined' && !isDesktopApp() && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;

// isIOS says whether this is an iPhone or iPad, where only an app added to the Home Screen
// can receive notifications.
export const isIOS = () =>
  typeof navigator !== 'undefined' && (/iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1));

// isInstalled says whether the app runs from the Home Screen or as an installed app.
export const isInstalled = () =>
  typeof window !== 'undefined' &&
  (window.matchMedia?.('(display-mode: standalone)').matches || (navigator as Navigator & { standalone?: boolean }).standalone === true);

function keyBytes(base64url: string): Uint8Array {
  const b64 = base64url.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (base64url.length % 4)) % 4);
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
}

// deviceId is the server's ID of a subscription: the first 16 bytes of the SHA-256 of its
// endpoint, in hex.
export async function deviceId(endpoint: string): Promise<string> {
  const hash = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(endpoint)));
  return Array.from(hash.slice(0, 16), (b) => b.toString(16).padStart(2, '0')).join('');
}

async function registration(): Promise<ServiceWorkerRegistration | undefined> {
  if (!pushSupported()) return undefined;
  // Without a service worker (e.g. the dev server) ready would never settle.
  return (await navigator.serviceWorker.getRegistration()) ? navigator.serviceWorker.ready : undefined;
}

// currentSubscription is this browser's push subscription, if it has one.
export async function currentSubscription(): Promise<PushSubscription | null> {
  const reg = await registration();
  return (await reg?.pushManager.getSubscription()) ?? null;
}

// subscribe asks for permission and makes this browser receive the user's notifications.
export async function subscribe(publicKey: string): Promise<'granted' | 'denied' | 'unavailable' | 'serviceError'> {
  const reg = await registration();
  if (!reg) return 'unavailable';
  const permission = await Notification.requestPermission();
  if (permission !== 'granted') return 'denied';
  let sub = await reg.pushManager.getSubscription();
  if (sub && !sameKey(sub, publicKey)) {
    await sub.unsubscribe();
    sub = null;
  }
  try {
    sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) as BufferSource });
  } catch (err) {
    // "Registration failed - push service error": the browser can't reach its push service
    // (Brave turns Google's off by default; it can also be blocked by a network or extension).
    if (err instanceof DOMException && err.name === 'AbortError') return 'serviceError';
    throw err;
  }
  await api.subscribePush(sub.toJSON());
  return 'granted';
}

function sameKey(sub: PushSubscription, publicKey: string): boolean {
  const key = sub.options?.applicationServerKey;
  if (!key) return true;
  const want = keyBytes(publicKey);
  const have = new Uint8Array(key);
  return have.length === want.length && have.every((b, i) => b === want[i]);
}

// unsubscribe stops notifications to this browser.
export async function unsubscribe(): Promise<void> {
  const sub = await currentSubscription();
  if (!sub) return;
  await api.unsubscribePush(await deviceId(sub.endpoint));
  await sub.unsubscribe();
}
