import { FormEvent, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { api, Label, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { isTask, LABEL_COLORS, labelName, labelStyle, noteLabels } from '../lib/labels';
import { CheckIcon, TagIcon } from './Icons';

export function LabelChip({ label, onRemove }: { label: Label; onRemove?: () => void }) {
  const { t } = useTranslation();
  const name = labelName(label);
  return (
    <span className="label-chip" style={labelStyle(label)}>
      <span className="label-dot" aria-hidden="true" />
      {name}
      {onRemove && (
        <button type="button" className="label-remove" onClick={onRemove} aria-label={t('labels.remove', { name })} title={t('labels.remove', { name })}>
          ×
        </button>
      )}
    </span>
  );
}

// ColorPicker offers the label palette and any other color.
export function ColorPicker({ value, onChange }: { value: string; onChange: (c: string) => void }) {
  const { t } = useTranslation();
  return (
    <div className="color-picker" role="radiogroup" aria-label={t('labels.color')}>
      {LABEL_COLORS.map((c) => (
        <button
          key={c}
          type="button"
          role="radio"
          aria-checked={value === c}
          aria-label={c}
          className={`color-swatch${value === c ? ' selected' : ''}`}
          style={{ background: c }}
          onClick={() => onChange(c)}
        />
      ))}
      <input type="color" className="color-custom" value={value} onChange={(e) => onChange(e.target.value)} aria-label={t('labels.customColor')} title={t('labels.customColor')} />
    </div>
  );
}

// LabelPicker is a popover that puts labels on a note or takes them off, and creates new
// labels on the spot.
function LabelPicker({ rec, onToggle, onClose }: { rec: Recording; onToggle: (id: string) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation();
  const { labels, reloadLabels } = useNotes();
  const [name, setName] = useState('');
  const [color, setColor] = useState(LABEL_COLORS[1]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      // Clicks on the popover and its toggle button are handled there.
      if (!(e.target as Element).closest?.('.label-add')) onClose();
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  const create = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      const l = await api.createLabel({ name, color });
      await reloadLabels();
      await onToggle(l.id);
      setName('');
    });
  };

  return (
    <div className="label-popover" role="dialog" aria-label={t('labels.title')}>
      <ul className="label-options">
        {(labels ?? []).map((l) => {
          const on = rec.labels?.includes(l.id) ?? false;
          return (
            <li key={l.id}>
              <button type="button" className="label-option" aria-pressed={on} disabled={busy} onClick={() => run(() => onToggle(l.id))} style={labelStyle(l)}>
                <span className="label-dot" aria-hidden="true" />
                <span className="label-option-name">{labelName(l)}</span>
                {on && <CheckIcon />}
              </button>
            </li>
          );
        })}
      </ul>
      <form className="label-new" onSubmit={create}>
        <input maxLength={40} placeholder={t('labels.newPlaceholder')} value={name} onChange={(e) => setName(e.target.value)} aria-label={t('labels.newName')} />
        <ColorPicker value={color} onChange={setColor} />
        <button type="submit" className="small-button" disabled={busy || !name.trim()}>
          {t('labels.create')}
        </button>
      </form>
      {error && <p className="error">{error}</p>}
      <Link to="/settings?tab=labels" className="label-manage">
        {t('labels.manage')}
      </Link>
    </div>
  );
}

// NoteLabels shows a note's labels in its header, a check box when it is a task, and the
// button that opens the label picker.
export function NoteLabels({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const { labels } = useNotes();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async (ids: string[]) => setRec(await api.setNoteLabels(rec.id, ids));
  const toggle = (id: string) => {
    const ids = rec.labels ?? [];
    return save(ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]);
  };
  const act = async (fn: () => Promise<void>) => {
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(errorText(err, t));
    }
  };

  return (
    <div className="note-labels">
      {isTask(rec) && (
        <label className={`task-toggle${rec.done ? ' done' : ''}`}>
          <input type="checkbox" checked={!!rec.done} onChange={(e) => act(async () => setRec(await api.setNoteDone(rec.id, e.target.checked)))} />
          {rec.done ? t('labels.done') : t('labels.open')}
        </label>
      )}
      {noteLabels(rec, labels).map((l) => (
        <LabelChip key={l.id} label={l} onRemove={() => act(() => toggle(l.id))} />
      ))}
      <div className="label-add">
        <button type="button" className="label-add-button" aria-expanded={open} onClick={() => setOpen(!open)}>
          <TagIcon /> {rec.labels?.length ? t('labels.edit') : t('labels.add')}
        </button>
        {open && <LabelPicker rec={rec} onToggle={toggle} onClose={() => setOpen(false)} />}
      </div>
      {error && <p className="error">{error}</p>}
    </div>
  );
}
