import { ReactNode } from 'react';
import { Link } from 'react-router-dom';

export function Layout({ children }: { children: ReactNode }) {
  return (
    <>
      <header className="header">
        <Link to="/" className="brand">
          knowpod-service
        </Link>
      </header>
      <main className="container">{children}</main>
    </>
  );
}
