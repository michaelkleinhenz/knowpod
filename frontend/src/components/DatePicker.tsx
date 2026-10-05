import { MouseEvent as ReactMouseEvent, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { locale } from '../i18n';
import { isoDate } from '../lib/dateParse';
import { dayOf, validDate } from '../lib/mentions';

interface Props {
  // value is the date shown at first ("2026-10-05"); rect is what the calendar opens at.
  value: string;
  rect: DOMRect;
  onPick: (date: string) => void;
  onClose: () => void;
}

const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);

// DatePicker is a month calendar to choose a day, opened below (or above) rect. It takes the
// keys while it is open, so the editor keeps its focus: the arrow keys move the day, Page
// Up/Down the month, Enter takes the day and Escape closes it.
export function DatePicker({ value, rect, onPick, onClose }: Props) {
  const { t, i18n } = useTranslation();
  const [day, setDay] = useState(() => (validDate(value) ? dayOf(value) : new Date()));
  // month is the first day of the month shown; it follows the chosen day.
  const [month, setMonth] = useState(() => new Date(day.getFullYear(), day.getMonth(), 1));
  const box = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ top: -9999, left: -9999 });

  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const h = el.offsetHeight;
    const w = el.offsetWidth;
    const below = rect.bottom + 6 + h <= window.innerHeight;
    setPos({
      top: below ? rect.bottom + 6 : Math.max(8, rect.top - h - 6),
      left: Math.max(8, Math.min(rect.left, window.innerWidth - w - 8)),
    });
  }, [rect]);

  const move = (d: Date) => {
    setDay(d);
    setMonth(new Date(d.getFullYear(), d.getMonth(), 1));
  };
  const shiftMonth = (n: number) => {
    const m = new Date(month.getFullYear(), month.getMonth() + n, 1);
    setMonth(m);
    // The chosen day moves along, to the same day of the month where there is one.
    const last = new Date(m.getFullYear(), m.getMonth() + 1, 0).getDate();
    setDay(new Date(m.getFullYear(), m.getMonth(), Math.min(day.getDate(), last)));
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const steps: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 };
      if (e.key === 'Escape') onClose();
      else if (e.key === 'Enter') onPick(isoDate(day));
      else if (e.key in steps) move(addDays(day, steps[e.key]));
      else if (e.key === 'PageUp' || e.key === 'PageDown') shiftMonth(e.key === 'PageUp' ? -1 : 1);
      else if (e.key === 'Tab') onClose();
      else return;
      if (e.key !== 'Tab') {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    const onDown = (e: MouseEvent) => !box.current?.contains(e.target as Node) && onClose();
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('mousedown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.removeEventListener('mousedown', onDown);
    };
  });

  // Weeks start on Monday in German, on Sunday in English.
  const weekStart = i18n.language?.startsWith('de') ? 1 : 0;
  const first = addDays(month, -((month.getDay() - weekStart + 7) % 7));
  const days = Array.from({ length: 42 }, (_, i) => addDays(first, i));
  const weeks = days[35].getMonth() === month.getMonth() ? 6 : 5;
  const todayIso = isoDate(new Date());
  const chosen = isoDate(day);
  const weekdays = days.slice(0, 7).map((d) => d.toLocaleDateString(locale(), { weekday: 'narrow' }));
  const keep = (e: ReactMouseEvent) => e.preventDefault(); // keep the editor's focus

  return (
    <div ref={box} className="date-picker" style={{ top: pos.top, left: pos.left }} role="dialog" aria-label={t('mentions.chooseDate')} onMouseDown={keep}>
      <div className="date-picker-head">
        <button type="button" className="date-picker-nav" aria-label={t('mentions.prevMonth')} title={t('mentions.prevMonth')} onClick={() => shiftMonth(-1)}>
          ‹
        </button>
        <span className="date-picker-month" aria-live="polite">
          {month.toLocaleDateString(locale(), { month: 'long', year: 'numeric' })}
        </span>
        <button type="button" className="date-picker-nav" aria-label={t('mentions.nextMonth')} title={t('mentions.nextMonth')} onClick={() => shiftMonth(1)}>
          ›
        </button>
      </div>
      <div className="date-picker-grid" role="grid">
        {weekdays.map((w, i) => (
          <span key={`w${i}`} className="date-picker-weekday" aria-hidden="true">
            {w}
          </span>
        ))}
        {days.slice(0, weeks * 7).map((d) => {
          const iso = isoDate(d);
          const cls = ['date-picker-day'];
          if (d.getMonth() !== month.getMonth()) cls.push('other');
          if (iso === todayIso) cls.push('today');
          if (iso === chosen) cls.push('selected');
          return (
            <button
              key={iso}
              type="button"
              role="gridcell"
              aria-selected={iso === chosen}
              aria-label={d.toLocaleDateString(locale(), { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' })}
              className={cls.join(' ')}
              onClick={() => onPick(iso)}
            >
              {d.getDate()}
            </button>
          );
        })}
      </div>
      <div className="date-picker-foot">
        <button type="button" className="date-picker-today" onClick={() => onPick(todayIso)}>
          {t('mentions.today')}
        </button>
      </div>
    </div>
  );
}
