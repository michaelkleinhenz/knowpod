// People in shared notes: who owns or made a note, how to name them briefly, and how filter
// terms such as owner:bob or from:me find them.
import type { Person, Recording } from '../api/client';

// madeBy is the user who made the note: its owner, or the editor who added it to a
// shared note.
export const madeBy = (r: Recording) => r.createdBy || r.ownerId;

// personName names a person briefly: the part of their email before the @.
export function personName(p: Pick<Person, 'email' | 'userId'>): string {
  return p.email ? p.email.split('@')[0] : p.userId;
}

// initials are up to two letters for an avatar: "anna.berg@…" is AB, "bob@…" is B.
export function initials(p: Pick<Person, 'email' | 'userId'>): string {
  const parts = personName(p).split(/[._+-]+/).filter(Boolean);
  return parts
    .slice(0, 2)
    .map((s) => s[0].toLocaleUpperCase())
    .join('');
}

// AVATAR_HUES are the avatar colors; a person always gets the same one.
const AVATAR_HUES = 8;
export function avatarHue(userId: string): number {
  let h = 0;
  for (const c of userId) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return h % AVATAR_HUES;
}

// findPeople returns the IDs of the people a filter value names: "me", a whole email, the
// part before the @ ("bob"), or the start of an email. An empty set names no one known.
export function findPeople(value: string, people: Person[], userId?: string): Set<string> {
  const v = value.trim().toLocaleLowerCase();
  if (v === 'me') return new Set(userId ? [userId] : []);
  const out = new Set<string>();
  for (const p of people) {
    const email = p.email.toLocaleLowerCase();
    if (email === v || personName(p).toLocaleLowerCase() === v || email.startsWith(v) || p.userId === value) out.add(p.userId);
  }
  return out;
}
