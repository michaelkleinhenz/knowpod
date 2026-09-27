import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router-dom';
import { api, OAuthAuthorization, OAuthRequest } from '../api/client';
import { useAuth } from '../auth';
import { errorText } from '../lib/errors';

// The parameters of an OAuth authorization request that are passed on to the server.
const PARAMS = ['client_id', 'redirect_uri', 'response_type', 'code_challenge', 'code_challenge_method', 'scope', 'state', 'resource'];

// OAuthAuthorize is the consent page AI assistants (Claude, ChatGPT, …) send the user to when
// they connect to knowpod's MCP server through OAuth: the signed-in user allows or declines
// the assistant's access, and the browser goes back to the assistant.
export function OAuthAuthorize() {
  const { t } = useTranslation();
  const { account } = useAuth();
  const { search } = useLocation();
  const params = useMemo(() => {
    const q = new URLSearchParams(search);
    const out: OAuthRequest = {};
    for (const k of PARAMS) {
      const v = q.get(k);
      if (v !== null) out[k] = v;
    }
    return out;
  }, [search]);
  const [info, setInfo] = useState<OAuthAuthorization | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.oauthAuthorization(params).then(
      (v) => {
        if (v.redirectTo) window.location.replace(v.redirectTo);
        else setInfo(v);
      },
      (err) => setError(errorText(err, t)),
    );
  }, [params, t]);

  async function decide(approve: boolean) {
    setBusy(true);
    setError(null);
    try {
      const { redirectTo } = await api.oauthDecide(params, approve);
      window.location.replace(redirectTo);
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  let host = '';
  try {
    host = info?.redirectUri ? new URL(info.redirectUri).host : '';
  } catch {
    // A custom scheme without a host: shown as is below.
  }
  return (
    <section className="card narrow">
      <h1>{t('oauth.title')}</h1>
      {!info && !error && <p className="muted">{t('common.loading')}</p>}
      {info && (
        <>
          <p>{t('oauth.asks', { name: info.clientName })}</p>
          <ul className="steps">
            <li>{t('oauth.read')}</li>
            <li>{t('oauth.write')}</li>
          </ul>
          <p className="muted">{t('oauth.account', { email: account?.email })}</p>
          <p className="muted">{t('oauth.returnTo', { target: host || info.redirectUri })}</p>
          <p className="muted">{t('oauth.revoke')}</p>
        </>
      )}
      {error && <p className="error">{error}</p>}
      {info && (
        <div className="button-row">
          <button type="button" className="primary-button" disabled={busy} onClick={() => void decide(true)}>
            {t('oauth.allow')}
          </button>
          <button type="button" className="secondary-button" disabled={busy} onClick={() => void decide(false)}>
            {t('oauth.deny')}
          </button>
        </div>
      )}
    </section>
  );
}
