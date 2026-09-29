import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../api/client';
import { useAuth } from '../auth';
import { DesktopAppSetup } from '../components/DesktopAppSetup';
import { LabelSettings } from '../components/LabelSettings';
import { PersonalBackup } from '../components/PersonalBackup';
import { FilterSettings } from '../components/SavedFilters';
import { TabbedPage, useTab } from '../components/Tabs';
import { ThemeSettings } from '../components/ThemeSettings';
import { LANGUAGES } from '../i18n';
import { Account } from './Account';
import { Devices } from './Devices';
import { APPEARANCES, applyAppearance, currentAppearance } from '../lib/appearance';
import { errorText } from '../lib/errors';
import { FONT_SIZES, applyFontSize, currentFontSize } from '../lib/fontSize';
import { APP_VERSION, desktopVersion } from '../lib/version';

const TABS = ['general', 'account', 'devices', 'themes', 'labels', 'filters', 'backup'] as const;

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

// AppearanceSettings switches between the light and dark color scheme, or follows the
// system's; it is saved with the user.
function AppearanceSettings() {
  const { t } = useTranslation();
  const { account, update } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const value = account?.appearance ?? currentAppearance();

  async function change(a: string) {
    setError(null);
    applyAppearance(a); // switch right away; saving follows
    try {
      update(await api.savePreferences({ appearance: a }));
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  return (
    <>
      <h2 className="card-title">{t('settings.appearance.title')}</h2>
      <p className="muted">{t('settings.appearance.hint')}</p>
      <div className="form">
        <label>
          {t('settings.appearance.label')}
          <select value={value} onChange={(e) => change(e.target.value)}>
            {APPEARANCES.map((a) => (
              <option key={a} value={a}>
                {t(`settings.appearance.names.${a || 'system'}`)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error && <p className="error">{error}</p>}
    </>
  );
}

// FontSizeSettings makes all text in the app smaller or larger; it is saved with the user.
function FontSizeSettings() {
  const { t } = useTranslation();
  const { account, update } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const value = account?.fontSize ?? currentFontSize();

  async function change(f: string) {
    setError(null);
    applyFontSize(f); // switch right away; saving follows
    try {
      update(await api.savePreferences({ fontSize: f }));
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  return (
    <>
      <h2 className="card-title">{t('settings.fontSize.title')}</h2>
      <p className="muted">{t('settings.fontSize.hint')}</p>
      <div className="form">
        <label>
          {t('settings.fontSize.label')}
          <select value={value} onChange={(e) => change(e.target.value)}>
            {FONT_SIZES.map((f) => (
              <option key={f} value={f}>
                {t(`settings.fontSize.names.${f || 'default'}`)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error && <p className="error">{error}</p>}
    </>
  );
}

// About shows the app version (and the desktop app's, when running in it).
function About() {
  const { t } = useTranslation();
  const desktop = desktopVersion();

  return (
    <>
      <h2 className="card-title">{t('settings.about.title')}</h2>
      <dl className="about-versions">
        <dt>{t('settings.about.version')}</dt>
        <dd>{APP_VERSION}</dd>
        {desktop && (
          <>
            <dt>{t('settings.about.desktopVersion')}</dt>
            <dd>{desktop}</dd>
          </>
        )}
      </dl>
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
        <>
          <section className="card">
            <LanguageSettings />
          </section>
          <section className="card">
            <AppearanceSettings />
          </section>
          <section className="card">
            <FontSizeSettings />
          </section>
          <section className="card">
            <DesktopAppSetup />
          </section>
          <section className="card">
            <About />
          </section>
        </>
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
      {tab === 'filters' && (
        <section className="card">
          <h2 className="card-title">{t('filters.title')}</h2>
          <FilterSettings />
        </section>
      )}
      {tab === 'backup' && <PersonalBackup />}
    </TabbedPage>
  );
}
