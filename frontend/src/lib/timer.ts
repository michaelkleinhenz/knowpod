// Time tracking in the UI: estimates typed as "45", "45m", "1h30" or "1,5h", durations shown
// as "1 h 05 min" or "12:34", focus sessions (Pomodoro), and the weeks of the time log.
import i18n, { TFunction } from 'i18next';
import { isoDate } from './dateParse';

// FOCUS_MINUTES is the length of a focus session (a Pomodoro).
export const FOCUS_MINUTES = 25;

// parseEstimate reads an estimate in minutes: "45", "45m", "1h", "1h30", "1h 30m", "1.5h" or
// "1,5 h". It returns 0 for an empty text and null for one it can't read.
export function parseEstimate(text: string): number | null {
  const s = text.trim().toLowerCase().replace(',', '.').replace(/\s+/g, '');
  if (!s) return 0;
  let m: RegExpExecArray | null;
  if ((m = /^(\d+)(m|min)?$/.exec(s))) return Number(m[1]);
  if ((m = /^(\d+(?:\.\d+)?)(h|std)$/.exec(s))) return Math.round(Number(m[1]) * 60);
  if ((m = /^(\d+)(?:h|std)(\d+)(m|min)?$/.exec(s))) return Number(m[1]) * 60 + Number(m[2]);
  return null;
}

// formatMinutes shows minutes briefly: "45 min", "2 h", "1 h 30 min".
export function formatMinutes(minutes: number): string {
  const t = i18n.t.bind(i18n);
  const h = Math.floor(minutes / 60);
  const m = Math.round(minutes % 60);
  if (h === 0) return t('time.minutes', { count: m });
  if (m === 0) return t('time.hours', { count: h });
  return t('time.hoursMinutes', { h, m: String(m).padStart(2, '0') });
}

// formatSeconds shows logged time in minutes (rounded down; under a minute as "0 min").
export function formatSeconds(seconds: number): string {
  return formatMinutes(Math.floor(Math.max(seconds, 0) / 60));
}

// formatClockDuration shows a running timer: "4:05" or "1:04:05".
export function formatClockDuration(seconds: number): string {
  const s = Math.max(Math.floor(seconds), 0);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

// weekOf returns the Monday and Sunday (YYYY-MM-DD) of the week d is in.
export function weekOf(d: Date): { from: string; to: string; monday: Date } {
  const monday = new Date(d.getFullYear(), d.getMonth(), d.getDate() - ((d.getDay() + 6) % 7));
  const sunday = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() + 6);
  return { from: isoDate(monday), to: isoDate(sunday), monday };
}

// notifyFocusDone tells the user a focus session is over: a notification where they are
// allowed, through the service worker when there is one (needed on phones).
export function notifyFocusDone(noteTitle: string, t: TFunction): void {
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
  const title = t('timer.focusDoneTitle');
  const options = { body: t('timer.focusDoneBody', { title: noteTitle }), tag: 'knowpod-focus', icon: '/favicon.svg' };
  const direct = () => {
    try {
      new Notification(title, options);
    } catch {
      // Some browsers only show notifications from a service worker.
    }
  };
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.getRegistration().then((reg) => (reg ? reg.showNotification(title, options) : direct()), direct);
  } else {
    direct();
  }
}
