import { ReactNode, useEffect, useState } from 'react';
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';

// Layout is the app frame: a header with the navigation, which collapses into a menu
// button on narrow screens.
export function Layout({ children }: { children: ReactNode }) {
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
              aria-label={open ? 'Close menu' : 'Open menu'}
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
                Conversations
              </NavLink>
              <NavLink to="/devices">Devices</NavLink>
              {admin && <NavLink to="/users">Users</NavLink>}
              {admin && <NavLink to="/settings">Settings</NavLink>}
              <NavLink to="/status">Status</NavLink>
              <NavLink to="/account">Account</NavLink>
              <span className="nav-user">{account.email}</span>
              <button type="button" className="link-button" onClick={handleLogout}>
                Sign out
              </button>
            </nav>
          </>
        )}
      </header>
      <main className="container">{children}</main>
    </>
  );
}
