import { FormEvent, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api, Recording, ShareRole, Sharing } from '../api/client';
import { useAuth } from '../auth';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { ShareIcon } from './Icons';

// ShareNote is the note toolbar's button that shows who the note is shared with. The owner
// shares it (and everything under it) by email, changes what each person may do, and stops
// sharing it; someone it is shared with sees who else has it and can leave it.
export function ShareNote({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { account: user } = useAuth();
  const notes = useNotes();
  const [open, setOpen] = useState(false);
  const [sharing, setSharing] = useState<Sharing | null>(null);
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<ShareRole>('editor');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const owner = (rec.access ?? 'owner') === 'owner';

  useEffect(() => {
    if (!open) return;
    setError(null);
    api.sharing(rec.id).then(setSharing, (err) => setError(errorText(err, t)));
    const onDown = (e: MouseEvent) => {
      if (!(e.target as Element).closest?.('.share-note')) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false);
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open, rec.id, t]);

  // change runs a change of the sharing; the note and the notes under it are shared
  // differently afterwards, so the list is loaded again.
  async function change(action: () => Promise<Sharing | null>) {
    setBusy(true);
    setError(null);
    try {
      const next = await action();
      if (!next) {
        // Left the note: it is gone for this user.
        setOpen(false);
        notes.remove(rec.id);
        void notes.reload();
        navigate('/');
        return;
      }
      setSharing(next);
      setRec(await api.recording(rec.id));
      void notes.reload();
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
      const out = await api.share(rec.id, to, role);
      setEmail('');
      return out;
    });
  }

  const me = sharing?.members.find((m) => m.userId === user?.id);
  const label = rec.shared ? t('sharing.sharedTitle') : t('sharing.title');

  return (
    <div className="share-note">
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
      {open && (
        <div className="label-popover share-popover" role="dialog" aria-label={t('sharing.title')}>
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
                    {m.inherited && <span className="share-via muted">{t('sharing.inherited')}</span>}
                  </span>
                  {owner && !m.inherited ? (
                    <>
                      <select
                        className="share-role"
                        aria-label={t('sharing.roleFor', { email: m.email })}
                        value={m.role}
                        disabled={busy}
                        onChange={(e) => void change(() => api.setShareRole(rec.id, m.userId, e.target.value as ShareRole))}
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
                        onClick={() => void change(() => api.unshare(rec.id, m.userId))}
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
          {sharing && sharing.members.length === 0 && <p className="muted share-hint">{t('sharing.none')}</p>}
          {owner ? (
            rec.type === 'board' ? (
              <p className="muted share-hint">{t('sharing.noBoards')}</p>
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
                <p className="muted share-hint">{t('sharing.explain')}</p>
              </form>
            )
          ) : (
            me &&
            (me.inherited ? (
              <p className="muted share-hint">{t('sharing.inheritedHint')}</p>
            ) : (
              <div className="share-form">
                <button
                  type="button"
                  className="pill-button danger"
                  disabled={busy}
                  onClick={() => window.confirm(t('sharing.leaveConfirm')) && void change(() => api.unshare(rec.id, me.userId))}
                >
                  {t('sharing.leave')}
                </button>
              </div>
            ))
          )}
          {error && <p className="error">{error}</p>}
        </div>
      )}
    </div>
  );
}
