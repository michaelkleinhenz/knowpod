import i18n from 'i18next';
import { NoteType, Recording } from '../api/client';
import { locale } from '../i18n';

// title is what a conversation is called in the UI: the AI summary's title, else the
// source's title (Pocket, file name), else a placeholder.
export function title(r: Recording): string {
  return r.summary?.title || r.title || i18n.t('conversations.untitled');
}

// noteType says what kind of note a recording is; notes from before types are audio.
export function noteType(r: Recording): NoteType {
  return r.type === 'text' || r.type === 'document' || r.type === 'board' ? r.type : 'audio';
}

// when is the moment a conversation happened.
export function when(r: Recording): Date {
  return new Date(r.recordedAt || r.createdAt);
}

// processing reports whether the recording is still moving through the pipeline.
export function processing(r: Recording): boolean {
  return r.status !== 'summarized' && r.status !== 'failed';
}

// statusLabel describes an unfinished recording; aiReady says whether OpenRouter is set up.
export function statusLabel(r: Recording, aiReady: boolean): string | null {
  const t = i18n.t.bind(i18n);
  if (r.type === 'document') {
    switch (r.status) {
      case 'remote':
        return t('state.documentRemote');
      case 'received':
        return t('state.documentReceived');
      case 'stored':
        return aiReady ? t('state.reading') : t('state.waitingAI');
    }
  }
  switch (r.status) {
    case 'remote':
      return t('state.remote');
    case 'uploading':
      return t('state.uploading');
    case 'received':
      return t('state.received');
    case 'stored':
      return aiReady ? t('state.transcribing') : t('state.waitingAI');
    case 'transcribed':
      return aiReady ? t('state.summarizing') : t('state.waitingAI');
    case 'failed':
      return t('state.failed');
    default:
      return null;
  }
}

export function formatDuration(ms?: number): string {
  if (!ms) return '';
  const s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}` : `${m}:${String(sec).padStart(2, '0')}`;
}

// formatClock formats a position in a recording as m:ss, or h:mm:ss from one hour on.
export function formatClock(ms: number): string {
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

export function formatBytes(n: number): string {
  const nf = new Intl.NumberFormat(locale(), { maximumFractionDigits: 1 });
  if (n < 1024) return `${nf.format(n)} B`;
  if (n < 1024 * 1024) return `${nf.format(Math.round(n / 1024))} KB`;
  return `${nf.format(n / 1024 / 1024)} MB`;
}

export function formatTime(d: Date): string {
  return d.toLocaleTimeString(locale(), { hour: 'numeric', minute: '2-digit' });
}

export function formatDate(d: Date | string | undefined, opts: Intl.DateTimeFormatOptions = { dateStyle: 'medium' }): string {
  if (!d) return '—';
  return new Date(d).toLocaleString(locale(), opts);
}

// dayKey groups dates by local calendar day.
export function dayKey(d: Date): string {
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

// dayLabel names a day relative to today: "Today", "Yesterday", or the weekday.
export function dayLabel(d: Date): { label: string; date: string } {
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  const year = d.getFullYear() === today.getFullYear() ? undefined : 'numeric';
  if (dayKey(d) === dayKey(today) || dayKey(d) === dayKey(yesterday)) {
    return {
      label: i18n.t(dayKey(d) === dayKey(today) ? 'days.today' : 'days.yesterday'),
      date: d.toLocaleDateString(locale(), { weekday: 'long', month: 'long', day: 'numeric', year }),
    };
  }
  return {
    label: d.toLocaleDateString(locale(), { weekday: 'short' }),
    date: d.toLocaleDateString(locale(), { month: 'long', day: 'numeric', year }),
  };
}

// languageName names a language tag in the current UI language, e.g. "de-DE" → "German
// (Germany)" / "Deutsch (Deutschland)".
export function languageName(tag: string): string {
  try {
    return new Intl.DisplayNames([locale()], { type: 'language' }).of(tag) ?? tag;
  } catch {
    return tag;
  }
}
