import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { NoteRow } from '../components/NoteRow';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { isTask } from '../lib/labels';
import { dayKey, dayLabel, formatTime } from '../lib/recordings';

// DoneTasks lists the checked-off tasks, which the workspace leaves out, by the day they were
// done (newest first). Unchecking one puts it back.
export function DoneTasks() {
  const { t } = useTranslation();
  const { recordings, aiReady, upsert } = useNotes();
  const [error, setError] = useState<string | null>(null);

  const groups = useMemo(() => {
    const done = (recordings ?? []).filter((r) => isTask(r) && r.done);
    const at = (r: Recording) => (r.doneAt ? new Date(r.doneAt).getTime() : 0);
    done.sort((a, b) => at(b) - at(a));
    const out: { key: string; day?: Date; items: Recording[] }[] = [];
    for (const r of done) {
      const day = r.doneAt ? new Date(r.doneAt) : undefined;
      const key = day ? dayKey(day) : 'unknown';
      if (out.length === 0 || out[out.length - 1].key !== key) out.push({ key, day, items: [] });
      out[out.length - 1].items.push(r);
    }
    return out;
  }, [recordings]);

  // reopen unchecks a task right away and stores it; a failure puts the check mark back.
  async function reopen(r: Recording, done: boolean) {
    setError(null);
    upsert({ ...r, done });
    try {
      upsert(await api.setNoteDone(r.id, done));
    } catch (err) {
      upsert(r);
      setError(errorText(err, t));
    }
  }

  return (
    <div className="time-log done-tasks">
      <div className="time-log-head">
        <h1>{t('doneTasks.title')}</h1>
      </div>
      <p className="muted">{t('doneTasks.intro')}</p>
      {error && <p className="error">{error}</p>}
      {!recordings && <p className="muted">{t('common.loading')}</p>}
      {recordings && groups.length === 0 && <p className="muted">{t('doneTasks.empty')}</p>}
      {groups.map((g) => {
        const { label, date } = g.day ? dayLabel(g.day) : { label: t('doneTasks.unknownDay'), date: '' };
        return (
          <div key={g.key} className="day-group">
            <h2 className="day-heading">
              {label} <span>{date}</span>
            </h2>
            <ul className="conversation-list">
              {g.items.map((r) => (
                <NoteRow key={r.id} rec={r} active={false} aiReady={aiReady} meta={r.doneAt ? formatTime(new Date(r.doneAt)) : ''} onSetDone={(n, d) => void reopen(n, d)} />
              ))}
            </ul>
          </div>
        );
      })}
    </div>
  );
}
