import { FormEvent, useEffect, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { api, RemarkableSettings } from '../api/client';
import { errorText } from '../lib/errors';
import { formatDate } from '../lib/recordings';

// RemarkableSetup pairs the signed-in user's reMarkable cloud account with a one-time code and
// shows what the last pull found. Imported documents go into the knowpod folder "reMarkable". Documents are only read, never
// changed on the tablet.
export function RemarkableSetup() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<RemarkableSettings | null>(null);
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'pair' | 'pull' | 'unpair' | 'ignore' | null>(null);
  // The ignored names as edited, one per line; null while unchanged.
  const [ignored, setIgnored] = useState<string | null>(null);
  const [ignoredSaved, setIgnoredSaved] = useState(false);

  useEffect(() => {
    api.remarkable().then(setSettings, (e) => setError(errorText(e, t)));
  }, [t]);

  async function run(kind: 'pair' | 'pull' | 'unpair' | 'ignore', action: () => Promise<void>) {
    setBusy(kind);
    setError(null);
    try {
      await action();
    } catch (err) {
      setError(errorText(err, t));
      // A failed pull is recorded on the link; show it.
      api.remarkable().then(setSettings, () => undefined);
    } finally {
      setBusy(null);
    }
  }

  // Pairing pulls right away, so the documents show up without another click.
  function handlePair(e: FormEvent) {
    e.preventDefault();
    run('pair', async () => {
      setSettings(await api.pairRemarkable(code));
      setCode('');
      setBusy('pull');
      setSettings(await api.pullRemarkable());
    });
  }

  // The ignored names are saved as a list; empty lines are dropped.
  function handleIgnored(e: FormEvent) {
    e.preventDefault();
    run('ignore', async () => {
      setSettings(await api.setRemarkableIgnoredNames((ignored ?? '').split('\n')));
      setIgnored(null);
      setIgnoredSaved(true);
    });
  }

  if (!settings) return error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>;
  const folder = <strong>{settings.folder}</strong>;
  const result = settings.lastResult;

  if (!settings.paired) {
    return (
      <>
        <p className="muted">
          <Trans i18nKey="remarkable.intro" values={{ folder: settings.folder }} components={{ 1: folder }} />
        </p>
        <p>
          <span className="status-pill bad">{t('remarkable.notPaired')}</span>
        </p>
        <ol className="steps">
          <li>
            <Trans
              i18nKey="remarkable.step1"
              values={{ url: settings.connectUrl.replace(/^https:\/\//, '') }}
              components={{ 1: <a href={settings.connectUrl} target="_blank" rel="noreferrer" /> }}
            />
          </li>
          <li>{t('remarkable.step2')}</li>
        </ol>
        <form onSubmit={handlePair} className="form">
          <label>
            {t('remarkable.code')}
            <input
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              inputMode="text"
              maxLength={12}
              placeholder="abcdefgh"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </label>
          {error && <p className="error">{error}</p>}
          <button type="submit" disabled={!!busy || code.replace(/\s/g, '').length !== 8}>
            {busy === 'pair' ? t('remarkable.pairing') : busy === 'pull' ? t('remarkable.pulling') : t('remarkable.pair')}
          </button>
        </form>
      </>
    );
  }

  return (
    <>
      <p className="muted">
        <Trans i18nKey="remarkable.introPaired" values={{ folder: settings.folder }} components={{ 1: folder }} />
      </p>
      <p>
        <span className="status-pill ok">{t('remarkable.paired')}</span>{' '}
        {settings.pairedAt && <span className="muted">{t('remarkable.pairedSince', { date: formatDate(settings.pairedAt) })}</span>}
      </p>
      <div className="stack">
        <dl className="facts">
          <dt>{t('remarkable.lastPull')}</dt>
          <dd>{settings.lastPullAt ? formatDate(settings.lastPullAt, { dateStyle: 'medium', timeStyle: 'short' }) : t('remarkable.never')}</dd>
          {result && !settings.lastError && (
            <>
              <dt>{t('remarkable.found')}</dt>
              <dd>
                {t('remarkable.result', { count: result.documents, imported: result.imported, updated: result.updated })}
                {!!result.ignored && ` · ${t('remarkable.ignoredCount', { count: result.ignored })}`}
              </dd>
            </>
          )}
        </dl>
        {settings.lastError && !error && (
          <div className="notice bad">
            <p>{t('remarkable.lastError', { error: settings.lastError })}</p>
          </div>
        )}
        {error && <p className="error">{error}</p>}
        <div className="button-row">
          <button type="button" className="primary-button" disabled={!!busy} onClick={() => run('pull', async () => setSettings(await api.pullRemarkable()))}>
            {busy === 'pull' ? t('remarkable.pulling') : t('remarkable.pull')}
          </button>
          <button
            type="button"
            className="secondary-button danger"
            disabled={!!busy}
            onClick={() =>
              window.confirm(t('remarkable.unpairConfirm')) &&
              run('unpair', async () => {
                await api.unpairRemarkable();
                setSettings(await api.remarkable());
              })
            }
          >
            {t('remarkable.unpair')}
          </button>
        </div>
      </div>
      <form onSubmit={handleIgnored} className="form settings-subform">
        <label>
          {t('remarkable.ignored')}
          <span className="muted field-note">{t('remarkable.ignoredHint')}</span>
          <textarea
            rows={4}
            spellCheck={false}
            placeholder={t('remarkable.ignoredPlaceholder')}
            value={ignored ?? settings.ignoredNames.join('\n')}
            onChange={(e) => {
              setIgnored(e.target.value);
              setIgnoredSaved(false);
            }}
          />
        </label>
        <div className="button-row">
          <button type="submit" className="secondary-button" disabled={!!busy || ignored === null}>
            {busy === 'ignore' ? t('common.saving') : t('common.save')}
          </button>
          {ignoredSaved && <span className="muted">{t('common.saved')}</span>}
        </div>
      </form>
      <p className="muted settings-footnote">{t('remarkable.footnote')}</p>
    </>
  );
}
