// Natural-language task dates, like Todoist's quick add: "Call Anna tomorrow 3pm p1",
// "Rechnung bezahlen jeden Monat am 1.", "Standup every weekday at 9:30". parseTask finds the
// date, time, repeat rule and priority in a text (English and German, whichever the words
// are in), and returns the text without them as the title.
import type { Due, Priority, Repeat, RepeatUnit } from '../api/client';

export interface ParsedTask {
  // title is the text without the recognized phrases.
  title: string;
  due?: Due;
  priority?: Priority;
  // found lists the recognized phrases as they were written.
  found: string[];
}

// Letters and digits end a word; \b doesn't know umlauts.
const B = '(?<![\\p{L}\\p{N}])';
const E = '(?![\\p{L}\\p{N}])';
const re = (src: string) => new RegExp(B + '(?:' + src + ')' + E, 'iu');

const WEEKDAYS: [RegExp, number][] = [
  [/^(sun(day)?|sonntag|so)$/i, 0],
  [/^(mon(day)?|montag|mo)$/i, 1],
  [/^(tue(s(day)?)?|dienstag|di)$/i, 2],
  [/^(wed(nesday)?|mittwoch|mi)$/i, 3],
  [/^(thu(r(s(day)?)?)?|donnerstag|do)$/i, 4],
  [/^(fri(day)?|freitag|fr)$/i, 5],
  [/^(sat(urday)?|samstag|sonnabend|sa)$/i, 6],
];
// Full weekday names (and unambiguous abbreviations) stand alone; short forms that are also
// ordinary words ("do", "so", "sat", "sun") need a word like "on", "next" or "am" before them.
const DAY_FULL =
  'sunday|monday|tuesday|wednesday|thursday|friday|saturday|sonntag|montag|dienstag|mittwoch|donnerstag|freitag|samstag|sonnabend|mon|tues?|wed|thu(?:rs?)?|fri';
const DAY_ANY = DAY_FULL + '|sun|sat|mo|di|mi|do|fr|sa|so';
// After "every" / "jeden", the two-letter German forms stay out ("every so often").
const DAY_EVERY = DAY_FULL + '|sun|sat';

const MONTHS: [RegExp, number][] = [
  [/^(jan(uary|uar)?|jän(ner)?)$/i, 0],
  [/^(feb(ruary|ruar)?)$/i, 1],
  [/^(mar(ch)?|märz|maerz|mrz)$/i, 2],
  [/^(apr(il)?)$/i, 3],
  [/^(may|mai)$/i, 4],
  [/^(jun[ei]?)$/i, 5],
  [/^(jul[yi]?)$/i, 6],
  [/^(aug(ust)?)$/i, 7],
  [/^(sep(t(ember)?)?)$/i, 8],
  [/^(o[ck]t(ober)?)$/i, 9],
  [/^(nov(ember)?)$/i, 10],
  [/^(de[cz](ember)?)$/i, 11],
];
const MONTH =
  'january|januar|jan|jänner|jän|february|februar|feb|march|märz|maerz|mar|april|apr|may|mai|june|juni|jun|july|juli|jul|august|aug|september|sept|sep|october|oktober|oct|okt|november|nov|december|dezember|dec|dez';

const NUMBER_WORDS: Record<string, number> = {
  a: 1, an: 1, one: 1, ein: 1, eine: 1, einem: 1, einen: 1, einer: 1, two: 2, zwei: 2, three: 3, drei: 3, four: 4, vier: 4,
  five: 5, fünf: 5, six: 6, sechs: 6, seven: 7, sieben: 7, eight: 8, acht: 8, nine: 9, neun: 9, ten: 10, zehn: 10, other: 2,
};
const NUM = '\\d{1,3}|a|an|one|ein|eine|einem|einen|einer|two|zwei|three|drei|four|vier|five|fünf|six|sechs|seven|sieben|eight|acht|nine|neun|ten|zehn';
const num = (s: string) => (/^\d+$/.test(s) ? parseInt(s, 10) : (NUMBER_WORDS[s.toLowerCase()] ?? 1));

const UNIT_WORDS: [RegExp, RepeatUnit][] = [
  [/^(days?|tage?n?|tag)$/i, 'day'],
  [/^(weeks?|wochen?)$/i, 'week'],
  [/^(months?|monate?n?|monat)$/i, 'month'],
  [/^(years?|jahre?n?|jahr)$/i, 'year'],
];
const UNIT = 'days?|tagen|tage|tag|weeks?|wochen|woche|months?|monaten|monate|monat|years?|jahren|jahre|jahr';
const unitOf = (s: string): RepeatUnit => UNIT_WORDS.find(([r]) => r.test(s))?.[1] ?? 'day';

