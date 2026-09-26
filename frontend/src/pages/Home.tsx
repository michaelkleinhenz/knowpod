import { useEffect, useState } from 'react';
import { api, Info } from '../api/client';

export function Home() {
  const [info, setInfo] = useState<Info | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.info().then(setInfo, (e: Error) => setError(e.message));
  }, []);

  return (
    <section className="card">
      <h1>knowpod</h1>
      {error && <p className="error">Backend unreachable: {error}</p>}
      {info && (
        <p>
          Backend connected (API {info.apiVersion}).
        </p>
      )}
      {!info && !error && <p>…</p>}
    </section>
  );
}
