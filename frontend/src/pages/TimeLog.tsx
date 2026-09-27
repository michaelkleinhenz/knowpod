import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { api, TimeEntry } from '../api/client';
import { DownloadIcon, NewNoteIcon, TrashIcon } from '../components/Icons';
import { useNotes } from '../context/NotesContext';
import { locale } from '../i18n';
import { isoDate } from '../lib/dateParse';
import { errorText } from '../lib/errors';
import { sortByTitle } from '../lib/folders';
import { formatTime, title } from '../lib/recordings';
import { formatMinutes, formatSeconds, weekOf } from '../lib/timer';

// ManualEntry logs time spent on a note by hand, e.g. when the timer wasn't started.
function ManualEntry({ day, onAdded }: { day: string; onAdded: () => void }) {
  const { t } = useTranslation();
  const { recordings } = useNotes();
  const [noteId, setNoteId] = useState('');
  const [date, setDate] = useState(day);
  const [start, setStart] = useState('09:00');
  const [end, setEnd] = useState('10:00');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => setDate(day), [day]);
  // Open tasks first, then the other notes, each by title.
  const notes = useMemo(() => {
    const list = (recordings ?? []).filter((r) => r.type !== 'board');
    const open = list.filter((r) => r.labels?.includes('task') && !r.done);
    return [...sortByTitle(open), ...sortByTitle(list.filter((r) => !open.includes(r)))];
  }, [recordings]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const from = new Date(`${date}T${start}`);
      const to = new Date(`${date}T${end}`);
      // An end before the start is on the next day (work past midnight).
      if (to <= from) to.setDate(to.getDate() + 1);
      await api.addTimeEntry(noteId, from.toISOString(), to.toISOString());
      onAdded();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="time-manual" onSubmit={submit}>
      <label>
        <span>{t('timeLog.note')}</span>
        <select required value={noteId} onChange={(e) => setNoteId(e.target.value)}>
          <option value="">{t('timeLog.pickNote')}</option>
          {notes.map((r) => (
            <option key={r.id} value={r.id}>
              {title(r)}
              {r.number ? ` #${r.number}` : ''}
            </option>
          ))}
        </select>
      </label>
      <label>
        <span>{t('timeLog.date')}</span>
        <input type="date" required value={date} onChange={(e) => setDate(e.target.value)} />
      </label>
      <label>
        <span>{t('timeLog.from')}</span>
        <input type="time" required value={start} onChange={(e) => setStart(e.target.value)} />
      </label>
      <label>
        <span>{t('timeLog.to')}</span>
        <input type="time" required value={end} onChange={(e) => setEnd(e.target.value)} />
      </label>
      <button type="submit" className="icon-submit" disabled={busy || !noteId} aria-label={t('timeLog.add')} title={t('timeLog.add')}>
        <NewNoteIcon />
      </button>
      {error && <p className="error">{error}</p>}
    </form>
  );
}