const weekdayOf = (s: string) => WEEKDAYS.find(([r]) => r.test(s.replace(/\.$/, '')))?.[1];
const monthOf = (s: string) => MONTHS.find(([r]) => r.test(s.replace(/\.$/, '')))?.[1];

// --- dates as local calendar days ---

const pad = (n: number) => String(n).padStart(2, '0');
export const isoDate = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);
const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate());
// nextWeekday is the first day with the weekday after today (strictly).
const nextWeekday = (today: Date, wd: number) => addDays(today, ((wd - today.getDay() + 7) % 7) || 7);
// onOrAfter is the first day with one of the weekdays from today on.
const onOrAfter = (today: Date, days: number[]) => {
  for (let i = 0; i < 7; i++) {
    const d = addDays(today, i);
    if (days.includes(d.getDay())) return d;
  }
  return today;
};
const addMonths = (d: Date, n: number) => {
  const first = new Date(d.getFullYear(), d.getMonth() + n, 1);
  const last = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate();
  return new Date(first.getFullYear(), first.getMonth(), Math.min(d.getDate(), last));
};
// dayOf makes a date from day and month; without a year, a day already past is next year's.
function dayOf(today: Date, day: number, month: number, year?: number): Date | undefined {
  if (month < 0 || month > 11 || day < 1 || day > 31) return undefined;
  let y = year ?? today.getFullYear();
  if (year !== undefined && year < 100) y = 2000 + year;
  const d = new Date(y, month, day);
  if (d.getMonth() !== month) return undefined; // e.g. 31.02.
  if (year === undefined && d < today) return new Date(y + 1, month, day);
  return d;
}

interface Found {
  date?: Date;
  time?: string;
  repeat?: Repeat;
  priority?: Priority;
}

type Rule = { re: RegExp; apply: (m: RegExpExecArray, today: Date, f: Found) => boolean | void };

const clock = (h: number, m = 0) => (h >= 0 && h < 24 && m >= 0 && m < 60 ? `${pad(h)}:${pad(m)}` : undefined);

