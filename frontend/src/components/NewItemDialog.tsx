import { FormEvent, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { errorText } from '../lib/errors';

// NewItemDialog asks for the title of a new note or task, creates it at the top level and
// opens it.
export function NewItemDialog({ kind, onClose }: { kind: 'note' | 'task'; onClose: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [title, setTitle] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => input.current?.focus(), []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const value = title.trim();
    if (!value || busy) return;
    setBusy(true);
    setError(null);
    try {
      const rec = await api.createTextNote(value, '', undefined, kind === 'task' ? { task: true } : undefined);
      onClose();
      navigate(`/conversations/${rec.id}`);
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  const heading = t(kind === 'task' ? 'nav.newTask' : 'nav.newNote');
  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form className="new-item-dialog" role="dialog" aria-modal="true" aria-label={heading} onSubmit={submit}>
        <h2>{heading}</h2>
        <input
          ref={input}
          type="text"
          value={title}
          placeholder={t('nav.newTitle')}
          aria-label={t('nav.newTitle')}
          onChange={(e) => setTitle(e.target.value)}
        />
        {error && <p className="error">{error}</p>}
        <div className="new-item-actions">
          <button type="button" className="pill-button" onClick={onClose}>
            {t('nav.cancel')}
          </button>
          <button type="submit" className="pill-button primary" disabled={busy || !title.trim()}>
            {t('nav.create')}
          </button>
        </div>
      </form>
    </div>
  );
}
