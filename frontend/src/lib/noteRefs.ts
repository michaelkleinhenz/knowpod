import { Recording } from '../api/client';
import { title, when } from './recordings';

// NOTE_REF finds references to other notes in text: "#" and a note's number, e.g. "#12".
// It isn't part of a word, a URL fragment ("page#12") or an HTML entity ("&#12;").
export const NOTE_REF = /(?<![\w&#/])#(\d{1,9})\b/g;

// noteRefPath opens the note with the number (see NoteByNumber).
export function noteRefPath(n: number | string): string {
  return `/n/${n}`;
}

// noteByNumber finds the note with the number in the list.
export function noteByNumber(notes: Recording[] | null | undefined, n: number): Recording | undefined {
  return notes?.find((r) => r.number === n);
}

// matchNotes lists the notes to offer after "#" was typed: with digits, the notes whose
// number starts with them (lowest first); otherwise those whose title contains the text,
// newest first.
export function matchNotes(notes: Recording[], query: string, exclude?: string, limit = 8): Recording[] {
  const q = query.trim().toLowerCase();
  const list = notes.filter((r) => r.number && r.id !== exclude);
  if (/^\d+$/.test(q)) {
    return list
      .filter((r) => String(r.number).startsWith(q))
      .sort((a, b) => (a.number ?? 0) - (b.number ?? 0))
      .slice(0, limit);
  }
  return list
    .filter((r) => !q || title(r).toLowerCase().includes(q))
    .sort((a, b) => when(b).getTime() - when(a).getTime())
    .slice(0, limit);
}
