import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { subNotes } from '../lib/folders';
import { formatDate, when } from '../lib/recordings';
import { NewNoteIcon } from './Icons';
import { NoteRow } from './NoteRow';

// SubNotes lists the sub-notes of the open note below it, like the files of a folder whose
// head is the note itself, and creates new ones.
export function SubNotes({ rec }: { rec: Recording }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { recordings, aiReady, upsert } = useNotes();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const kids = useMemo(() => subNotes(recordings ?? []).get(rec.id) ?? [], [recordings, rec.id]);

  // create makes an empty text note under this one and opens it, ready to type its title.
  async function create() {
    setBusy(true);
    setError(null);
    try {
      const sub = await api.createTextNote(t('conversations.untitled'), '', rec.id);
      upsert(sub);
      navigate(`/conversations/${sub.id}`, { state: { created: true } });
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

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

  return (
    <section className="sub-notes" aria-label={t('subNotes.title')}>
      <div className="sub-notes-head">
        <h2>
          {t('subNotes.title')} {kids.length > 0 && <span className="tree-count">{kids.length}</span>}
        </h2>
        <button type="button" className="pill-button" onClick={() => void create()} disabled={busy}>
          <NewNoteIcon /> <span>{t('subNotes.new')}</span>
        </button>
      </div>
      {error && <p className="error">{error}</p>}
      {kids.length > 0 ? (
        <ul className="conversation-list">
          {kids.map((r) => (
            <NoteRow key={r.id} rec={r} active={false} aiReady={aiReady} meta={formatDate(when(r))} onSetDone={(r, d) => void setDone(r, d)} />
          ))}
        </ul>
      ) : (
        <p className="muted sub-notes-empty">{t('subNotes.empty')}</p>
      )}
    </section>
  );
}
