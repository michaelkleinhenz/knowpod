import { ReactNode } from 'react';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { useAuth } from './auth';
import { Layout } from './components/Layout';
import { Account } from './pages/Account';
import { Devices } from './pages/Devices';
import { Conversation } from './pages/Conversation';
import { Conversations } from './pages/Conversations';
import { Login } from './pages/Login';
import { Settings } from './pages/Settings';
import { Status } from './pages/Status';

// RequireLogin sends signed-out visitors to the login page and back afterwards.
function RequireLogin({ children }: { children: ReactNode }) {
  const { account, loading } = useAuth();
  const location = useLocation();
  if (loading) return <p className="muted">Loading…</p>;
  if (!account) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          path="/"
          element={
            <RequireLogin>
              <Conversations />
            </RequireLogin>
          }
        />
        <Route
          path="/conversations/:id"
          element={
            <RequireLogin>
              <Conversation />
            </RequireLogin>
          }
        />
        <Route
          path="/settings"
          element={
            <RequireLogin>
              <Settings />
            </RequireLogin>
          }
        />
        <Route
          path="/devices"
          element={
            <RequireLogin>
              <Devices />
            </RequireLogin>
          }
        />
        <Route
          path="/status"
          element={
            <RequireLogin>
              <Status />
            </RequireLogin>
          }
        />
        <Route
          path="/account"
          element={
            <RequireLogin>
              <Account />
            </RequireLogin>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Layout>
  );
}
