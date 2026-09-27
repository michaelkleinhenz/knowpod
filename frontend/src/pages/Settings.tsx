import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../api/client';
import { useAuth } from '../auth';
import { LabelSettings } from '../components/LabelSettings';
import { TabbedPage, useTab } from '../components/Tabs';
import { ThemeSettings } from '../components/ThemeSettings';
import { LANGUAGES } from '../i18n';
import { Account } from './Account';
import { Devices } from './Devices';
import { errorText } from '../lib/errors';

const TABS = ['general', 'account', 'devices', 'themes', 'labels'] as const;

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

// Settings groups the user's own settings in tabs: they apply only to the signed-in user.
// Settings for all users are under Admin.
export function Settings() {
  const { t } = useTranslation();
  const [tab, setTab] = useTab(TABS);

  return (
    <TabbedPage title={t('settings.title')} tabs={TABS} tab={tab} setTab={setTab} label={(id) => t(`settings.tabs.${id}`)}>
      {tab === 'general' && (
        <section className="card">
          <LanguageSettings />
        </section>
      )}
      {tab === 'account' && <Account />}
      {tab === 'devices' && <Devices />}
      {tab === 'themes' && (
        <section className="card">
          <h2 className="card-title">{t('settings.themes.title')}</h2>
          <ThemeSettings />
        </section>
      )}
      {tab === 'labels' && (
        <section className="card">
          <h2 className="card-title">{t('labels.title')}</h2>
          <LabelSettings />
        </section>
      )}
    </TabbedPage>
  );
}
