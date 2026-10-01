// Due marks in a note's text: "[2026-10-01]" (or with a time, "[2026-10-01 15:00]") in a list
// item or paragraph marks it as due then, without making a task of it. Typed as "[today]",
// "[fri]", "[next monday]", "[5.10.]" or "[heute 15 Uhr]", the mark becomes the date, so it
// stays right on the following days.
import i18n from 'i18next';
import { locale } from '../i18n';
import { isoDate, parseTask } from './dateParse';

// DUE_MARK finds due marks; the brackets may be escaped ("\[…\]"), as the editor saves them.
export const DUE_MARK = /\\?\[(\d{4}-\d{2}-\d{2})(?: (\d{1,2}:\d{2}))?\\?\]/g;

// dueMarkFor returns the mark for what was typed between the brackets ("today" →
// "[2026-10-01]"), or null when it is no date (or a repeating one).
export function dueMarkFor(text: string, now: Date = new Date()): string | null {
  const words = text.trim();
  if (!words || words.length > 30) return null;
  // Short weekdays that are also words ("sun", "mo") only count after "on".
  for (const t of [words, `on ${words}`]) {
    const p = parseTask(t, now);
    if (p.due && !p.due.repeat && !p.title.trim()) return `[${p.due.date}${p.due.time ? ` ${p.due.time}` : ''}]`;
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

// dueLabel names a mark's date for its tooltip: "Due today, 15:00", "Overdue since Sep 29".
export function dueLabel(date: string, time?: string, now: Date = new Date()): string {
  const [y, m, d] = date.split('-').map(Number);
  const day = new Date(y, m - 1, d);
  const tomorrow = new Date(now);
  tomorrow.setDate(now.getDate() + 1);
  let when =
    date === isoDate(now)
      ? i18n.t('days.today')
      : date === isoDate(tomorrow)
        ? i18n.t('days.tomorrow')
        : day.toLocaleDateString(locale(), { weekday: 'short', month: 'short', day: 'numeric', year: y === now.getFullYear() ? undefined : 'numeric' });
  if (time) when += `, ${time}`;
  return i18n.t(dueState(date, now) === 'overdue' ? 'dueMarks.overdue' : 'dueMarks.due', { when });
}