// The rules, most specific first. Each removes what it matched from the text; a rule whose
// part is already known (e.g. a second date) leaves the words in the title.
const RULES: Rule[] = [
  // Priority: p1–p3, or !1–!3.
  {
    re: re('p([1-3])|!([1-3])'),
    apply: (m, _t, f) => {
      if (f.priority) return false;
      f.priority = Number(m[1] ?? m[2]) as Priority;
    },
  },
  // Repeat: "every weekday", "werktags", "jeden Werktag".
  {
    re: re('every\\s+(?:weekday|workday|business\\s+day)|werktags|jeden\\s+werktag|an\\s+werktagen'),
    apply: (_m, _t, f) => {
      if (f.repeat) return false;
      f.repeat = { every: 1, unit: 'weekday' };
    },
  },
  // Repeat on weekdays: "every monday and friday", "every mon, wed", "jeden Montag", "montags".
  {
    re: re(`(?:every|jeden|jede|jedem)\\s+((?:${DAY_EVERY})\\.?(?:\\s*(?:,|and|und|&)\\s*(?:${DAY_EVERY})\\.?)*)`),
    apply: (m, _t, f) => {
      if (f.repeat) return false;
      const days = m[1].split(/\s*(?:,|and|und|&)\s*|\s+/i).map(weekdayOf).filter((d): d is number => d !== undefined);
      if (days.length === 0) return false;
      f.repeat = { every: 1, unit: 'week', weekdays: [...new Set(days)].sort() };
    },
  },
  {
    re: re('(montags|dienstags|mittwochs|donnerstags|freitags|samstags|sonntags)'),
    apply: (m, _t, f) => {
      if (f.repeat) return false;
      f.repeat = { every: 1, unit: 'week', weekdays: [weekdayOf(m[1].slice(0, -1))!] };
    },
  },
  // Repeat: "every 2 weeks", "every other day", "alle 3 Monate", "jeden Monat", "every month".
  {
    re: re(`(?:every|alle|jeden|jede|jedes)\\s+(?:(${NUM})\\s+)?(?:(other)\\s+)?(${UNIT})`),
    apply: (m, _t, f) => {
      if (f.repeat) return false;
      f.repeat = { every: m[2] ? 2 : m[1] ? num(m[1]) : 1, unit: unitOf(m[3]) };
    },
  },
  {
    re: re('daily|täglich|weekly|wöchentlich|monthly|monatlich|yearly|annually|jährlich'),
    apply: (m, _t, f) => {
      if (f.repeat) return false;
      const w = m[0].toLowerCase();
      const unit: RepeatUnit = /^(daily|täglich)$/.test(w) ? 'day' : /^(weekly|wöchentlich)$/.test(w) ? 'week' : /^(monthly|monatlich)$/.test(w) ? 'month' : 'year';
      f.repeat = { every: 1, unit };
    },
  },
  // Dates: ISO, 5.10.(2026), "5. Oktober", "Oct 5th", "5 October 2026".
  {
    re: re('(\\d{4})-(\\d{2})-(\\d{2})'),
    apply: (m, today, f) => {
      if (f.date) return false;
      f.date = dayOf(today, +m[3], +m[2] - 1, +m[1]);
      return !!f.date;
    },
  },
  {
    re: re('(?:(?:on|am|bis|by|until)\\s+)?(\\d{1,2})\\.(\\d{1,2})\\.(\\d{2,4})?'),
    apply: (m, today, f) => {
      if (f.date) return false;
      f.date = dayOf(today, +m[1], +m[2] - 1, m[3] ? +m[3] : undefined);
      return !!f.date;
    },
  },
  {
    re: re(`(?:(?:on|am|bis|by|until)\\s+)?(\\d{1,2})(?:\\.|st|nd|rd|th)?\\s*(?:of\\s+)?(${MONTH})\\.?(?:\\s+(\\d{4}))?`),
    apply: (m, today, f) => {
      if (f.date) return false;
      f.date = dayOf(today, +m[1], monthOf(m[2]) ?? -1, m[3] ? +m[3] : undefined);
      return !!f.date;
    },
  },
  {
    re: re(`(?:(?:on|by|until)\\s+)?(${MONTH})\\.?\\s+(\\d{1,2})(?:st|nd|rd|th)?(?:,?\\s+(\\d{4}))?`),
    apply: (m, today, f) => {
      if (f.date) return false;
      f.date = dayOf(today, +m[2], monthOf(m[1]) ?? -1, m[3] ? +m[3] : undefined);
      return !!f.date;
    },
  },
  // A day of the month: "on the 15th", "am 1.".
  {
    re: re('(?:on\\s+the|the|am)\\s+(\\d{1,2})(?:\\.|st|nd|rd|th)'),
    apply: (m, today, f) => {
      if (f.date) return false;
      const day = +m[1];
      let d = dayOf(today, day, today.getMonth(), today.getFullYear());
      for (let i = 1; (!d || d < today) && i <= 12; i++) d = dayOf(today, day, (today.getMonth() + i) % 12, today.getFullYear() + Math.floor((today.getMonth() + i) / 12));
      f.date = d;
      return !!f.date;
    },
  },
  // Time: "at 3pm", "3:30 pm", "um 15 Uhr", "15:30", "at 15", "noon", "mittags". A bare
  // hour from 1 to 6 ("at 5") is taken as the afternoon.
  {
    re: re('(?:at|um|@)?\\s*(\\d{1,2})(?:[:.](\\d{2}))?\\s*(am|pm|a\\.m\\.|p\\.m\\.)'),
    apply: (m, _t, f) => {
      if (f.time) return false;
      let h = parseInt(m[1], 10);
      if (h < 1 || h > 12) return false;
      const pm = m[3].toLowerCase().startsWith('p');
      h = (h % 12) + (pm ? 12 : 0);
      f.time = clock(h, m[2] ? parseInt(m[2], 10) : 0);
      return !!f.time;
    },
  },
  ...['(?:(?:at|um|@)\\s*)?(\\d{1,2}):(\\d{2})(?:\\s*uhr)?', '(?:at|um|@)\\s*(\\d{1,2})(?:\\.(\\d{2}))?(?:\\s*uhr)?', '(\\d{1,2})(?:\\.(\\d{2}))?\\s*uhr'].map(
    (src): Rule => ({
      re: re(src),
      apply: (m, _t, f) => {
        if (f.time) return false;
        let h = parseInt(m[1], 10);
        if (!m[2] && h >= 1 && h <= 6) h += 12;
        f.time = clock(h, m[2] ? parseInt(m[2], 10) : 0);
        return !!f.time;
      },
    }),
  ),
  {
    re: re('(?:at\\s+)?noon|mittags|(?:um\\s+)?mittag'),
    apply: (_m, _t, f) => {
      if (f.time) return false;
      f.time = '12:00';
    },
  },
  // Relative days.
  {
    re: re('(?:the\\s+)?day\\s+after\\s+tomorrow|übermorgen'),
    apply: (_m, today, f) => {
      if (f.date) return false;
      f.date = addDays(today, 2);
    },
  },
  {
    re: re('tonight|heute\\s+abend'),
    apply: (_m, today, f) => {
      if (f.date) return false;
      f.date = today;
      f.time ??= '20:00';
    },
  },
  {
    re: re('today|heute'),
    apply: (_m, today, f) => {
      if (f.date) return false;
      f.date = today;
    },
  },
  {
    re: re('tomorrow|tmrw?|(?<!guten\\s)morgen'),
    apply: (_m, today, f) => {
      if (f.date) return false;
      f.date = addDays(today, 1);
    },
  },
  {
    re: re('(?:in|within|innerhalb)\\s+(' + NUM + ')\\s+(' + UNIT + ')'),
    apply: (m, today, f) => {
      if (f.date) return false;
      const n = num(m[1]);
      const unit = unitOf(m[2]);
      f.date = unit === 'day' ? addDays(today, n) : unit === 'week' ? addDays(today, 7 * n) : unit === 'month' ? addMonths(today, n) : addMonths(today, 12 * n);
    },
  },
  {
    re: re('next\\s+week|nächste\\s+woche|kommende\\s+woche'),
    apply: (_m, today, f) => {
      if (f.date) return false;
      f.date = nextWeekday(today, 1);
    },
  },
  {
    re: re('next\\s+month|nächsten\\s+monat|kommenden\\s+monat'),
    apply: (_m, today, f) => {
      if (f.date) return false;
      f.date = new Date(today.getFullYear(), today.getMonth() + 1, 1);
    },
  },
  {
    re: re('(?:this\\s+|on\\s+the\\s+|am\\s+)?weekend|(?:am\\s+)?wochenende'),
    apply: (_m, today, f) => {
      if (f.date) return false;
      f.date = today.getDay() === 6 || today.getDay() === 0 ? today : nextWeekday(today, 6);
    },
  },
  // Weekdays: "friday", "on mon", "next tuesday", "am Do", "nächsten Freitag".
  {
    re: re(`(?:(?:on|next|this|coming|by|until|am|bis|nächsten|nächster|nächste|kommenden|diesen)\\s+)(${DAY_ANY})\\.?|(${DAY_FULL})`),
    apply: (m, today, f) => {
      if (f.date) return false;
      const wd = weekdayOf(m[1] ?? m[2]);
      if (wd === undefined) return false;
      f.date = nextWeekday(today, wd);
    },
  },
];

