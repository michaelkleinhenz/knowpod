import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { api, NoteVersion, Recording } from '../api/client';
import { errorText } from '../lib/errors';
import { formatDate } from '../lib/recordings';
import { HistoryIcon } from './Icons';
import { Markdown } from './Markdown';

const when = (iso: string) => formatDate(iso, { dateStyle: 'medium', timeStyle: 'short' });

// VersionHistory is the note toolbar's button that shows the earlier versions of the note's
// title and text; editors restore one (onRestore), which keeps the current text as a version.
export function VersionHistory({ rec, canRestore, onRestore }: { rec: Recording; canRestore: boolean; onRestore: (versionId: string) => Promise<void> }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const label = t('history.button');
  return (
    <>
      <button type="button" className={`icon-button${open ? ' active' : ''}`} title={label} aria-label={label} onClick={() => setOpen(true)}>
        <HistoryIcon />
      </button>
      {open &&
        createPortal(
          <HistoryDialog
            rec={rec}
            canRestore={canRestore}
            onRestore={async (id) => {
              await onRestore(id);
              setOpen(false);
            }}
            onClose={() => setOpen(false)}
          />,
          document.body,
        )}
    </>
  );
}

function HistoryDialog({ rec, canRestore, onRestore, onClose }: { rec: Recording; canRestore: boolean; onRestore: (versionId: string) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation();
  const [versions, setVersions] = useState<NoteVersion[] | null>(null);
  const [chosen, setChosen] = useState<NoteVersion | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Answers for a version chosen before are ignored.
  const chosenId = useRef('');
  const close = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    close.current?.focus();
    api.versions(rec.id).then(
      (list) => {
        setVersions(list);
        if (list.length) void choose(list[0]);
      },
      (err) => setError(errorText(err, t)),
    );
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // Loaded once when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rec.id]);

  async function choose(v: NoteVersion) {
    chosenId.current = v.id;
    setChosen(v);
    setError(null);
    try {
      const full = await api.version(rec.id, v.id);
      if (chosenId.current === v.id) setChosen(full);
    } catch (err) {
      if (chosenId.current === v.id) setError(errorText(err, t));
    }
  }

  async function restore() {
    if (!chosen || !window.confirm(t('history.restoreConfirm'))) return;
    setBusy(true);
    setError(null);
    try {
      await onRestore(chosen.id);
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="history-dialog" role="dialog" aria-modal="true" aria-label={t('history.title')}>
        <div className="history-head">
          <h2>{t('history.title')}</h2>
          <button ref={close} type="button" className="pill-button" onClick={onClose}>
            {t('history.close')}
          </button>
        </div>
        <p className="muted field-note">{t('history.intro')}</p>
        {error && <p className="error">{error}</p>}
        {!versions && !error && <p className="muted">{t('common.loading')}</p>}
        {versions && versions.length === 0 && <p className="muted">{t('history.none')}</p>}
        {versions && versions.length > 0 && (
          <div className="history-body">
            <ul className="history-list" role="listbox" aria-label={t('history.title')}>
              {versions.map((v) => (
                <li key={v.id}>
                  <button type="button" role="option" aria-selected={chosen?.id === v.id} className={chosen?.id === v.id ? 'active' : ''} onClick={() => void choose(v)}>
                    <span className="history-when">{when(v.savedAt)}</span>
                    <span className="history-reason muted">{t(`history.reasons.${v.reason}`)}</span>
                  </button>
                </li>
              ))}
            </ul>
            <div className="history-preview">
              {chosen ? (
                <>
                  <p className="muted history-meta">{t('history.savedAt', { when: when(chosen.savedAt) })}</p>
                  <h3 className="history-title">{chosen.title}</h3>
                  <div className="prose">{chosen.markdown === undefined ? <p className="muted">{t('common.loading')}</p> : <Markdown text={chosen.markdown} />}</div>
                  {canRestore && (
                    <p className="history-actions">
                      <button type="button" className="pill-button primary" disabled={busy || chosen.markdown === undefined} onClick={() => void restore()}>
                        {t('history.restore')}
                      </button>
                    </p>
                  )}
                </>
              ) : (
                <p className="muted">{t('history.choose')}</p>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
