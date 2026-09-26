import { FormEvent, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Theme, ThemeInput } from '../api/client';
import { errorText } from '../lib/errors';
import { useThemeText } from '../lib/themes';

const EMPTY: ThemeInput = { name: '', description: '', instructions: '' };

// ThemeForm creates or edits a theme.
function ThemeForm(props: { initial: ThemeInput; submitLabel: string; onSubmit: (t: ThemeInput) => Promise<void>; onCancel?: () => void }) {
  const { t } = useTranslation();
  const [value, setValue] = useState<ThemeInput>(props.initial);
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
    <form className="form theme-form" onSubmit={handleSubmit}>
      <div className="form-grid">
        <label>
          {t('settings.themes.name')}
          <input required maxLength={60} value={value.name} onChange={(e) => setValue({ ...value, name: e.target.value })} />
        </label>
        <label>
          {t('settings.themes.description')}
          <input maxLength={120} value={value.description} onChange={(e) => setValue({ ...value, description: e.target.value })} />
          <span className="muted field-note">{t('settings.themes.descriptionHint')}</span>
        </label>
      </div>
      <label>
        {t('settings.themes.instructions')}
        <span className="muted field-note">{t('settings.themes.instructionsHint')}</span>
        <textarea
          required
          maxLength={4000}
          rows={6}
          placeholder={t('settings.themes.instructionsPlaceholder')}
          value={value.instructions}
          onChange={(e) => setValue({ ...value, instructions: e.target.value })}
        />
      </label>
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

// ThemeSettings lists the built-in summary themes and manages the user's own.
export function ThemeSettings() {
  const { t } = useTranslation();
  const themeText = useThemeText();
  const [themes, setThemes] = useState<Theme[] | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.themes().then(setThemes, (e) => setError(errorText(e, t)));
  }, [t]);
  useEffect(load, [load]);

  async function remove(th: Theme) {
    if (!window.confirm(t('settings.themes.deleteConfirm', { name: th.name }))) return;
    try {
      await api.deleteTheme(th.id);
      load();
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  async function reset(th: Theme) {
    if (!window.confirm(t('settings.themes.resetConfirm', { name: themeText(th).name }))) return;
    try {
      await api.deleteTheme(th.id); // for a built-in theme: removes the user's version
      load();
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  const builtIn = themes?.filter((th) => th.builtIn) ?? [];
  const own = themes?.filter((th) => !th.builtIn) ?? [];

  return (
    <>
      <p className="muted">{t('settings.themes.intro')}</p>
      {error && <p className="error">{error}</p>}
      {!themes && !error && <p className="muted">{t('common.loading')}</p>}

      {themes && (
        <>
          <h3 className="subheading">{t('settings.themes.yours')}</h3>
          {own.length === 0 && <p className="muted">{t('settings.themes.none')}</p>}
          <ul className="theme-list">
            {own.map((th) =>
              editing === th.id ? (
                <li key={th.id}>
                  <ThemeForm
                    initial={{ name: th.name, description: th.description, instructions: th.instructions }}
                    submitLabel={t('common.save')}
                    onSubmit={async (v) => {
                      await api.updateTheme(th.id, v);
                      setEditing(null);
                      load();
                    }}
                    onCancel={() => setEditing(null)}
                  />
                </li>
              ) : (
                <li key={th.id} className="theme-row">
                  <div className="theme-text">
                    <span className="theme-name">{th.name}</span>
                    {th.description && <span className="muted theme-desc">{th.description}</span>}
                  </div>
                  <div className="theme-actions">
                    <button type="button" className="small-button" onClick={() => setEditing(th.id)}>
                      {t('common.edit')}
                    </button>
                    <button type="button" className="small-button danger" onClick={() => remove(th)}>
                      {t('common.delete')}
                    </button>
                  </div>
                </li>
              ),
            )}
          </ul>

          <h3 className="subheading">{t('settings.themes.new')}</h3>
          <ThemeForm
            initial={EMPTY}
            submitLabel={t('settings.themes.create')}
            onSubmit={async (v) => {
              await api.createTheme(v);
              load();
            }}
          />

          <h3 className="subheading">{t('settings.themes.builtIn')}</h3>
          <ul className="theme-list">
            {builtIn.map((th) => {
              const text = themeText(th);
              return editing === th.id ? (
                <li key={th.id}>
                  <ThemeForm
                    initial={{ name: text.name, description: text.description, instructions: th.instructions }}
                    submitLabel={t('common.save')}
                    onSubmit={async (v) => {
                      await api.updateTheme(th.id, v);
                      setEditing(null);
                      load();
                    }}
                    onCancel={() => setEditing(null)}
                  />
                </li>
              ) : (
                <li key={th.id} className="theme-row builtin">
                  <details>
                    <summary>
                      <span className="theme-name">{text.name}</span>
                      {th.customized && <span className="role-pill plain">{t('settings.themes.customized')}</span>}
                      <span className="muted theme-desc">{text.description}</span>
                    </summary>
                    <p className="theme-instructions">{th.instructions}</p>
                  </details>
                  <div className="theme-actions">
                    <button type="button" className="small-button" onClick={() => setEditing(th.id)}>
                      {t('settings.themes.customize')}
                    </button>
                    {th.customized && (
                      <button type="button" className="small-button" onClick={() => reset(th)}>
                        {t('settings.themes.reset')}
                      </button>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        </>
      )}
    </>
  );
}
