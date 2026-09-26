import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { de } from './de';
import { en } from './en';

export const LANGUAGES = ['en', 'de'] as const;
export type Language = (typeof LANGUAGES)[number];

const STORAGE_KEY = 'knowpod.language';

// initialLanguage is the language before the user's saved setting is known: the last one
// used on this device, else the browser's.
function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === 'en' || saved === 'de') return saved;
  } catch {
    // storage unavailable
  }
  return navigator.language?.toLowerCase().startsWith('de') ? 'de' : 'en';
}

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, de: { translation: de } },
  lng: initialLanguage(),
  fallbackLng: 'en',
  interpolation: { escapeValue: false }, // React escapes
  returnNull: false,
});

document.documentElement.lang = i18n.language;
i18n.on('languageChanged', (lng) => {
  document.documentElement.lang = lng;
  try {
    localStorage.setItem(STORAGE_KEY, lng);
  } catch {
    // storage unavailable
  }
});

// applyLanguage switches to a user's saved language ("" = keep the browser/device default).
export function applyLanguage(lang?: string) {
  if ((lang === 'en' || lang === 'de') && lang !== i18n.language) void i18n.changeLanguage(lang);
}

// locale is the locale for formatting dates and numbers in the current language.
export function locale(): string {
  return i18n.language === 'de' ? 'de-DE' : 'en-US';
}

export default i18n;
