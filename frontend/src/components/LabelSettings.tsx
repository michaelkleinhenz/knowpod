import { FormEvent, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Label, LabelInput } from '../api/client';
import { errorText } from '../lib/errors';
import { LABEL_COLORS } from '../lib/labels';
import { ColorPicker, LabelChip } from './Labels';

// LabelForm creates or edits a label.
function LabelForm(props: { initial: LabelInput; submitLabel: string; onSubmit: (l: LabelInput) => Promise<void>; onCancel?: () => void }) {
  const { t } = useTranslation();
  const [value, setValue] = useState<LabelInput>(props.initial);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await props.onSubmit(value);
      setValue(props.initial);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="form label-form" onSubmit={handleSubmit}>
      <label>
        {t('labels.name')}
        <input required maxLength={40} value={value.name} onChange={(e) => setValue({ ...value, name: e.target.value })} />
      </label>
      <div className="label-form-color">
        <span>{t('labels.color')}</span>
        <ColorPicker value={value.color} onChange={(color) => setValue({ ...value, color })} />
      </div>
      {error && <p className="error">{error}</p>}
      <div className="button-row">
        <button type="submit" disabled={busy}>
          {busy ? t('common.saving') : props.submitLabel}
        </button>
        {props.onCancel && (
          <button type="button" className="secondary-button" onClick={props.onCancel}>
            {t('common.cancel')}
          </button>
        )}
      </div>
    </form>
  );
}

// LabelSettings lists the built-in labels and manages the user's own.
export function LabelSettings() {
  const { t } = useTranslation();
  const [labels, setLabels] = useState<Label[] | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.labels().then(setLabels, (e) => setError(errorText(e, t)));
  }, [t]);
  useEffect(load, [load]);

  async function remove(l: Label) {
    if (!window.confirm(t('labels.deleteConfirm', { name: l.name }))) return;
    try {
      await api.deleteLabel(l.id);
      load();
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  const builtIn = labels?.filter((l) => l.builtIn) ?? [];
  const own = labels?.filter((l) => !l.builtIn) ?? [];

  return (
    <>
      <p className="muted">{t('labels.intro')}</p>
      {error && <p className="error">{error}</p>}
      {!labels && !error && <p className="muted">{t('common.loading')}</p>}

      {labels && (
        <>
          <h3 className="subheading">{t('labels.yours')}</h3>
          {own.length === 0 && <p className="muted">{t('labels.none')}</p>}
          <ul className="theme-list">
            {own.map((l) =>
              editing === l.id ? (
                <li key={l.id}>
                  <LabelForm
                    initial={{ name: l.name, color: l.color }}
                    submitLabel={t('common.save')}
                    onSubmit={async (v) => {
                      await api.updateLabel(l.id, v);
                      setEditing(null);
                      load();
                    }}
                    onCancel={() => setEditing(null)}
                  />
                </li>
              ) : (
                <li key={l.id} className="theme-row">
                  <LabelChip label={l} />
                  <div className="theme-actions">
                    <button type="button" className="small-button" onClick={() => setEditing(l.id)}>
                      {t('common.edit')}
                    </button>
                    <button type="button" className="small-button danger" onClick={() => remove(l)}>
                      {t('common.delete')}
                    </button>
                  </div>
                </li>
              ),
            )}
          </ul>

          <h3 className="subheading">{t('labels.new')}</h3>
          <LabelForm
            initial={{ name: '', color: LABEL_COLORS[1] }}
            submitLabel={t('labels.create')}
            onSubmit={async (v) => {
              await api.createLabel(v);
              load();
            }}
          />

          <h3 className="subheading">{t('labels.builtInTitle')}</h3>
          <ul className="theme-list">
            {builtIn.map((l) => (
              <li key={l.id} className="theme-row">
                <LabelChip label={l} />
                <span className="muted theme-desc">{t(`labels.builtInHint.${l.id}`, { defaultValue: '' })}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}
