// Task dates as the UI shows them: "Today 15:00", "Tomorrow", "Fri", "Oct 5"; repeat rules
// ("every weekday"); and the order and day groups of the Tasks view.
import i18n from 'i18next';
import type { Due, Recording, Repeat } from '../api/client';
import { locale } from '../i18n';
import { isoDate } from './dateParse';
import { isTask } from './labels';

// dueDate is the task's day as a local date.
export function dueDate(due: Due): Date {
  const [y, m, d] = due.date.split('-').map(Number);
  return new Date(y, m - 1, d);
}

// dueMoment is when the task is due: its time, or the end of its day.
function dueMoment(due: Due): Date {
  const d = dueDate(due);
  if (!due.time) return new Date(d.getFullYear(), d.getMonth(), d.getDate(), 23, 59, 59);
  const [h, m] = due.time.split(':').map(Number);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate(), h, m);
}

// overdue reports whether an open task's date (and time) has passed.
export function overdue(r: Recording, now = new Date()): boolean {
  return !!r.due && !r.done && dueMoment(r.due) < now;
}

const daysBetween = (a: Date, b: Date) => Math.round((Date.UTC(b.getFullYear(), b.getMonth(), b.getDate()) - Date.UTC(a.getFullYear(), a.getMonth(), a.getDate())) / 86_400_000);

// formatClockTime shows HH:MM the way the UI language writes times.
export function formatClockTime(time: string): string {
  const [h, m] = time.split(':').map(Number);
  return new Date(2000, 0, 1, h, m).toLocaleTimeString(locale(), { hour: 'numeric', minute: '2-digit' });
}

// dayName names a day relative to today: Yesterday, Today, Tomorrow, the weekday within the
// next week, else the date.
export function dayName(d: Date, now = new Date()): string {
  const diff = daysBetween(now, d);
  if (diff === 0) return i18n.t('days.today');
  if (diff === 1) return i18n.t('days.tomorrow');
  if (diff === -1) return i18n.t('days.yesterday');
  if (diff > 1 && diff < 7) return d.toLocaleDateString(locale(), { weekday: 'long' });
  return d.toLocaleDateString(locale(), {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric',
  });
}

// formatDue shows a due date, e.g. "Tomorrow 15:00" or "Mon, Oct 5".
export function formatDue(due: Due, now = new Date()): string {
  const day = dayName(dueDate(due), now);
  return due.time ? `${day} ${formatClockTime(due.time)}` : day;
}

// weekdayLong names a weekday (0 = Sunday) in full in the UI language.
export function weekdayLong(wd: number): string {
  return new Date(2026, 0, 4 + wd).toLocaleDateString(locale(), { weekday: 'long' });
}

// nthWeekdayRepeat is the monthly repeat on the date's weekday that falls on it, e.g. every
// third Friday for Oct 16, 2026; a fifth one is the month's last.
export function nthWeekdayRepeat(d: Date): Repeat {
  const nth = Math.ceil(d.getDate() / 7);
  return { every: 1, unit: 'month', nth: nth > 4 ? -1 : nth, weekdays: [d.getDay()] };
}

// weekdayShort names a weekday (0 = Sunday) briefly in the UI language.
export function weekdayShort(wd: number): string {
  return new Date(2026, 0, 4 + wd).toLocaleDateString(locale(), { weekday: 'short' });
}

// formatRepeat describes a repeat rule, e.g. "every 2 weeks" or "every Mon, Fri".
export function formatRepeat(r: Repeat): string {
  const t = i18n.t.bind(i18n);
  if (r.unit === 'weekday') return t('tasks.repeat.weekday');
  if (r.unit === 'month' && r.nth && r.weekdays?.length === 1) {
    const opts = { count: r.every, nth: t(`tasks.repeat.nth.${r.nth < 0 ? 'last' : r.nth}`), day: weekdayLong(r.weekdays[0]) };
    return r.every > 1 ? t('tasks.repeat.everyNMonthsOnNth', opts) : t('tasks.repeat.monthlyOnNth', opts);
  }
  if (r.unit === 'week' && r.weekdays?.length) {
    const days = r.weekdays.map(weekdayShort).join(', ');
    return r.every > 1 ? t('tasks.repeat.everyNWeeksOn', { count: r.every, days }) : t('tasks.repeat.weeklyOn', { days });
  }
  return t(`tasks.repeat.${r.unit}`, { count: r.every });
}

