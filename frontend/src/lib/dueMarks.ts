// Due marks in a note's text: "[2026-10-01]" (or with a time, "[2026-10-01 15:00]") in a list
// item or paragraph marks it as due then, without making a task of it. Typed as "[today]",
// "[fri]", "[next monday]", "[5.10.]" or "[heute 15 Uhr]", the mark becomes the date, so it
// stays right on the following days. The mark remembers the language it was typed in
// ("[2026-10-02 de]") and is shown relative to today in it: "[morgen]", "[tomorrow 15:00]".
import i18n from 'i18next';
import { locale } from '../i18n';
import { isoDate, parseTask } from './dateParse';

// DUE_MARK finds due marks: date, time and language; the brackets may be escaped ("\[…\]"), as
// the editor saves them.
export const DUE_MARK = /\\?\[(\d{4}-\d{2}-\d{2})(?: (\d{1,2}:\d{2}))?(?: (en|de))?\\?\]/g;

export type DueLanguage = 'en' | 'de';

// Words that only German (or only English) dates use; the short German weekdays ("fr", "mi")
// count as German, "am"/"pm" as English.
const W = (words: string) => new RegExp(`(?<![\\p{L}])(?:${words})(?![\\p{L}])`, 'iu');
const GERMAN = W(
  'heute|morgen|übermorgen|uebermorgen|montag|dienstag|mittwoch|donnerstag|freitag|samstag|sonntag|sonnabend|mo|di|mi|do|fr|sa|so|' +
    'nächste[nmrs]?|naechste[nmrs]?|kommende[nmrs]?|übernächste[nmrs]?|uhr|tagen?|tage|wochen?|monate?n?|jahre?n?|' +
    'januar|jänner|februar|märz|maerz|mai|juni|juli|oktober|okt|dezember|dez|mittags?|abends?|früh|vormittags?|nachmittags?|morgens',
);
const ENGLISH = W(
  'today|tomorrow|monday|tuesday|wednesday|thursday|friday|saturday|sunday|mon|tues?|wed|thu(?:rs?)?|fri|sat|sun|next|this|' +
    'days?|weeks?|months?|years?|january|february|march|may|june|july|october|oct|december|dec|am|pm|noon|morning|afternoon|evening|tonight',
);

// dueLanguage says which language a date was typed in, or, for numbers only, the app's.
export function dueLanguage(text: string): DueLanguage {
  if (GERMAN.test(text)) return 'de';
  if (ENGLISH.test(text)) return 'en';
  return i18n.language?.startsWith('de') ? 'de' : 'en';
}

// dueMarkFor returns the mark for what was typed between the brackets ("today" →
// "[2026-10-01 en]"), or null when it is no date (or a repeating one).
export function dueMarkFor(text: string, now: Date = new Date()): string | null {
  const words = text.trim();
  if (!words || words.length > 30) return null;
  // Short weekdays that are also words ("sun", "mo") only count after "on".
  for (const t of [words, `on ${words}`]) {
    const p = parseTask(t, now);
    if (p.due && !p.due.repeat && !p.title.trim()) return `[${p.due.date}${p.due.time ? ` ${p.due.time}` : ''} ${dueLanguage(words)}]`;
  }
  return null;
}

export type DueState = 'overdue' | 'today' | 'soon' | 'later';

// dueState says when a mark's date is: past, today, within the next week, or later.
export function dueState(date: string, now: Date = new Date()): DueState {
  const today = isoDate(now);
  if (date < today) return 'overdue';
  if (date === today) return 'today';
  const week = new Date(now);
  week.setDate(now.getDate() + 7);
  return date <= isoDate(week) ? 'soon' : 'later';
}

const dayOf = (date: string) => {
  const [y, m, d] = date.split('-').map(Number);
  return new Date(y, m - 1, d);
};

// daysUntil counts the days from today to a date (negative when it's past).
const daysUntil = (date: string, now: Date) =>
  Math.round((dayOf(date).getTime() - dayOf(isoDate(now)).getTime()) / 86_400_000);

// dueText names a mark's date relative to today, in the language it was typed in (else the
// app's): "today", "morgen", "übermorgen", "Friday", "3 days ago", "Oct 15", with the time.
export function dueText(date: string, time?: string, lang?: string, now: Date = new Date()): string {
  const language = lang || (i18n.language?.startsWith('de') ? 'de' : 'en');
  const days = daysUntil(date, now);
  const relative = new Intl.RelativeTimeFormat(language, { numeric: 'auto' });
  let when: string;
  const named = Math.abs(days) <= 2 ? relative.format(days, 'day') : '';
  if (named && !/\d/.test(named)) when = named; // today, tomorrow, übermorgen, …
  else if (days > 0 && days < 7) when = dayOf(date).toLocaleDateString(language, { weekday: 'long' });
  else if (days < 0 && days > -7) when = relative.format(days, 'day');
  else {
    const day = dayOf(date);
    when = day.toLocaleDateString(language, { month: 'short', day: 'numeric', year: day.getFullYear() === now.getFullYear() ? undefined : 'numeric' });
  }
  return time ? `${when} ${time}` : when;
}

// dueLabel spells out a mark's date for its tooltip: "Due: Fri, Oct 2, 15:00".
export function dueLabel(date: string, time?: string, now: Date = new Date()): string {
  const day = dayOf(date);
  let when = day.toLocaleDateString(locale(), { weekday: 'short', month: 'short', day: 'numeric', year: day.getFullYear() === now.getFullYear() ? undefined : 'numeric' });
  if (time) when += `, ${time}`;
  return i18n.t(dueState(date, now) === 'overdue' ? 'dueMarks.overdue' : 'dueMarks.due', { when });
}

// dueLineState says how a line with due marks is colored: by its most pressing mark, or null
// when it has none.
export function dueLineState(text: string, now: Date = new Date()): DueState | null {
  let first: string | null = null;
  for (const m of text.matchAll(new RegExp(DUE_MARK))) if (!first || m[1] < first) first = m[1];
  return first ? dueState(first, now) : null;
}
