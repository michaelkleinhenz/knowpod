import { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { errorText } from '../lib/errors';
import { builtInTemplates, fillTemplate, loadTemplate, ownTemplates } from '../lib/templates';

// NewItemDialog asks for the title of a new note or task, and optionally a template whose
// title and text it starts with; it creates the item at the top level and opens it.
export function NewItemDialog({ kind, onClose }: { kind: 'note' | 'task'; onClose: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [title, setTitle] = useState('');
  const [templateId, setTemplateId] = useState('');
  const [notes, setNotes] = useState<Recording[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => input.current?.focus(), []);

  // The user's own templates are among their notes.
  useEffect(() => {
    api.recordings().then(setNotes, () => setNotes([]));
  }, []);
  const builtIn = useMemo(() => builtInTemplates(t), [t]);
  const own = useMemo(() => ownTemplates(notes), [notes]);
  const template = [...own, ...builtIn].find((tpl) => tpl.id === templateId);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const typed = title.trim();
    if ((!typed && !template) || busy) return;
    setBusy(true);
    setError(null);
    try {
      const now = new Date();
      const name = typed || fillTemplate(template!.title, { now }).trim() || t('conversations.untitled');
      const markdown = template ? fillTemplate(await loadTemplate(template), { title: name, now }) : '';
      const rec = await api.createTextNote(name, markdown, undefined, kind === 'task' ? { task: true } : undefined);
      onClose();
      navigate(`/conversations/${rec.id}`);
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  const heading = t(kind === 'task' ? 'nav.newTask' : 'nav.newNote');
  const titleLabel = template ? t('templates.titleOptional') : t('nav.newTitle');
  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form className="new-item-dialog" role="dialog" aria-modal="true" aria-label={heading} onSubmit={submit}>
        <h2>{heading}</h2>
        <input ref={input} type="text" value={title} placeholder={titleLabel} aria-label={titleLabel} onChange={(e) => setTitle(e.target.value)} />
        <label className="new-item-template">
          <span>{t('templates.label')}</span>
          <select value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
            <option value="">{t('templates.blank')}</option>
            {own.length > 0 && (
              <optgroup label={t('templates.ownGroup')}>
                {own.map((tpl) => (
                  <option key={tpl.id} value={tpl.id}>
                    {tpl.name}
                  </option>
                ))}
              </optgroup>
            )}
            <optgroup label={t('templates.builtInGroup')}>
              {builtIn.map((tpl) => (
                <option key={tpl.id} value={tpl.id}>
                  {tpl.name}
                </option>
              ))}
            </optgroup>
          </select>
        </label>
        {error && <p className="error">{error}</p>}
        <div className="new-item-actions">
          <button type="button" className="pill-button" onClick={onClose}>
            {t('nav.cancel')}
          </button>
          <button type="submit" className="pill-button primary" disabled={busy || (!title.trim() && !template)}>
            {t('nav.create')}
          </button>
        </div>
      </form>
    </div>
  );
}
