import { ReactNode } from 'react';
import { Link, NavLink, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';

export function Layout({ children }: { children: ReactNode }) {
  const { account, logout } = useAuth();
  const navigate = useNavigate();

  async function handleLogout() {
    await logout();
    navigate('/login');
  }

  return (
    <>
      <header className="header">
        <Link to="/" className="brand">
          knowpod-service
        </Link>
        {account && (
          <nav className="nav">
            <NavLink to="/" end>
              Home
            </NavLink>
            <NavLink to="/account">Account</NavLink>
            <span className="nav-user">{account.email}</span>
            <button type="button" className="link-button" onClick={handleLogout}>
              Sign out
            </button>
          </nav>
        )}
      </header>
      <main className="container">{children}</main>
    </>
  );
}
