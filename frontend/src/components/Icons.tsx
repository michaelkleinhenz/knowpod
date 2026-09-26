// Small inline icons (stroke uses currentColor).

export function DocIcon() {
  return (
    <svg className="doc-icon" width="30" height="38" viewBox="0 0 30 38" aria-hidden="true">
      <rect x="0.5" y="0.5" width="29" height="37" rx="4" fill="var(--color-surface)" stroke="var(--color-border)" />
      <rect x="7" y="8" width="8" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.55" />
      <rect x="7" y="15" width="16" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.35" />
      <rect x="7" y="21" width="16" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.35" />
      <rect x="7" y="27" width="16" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.35" />
    </svg>
  );
}

export function SearchIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" strokeLinecap="round" />
    </svg>
  );
}

export function RefreshIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M20 11a8 8 0 0 0-14.9-3.5M4 4v4h4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M4 13a8 8 0 0 0 14.9 3.5M20 20v-4h-4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
