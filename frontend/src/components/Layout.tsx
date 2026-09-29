import { ReactNode, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import { useOffline } from '../api/offline';
import { useAuth } from '../auth';
import { RecorderProvider } from '../context/Recorder';
import { useDesktopNotifications } from '../lib/desktop';
import { SignOutIcon } from './Icons';
import { NewItemDialog } from './NewItemDialog';

// Layout is the app frame: a header with the navigation, which collapses into a menu
// button on narrow screens.
export function Layout({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const { account, logout } = useAuth();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [open, setOpen] = useState(false);
  const [newKind, setNewKind] = useState<'note' | 'task' | null>(null);
  const onConversations = pathname === '/' || pathname === '/briefing' || pathname.startsWith('/conversations/');
  // The time log, the done tasks, Ask and shared items are shown next to the notes list, like a note.
  const onTime = ['/time', '/done', '/ask', '/share'].includes(pathname);
  const admin = account?.role === 'admin';
  const offline = useOffline();

  // Close the mobile menu after navigating.
  useEffect(() => setOpen(false), [pathname]);

  // A clicked notification opens its page here (see public/push-sw.js).
  useEffect(() => {
    if (!('serviceWorker' in navigator)) return;
    const onMessage = (e: MessageEvent) => {
      const data = e.data as { type?: string; url?: string } | null;
      if (data?.type !== 'knowpod:open' || !data.url) return;
      const url = new URL(data.url, window.location.origin);
      if (url.origin === window.location.origin) navigate(url.pathname + url.search);
    };
    navigator.serviceWorker.addEventListener('message', onMessage);
    return () => navigator.serviceWorker.removeEventListener('message', onMessage);
  }, [navigate]);

  // The desktop app shows notifications from the server's live stream (see lib/desktop.ts).
  useDesktopNotifications(!!account, navigate);

  async function handleLogout() {
    await logout();
    navigate('/login');
  }

  return (
    <RecorderProvider>
      <header className="header">
        <Link to="/" className="brand">
          <img src="/favicon.svg" alt="" width="26" height="26" />
          knowpod
        </Link>
        {account && offline && (
          <span className="offline-badge" role="status" title={t('offline.hint')}>
            {t('offline.badge')}
          </span>
        )}
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
              <button type="button" className="nav-new" onClick={() => setNewKind('note')}>
                + {t('nav.newNote')}
              </button>
              <button type="button" className="nav-new" onClick={() => setNewKind('task')}>
                + {t('nav.newTask')}
              </button>
              <span className="nav-divider" aria-hidden="true" />
              <NavLink to="/" className={() => (onConversations ? 'active' : '')}>
                {t('nav.conversations')}
              </NavLink>
              <NavLink to="/ask">{t('nav.ask')}</NavLink>
              <NavLink to="/time">{t('nav.time')}</NavLink>
              <NavLink to="/done">{t('nav.done')}</NavLink>
              <NavLink to="/settings">{t('nav.settings')}</NavLink>
              {admin && <NavLink to="/admin">{t('nav.admin')}</NavLink>}
              <span className="nav-divider" aria-hidden="true" />
              <span className="nav-user">{account.email}</span>
              {/* An icon in the header; the phone menu also names it. */}
              <button type="button" className="link-button nav-signout" title={t('nav.signOut')} onClick={handleLogout}>
                <SignOutIcon />
                <span className="nav-signout-text">{t('nav.signOut')}</span>
              </button>
            </nav>
          </>
        )}
      </header>
      <main className={`container${onConversations || onTime ? ' full' : ''}`}>{children}</main>
      {newKind && <NewItemDialog kind={newKind} onClose={() => setNewKind(null)} />}
    </RecorderProvider>
  );
}
