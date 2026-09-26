import type { ReactNode } from 'react';
import type { NoteType } from '../api/client';

// Small inline icons (stroke uses currentColor).

// NoteIcon shows a note as a page; its content says the note's type: a sound wave for audio
// recordings, lines of text with a heading for text notes.
export function NoteIcon({ type, label }: { type: NoteType; label?: string }) {
  return (
    <svg
      className={`doc-icon doc-icon-${type}`}
      width="30"
      height="38"
      viewBox="0 0 30 38"
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      {label && <title>{label}</title>}
      <rect x="0.5" y="0.5" width="29" height="37" rx="4" fill="var(--color-surface)" stroke="var(--color-border)" />
      {type === 'text' ? (
        <>
          <rect x="7" y="8" width="12" height="2.6" rx="1.3" fill="var(--color-primary)" opacity="0.8" />
          <rect x="7" y="15" width="16" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.4" />
          <rect x="7" y="20.5" width="16" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.4" />
          <rect x="7" y="26" width="10" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.4" />
        </>
      ) : (
        <>
          <rect x="7" y="8" width="8" height="2.2" rx="1.1" fill="var(--color-muted)" opacity="0.55" />
          <g stroke="var(--color-primary)" strokeWidth="2" strokeLinecap="round" opacity="0.8">
            <path d="M8 21v2M11.5 18v8M15 16v12M18.5 19v6M22 20.5v3" />
          </g>
        </>
      )}
    </svg>
  );
}

export function NewNoteIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M12 5v14M5 12h14" strokeLinecap="round" />
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

export function TagIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3 12V4a1 1 0 0 1 1-1h8l9 9-9 9z" />
      <circle cx="8" cy="8" r="1.5" />
    </svg>
  );
}
