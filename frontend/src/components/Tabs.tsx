import { ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';

// useTab reads the open tab from the URL (?tab=themes), so tabs can be linked; the first tab
// has no parameter. Unknown or unavailable tabs open the first one.
export function useTab<T extends string>(tabs: readonly T[]): [T, (tab: T) => void] {
  const [params, setParams] = useSearchParams();
  const requested = params.get('tab') as T | null;
  const tab = requested && tabs.includes(requested) ? requested : tabs[0];
  return [tab, (id: T) => setParams(id === tabs[0] ? {} : { tab: id }, { replace: true })];
}

// TabbedPage is a page with a title, a row of tabs and the open tab's content below them.
export function TabbedPage<T extends string>({
  title,
  tabs,
  tab,
  setTab,
  label,
  children,
}: {
  title: string;
  tabs: readonly T[];
  tab: T;
  setTab: (tab: T) => void;
  label: (tab: T) => string;
  children: ReactNode;
}) {
  return (
    <div className="page">
      <h1 className="page-title">{title}</h1>
      <div className="segmented tabs" role="tablist" aria-label={title}>
        {tabs.map((id) => (
          <button
            key={id}
            type="button"
            role="tab"
            id={`tab-${id}`}
            aria-selected={tab === id}
            aria-controls={`panel-${id}`}
            className={tab === id ? 'active' : ''}
            onClick={() => setTab(id)}
          >
            {label(id)}
          </button>
        ))}
      </div>
      <div className="tab-panel" role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {children}
      </div>
    </div>
  );
}
