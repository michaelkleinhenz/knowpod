import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api } from '../api/client';
import { BackIcon } from '../components/Icons';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { noteByNumber } from '../lib/noteRefs';

// NoteByNumber opens the note with the number in the URL (/n/12), which "#12" in a note's
// text links to: from the loaded list, or else looked up on the server.
export function NoteByNumber() {
  const { t } = useTranslation();
  const { number = '' } = useParams();
  const navigate = useNavigate();
  const { recordings } = useNotes();
  const [error, setError] = useState<string | null>(null);
  const n = Number(number);

  useEffect(() => {
    if (!Number.isInteger(n) || n <= 0) {
      setError(t('noteRefs.notFound', { number }));
      return;
    }
    const known = noteByNumber(recordings, n);
    if (known) {
      navigate(`/conversations/${known.id}`, { replace: true });
      return;
    }
    if (!recordings) return; // wait for the list first
    let cancelled = false;
    api
      .recordingByNumber(n)
      .then((list) => {
        if (cancelled) return;
        if (list.length > 0) navigate(`/conversations/${list[0].id}`, { replace: true });
        else setError(t('noteRefs.notFound', { number: n }));
      })
      .catch((err) => !cancelled && setError(errorText(err, t)));
    return () => {
      cancelled = true;
    };
  }, [n, number, recordings, navigate, t]);

  return (
    <section className="conversation">
      <Link to="/" className="back-link">
        <BackIcon />
        <span>{t('conversation.back')}</span>
      </Link>
      {error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>}
    </section>
  );
}
