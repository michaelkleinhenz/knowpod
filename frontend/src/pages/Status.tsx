import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, Health, Info, OpenAPIOperation, OpenAPISpec, PocketSettings } from '../api/client';
import { CopyButton } from '../components/CopyButton';
import { inline, Markdown } from '../components/Markdown';

const METHODS = ['get', 'post', 'put', 'patch', 'delete'];

const AUTH_LABELS: Record<string, string> = {
  deviceToken: 'Device token',
  sessionCookie: 'Web UI sign-in',
  adminToken: 'Admin token',
};

interface Endpoint {
  method: string;
  path: string;
  op: OpenAPIOperation;
}

// authLabel describes who may call an operation, from its OpenAPI security requirements.
function authLabel(op: OpenAPIOperation): string {
  if (!op.security || op.security.length === 0) return 'Public';
  return op.security.map((req) => Object.keys(req).map((k) => AUTH_LABELS[k] ?? k).join(' + ')).join(' or ');
}

function ConfigState({ ok }: { ok: boolean }) {
  return ok ? <span className="status-pill ok">Set</span> : <span className="status-pill bad">Not set</span>;
}

// PocketCard shows the signed-in user's Pocket webhook URL and its state; the secrets are
// set on the Account page.
function PocketCard({ pocket, origin }: { pocket: PocketSettings | null; origin: string }) {
  if (!pocket) return null;
  const url = origin + pocket.webhookPath;
  return (
    <section className="card">
      <h2 className="card-title">Your Pocket integration</h2>
      <dl className="facts">
        <dt>Webhook URL</dt>
        <dd className="with-action">
          <code>{url}</code> <CopyButton text={url} />
        </dd>
        <dt>Signing secret</dt>
        <dd>
          <ConfigState ok={pocket.webhookSecretConfigured} />
        </dd>
        <dt>API key</dt>
        <dd>
          <ConfigState ok={pocket.apiKeyConfigured} />
        </dd>
      </dl>
      <p className="muted field-note">
        Set the secret and API key on the <Link to="/account">Account</Link> page.
      </p>
    </section>
  );
}

function EndpointRow({ ep, base }: { ep: Endpoint; base: string }) {
  const { op } = ep;
  const params = op.parameters ?? [];
  const bodyTypes = Object.keys(op.requestBody?.content ?? {});
  const responses = Object.entries(op.responses ?? {});
  return (
    <details className="endpoint">
      <summary>
        <span className={`method method-${ep.method}`}>{ep.method.toUpperCase()}</span>
        <code className="endpoint-path">{base + ep.path}</code>
        <span className="endpoint-summary">{op.summary}</span>
        <span className="auth-badge">{authLabel(op)}</span>
      </summary>
      <div className="endpoint-body">
        {op.description && <Markdown text={op.description} />}
        {params.length > 0 && (
          <>
            <h4>Parameters</h4>
            <ul>
              {params.map((p) => (
                <li key={p.in + p.name}>
                  <code>{p.name}</code> <span className="muted">({p.in}{p.required ? ', required' : ''})</span>
                  {p.description && <> — {inline(p.description)}</>}
                </li>
              ))}
            </ul>
          </>
        )}
        {bodyTypes.length > 0 && (
          <>
            <h4>Request body</h4>
            <p>
              {bodyTypes.map((t) => (
                <code key={t}>{t}</code>
              ))}
            </p>
          </>
        )}
        {responses.length > 0 && (
          <>
            <h4>Responses</h4>
            <ul>
              {responses.map(([code, r]) => (
                <li key={code}>
                  <code>{code}</code> {r.description && inline(r.description.split('\n')[0])}
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </details>
  );
}

export function Status() {
  const [health, setHealth] = useState<Health | null>(null);
  const [info, setInfo] = useState<Info | null>(null);
  const [spec, setSpec] = useState<OpenAPISpec | null>(null);
  const [pocket, setPocket] = useState<PocketSettings | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.health().then(setHealth, () => setHealth({ status: 'unreachable', service: '' }));
    api.info().then(setInfo, () => undefined);
    api.openapi().then(setSpec, (e: Error) => setError(e.message));
    api.pocket().then(setPocket, () => undefined);
  }, []);

  const origin = window.location.origin;
  const base = `${origin}/api/v1`;

  // Group operations by their first tag, in the order the spec declares the tags.
  const groups = new Map<string, Endpoint[]>();
  for (const t of spec?.tags ?? []) groups.set(t.name, []);
  for (const [path, item] of Object.entries(spec?.paths ?? {})) {
    for (const method of METHODS) {
      const op = item[method] as OpenAPIOperation | undefined;
      if (!op) continue;
      const tag = op.tags?.[0] ?? 'Other';
      if (!groups.has(tag)) groups.set(tag, []);
      groups.get(tag)!.push({ method, path, op });
    }
  }
  const tagInfo = new Map((spec?.tags ?? []).map((t) => [t.name, t.description]));

  return (
    <>
      <section className="card">
        <h1>Status</h1>
        <dl className="facts">
          <dt>Service</dt>
          <dd>
            {health ? (
              <span className={`status-pill ${health.status === 'ok' ? 'ok' : 'bad'}`}>
                {health.status === 'ok' ? 'Operational' : health.status === 'unavailable' ? 'Database unavailable' : 'Unreachable'}
              </span>
            ) : (
              <span className="muted">Checking…</span>
            )}
          </dd>
          <dt>API version</dt>
          <dd>{info?.apiVersion ?? '…'}{spec && <span className="muted"> (spec {spec.info.version})</span>}</dd>
          <dt>Base URL</dt>
          <dd className="with-action">
            <code>{base}</code> <CopyButton text={base} />
          </dd>
          <dt>Health check</dt>
          <dd>
            <code>{origin}/healthz</code>
          </dd>
          <dt>API description</dt>
          <dd>
            <a href="/api/v1/openapi.yaml" target="_blank" rel="noreferrer">
              openapi.yaml
            </a>{' '}
            ·{' '}
            <a href="/api/v1/openapi.json" target="_blank" rel="noreferrer">
              openapi.json
            </a>{' '}
            <span className="muted">(OpenAPI 3, e.g. for Postman or code generators)</span>
          </dd>
        </dl>
      </section>

      <PocketCard pocket={pocket} origin={origin} />

      {spec?.info.description && (
        <section className="card">
          <h2 className="card-title">How it works</h2>
          <div className="prose">
            <Markdown text={spec.info.description} />
          </div>
        </section>
      )}

      <section className="card">
        <h2 className="card-title">API reference</h2>
        {error && <p className="error">Could not load the API description: {error}</p>}
        {!spec && !error && <p className="muted">Loading…</p>}
        {[...groups.entries()]
          .filter(([, eps]) => eps.length > 0)
          .map(([tag, eps]) => (
            <div key={tag} className="endpoint-group">
              <h3>{tag}</h3>
              {tagInfo.get(tag) && <p className="muted">{tagInfo.get(tag)}</p>}
              {eps.map((ep) => (
                <EndpointRow key={ep.method + ep.path} ep={ep} base="/api/v1" />
              ))}
            </div>
          ))}
      </section>
    </>
  );
}
