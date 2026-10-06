import { ReactNode, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import { useOffline } from '../api/offline';
import { useAuth } from '../auth';
import { RecorderProvider, useRecorder } from '../context/Recorder';
import { appContext, isMobileApp, useDesktopNotifications } from '../lib/desktop';
import { MicIcon, SignOutIcon, UsbIcon, WifiIcon } from './Icons';
import { NewItemDialog } from './NewItemDialog';
import { PocketUsbSyncDialog, pocketUsbSyncAvailable } from './PocketUsbSync';

// Layout is the app frame: a header with the navigation, which collapses into a menu
// button on narrow screens.
export function Layout({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const { account, logout } = useAuth();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [open, setOpen] = useState(false);
  const [newKind, setNewKind] = useState<'note' | 'task' | null>(null);
  // pocketSync shows the Pocket Sync dialog (desktop and Android apps only).
  const [pocketSync, setPocketSync] = useState(false);
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

  // The page's scrollbar paints the header's colors beside the header (see lib/scrollbar.ts);
  // it needs the header's height, which changes as the header wraps.
  const headerRef = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    const header = headerRef.current;
    if (!header) return;
    const root = document.documentElement;
    const update = () => root.style.setProperty('--header-height', `${header.offsetHeight}px`);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(header);
    return () => {
      observer.disconnect();
      root.style.removeProperty('--header-height');
    };
  }, []);

  // Opening the dialog also closes the mobile menu, which would otherwise stay open behind it.
  function startNew(kind: 'note' | 'task') {
    setOpen(false);
    setNewKind(kind);
  }

  async function handleLogout() {
    await logout();
    navigate('/login');
  }

  return (
    <RecorderProvider>
      <header className="header" ref={headerRef}>
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
            <HeaderRecordButton />
            {/* On phones, Pocket Sync sits in the header right of the record button, not in the menu. */}
            {pocketUsbSyncAvailable() && (
              <button
                type="button"
                className="header-pocket-sync"
                title={t('pocketUsbSync.button', { context: appContext() })}
                aria-label={t('pocketUsbSync.button', { context: appContext() })}
                onClick={() => {
                  setOpen(false);
                  setPocketSync(true);
                }}
              >
                <PocketSyncIcon size={22} />
              </button>
            )}
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
              <div className="nav-new-group">
                <button type="button" className="nav-new" onClick={() => startNew('note')}>
                  + {t('nav.newNote')}
                </button>
                <button type="button" className="nav-new" onClick={() => startNew('task')}>
                  + {t('nav.newTask')}
                </button>
                {pocketUsbSyncAvailable() && (
                  <button
                    type="button"
                    className="nav-icon-button nav-pocket-sync"
                    title={t('pocketUsbSync.button', { context: appContext() })}
                    aria-label={t('pocketUsbSync.button', { context: appContext() })}
                    onClick={() => {
                      setOpen(false);
                      setPocketSync(true);
                    }}
                  >
                    <PocketSyncIcon />
                  </button>
                )}
              </div>
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
      {pocketSync && <PocketUsbSyncDialog onClose={() => setPocketSync(false)} />}
    </RecorderProvider>
  );
}

// PocketSyncIcon is the Pocket Sync button's icon: WiFi in the Android and iOS apps, USB in the
// desktop app.
function PocketSyncIcon({ size }: { size?: number }) {
  return isMobileApp() ? <WifiIcon size={size} /> : <UsbIcon size={size} />;
}

// HeaderRecordButton starts a voice memo from the phone's header, left of the menu button; while
// one is recorded it opens the recording screen again. Wide screens record from the notes list.
function HeaderRecordButton() {
  const { t } = useTranslation();
  const recorder = useRecorder();
  return (
    <button
      type="button"
      className={`header-record${recorder.recording ? ' recording' : ''}`}
      onClick={recorder.recording ? recorder.expand : recorder.start}
      disabled={recorder.active && !recorder.recording}
      title={recorder.recording ? t('recorder.expand') : t('recorder.record')}
      aria-label={recorder.recording ? t('recorder.expand') : t('recorder.record')}
    >
      <MicIcon size={22} />
    </button>
  );
}
