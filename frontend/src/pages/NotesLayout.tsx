import { useTranslation } from 'react-i18next';
import { Outlet, useParams } from 'react-router-dom';
import { NotesList } from '../components/NotesList';
import { NotesProvider } from '../context/NotesContext';

// NotesLayout shows the notes list as a sidebar next to the open note on desktop. On narrow
// screens only one of them is visible: the list at "/", the note when one is open (CSS).
export function NotesLayout() {
  const { id } = useParams();
  return (
    <NotesProvider>
      <div className={`notes-layout${id ? ' has-note' : ''}`}>
        <aside className="notes-sidebar">
          <NotesList activeId={id} />
        </aside>
        <div className="notes-main">
          <Outlet />
        </div>
      </div>
    </NotesProvider>
  );
}

// NotesHome fills the main area on desktop while no note is open.
export function NotesHome() {
  const { t } = useTranslation();
  return (
    <div className="notes-empty">
      <p className="muted">{t('conversations.selectHint')}</p>
    </div>
  );
}
