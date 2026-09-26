import { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { useAuth } from './auth';
import { Layout } from './components/Layout';
import { Account } from './pages/Account';
import { Devices } from './pages/Devices';
import { Conversation } from './pages/Conversation';
import { Login } from './pages/Login';
import { NotesHome, NotesLayout } from './pages/NotesLayout';
import { Settings } from './pages/Settings';
import { Users } from './pages/Users';
import { Status } from './pages/Status';

// RequireLogin sends signed-out visitors to the login page and back afterwards. With admin,
// only administrators get through.
function RequireLogin({ children, admin = false }: { children: ReactNode; admin?: boolean }) {
  const { t } = useTranslation();
  const { account, loading } = useAuth();
  const location = useLocation();
  if (loading) return <p className="muted">{t('common.loading')}</p>;
  if (!account) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  if (admin && account.role !== 'admin') return <Navigate to="/" replace />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          element={
            <RequireLogin>
              <NotesLayout />
            </RequireLogin>
          }
        >
          <Route path="/" element={<NotesHome />} />
          <Route path="/conversations/:id" element={<Conversation />} />
        </Route>
        <Route
          path="/settings"
          element={
            <RequireLogin>
              <Settings />
            </RequireLogin>
          }
        />
        <Route
          path="/users"
          element={
            <RequireLogin admin>
              <Users />
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
