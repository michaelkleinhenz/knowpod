import { CSSProperties, FormEvent, RefObject, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api, Folder, Recording, ShareRole, Sharing } from '../api/client';
import { useAuth } from '../auth';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { ShareIcon } from './Icons';

// What a SharePanel shares: a note or a folder, through the API calls for it.
interface ShareTarget {
  kind: 'note' | 'folder';
  // owner says the user owns it: they share it; others see who has it and can leave.
  owner: boolean;
  load: () => Promise<Sharing>;
  share: (email: string, role: ShareRole) => Promise<Sharing>;
  setRole: (userId: string, role: ShareRole) => Promise<Sharing>;
  unshare: (userId: string) => Promise<Sharing | null>;
  // changed runs after the sharing changed (the notes are shared differently); left after
  // the user left it.
  changed: () => Promise<void> | void;
  left: () => void;
  // blocked, when set, says why it can't be shared (instead of the form).
  blocked?: string;
}

// SharePanel shows who a note or folder is shared with. The owner shares it (and everything
// under or in it) by email, changes what each person may do, and stops sharing it; someone
// it is shared with sees who else has it and can leave it.
function SharePanel({ target, className, style }: { target: ShareTarget; className?: string; style?: CSSProperties }) {
  const { t } = useTranslation();
  const { account: user } = useAuth();
  const [sharing, setSharing] = useState<Sharing | null>(null);
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<ShareRole>('editor');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const k = target.kind;

  useEffect(() => {
    setError(null);
    target.load().then(setSharing, (err) => setError(errorText(err, t)));
    // Loaded once when the panel opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t]);

  async function change(action: () => Promise<Sharing | null>) {
    setBusy(true);
    setError(null);
    try {
      const next = await action();
      if (!next) {
        target.left();
        return;
      }
      setSharing(next);
      await target.changed();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const to = email.trim();
    if (!to) return;
    void change(async () => {
      const out = await target.share(to, role);
      setEmail('');
      return out;
    });
  }

  const me = sharing?.members.find((m) => m.userId === user?.id);
  return (
    <div className={`label-popover share-popover${className ? ` ${className}` : ''}`} style={style} role="dialog" aria-label={t('sharing.title')}>
      <h3 className="share-heading">{t('sharing.heading')}</h3>
      {!sharing && !error && <p className="muted share-hint">{t('common.loading')}</p>}
      {sharing && (
        <ul className="share-list">
          <li className="share-person">
            <span className="share-email" title={sharing.owner.email}>
              {sharing.owner.email}
              {sharing.owner.userId === user?.id && <span className="muted"> {t('sharing.you')}</span>}
            </span>
            <span className="share-role muted">{t('sharing.roles.owner')}</span>
          </li>
          {sharing.members.map((m) => (
            <li key={m.userId} className="share-person">
              <span className="share-email" title={m.email}>
                {m.email || m.userId}
                {m.userId === user?.id && <span className="muted"> {t('sharing.you')}</span>}
                {m.inherited && <span className="share-via muted">{t(`sharing.${k}.inherited`)}</span>}
              </span>
              {target.owner && !m.inherited ? (
                <>
                  <select
                    className="share-role"
                    aria-label={t('sharing.roleFor', { email: m.email })}
                    value={m.role}
                    disabled={busy}
                    onChange={(e) => void change(() => target.setRole(m.userId, e.target.value as ShareRole))}
                  >
                    <option value="editor">{t('sharing.roles.editor')}</option>
                    <option value="viewer">{t('sharing.roles.viewer')}</option>
                  </select>
                  <button
                    type="button"
                    className="share-remove"
                    disabled={busy}
                    title={t('sharing.remove', { email: m.email })}
                    aria-label={t('sharing.remove', { email: m.email })}
                    onClick={() => void change(() => target.unshare(m.userId))}
                  >
                    ×
                  </button>
                </>
              ) : (
                <span className="share-role muted">{t(`sharing.roles.${m.role}`)}</span>
              )}
            </li>
          ))}
        </ul>
      )}
      {sharing && sharing.members.length === 0 && <p className="muted share-hint">{t(`sharing.${k}.none`)}</p>}
      {target.owner ? (
        target.blocked ? (
          <p className="muted share-hint">{target.blocked}</p>
        ) : (
          <form className="share-form" onSubmit={submit}>
            <input
              type="email"
              placeholder={t('sharing.email')}
              aria-label={t('sharing.email')}
              value={email}
              disabled={busy}
              onChange={(e) => setEmail(e.target.value)}
            />
            <select aria-label={t('sharing.role')} value={role} disabled={busy} onChange={(e) => setRole(e.target.value as ShareRole)}>
              <option value="editor">{t('sharing.roles.editor')}</option>
              <option value="viewer">{t('sharing.roles.viewer')}</option>
            </select>
            <button type="submit" className="pill-button" disabled={busy || !email.trim()}>
              {t('sharing.share')}
            </button>
            <p className="muted share-hint">{t(`sharing.${k}.explain`)}</p>
          </form>
        )
      ) : (
        me &&
        (me.inherited ? (
          <p className="muted share-hint">{t(`sharing.${k}.inheritedHint`)}</p>
        ) : (
          <div className="share-form">
            <button
              type="button"
              className="pill-button danger"
              disabled={busy}
              onClick={() => window.confirm(t(`sharing.${k}.leaveConfirm`)) && void change(() => target.unshare(me.userId))}
            >
              {t('sharing.leave')}
            </button>
          </div>
        ))
      )}
      {error && <p className="error">{error}</p>}
    </div>
  );
}

// useDismiss closes a popover on a click outside of it (and its button) or on Escape.
function useDismiss(open: boolean, close: () => void, inside: RefObject<HTMLElement | null>[]) {
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!inside.some((r) => r.current?.contains(e.target as Node))) close();
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close();
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
    // inside holds refs, which stay the same.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, close]);
}

// ShareNote is the note toolbar's button that shows who the note is shared with.
export function ShareNote({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const notes = useNotes();
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), [box]);
  const id = rec.id;
  const label = rec.shared ? t('sharing.sharedTitle') : t('sharing.title');

  const target: ShareTarget = {
    kind: 'note',
    owner: (rec.access ?? 'owner') === 'owner',
    load: () => api.sharing(id),
    share: (email, role) => api.share(id, email, role),
    setRole: (userId, role) => api.setShareRole(id, userId, role),
    unshare: (userId) => api.unshare(id, userId),
    // The note and the notes under it are shared differently now.
    changed: async () => {
      setRec(await api.recording(id));
      void notes.reload();
    },
    left: () => {
      // Left the note: it is gone for this user.
      setOpen(false);
      notes.remove(id);
      void notes.reload();
      navigate('/');
    },
    blocked: rec.type === 'board' ? t('sharing.noBoards') : undefined,
  };

  return (
    <div className="share-note" ref={box}>
      <button
        type="button"
        className={`icon-button${open ? ' active' : ''}${rec.shared ? ' shared' : ''}`}
        aria-expanded={open}
        title={label}
        aria-label={label}
        onClick={() => setOpen(!open)}
      >
        <ShareIcon />
      </button>
      {open && <SharePanel target={target} />}
    </div>
  );
}

// ShareFolder is a folder row's button that shows who the folder is shared with. Its panel
// floats over the page (the sidebar's list would cut it off), below the button and within
// the window.
export function ShareFolder({ folder: f }: { folder: Folder }) {
  const { t } = useTranslation();
  const notes = useNotes();
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<CSSProperties>({});
  const button = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), [button, panel]);
  const id = f.id;

  useLayoutEffect(() => {
    if (!open || !button.current) return;
    const place = () => {
      const r = button.current!.getBoundingClientRect();
      const width = Math.min(340, window.innerWidth - 32);
      setPos({ position: 'fixed', top: r.bottom + 6, left: Math.max(16, Math.min(r.left, window.innerWidth - width - 16)), right: 'auto', width });
    };
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [open]);

  const target: ShareTarget = {
    kind: 'folder',
    owner: (f.access ?? 'owner') === 'owner',
    load: () => api.folderSharing(id),
    share: (email, role) => api.shareFolder(id, email, role),
    setRole: (userId, role) => api.setFolderShareRole(id, userId, role),
    unshare: (userId) => api.unshareFolder(id, userId),
    // The notes in the folder are shared differently now.
    changed: async () => {
      await Promise.all([notes.reload(), notes.reloadFolders()]);
    },
    left: () => {
      setOpen(false);
      void notes.reload();
      void notes.reloadFolders();
    },
  };
  const label = f.shared ? t('sharing.sharedTitle') : t('sharing.folder.title');

  return (
    <>
      <button
        ref={button}
        type="button"
        className={`icon-button${open ? ' active' : ''}${f.shared ? ' shared' : ''}`}
        aria-expanded={open}
        title={label}
        aria-label={t('sharing.folder.label', { name: f.name })}
        onClick={() => setOpen(!open)}
      >
        <ShareIcon size={16} />
      </button>
      {open &&
        createPortal(
          <div ref={panel}>
            <SharePanel target={target} className="share-popover-floating" style={pos} />
          </div>,
          document.body,
        )}
    </>
  );
}
