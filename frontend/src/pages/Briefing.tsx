import { useEffect, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { api, DailyBriefing } from '../api/client';
import { BackIcon, HomeIcon, RefreshIcon, SlidersIcon } from '../components/Icons';
import { Markdown } from '../components/Markdown';
import { errorText } from '../lib/errors';
import { formatTime } from '../lib/recordings';

// Briefing is the home page: today's daily briefing, with the tasks due, what came in and
// the open action items. It is made on the server at the user's briefing time; opened
// before, it is made right away. "#12" in it opens note 12.
export function Briefing() {
  const { t } = useTranslation();
  const [briefing, setBriefing] = useState<DailyBriefing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    api.todayBriefing().then(
      (b) => live && setBriefing(b),
      (err) => live && setError(errorText(err, t)),
    );
    return () => {
      live = false;
    };
  }, [t]);

  async function remake() {
    setBusy(true);
    setError(null);
    try {
      setBriefing(await api.remakeTodayBriefing());
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="briefing-page">
      <Link to="/" className="back-link">
        <BackIcon />
        <span>{t('conversation.back')}</span>
      </Link>
      <div className="time-log-head briefing-head">
        <h1>
          <HomeIcon size={22} /> {briefing?.title ?? t('briefing.daily')}
        </h1>
        {briefing && !briefing.off && (
          <button type="button" className="pill-button" disabled={busy} title={t('briefing.remakeTitle')} onClick={() => void remake()}>
            <RefreshIcon /> <span>{busy ? t('briefing.remaking') : t('briefing.remake')}</span>
          </button>
        )}
        <Link to="/settings?tab=account#briefing" className="pill-button" title={t('briefing.settings')} aria-label={t('briefing.settings')}>
          <SlidersIcon />
        </Link>
      </div>
      {error && <p className="error">{error}</p>}
      {!briefing && !error && <p className="muted">{t('common.loading')}</p>}
      {briefing?.off && (
        <div className="briefing-off">
          <p>{t('briefing.off')}</p>
          <p className="muted">
            <Trans i18nKey="briefing.offHint" components={{ 1: <Link to="/settings?tab=account#briefing" /> }} />
          </p>
        </div>
      )}
      {briefing && !briefing.off && (
        <>
          {briefing.madeAt && <p className="muted briefing-meta">{t('briefing.madeAt', { time: formatTime(new Date(briefing.madeAt)) })}</p>}
          <div className="prose briefing-body">
            <Markdown text={briefing.markdown ?? ''} noteLinks />
          </div>
        </>
      )}
    </div>
  );
}
