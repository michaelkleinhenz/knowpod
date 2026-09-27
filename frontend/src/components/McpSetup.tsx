import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, McpSettings } from '../api/client';
import { errorText } from '../lib/errors';
import { formatDate } from '../lib/recordings';
import { CopyButton } from './CopyButton';

// McpSetup turns on the MCP server for the user: it makes the access token AI assistants
// (Claude, ChatGPT) sign in with, and explains how to add the server to them. The token is
// shown once, when it is made; a new one replaces it.
export function McpSetup() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<McpSettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.mcp().then(setSettings, (err) => setError(errorText(err, t)));
  }, [t]);

  async function act(fn: () => Promise<McpSettings>, confirmText?: string) {
    if (confirmText && !window.confirm(confirmText)) return;
    setBusy(true);
    setError(null);
    try {
      setSettings(await fn());
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }
  const enable = () => act(api.enableMcp, settings?.enabled ? t('mcp.resetConfirm') : undefined);
  const disable = () =>
    act(async () => {
      await api.disableMcp();
      return { enabled: false };
    }, t('mcp.disableConfirm'));

  if (!settings) return error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>;
  const url = window.location.origin + '/mcp';
  const token = settings.token ?? t('mcp.tokenPlaceholder');
  const header = `Bearer ${token}`;
  const command = `claude mcp add --transport http knowpod ${url} --header "Authorization: Bearer ${token}"`;
  return (
    <>
      <p className="muted">{t('mcp.intro')}</p>
      {settings.enabled && (
        <dl className="facts">
          <dt>{t('mcp.url')}</dt>
          <dd className="with-action">
            <code>{url}</code> <CopyButton text={url} />
          </dd>
          {settings.token && (
            <>
              <dt>{t('mcp.token')}</dt>
              <dd className="with-action">
                <code>{settings.token}</code> <CopyButton text={settings.token} />
              </dd>
            </>
          )}
        </dl>
      )}
      {settings.token && <p className="notice">{t('mcp.tokenOnce')}</p>}
      {settings.enabled && !settings.token && <p>{t('mcp.enabledSince', { date: formatDate(settings.createdAt) })}</p>}
      {settings.enabled && (
        <>
          <h3 className="card-subtitle">{t('mcp.claudeTitle')}</h3>
          <ol className="steps">
            <li>{t('mcp.claudeStep1')}</li>
            <li>{t('mcp.claudeStep2')}</li>
            <li>
              {t('mcp.claudeStep3')}{' '}
              <span className="with-action mcp-snippet">
                <code>Authorization: {header}</code> {settings.token && <CopyButton text={header} />}
              </span>
            </li>
          </ol>
          <p>
            {t('mcp.claudeCode')}{' '}
            <span className="with-action mcp-snippet">
              <code>{command}</code> {settings.token && <CopyButton text={command} />}
            </span>
          </p>
          <h3 className="card-subtitle">{t('mcp.chatgptTitle')}</h3>
          <ol className="steps">
            <li>{t('mcp.chatgptStep1')}</li>
            <li>{t('mcp.chatgptStep2')}</li>
            <li>{t('mcp.chatgptStep3')}</li>
          </ol>
        </>
      )}
      {error && <p className="error">{error}</p>}
      <div className="button-row">
        <button type="button" className="primary-button" disabled={busy} onClick={() => void enable()}>
          {settings.enabled ? t('mcp.reset') : t('mcp.enable')}
        </button>
        {settings.enabled && (
          <button type="button" className="secondary-button danger" disabled={busy} onClick={() => void disable()}>
            {t('mcp.disable')}
          </button>
        )}
      </div>
    </>
  );
}
