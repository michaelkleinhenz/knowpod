import { CSSProperties, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { flatTree } from '../lib/folders';
import { CheckIcon, FolderIcon, MoveIcon } from './Icons';

// MoveToFolder is the note toolbar's button that moves the note into another folder (or to
// the top level). It works without dragging, e.g. on phones.
export function MoveToFolder({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const { folders } = useNotes();
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

  async function move(folderId: string) {
    setBusy(true);
    setError(null);
    try {
      setRec(await api.setNoteFolder(rec.id, folderId));
      setOpen(false);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  const current = rec.folderId ?? '';
  const options = [{ id: '', name: t('folders.topLevel'), depth: 0 }, ...flatTree(folders ?? []).map(({ folder, depth }) => ({ id: folder.id, name: folder.name, depth: depth + 1 }))];

  return (
    <div className="move-folder">
      <button type="button" className={`icon-button${open ? ' active' : ''}`} aria-expanded={open} title={t('folders.move')} aria-label={t('folders.move')} onClick={() => setOpen(!open)}>
        <MoveIcon />
      </button>
      {open && (
        <div className="label-popover move-popover" role="dialog" aria-label={t('folders.move')}>
          <ul className="label-options">
            {options.map((o) => (
              <li key={o.id}>
                <button
                  type="button"
                  className="label-option"
                  aria-pressed={o.id === current}
                  disabled={busy}
                  style={{ paddingLeft: `${0.5 + o.depth * 0.9}rem` } as CSSProperties}
                  onClick={() => (o.id === current ? setOpen(false) : void move(o.id))}
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
