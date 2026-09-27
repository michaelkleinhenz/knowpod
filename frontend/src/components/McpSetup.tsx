import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, McpApp, McpSettings } from '../api/client';
import { errorText } from '../lib/errors';
import { formatDate } from '../lib/recordings';
import { CopyButton } from './CopyButton';

// McpSetup explains how to add knowpod's MCP server to AI assistants (Claude, ChatGPT). They
// sign in through OAuth: the user allows them on knowpod's consent page, and they are listed
// here to be disconnected. For assistants without OAuth, it makes a personal access token,
// shown once, when it is made; a new one replaces it.
export function McpSetup() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<McpSettings | null>(null);
  const [apps, setApps] = useState<McpApp[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.mcp().then(setSettings, (err) => setError(errorText(err, t)));
    api.mcpApps().then(setApps, (err) => setError(errorText(err, t)));
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

  async function disconnect(app: McpApp) {
    if (!window.confirm(t('mcp.disconnectConfirm', { name: app.name }))) return;
    setBusy(true);
    setError(null);
    try {
      await api.disconnectMcpApp(app.id);
      setApps((list) => list.filter((a) => a.id !== app.id));
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  if (!settings) return error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>;
  const url = window.location.origin + '/mcp';
  const token = settings.token ?? t('mcp.tokenPlaceholder');
  const header = `Bearer ${token}`;
  const command = `claude mcp add --transport http knowpod ${url}`;
  const tokenCommand = `${command} --header "Authorization: Bearer ${token}"`;
  return (
    <>
      <p className="muted">{t('mcp.intro')}</p>
      <dl className="facts">
        <dt>{t('mcp.url')}</dt>
        <dd className="with-action">
          <code>{url}</code> <CopyButton text={url} />
        </dd>
      </dl>
      <h3 className="card-subtitle">{t('mcp.claudeTitle')}</h3>
      <ol className="steps">
        <li>{t('mcp.claudeStep1')}</li>
        <li>{t('mcp.claudeStep2')}</li>
        <li>{t('mcp.claudeStep3')}</li>
      </ol>
      <p>
        {t('mcp.claudeCode')}{' '}
        <span className="with-action mcp-snippet">
          <code>{command}</code> <CopyButton text={command} />
        </span>
      </p>
      <h3 className="card-subtitle">{t('mcp.chatgptTitle')}</h3>
      <ol className="steps">
        <li>{t('mcp.chatgptStep1')}</li>
        <li>{t('mcp.chatgptStep2')}</li>
        <li>{t('mcp.chatgptStep3')}</li>
      </ol>

      <h3 className="card-subtitle">{t('mcp.appsTitle')}</h3>
      {apps.length === 0 ? (
        <p className="muted">{t('mcp.noApps')}</p>
      ) : (
        <ul className="mcp-apps">
          {apps.map((a) => (
            <li key={a.id}>
              <span>
                {a.name || t('mcp.unnamedApp')}{' '}
                <span className="muted">
                  · {t('mcp.connectedOn', { date: formatDate(a.createdAt) })}
                  {a.lastUsedAt && <> · {t('mcp.lastUsed', { date: formatDate(a.lastUsedAt) })}</>}
                </span>
              </span>
              <button type="button" className="small-button danger" disabled={busy} onClick={() => void disconnect(a)}>
                {t('mcp.disconnect')}
              </button>
            </li>
          ))}
        </ul>
      )}

      <h3 className="card-subtitle">{t('mcp.tokenTitle')}</h3>
      <p className="muted">{t('mcp.tokenIntro')}</p>
      {settings.token && (
        <dl className="facts">
          <dt>{t('mcp.token')}</dt>
          <dd className="with-action">
            <code>{settings.token}</code> <CopyButton text={settings.token} />
          </dd>
        </dl>
      )}
      {settings.token && <p className="notice">{t('mcp.tokenOnce')}</p>}
      {settings.enabled && !settings.token && <p>{t('mcp.enabledSince', { date: formatDate(settings.createdAt) })}</p>}
      {settings.enabled && (
        <>
          <p>
            {t('mcp.tokenHeader')}{' '}
            <span className="with-action mcp-snippet">
              <code>Authorization: {header}</code> {settings.token && <CopyButton text={header} />}
            </span>
          </p>
          <p>
            {t('mcp.tokenClaudeCode')}{' '}
            <span className="with-action mcp-snippet">
              <code>{tokenCommand}</code> {settings.token && <CopyButton text={tokenCommand} />}
            </span>
          </p>
        </>
      )}
      {error && <p className="error">{error}</p>}
      <div className="button-row">
        <button type="button" className="secondary-button" disabled={busy} onClick={() => void enable()}>
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
