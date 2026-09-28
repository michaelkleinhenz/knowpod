import { CSSProperties, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { flatTree, notePath } from '../lib/folders';
import { title } from '../lib/recordings';
import { CheckIcon, FolderIcon, MoveIcon } from './Icons';

// MoveToFolder is the note toolbar's button that moves the note into another folder (or to
// the top level), or a sub-note out of its parent note. It works without dragging, e.g. on
// phones.
export function MoveToFolder({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const { folders, recordings } = useNotes();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!(e.target as Element).closest?.('.move-folder')) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false);
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  async function move(to: () => Promise<Recording>) {
    setBusy(true);
    setError(null);
    try {
      setRec(await to());
      setOpen(false);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  // A sub-note moves up one level: under its parent's parent, or into the folder its parent is in.
  const parents = notePath(rec, recordings);
  const parent = parents[parents.length - 1];
  const grandparent = parents[parents.length - 2];
  const moveOut = () =>
    move(() => (grandparent ? api.setNoteParent(rec.id, grandparent.id) : api.setNoteFolder(rec.id, parent?.folderId ?? '')));
  const current = parent ? null : (rec.folderId ?? '');
  // A note in a folder shared with the user moves only between the folders they can edit
  // there; other notes go into the user's own folders.
  const all = folders ?? [];
  const inShared = all.some((f) => f.id === rec.folderId && (f.access ?? 'owner') !== 'owner');
  const usable = all.filter((f) => (inShared ? f.access === 'editor' : (f.access ?? 'owner') === 'owner'));
  const options = [
    ...(inShared ? [] : [{ id: '', name: t('folders.topLevel'), depth: 0 }]),
    ...flatTree(usable).map(({ folder, depth }) => ({ id: folder.id, name: folder.name, depth: depth + 1 })),
  ];

  return (
    <div className="move-folder">
      <button type="button" className={`icon-button${open ? ' active' : ''}`} aria-expanded={open} title={t('folders.move')} aria-label={t('folders.move')} onClick={() => setOpen(!open)}>
        <MoveIcon />
      </button>
      {open && (
        <div className="label-popover move-popover" role="dialog" aria-label={t('folders.move')}>
          <ul className="label-options">
            {parent && (
              <li>
                <button type="button" className="label-option" disabled={busy} onClick={() => void moveOut()}>
                  <MoveIcon />
                  <span className="label-option-name">{t('subNotes.moveOut', { title: title(parent) })}</span>
                </button>
              </li>
            )}
            {options.map((o) => (
              <li key={o.id}>
                <button
                  type="button"
                  className="label-option"
                  aria-pressed={o.id === current}
                  disabled={busy}
                  style={{ paddingLeft: `${0.5 + o.depth * 0.9}rem` } as CSSProperties}
                  onClick={() => (o.id === current ? setOpen(false) : void move(() => api.setNoteFolder(rec.id, o.id)))}
                >
                  <FolderIcon />
                  <span className="label-option-name">{o.name}</span>
                  {o.id === current && <CheckIcon />}
                </button>
              </li>
            ))}
          </ul>
          {folders?.length === 0 && <p className="muted move-hint">{t('folders.noneYet')}</p>}
          {error && <p className="error">{error}</p>}
        </div>
      )}
    </div>
  );
}
