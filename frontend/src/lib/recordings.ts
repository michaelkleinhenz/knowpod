import { Recording } from '../api/client';

// title is what a conversation is called in the UI: the AI summary's title, else the
// source's title (Pocket), else a placeholder.
export function title(r: Recording): string {
  return r.summary?.title || r.title || 'Untitled recording';
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
  switch (r.status) {
    case 'remote':
      return 'Fetching audio…';
    case 'uploading':
      return 'Uploading…';
    case 'received':
      return 'Processing audio…';
    case 'stored':
      return aiReady ? 'Transcribing…' : 'Waiting for AI setup';
    case 'transcribed':
      return aiReady ? 'Summarizing…' : 'Waiting for AI setup';
    case 'failed':
      return 'Failed';
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

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export function formatTime(d: Date): string {
  return d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
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
  const date = d.toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric', year: d.getFullYear() === today.getFullYear() ? undefined : 'numeric' });
  if (dayKey(d) === dayKey(today)) return { label: 'Today', date };
  if (dayKey(d) === dayKey(yesterday)) return { label: 'Yesterday', date };
  return { label: d.toLocaleDateString(undefined, { weekday: 'short' }), date: d.toLocaleDateString(undefined, { month: 'long', day: 'numeric', year: d.getFullYear() === today.getFullYear() ? undefined : 'numeric' }) };
}
