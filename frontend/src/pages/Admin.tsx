import { useTranslation } from 'react-i18next';
import { OpenRouterSettings } from '../components/OpenRouterSettings';
import { TabbedPage, useTab } from '../components/Tabs';
import { Users } from './Users';

const TABS = ['users', 'general'] as const;

// Admin is for administrators: the users, and the settings that apply to all users.
export function Admin() {
  const { t } = useTranslation();
  const [tab, setTab] = useTab(TABS);

  return (
    <TabbedPage title={t('admin.title')} tabs={TABS} tab={tab} setTab={setTab} label={(id) => t(`admin.tabs.${id}`)}>
      {tab === 'users' && <Users />}
      {tab === 'general' && (
        <section className="card">
          <h2 className="card-title">{t('settings.ai.title')}</h2>
          <OpenRouterSettings />
        </section>
      )}
    </TabbedPage>
  );
}
