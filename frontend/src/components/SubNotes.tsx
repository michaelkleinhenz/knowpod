import { ReactNode, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { subNotes, withSubNotes } from '../lib/folders';
import { formatDate, when } from '../lib/recordings';
import { setOpen, useOpen } from '../lib/treeOpen';
import { NoteRow } from './NoteRow';

// SubNotes lists the sub-notes of the open note below it, like the files of a folder whose
// head is the note itself; new ones are added with the + on the note's row in the sidebar.
// Sub-notes with sub-notes of their own open and close like in the sidebar (and together
// with it).
export function SubNotes({ rec }: { rec: Recording }) {
  const { t } = useTranslation();
  const { recordings, aiReady, upsert } = useNotes();
  const [error, setError] = useState<string | null>(null);
  const open = useOpen();
  const subs = useMemo(() => subNotes(recordings ?? []), [recordings]);
  const kids = subs.get(rec.id) ?? [];
  // The notes below this one that open and close; the section opens or closes them all.
  const nested = kids.flatMap((k) => withSubNotes(k.id, subs));
  const allOpen = nested.length > 0 && nested.every((id) => open.has(id));

  async function setDone(r: Recording, done: boolean) {
    setError(null);
    upsert({ ...r, done });
    try {
      upsert(await api.setNoteDone(r.id, done));
    } catch (err) {
      upsert(r);
      setError(errorText(err, t));
    }
  }

  const rows = (list: Recording[], depth: number): ReactNode =>
    list.map((r) => {
      const below = depth < 16 ? (subs.get(r.id) ?? []) : [];
      const isOpen = open.has(r.id);
      return (
        <NoteRow
          key={r.id}
          rec={r}
          active={false}
          aiReady={aiReady}
          meta={formatDate(when(r))}
          onSetDone={(r, d) => void setDone(r, d)}
          sub={
            below.length > 0
              ? { count: below.length, open: isOpen, onToggle: (e) => setOpen(e.altKey ? withSubNotes(r.id, subs) : [r.id], !isOpen) }
              : undefined
          }
        >
          {below.length > 0 && isOpen && <ul className="tree-children sub-note-list">{rows(below, depth + 1)}</ul>}
        </NoteRow>
      );
    });

  return (
    <section className="sub-notes" aria-label={t('subNotes.title')}>
      <div className="sub-notes-head">
        <h2>
          {t('subNotes.title')} {kids.length > 0 && <span className="tree-count">{kids.length}</span>}
        </h2>
        <div className="head-actions">
          {nested.length > 0 && (
            <button type="button" className="link-button" aria-expanded={allOpen} onClick={() => setOpen(nested, !allOpen)}>
              {t(allOpen ? 'subNotes.collapseAll' : 'subNotes.expandAll')}
            </button>
          )}
        </div>
      </div>
      {error && <p className="error">{error}</p>}
      {kids.length > 0 ? (
        <ul className="conversation-list">
          {rows(kids, 0)}
        </ul>
      ) : (
        <p className="muted sub-notes-empty">{t('subNotes.empty')}</p>
      )}
    </section>
  );
}