// TimeLog shows the time logged in a week: the total, the time per note against its
// estimate, and each entry by day. Entries can be added by hand and deleted, and the week is
// exported as CSV.
export function TimeLog() {
  const { t } = useTranslation();
  const { timer, recordings } = useNotes();
  const [week, setWeek] = useState(() => weekOf(new Date()));
  const [entries, setEntries] = useState<TimeEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const load = useCallback(() => {
    setError(null);
    api.timeEntries(week.from, week.to).then(setEntries, (err) => setError(errorText(err, t)));
  }, [week, t]);
  // Reload when the timer starts or stops, so the running entry shows.
  useEffect(load, [load, timer?.id]);

  const shift = (weeks: number) =>
    setWeek(weekOf(new Date(week.monday.getFullYear(), week.monday.getMonth(), week.monday.getDate() + 7 * weeks)));
  const thisWeek = weekOf(new Date()).from === week.from;

  async function remove(e: TimeEntry) {
    if (!window.confirm(t('timeLog.deleteConfirm', { title: e.noteTitle }))) return;
    try {
      await api.deleteTimeEntry(e.id);
      load();
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  const estimates = useMemo(() => new Map((recordings ?? []).map((r) => [r.id, r.estimate ?? 0])), [recordings]);
  const total = (entries ?? []).reduce((n, e) => n + e.seconds, 0);
  const perNote = useMemo(() => {
    const by = new Map<string, { noteId: string; title: string; number?: number; seconds: number }>();
    for (const e of entries ?? []) {
      const row = by.get(e.noteId) ?? { noteId: e.noteId, title: e.noteTitle, number: e.noteNumber, seconds: 0 };
      row.seconds += e.seconds;
      by.set(e.noteId, row);
    }
    return [...by.values()].sort((a, b) => b.seconds - a.seconds);
  }, [entries]);
  const days = useMemo(() => {
    const out = new Map<string, TimeEntry[]>();
    for (const e of entries ?? []) {
      const key = isoDate(new Date(e.start));
      out.set(key, [...(out.get(key) ?? []), e]);
    }
    return [...out.entries()];
  }, [entries]);

  const range = `${week.monday.toLocaleDateString(locale(), { month: 'short', day: 'numeric' })} – ${new Date(`${week.to}T12:00:00`).toLocaleDateString(locale(), { month: 'short', day: 'numeric', year: 'numeric' })}`;

  return (
    <div className="time-log">
      <div className="time-log-head">
        <h1>{t('timeLog.title')}</h1>
        <div className="time-week" role="group" aria-label={t('timeLog.week')}>
          <button type="button" className="pill-button" onClick={() => shift(-1)} aria-label={t('timeLog.previousWeek')} title={t('timeLog.previousWeek')}>
            ‹
          </button>
          <span className="time-week-range">{range}</span>
          <button type="button" className="pill-button" onClick={() => shift(1)} aria-label={t('timeLog.nextWeek')} title={t('timeLog.nextWeek')}>
            ›
          </button>
          {!thisWeek && (
            <button type="button" className="pill-button" onClick={() => setWeek(weekOf(new Date()))}>
              {t('timeLog.thisWeek')}
            </button>
          )}
        </div>
        <div className="head-actions">
          <button type="button" className="pill-button" onClick={() => setAdding(!adding)} aria-expanded={adding}>
            {t('timeLog.logTime')}
          </button>
          <a className="pill-button" href={api.timeExportURL(week.from, week.to)} download>
            <DownloadIcon /> <span>{t('timeLog.export')}</span>
          </a>
        </div>
      </div>

      {adding && (
        <ManualEntry
          day={thisWeek ? isoDate(new Date()) : week.from}
          onAdded={() => {
            setAdding(false);
            load();
          }}
        />
      )}
      {error && <p className="error">{error}</p>}
      {!entries && !error && <p className="muted">{t('common.loading')}</p>}
      {entries && entries.length === 0 && <p className="muted">{t('timeLog.empty')}</p>}

      {entries && entries.length > 0 && (
        <>
          <section className="card time-summary">
            <p className="time-total">
              {t('timeLog.total')} <strong>{formatSeconds(total)}</strong>
            </p>
            <table className="time-table">
              <thead>
                <tr>
                  <th>{t('timeLog.note')}</th>
                  <th className="num">{t('timeLog.spent')}</th>
                  <th className="num">{t('time.estimate')}</th>
                </tr>
              </thead>
              <tbody>
                {perNote.map((row) => {
                  const estimate = estimates.get(row.noteId) ?? 0;
                  return (
                    <tr key={row.noteId}>
                      <td>
                        <Link to={`/conversations/${row.noteId}`}>{row.title}</Link>
                        {row.number ? <span className="note-row-number">#{row.number}</span> : null}
                      </td>
                      <td className={`num${estimate && row.seconds > estimate * 60 ? ' over' : ''}`}>{formatSeconds(row.seconds)}</td>
                      <td className="num muted">{estimate ? formatMinutes(estimate) : '—'}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </section>

          {days.map(([day, list]) => (
            <div key={day} className="day-group">
              <h2 className="day-heading">
                {new Date(`${day}T12:00:00`).toLocaleDateString(locale(), { weekday: 'long', month: 'long', day: 'numeric' })}{' '}
                <span>{formatSeconds(list.reduce((n, e) => n + e.seconds, 0))}</span>
              </h2>
              <ul className="time-entries">
                {list.map((e) => (
                  <li key={e.id} className={e.end ? undefined : 'running'}>
                    <span className="time-entry-when">
                      {formatTime(new Date(e.start))}–{e.end ? formatTime(new Date(e.end)) : t('timeLog.running')}
                    </span>
                    <Link to={`/conversations/${e.noteId}`} className="time-entry-note">
                      {e.noteTitle}
                    </Link>
                    <span className="time-entry-length">
                      {e.until && <span className="state-pill">{t('timeLog.focus')}</span>}
                      {formatSeconds(e.seconds)}
                    </span>
                    {e.end && (
                      <button type="button" className="icon-button small danger" title={t('common.delete')} aria-label={t('timeLog.deleteLabel', { title: e.noteTitle })} onClick={() => void remove(e)}>
                        <TrashIcon />
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </>
      )}
    </div>
  );
}
