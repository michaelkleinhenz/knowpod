import { ReactNode, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';

// Layout is the app frame: a header with the navigation, which collapses into a menu
// button on narrow screens.
export function Layout({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const { account, logout } = useAuth();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [open, setOpen] = useState(false);
  const onConversations = pathname === '/' || pathname.startsWith('/conversations/');
  const admin = account?.role === 'admin';

  // Close the mobile menu after navigating.
  useEffect(() => setOpen(false), [pathname]);

  async function handleLogout() {
    await logout();
    navigate('/login');
  }

  return (
    <>
      <header className="header">
        <Link to="/" className="brand">
          <img src="/favicon.svg" alt="" width="26" height="26" />
          knowpod
        </Link>
        {account && (
          <>
            <button
              type="button"
              className="menu-button"
              aria-label={open ? t('nav.closeMenu') : t('nav.openMenu')}
              aria-expanded={open}
              aria-controls="main-nav"
              onClick={() => setOpen(!open)}
            >
              <span />
              <span />
              <span />
            </button>
            <nav id="main-nav" className={`nav${open ? ' open' : ''}`}>
              <NavLink to="/" className={() => (onConversations ? 'active' : '')}>
                {t('nav.conversations')}
              </NavLink>
              <NavLink to="/devices">{t('nav.devices')}</NavLink>
              {admin && <NavLink to="/users">{t('nav.users')}</NavLink>}
              <NavLink to="/settings">{t('nav.settings')}</NavLink>
              <NavLink to="/status">{t('nav.status')}</NavLink>
              <NavLink to="/account">{t('nav.account')}</NavLink>
              <span className="nav-user">{account.email}</span>
              <button type="button" className="link-button" onClick={handleLogout}>
                {t('nav.signOut')}
              </button>
            </nav>
          </>
        )}
      </header>
      <main className="container">{children}</main>
    </>
  );
}
