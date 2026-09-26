import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../api/client';
import { useAuth } from '../auth';
import { OpenRouterSettings } from '../components/OpenRouterSettings';
import { ThemeSettings } from '../components/ThemeSettings';
import { LANGUAGES } from '../i18n';
import { errorText } from '../lib/errors';

// LanguageSettings changes the app language; it is saved with the user.
function LanguageSettings() {
  const { t, i18n } = useTranslation();
  const { update } = useAuth();
  const [error, setError] = useState<string | null>(null);

  async function change(lang: string) {
    setError(null);
    void i18n.changeLanguage(lang); // switch right away; saving follows
    try {
      update(await api.savePreferences({ language: lang }));
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  return (
    <>
      <p className="muted">{t('settings.language.hint')}</p>
      <div className="form">
        <label>
          {t('settings.language.label')}
          <select value={i18n.language} onChange={(e) => change(e.target.value)}>
            {LANGUAGES.map((l) => (
              <option key={l} value={l}>
                {t(`settings.language.names.${l}`)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error && <p className="error">{error}</p>}
    </>
  );
}

export function Settings() {
  const { t } = useTranslation();
  const { account } = useAuth();
  return (
    <div className="page">
      <section className="card">
        <h1>{t('settings.title')}</h1>
        <h2 className="card-title">{t('settings.language.title')}</h2>
        <LanguageSettings />
      </section>
      <section className="card">
        <h2 className="card-title">{t('settings.themes.title')}</h2>
        <ThemeSettings />
      </section>
      {account?.role === 'admin' && (
        <section className="card">
          <h2 className="card-title">{t('settings.ai.title')}</h2>
          <OpenRouterSettings />
        </section>
      )}
    </div>
  );
}
