import { useTranslation } from 'react-i18next';

// useThemeText returns a function giving a theme's name and description in the UI
// language: built-in themes are translated by ID, users' own and customized themes keep
// their text.
export function useThemeText() {
  const { t, i18n } = useTranslation();
  return (th: { id: string; name: string; description?: string; builtIn?: boolean; customized?: boolean }) => {
    if (th.builtIn && !th.customized && i18n.exists(`themes.${th.id}.name`)) {
      return { name: t(`themes.${th.id}.name`), description: t(`themes.${th.id}.description`) };
    }
    return { name: th.name, description: th.description ?? '' };
  };
}
