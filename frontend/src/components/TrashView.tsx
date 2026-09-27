import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Recording, TRASH_DAYS } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import type { Matcher } from '../lib/filterQuery';
import { daysLeft } from '../lib/trash';
import { EmptyTrashIcon } from './Icons';
import { NoteRow } from './NoteRow';

interface Props {
  // search is the search or filter the list is narrowed by; null when there is none.
  search: Matcher | null;
  activeId?: string;
  aiReady: boolean;
  onSetDone: (r: Recording, done: boolean) => void;
}

// TrashView lists the notes deleted in the last TRASH_DAYS days, with how long each stays;
// while searching, only the ones found. The sidebar shows it instead of the notes when its
// trash button is on.
export function TrashView({ search, activeId, aiReady, onSetDone }: Props) {
  const { t } = useTranslation();
  const { trash, emptyTrash } = useNotes();
  const [error, setError] = useState<string | null>(null);
  const trashed = (trash ?? []).filter((r) => !search || search(r));

  async function clear() {
    if (!window.confirm(t('trash.emptyConfirm', { count: trash?.length ?? 0 }))) return;
    setError(null);
    try {
      await emptyTrash();
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  return (
    <div className="trash-view">
      <div className="trash-view-head">
        <h2 className="day-heading">
          {t('trash.title')} <span>{trashed.length || ''}</span>
        </h2>
        {!!trash?.length && !search && (
          <button type="button" className="icon-button danger" title={t('trash.empty')} aria-label={t('trash.empty')} onClick={() => void clear()}>
            <EmptyTrashIcon />
          </button>
        )}
      </div>
      {error && <p className="error">{error}</p>}
      {trashed.length === 0 && <p className="muted tree-hint">{search ? t('trash.noMatch') : t('trash.hint', { days: TRASH_DAYS })}</p>}
      <ul className="conversation-list">
        {trashed.map((r) => (
          <NoteRow
            key={r.id}
            rec={r}
            active={r.id === activeId}
            aiReady={aiReady}
            meta={t('trash.daysLeft', { count: daysLeft(r.deletedAt ?? '') })}
            onSetDone={onSetDone}
            taskDate={false}
          />
        ))}
      </ul>
      {trashed.length > 0 && <p className="muted tree-hint">{t('trash.hint', { days: TRASH_DAYS })}</p>}
    </div>
  );
}