// REMINDERS are the reminder choices, in minutes before the due time (for a day without a
// time: before 9:00 that day).
export const REMINDERS = [0, 5, 15, 30, 60, 120, 1440, 2880, 10080];

// formatReminder describes when a reminder goes off.
export function formatReminder(minutes: number | undefined, allDay: boolean): string {
  const t = i18n.t.bind(i18n);
  if (minutes === undefined) return t('tasks.remind.none');
  if (minutes === 0) return allDay ? t('tasks.remind.morning', { time: formatClockTime('09:00') }) : t('tasks.remind.atTime');
  if (minutes % 1440 === 0) return t(allDay ? 'tasks.remind.daysBeforeMorning' : 'tasks.remind.daysBefore', { count: minutes / 1440, time: formatClockTime('09:00') });
  if (minutes % 60 === 0) return t('tasks.remind.hoursBefore', { count: minutes / 60 });
  return t('tasks.remind.minutesBefore', { count: minutes });
}

// compareTasks orders tasks by date (no date last), then priority (P1 first, P4 — none —
// last), then time (timed before all-day).
export function compareTasks(a: Recording, b: Recording): number {
  const da = a.due?.date ?? '9999';
  const db = b.due?.date ?? '9999';
  if (da !== db) return da < db ? -1 : 1;
  const pa = a.priority || 4;
  const pb = b.priority || 4;
  if (pa !== pb) return pa - pb;
  const ta = a.due?.time ?? '99:99';
  const tb = b.due?.time ?? '99:99';
  return ta < tb ? -1 : ta > tb ? 1 : 0;
}

export interface TaskGroup {
  key: string;
  label: string;
  // date is the group's day, shown next to its name.
  date?: string;
  overdue?: boolean;
  items: Recording[];
}

// UPCOMING_DAYS is how many days (from today) get a group of their own.
const UPCOMING_DAYS = 7;

// groupTasks sorts the open tasks into Overdue, Today, the next days, Later and No date.
export function groupTasks(notes: Recording[], now = new Date()): TaskGroup[] {
  const t = i18n.t.bind(i18n);
  const open = notes.filter((r) => isTask(r) && !r.done).sort(compareTasks);
  const today = isoDate(now);
  const groups = new Map<string, TaskGroup>();
  const add = (key: string, make: () => Omit<TaskGroup, 'items'>, r: Recording) => {
    if (!groups.has(key)) groups.set(key, { ...make(), items: [] });
    groups.get(key)!.items.push(r);
  };
  const last = isoDate(new Date(now.getFullYear(), now.getMonth(), now.getDate() + UPCOMING_DAYS - 1));
  for (const r of open) {
    if (!r.due) add('none', () => ({ key: 'none', label: t('tasks.groups.noDate') }), r);
    else if (r.due.date < today) add('overdue', () => ({ key: 'overdue', label: t('tasks.groups.overdue'), overdue: true }), r);
    else if (r.due.date > last) add('later', () => ({ key: 'later', label: t('tasks.groups.later') }), r);
    else {
      const d = dueDate(r.due);
      add(r.due.date, () => ({ key: r.due!.date, label: dayName(d, now), date: d.toLocaleDateString(locale(), { weekday: daysBetween(now, d) < 2 ? 'long' : undefined, month: 'long', day: 'numeric' }) }), r);
    }
  }
  const order = (g: TaskGroup) => (g.key === 'overdue' ? '0' : g.key === 'later' ? '8' : g.key === 'none' ? '9' : '1' + g.key);
  return [...groups.values()].sort((a, b) => (order(a) < order(b) ? -1 : 1));
}
