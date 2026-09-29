import { ChangeEvent, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, BackupSummary } from '../api/client';
import { errorText } from '../lib/errors';
import { CopyButton } from './CopyButton';

// BackupSettings lets administrators download a full backup and restore one.
export function BackupSettings() {
  const { t } = useTranslation();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<BackupSummary | null>(null);
  const url = `${window.location.origin}${api.backupURL}`;
  const command = `curl -H "Authorization: Bearer $ADMIN_TOKEN" -OJ ${url}`;

  async function restore(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file || !window.confirm(t('backup.restoreConfirm', { name: file.name }))) return;
    setBusy(true);
    setError(null);
    setDone(null);
    try {
      setDone(await api.restoreBackup(file));
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="card">
        <h2 className="card-title">{t('backup.download.title')}</h2>
        <p className="muted">{t('backup.download.hint')}</p>
        <p>
          <a className="secondary-button" href={api.backupURL} download>
            {t('backup.download.button')}
          </a>
        </p>
        <p className="muted">{t('backup.download.script')}</p>
        <p>
          <code>{command}</code> <CopyButton text={command} />
        </p>
      </section>
      <section className="card">
        <h2 className="card-title">{t('backup.restore.title')}</h2>
        <p className="muted">{t('backup.restore.hint')}</p>
        <input ref={input} type="file" accept=".zip,application/zip" hidden onChange={restore} />
        <button type="button" className="secondary-button danger" disabled={busy} onClick={() => input.current?.click()}>
          {busy ? t('backup.restore.running') : t('backup.restore.button')}
        </button>
        {error && <p className="error">{error}</p>}
        {done && (
          <p className="success">
            {t('backup.restore.done', {
              notes: done.collections.recordings ?? 0,
              users: done.collections.users ?? 0,
              files: done.objects,
            })}
          </p>
        )}
      </section>
    </>
  );
}
