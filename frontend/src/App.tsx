import { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { useAuth } from './auth';
import { Layout } from './components/Layout';
import { Admin } from './pages/Admin';
import { Ask } from './pages/Ask';
import { Briefing } from './pages/Briefing';
import { Conversation } from './pages/Conversation';
import { DoneTasks } from './pages/DoneTasks';
import { Login } from './pages/Login';
import { NoteByNumber } from './pages/NoteByNumber';
import { NotesHome, NotesLayout } from './pages/NotesLayout';
import { OAuthAuthorize } from './pages/OAuthAuthorize';
import { Settings } from './pages/Settings';
import { Share } from './pages/Share';
import { TimeLog } from './pages/TimeLog';

// RequireLogin sends signed-out visitors to the login page and back afterwards. With admin,
// only administrators get through.
function RequireLogin({ children, admin = false }: { children: ReactNode; admin?: boolean }) {
  const { t } = useTranslation();
  const { account, loading } = useAuth();
  const location = useLocation();
  if (loading) return <p className="muted">{t('common.loading')}</p>;
  if (!account) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
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
          <Route path="/briefing" element={<Briefing />} />
          <Route path="/conversations/:id" element={<Conversation />} />
          <Route path="/n/:number" element={<NoteByNumber />} />
          <Route path="/time" element={<TimeLog />} />
          <Route path="/done" element={<DoneTasks />} />
          <Route path="/ask" element={<Ask />} />
          <Route path="/share" element={<Share />} />
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
          path="/oauth/authorize"
          element={
            <RequireLogin>
              <OAuthAuthorize />
            </RequireLogin>
          }
        />
        <Route
          path="/admin"
          element={
            <RequireLogin admin>
              <Admin />
            </RequireLogin>
          }
        />
        {/* Former pages, now tabs. */}
        <Route path="/account" element={<Navigate to="/settings?tab=account" replace />} />
        <Route path="/devices" element={<Navigate to="/settings?tab=devices" replace />} />
        <Route path="/users" element={<Navigate to="/admin" replace />} />
        <Route path="/status" element={<Navigate to="/settings" replace />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Layout>
  );
}