// parseTask reads the date, time, repeat rule and priority in text. now is the current time.
export function parseTask(text: string, now: Date = new Date()): ParsedTask {
  const today = startOfDay(now);
  const f: Found = {};
  const found: { at: number; text: string }[] = [];
  // Matched phrases are blanked out, so later rules don't see them and positions stay put.
  let work = text;
  for (const rule of RULES) {
    for (let from = 0; ; ) {
      const g = new RegExp(rule.re.source, 'giu');
      g.lastIndex = from;
      const m = g.exec(work);
      if (!m) break;
      from = m.index + Math.max(m[0].length, 1);
      if (!m[0].trim()) continue;
      const before = { ...f };
      if (rule.apply(m, today, f) === false) {
        Object.assign(f, before);
        continue;
      }
      found.push({ at: m.index, text: text.slice(m.index, m.index + m[0].length).trim() });
      work = work.slice(0, m.index) + ' '.repeat(m[0].length) + work.slice(m.index + m[0].length);
      break;
    }
  }
  const title = work
    .replace(/\s+/g, ' ')
    .replace(/\s+([,.;:!?])/g, '$1')
    .replace(/(?:^|\s)(?:at|on|by|um|am|bis|,)\s*$/i, '')
    .replace(/^[\s,.;:!?–-]+/, '')
    .trim();

  let due: Due | undefined;
  if (f.date || f.time || f.repeat) {
    let date = f.date;
    const r = f.repeat;
    if (!date && r?.weekdays) date = onOrAfter(today, r.weekdays);
    if (!date && r?.unit === 'weekday') date = onOrAfter(today, [1, 2, 3, 4, 5]);
    if (!date && f.time) {
      // A time alone is today's, or tomorrow's once it has passed.
      const [h, m] = f.time.split(':').map(Number);
      date = h * 60 + m > now.getHours() * 60 + now.getMinutes() ? today : addDays(today, 1);
    }
    date ??= today;
    due = { date: isoDate(date) };
    if (f.time) due.time = f.time;
    if (r) due.repeat = r.unit === 'month' ? { ...r, monthDay: date.getDate() } : r;
  }
  return { title, due, priority: f.priority, found: found.sort((a, b) => a.at - b.at).map((x) => x.text) };
}
