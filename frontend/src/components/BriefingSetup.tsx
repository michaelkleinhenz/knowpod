import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { api, BRIEFING_SECTIONS, BriefingSection, BriefingSettings } from '../api/client';
import { locale } from '../i18n';
import { errorText } from '../lib/errors';

// Weekdays in the order they are offered, Monday first; 0 is Sunday.
const WEEKDAYS = [1, 2, 3, 4, 5, 6, 0];

// BriefingSetup turns the daily briefing and the weekly review on and off, sets when they are
// made, whether the daily briefing is announced and what it shows, and makes a weekly review
// right away to try it.
export function BriefingSetup() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [settings, setSettings] = useState<BriefingSettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api.briefing().then(setSettings, (err) => setError(errorText(err, t)));
  }, [t]);

  async function save(next: BriefingSettings) {
    setSettings(next);
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      setSettings(await api.saveBriefing(next));
      setSaved(true);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  async function makeWeekly() {
    setBusy(true);
    setError(null);
    try {
      const rec = await api.makeBriefing('weekly');
      navigate(`/conversations/${rec.id}`);
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  if (!settings) return error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>;
  const toggleSection = (sec: BriefingSection, on: boolean) =>
    void save({ ...settings, sections: BRIEFING_SECTIONS.filter((x) => (x === sec ? on : settings.sections.includes(x))) });
  const dayName = (d: number) => new Intl.DateTimeFormat(locale(), { weekday: 'long' }).format(new Date(2026, 0, 4 + d)); // 2026-01-04 is a Sunday
  return (
    <>
      <p className="muted">{t('briefing.intro')}</p>
      <div className="form briefing-form">
        <label className="checkbox">
          <input type="checkbox" checked={settings.daily} disabled={busy} onChange={(e) => void save({ ...settings, daily: e.target.checked })} />
          {t('briefing.daily')}
        </label>
        {settings.daily && (
          <div className="briefing-options">
            <label className="checkbox">
              <input type="checkbox" checked={settings.notify} disabled={busy} onChange={(e) => void save({ ...settings, notify: e.target.checked })} />
              {t('briefing.notify')}
            </label>
            <fieldset className="briefing-sections">
              <legend>{t('briefing.sections')}</legend>
              {BRIEFING_SECTIONS.map((sec) => (
                <label key={sec} className="checkbox">
                  <input type="checkbox" checked={settings.sections.includes(sec)} disabled={busy} onChange={(e) => toggleSection(sec, e.target.checked)} />
                  {t(`briefing.section.${sec}`)}
                </label>
              ))}
            </fieldset>
            {settings.sections.includes('actionItems') && (
              <label className="briefing-days">
                {t('briefing.actionItemDays')}
                <input
                  type="number"
                  min={1}
                  max={30}
                  value={settings.actionItemDays}
                  disabled={busy}
                  onChange={(e) => setSettings({ ...settings, actionItemDays: Number(e.target.value) })}
                  onBlur={(e) => {
                    const n = Math.round(Number(e.target.value));
                    if (n >= 1 && n <= 30) void save({ ...settings, actionItemDays: n });
                  }}
                />
                {t('briefing.days')}
              </label>
            )}
          </div>
        )}
        <label className="checkbox">
          <input type="checkbox" checked={settings.weekly} disabled={busy} onChange={(e) => void save({ ...settings, weekly: e.target.checked })} />
          {t('briefing.weekly')}
        </label>
        <div className="briefing-when">
          <label>
            {t('briefing.time')}
            <input
              type="time"
              value={settings.time}
              disabled={busy}
              onChange={(e) => setSettings({ ...settings, time: e.target.value })}
              onBlur={(e) => e.target.value && void save({ ...settings, time: e.target.value })}
            />
          </label>
          <label>
            {t('briefing.weeklyDay')}
            <select value={settings.weeklyDay} disabled={busy} onChange={(e) => void save({ ...settings, weeklyDay: Number(e.target.value) })}>
              {WEEKDAYS.map((d) => (
                <option key={d} value={d}>
                  {dayName(d)}
                </option>
              ))}
            </select>
          </label>
        </div>
      </div>
      <p className="muted field-note">{t('briefing.notifyHint')}</p>
      {error && <p className="error">{error}</p>}
      {saved && !error && <p className="success">{t('common.saved')}</p>}
      <div className="button-row">
        {settings.daily && (
          <Link to="/briefing" className="secondary-button">
            {t('briefing.openToday')}
          </Link>
        )}
        <button type="button" className="secondary-button" disabled={busy} onClick={() => void makeWeekly()}>
          {t('briefing.makeWeekly')}
        </button>
      </div>
    </>
  );
}
