import { ChangeEvent, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, BackupSummary } from '../api/client';
import { errorText } from '../lib/errors';

// PersonalBackup lets users download a backup of their own notes and content and restore one.
export function PersonalBackup() {
  const { t } = useTranslation();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<BackupSummary | null>(null);

  async function restore(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file || !window.confirm(t('personalBackup.restoreConfirm', { name: file.name }))) return;
    setBusy(true);
    setError(null);
    setDone(null);
    try {
      setDone(await api.restorePersonalBackup(file));
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="card">
        <h2 className="card-title">{t('personalBackup.download.title')}</h2>
        <p className="muted">{t('personalBackup.download.hint')}</p>
        <p>
          <a className="secondary-button" href={api.personalBackupURL} download>
            {t('personalBackup.download.button')}
          </a>
        </p>
      </section>
      <section className="card">
        <h2 className="card-title">{t('personalBackup.restore.title')}</h2>
        <p className="muted">{t('personalBackup.restore.hint')}</p>
        <input ref={input} type="file" accept=".zip,application/zip" hidden onChange={restore} />
        <button type="button" className="secondary-button danger" disabled={busy} onClick={() => input.current?.click()}>
          {busy ? t('personalBackup.restore.running') : t('personalBackup.restore.button')}
        </button>
        {error && <p className="error">{error}</p>}
        {done && (
          <p className="success">
            {t('personalBackup.restore.done', { notes: done.collections.recordings ?? 0, files: done.objects })}{' '}
            <button type="button" className="link-button" onClick={() => window.location.reload()}>
              {t('personalBackup.restore.reload')}
            </button>
          </p>
        )}
      </section>
    </>
  );
}
