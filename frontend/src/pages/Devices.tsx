import { FormEvent, useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, Device } from '../api/client';
import { CopyButton } from '../components/CopyButton';

function formatDate(iso?: string): string {
  if (!iso) return '—';
  return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

// Devices lists the registered recorders and lets the user add and remove them. Tokens are
// only available right after they are issued, so they are kept in page state and shown in
// the device's row until the user leaves the page.
export function Devices() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [tokens, setTokens] = useState<Record<string, string>>({});
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null); // "add" or the device ID being changed

  const load = useCallback(async () => {
    try {
      const all = await api.devices();
      setDevices(all.filter((d) => !d.revokedAt));
    } catch (err) {
      setError((err as Error).message);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function handleAdd(e: FormEvent) {
    e.preventDefault();
    setBusy('add');
    setError(null);
    try {
      const { device, token } = await api.createDevice(name.trim());
      setTokens((t) => ({ ...t, [device.id]: token }));
      setName('');
      await load();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(null);
    }
  }

  async function handleRotate(d: Device) {
    if (!window.confirm(`Issue a new token for "${d.name}"? The current token stops working immediately.`)) return;
    setBusy(d.id);
    setError(null);
    try {
      const { token } = await api.rotateDeviceToken(d.id);
      setTokens((t) => ({ ...t, [d.id]: token }));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(null);
    }
  }

  async function handleRemove(d: Device) {
    if (!window.confirm(`Remove "${d.name}"? Its token stops working. Recordings already uploaded are kept.`)) return;
    setBusy(d.id);
    setError(null);
    try {
      await api.removeDevice(d.id);
      setTokens(({ [d.id]: _removed, ...rest }) => rest);
      await load();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(null);
    }
  }

  const uploadsURL = `${window.location.origin}/api/v1/uploads`;

  return (
    <>
      <section className="card">
        <h1>Devices</h1>
        <p className="muted">
          Recorders that may upload audio. Each device authenticates with its own token, sent as{' '}
          <code>Authorization: Bearer &lt;token&gt;</code> to <code>{uploadsURL}</code>. See{' '}
          <Link to="/status">Status</Link> for the full API.
        </p>

        <form onSubmit={handleAdd} className="inline-form">
          <label className="sr-only" htmlFor="device-name">
            Device name
          </label>
          <input
            id="device-name"
            placeholder="Device name, e.g. Kitchen recorder"
            required
            maxLength={100}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <button type="submit" disabled={busy === 'add' || !name.trim()}>
            {busy === 'add' ? 'Adding…' : 'Add device'}
          </button>
        </form>
        {error && <p className="error">{error}</p>}
      </section>

      <section className="card">
        {!devices && !error && <p className="muted">Loading…</p>}
        {devices && devices.length === 0 && <p className="muted">No devices yet. Add one above to get an upload token.</p>}
        {devices && devices.length > 0 && (
          <ul className="device-list">
            {devices.map((d) => {
              const token = tokens[d.id];
              return (
                <li key={d.id} className={token ? 'device new-token' : 'device'}>
                  <div className="device-head">
                    <div>
                      <div className="device-name">{d.name}</div>
                      <div className="device-meta muted">
                        Added {formatDate(d.createdAt)} · Last seen {formatDate(d.lastSeenAt)}
                      </div>
                    </div>
                    <div className="device-actions">
                      <button type="button" className="small-button" disabled={busy === d.id} onClick={() => handleRotate(d)}>
                        New token
                      </button>
                      <button
                        type="button"
                        className="small-button danger"
                        disabled={busy === d.id}
                        onClick={() => handleRemove(d)}
                      >
                        Remove
                      </button>
                    </div>
                  </div>
                  <div className="device-token">
                    <span className="token-label">API token</span>
                    {token ? (
                      <>
                        <code className="token">{token}</code>
                        <CopyButton text={token} />
                        <p className="token-note">Copy this token now and configure it on the device. It won't be shown again.</p>
                      </>
                    ) : (
                      <span className="muted">Hidden. Tokens are shown only once; use “New token” if it was lost.</span>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </>
  );
}
