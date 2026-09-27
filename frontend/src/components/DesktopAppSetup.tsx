import { Trans, useTranslation } from 'react-i18next';
import { isDesktopApp } from '../lib/push';
import { CopyButton } from './CopyButton';

// RELEASES is where the desktop app's installers are published (see README.md#version).
const RELEASES = 'https://github.com/michaelkleinhenz/knowpod-service/releases';

// DesktopAppSetup explains how to connect the desktop app (desktop/) to this server: it asks
// for the server's address on its first start, which is the address of this page.
export function DesktopAppSetup() {
  const { t } = useTranslation();
  const server = window.location.origin;

  return (
    <>
      <h2 className="card-title">{t('desktopApp.title')}</h2>
      {isDesktopApp() ? (
        <p className="muted">
          <Trans i18nKey="desktopApp.connected" values={{ server }} components={{ 1: <code /> }} />
        </p>
      ) : (
        <>
          <p className="muted">{t('desktopApp.intro')}</p>
          <dl className="facts">
            <dt>{t('desktopApp.server')}</dt>
            <dd className="with-action">
              <code>{server}</code> <CopyButton text={server} />
            </dd>
          </dl>
          <ol className="steps">
            <li>
              <Trans i18nKey="desktopApp.stepInstall" components={{ 1: <a href={RELEASES} target="_blank" rel="noreferrer" /> }} />
            </li>
            <li>{t('desktopApp.stepServer')}</li>
            <li>{t('desktopApp.stepSignIn')}</li>
          </ol>
          <p className="muted">{t('desktopApp.notifications')}</p>
        </>
      )}
    </>
  );
}
