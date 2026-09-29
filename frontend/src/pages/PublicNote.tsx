import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { api, PublicNote as Note } from '../api/client';
import { Markdown } from '../components/Markdown';
import { formatDate } from '../lib/recordings';

// PublicNote shows a published note to anyone with its link, without signing in: its title
// and text, read-only, outside the app's frame.
export function PublicNote() {
  const { t } = useTranslation();
  const { token = '' } = useParams();
  const [note, setNote] = useState<Note | null>(null);
  const [missing, setMissing] = useState(false);

  useEffect(() => {
    let stale = false;
    api.publicNote(token).then(
      (n) => !stale && setNote(n),
      () => !stale && setMissing(true),
    );
    return () => {
      stale = true;
    };
  }, [token]);

  useEffect(() => {
    if (note) document.title = `${note.title} · knowpod`;
  }, [note]);

  return (
    <div className="public-page">
      <main className="public-note">
        {note ? (
          <article>
            <h1>{note.title}</h1>
            <p className="muted public-meta">
              {formatDate(note.date, { dateStyle: 'long' })}
              {note.updatedAt && note.updatedAt !== note.date && (
                <> · {t('publicPage.updated', { when: formatDate(note.updatedAt, { dateStyle: 'medium', timeStyle: 'short' }) })}</>
              )}
            </p>
            <div className="prose">
              <Markdown text={note.markdown} />
            </div>
          </article>
        ) : missing ? (
          <p className="muted">{t('publicPage.notFound')}</p>
        ) : (
          <p className="muted">{t('publicPage.loading')}</p>
        )}
      </main>
      <footer className="public-footer">
        <img src="/favicon.svg" alt="" width="18" height="18" />
        {t('publicPage.footer')}
      </footer>
    </div>
  );
}
