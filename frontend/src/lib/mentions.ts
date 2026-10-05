// Mentions in a note's text, offered after "@" is typed: a person the user shares notes with,
// kept in the Markdown as "@" and their email ("@anna.berg@example.com"), and a date, kept as
// "@2026-10-05". Both are shown as pills.
import type { Person } from '../api/client';
import { locale } from '../i18n';
import { isoDate } from './dateParse';
import { personName } from './people';

// MENTION finds person mentions in text; DATE_MENTION finds date mentions. Neither is part of
// a word or an email address.
export const MENTION = /(?<![\w.%+@-])@([\w.%+-]+@[\w-]+(?:\.[\w-]+)+)/g;
export const DATE_MENTION = /(?<![\w.%+@-])@(\d{4}-\d{2}-\d{2})(?![\w-])/g;

// mentionName names a mentioned person briefly: "@anna.berg".
export function mentionName(email: string): string {
  return `@${personName({ email, userId: email })}`;
}

// validDate says whether "2026-10-05" is a real day.
export function validDate(date: string): boolean {
  const [y, m, d] = date.split('-').map(Number);
  const day = new Date(y, m - 1, d);
  return day.getFullYear() === y && day.getMonth() === m - 1 && day.getDate() === d;
}

// dayOf is the local day of "2026-10-05".
export function dayOf(date: string): Date {
  const [y, m, d] = date.split('-').map(Number);
  return new Date(y, m - 1, d);
}

// dateMentionText shows a mentioned date: "Mon, Oct 5, 2026".
export function dateMentionText(date: string): string {
  if (!validDate(date)) return `@${date}`;
  return dayOf(date).toLocaleDateString(locale(), { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' });
}

// today is today's date, "2026-10-05".
export const today = () => isoDate(new Date());

// matchPeople lists the people to offer after "@": the others whose email (or its part before
// the @) contains what was typed.
export function matchPeople(people: Person[], query: string, limit = 8): Person[] {
  const q = query.trim().toLocaleLowerCase();
  return people
    .filter((p) => !p.self && p.email && (!q || p.email.toLocaleLowerCase().includes(q)))
    .sort((a, b) => {
      // Names starting with the text come first.
      const sa = personName(a).toLocaleLowerCase().startsWith(q) ? 0 : 1;
      const sb = personName(b).toLocaleLowerCase().startsWith(q) ? 0 : 1;
      return sa - sb || a.email.localeCompare(b.email);
    })
    .slice(0, limit);
}
