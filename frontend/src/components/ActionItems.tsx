import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ActionItem, api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { formatDue } from '../lib/tasks';
import { CalendarIcon, CheckIcon } from './Icons';

// ActionItems lists the follow-ups the AI found in the conversation, below the summary.
// Each can be turned into a task (a sub-note labeled as a task, due on the item's date) or
// dismissed; items already made into tasks link to them.
export function ActionItems({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const { recordings, upsert } = useNotes();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showDismissed, setShowDismissed] = useState(false);
  const items = rec.summary?.actionItems ?? [];
  if (items.length === 0) return null;

  // A task deleted since is offered again.
  const taskOf = (it: ActionItem) => (it.taskId ? recordings?.find((r) => r.id === it.taskId) : undefined);
  const open = items.filter((it) => !it.dismissed && !taskOf(it));
  const dismissed = items.filter((it) => it.dismissed && !taskOf(it));

  async function run(key: string, fn: () => Promise<void>) {
    setBusy(key);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(null);
    }
  }

  const create = async (it: ActionItem) => {
    const { task, note } = await api.createActionItemTask(rec.id, it.id);
    upsert(task);
    setRec(note);
  };
  const createAll = () =>
    run('all', async () => {
      for (const it of open) await create(it);
    });

  const row = (it: ActionItem) => {
    const task = taskOf(it);
    return (
      <li key={it.id} className={`action-item${task ? ' made' : ''}${it.dismissed ? ' dismissed' : ''}`}>
        <div className="action-item-text">
          <span>{it.text}</span>
          <span className="action-item-meta">
            {it.owner && <span className="action-owner">{it.owner}</span>}
            {it.due && (
              <span className="due-chip small">
                <CalendarIcon size={12} />
                {formatDue({ date: it.due })}
              </span>
            )}
          </span>
        </div>
        <div className="action-item-actions">
          {task ? (
            <Link to={`/conversations/${task.id}`} className={`action-task-link${task.done ? ' done' : ''}`}>
              <CheckIcon /> {task.done ? t('actionItems.taskDone') : t('actionItems.taskMade')}
              {task.number ? ` #${task.number}` : ''}
            </Link>
          ) : it.dismissed ? (
            <button type="button" className="link-button" disabled={!!busy} onClick={() => run(it.id, async () => setRec(await api.dismissActionItem(rec.id, it.id, false)))}>
              {t('actionItems.restore')}
            </button>
          ) : (
            <>
              <button type="button" className="small-button" disabled={!!busy} onClick={() => run(it.id, () => create(it))}>
                {busy === it.id ? t('common.saving') : t('actionItems.makeTask')}
              </button>
              <button
                type="button"
                className="action-dismiss"
                disabled={!!busy}
                title={t('actionItems.dismiss')}
                aria-label={t('actionItems.dismissLabel', { text: it.text })}
                onClick={() => run(it.id, async () => setRec(await api.dismissActionItem(rec.id, it.id, true)))}
              >
                ×
              </button>
            </>
          )}
        </div>
      </li>
    );
  };

  return (
    <section className="action-items" aria-label={t('actionItems.title')}>
      <div className="action-items-head">
        <h2>{t('actionItems.title')}</h2>
        {open.length > 1 && (
          <button type="button" className="link-button" disabled={!!busy} onClick={createAll}>
            {busy === 'all' ? t('common.saving') : t('actionItems.makeAll', { count: open.length })}
          </button>
        )}
      </div>
      <p className="muted field-note">{t('actionItems.hint')}</p>
      {error && <p className="error">{error}</p>}
      <ul>{items.filter((it) => !it.dismissed || !!taskOf(it)).map(row)}</ul>
      {dismissed.length > 0 && (
        <>
          <button type="button" className="link-button action-show-dismissed" aria-expanded={showDismissed} onClick={() => setShowDismissed(!showDismissed)}>
            {t(showDismissed ? 'actionItems.hideDismissed' : 'actionItems.showDismissed', { count: dismissed.length })}
          </button>
          {showDismissed && <ul>{dismissed.map(row)}</ul>}
        </>
      )}
    </section>
  );
}
