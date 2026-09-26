import type { ReactNode } from 'react';

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

export function UploadIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M12 16V4M7 9l5-5 5 5" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M4 16v3a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-3" strokeLinecap="round" />
    </svg>
  );
}

// Toolbar icons (18px, 24-unit grid, stroke uses currentColor).
function ToolIcon({ children }: { children: ReactNode }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {children}
    </svg>
  );
}

export function SlidersIcon() {
  return (
    <ToolIcon>
      <path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1" />
      <circle cx="15" cy="6" r="2" />
      <circle cx="9" cy="12" r="2" />
      <circle cx="17" cy="18" r="2" />
    </ToolIcon>
  );
}

export function DownloadIcon() {
  return (
    <ToolIcon>
      <path d="M12 4v11M7 10l5 5 5-5M5 20h14" />
    </ToolIcon>
  );
}

export function CopyIcon() {
  return (
    <ToolIcon>
      <rect x="9" y="9" width="11" height="11" rx="2" />
      <path d="M5 15V6a2 2 0 0 1 2-2h8" />
    </ToolIcon>
  );
}

export function CheckIcon() {
  return (
    <ToolIcon>
      <path d="m5 12.5 4.5 4.5L19 7.5" />
    </ToolIcon>
  );
}

// RetranscribeIcon: a sound wave with a circular arrow.
export function RetranscribeIcon() {
  return (
    <ToolIcon>
      <path d="M3 10v4M6.5 7v10M10 9.5v5" />
      <path d="M20.5 12a6.5 6.5 0 0 1-6.5 6.5M14 5.5a6.5 6.5 0 0 1 6 4" />
      <path d="M20.8 6.5 20 9.5l-3-.8" />
    </ToolIcon>
  );
}

export function TrashIcon() {
  return (
    <ToolIcon>
      <path d="M4 7h16M9.5 7V4.5h5V7M6.5 7l1 12.5h9l1-12.5M10 11v5M14 11v5" />
    </ToolIcon>
  );
}
