import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { api } from '../api/client';
import { useAuth } from '../auth';
import { OpenRouterSettings } from '../components/OpenRouterSettings';
import { ThemeSettings } from '../components/ThemeSettings';
import { LANGUAGES } from '../i18n';
import { errorText } from '../lib/errors';

type Tab = 'general' | 'themes' | 'ai';

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
      <h2 className="card-title">{t('settings.language.title')}</h2>
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

// Settings groups the user's settings in tabs; the AI tab is for administrators. The tab is
// part of the URL (?tab=themes) so it can be linked.
export function Settings() {
  const { t } = useTranslation();
  const { account } = useAuth();
  const [params, setParams] = useSearchParams();
  const tabs: Tab[] = account?.role === 'admin' ? ['general', 'themes', 'ai'] : ['general', 'themes'];
  const requested = params.get('tab') as Tab | null;
  const tab: Tab = requested && tabs.includes(requested) ? requested : 'general';

  return (
    <div className="page">
      <h1 className="page-title">{t('settings.title')}</h1>
      <div className="segmented tabs" role="tablist" aria-label={t('settings.title')}>
        {tabs.map((id) => (
          <button
            key={id}
            type="button"
            role="tab"
            id={`tab-${id}`}
            aria-selected={tab === id}
            aria-controls={`panel-${id}`}
            className={tab === id ? 'active' : ''}
            onClick={() => setParams(id === 'general' ? {} : { tab: id }, { replace: true })}
          >
            {t(`settings.tabs.${id}`)}
          </button>
        ))}
      </div>
      <section className="card" role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {tab === 'general' && <LanguageSettings />}
        {tab === 'themes' && (
          <>
            <h2 className="card-title">{t('settings.themes.title')}</h2>
            <ThemeSettings />
          </>
        )}
        {tab === 'ai' && (
          <>
            <h2 className="card-title">{t('settings.ai.title')}</h2>
            <OpenRouterSettings />
          </>
        )}
      </section>
    </div>
  );
}
