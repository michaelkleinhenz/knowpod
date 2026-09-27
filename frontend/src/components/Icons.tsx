import type { ReactNode } from 'react';
import type { NoteType } from '../api/client';

// Small inline icons (stroke uses currentColor).

// NoteIcon shows a note as a page; its content says the note's type: a sound wave for audio
// recordings, lines of text with a heading for text notes, handwriting for reMarkable
// documents, columns of cards for boards.
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
      {type === 'document' ? (
        <g fill="none" stroke="var(--color-primary)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" opacity="0.8">
          <path d="M7 11c1.5-2.5 2.5-2.5 3 0s1.5 2.5 3 0 2.5-2.5 3 0" />
          <path d="M7 18.5c1.2-2 2.2-2 2.8 0s1.8 2 3 0 2-2 2.7 0 1.8 2 3 0 1.5-1.5 2.5-.5" />
          <path d="M7 26c1.4-2.2 2.4-2.2 3 0s1.6 2.2 3 0" opacity="0.6" />
        </g>
      ) : type === 'board' ? (
        <>
          <rect x="6" y="7" width="18" height="2.4" rx="1.2" fill="var(--color-muted)" opacity="0.45" />
          <g fill="var(--color-primary)" opacity="0.8">
            <rect x="6" y="13" width="5" height="5" rx="1" />
            <rect x="6" y="20" width="5" height="5" rx="1" />
            <rect x="6" y="27" width="5" height="4" rx="1" />
            <rect x="12.5" y="13" width="5" height="5" rx="1" />
            <rect x="19" y="13" width="5" height="5" rx="1" opacity="0.6" />
            <rect x="19" y="20" width="5" height="5" rx="1" opacity="0.6" />
          </g>
        </>
      ) : type === 'text' ? (
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

// BackIcon is a circle with a left arrow in it, used by the link back to the notes list.
export function BackIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
      <circle cx="12" cy="12" r="9.25" />
      <path d="M13.5 8.5 10 12l3.5 3.5" strokeLinecap="round" strokeLinejoin="round" />
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

// SignOutIcon is a door with an arrow leading out of it.
export function SignOutIcon() {
  return (
    <ToolIcon>
      <path d="M10 20H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h4M15 16l4-4-4-4M19 12H9" />
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

// EmptyTrashIcon is a trash can with a cross, for deleting everything in the trash.
export function EmptyTrashIcon() {
  return (
    <ToolIcon>
      <path d="M4 7h16M9.5 7V4.5h5V7M6.5 7l1 12.5h9l1-12.5M10 11l4 5M14 11l-4 5" />
    </ToolIcon>
  );
}

export function FolderIcon({ open = false }: { open?: boolean }) {
  return (
    <svg className="folder-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" aria-hidden="true">
      {open ? (
        <path d="M3 19V6a1 1 0 0 1 1-1h5l2 2h8a1 1 0 0 1 1 1v2M3 19l2.6-8.2a1 1 0 0 1 1-.8H21l-2.7 8.3a1 1 0 0 1-1 .7z" />
      ) : (
        <path d="M3 18V6a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z" />
      )}
    </svg>
  );
}

export function NewFolderIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3 18V6a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1zM12 10v6M9 13h6" />
    </svg>
  );
}

// MoveIcon is a folder with an arrow into it, for moving a note to another folder.
export function MoveIcon() {
  return (
    <ToolIcon>
      <path d="M3 18V6a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1zM9 13h6M13 10.5l2.5 2.5-2.5 2.5" />
    </ToolIcon>
  );
}

export function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`tree-chevron${open ? ' open' : ''}`}
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="m9 6 6 6-6 6" />
    </svg>
  );
}

export function PencilIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4" />
    </svg>
  );
}

export function NewBoardIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M9 4v16M15 4v16" />
    </svg>
  );
}

export function GripIcon() {
  return (
    <svg width="12" height="18" viewBox="0 0 12 18" fill="currentColor" aria-hidden="true">
      <circle cx="3.5" cy="3.5" r="1.5" />
      <circle cx="8.5" cy="3.5" r="1.5" />
      <circle cx="3.5" cy="9" r="1.5" />
      <circle cx="8.5" cy="9" r="1.5" />
      <circle cx="3.5" cy="14.5" r="1.5" />
      <circle cx="8.5" cy="14.5" r="1.5" />
    </svg>
  );
}

// Small icons for task dates, repeats, priorities and reminders, sized to the text.
function InlineIcon({ children, size = 14 }: { children: ReactNode; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {children}
    </svg>
  );
}

export function CalendarIcon({ size }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <rect x="3.5" y="5" width="17" height="15.5" rx="2" />
      <path d="M3.5 10h17M8 3v4M16 3v4" />
    </InlineIcon>
  );
}

export function RepeatIcon({ size = 12 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <path d="M17 2l3 3-3 3" />
      <path d="M4 11V9a4 4 0 0 1 4-4h12" />
      <path d="M7 22l-3-3 3-3" />
      <path d="M20 13v2a4 4 0 0 1-4 4H4" />
    </InlineIcon>
  );
}

export function FlagIcon({ size = 14, filled = false }: { size?: number; filled?: boolean }) {
  return (
    <InlineIcon size={size}>
      <path d="M5 21V4" />
      <path d="M5 4h11l-2 4 2 4H5" fill={filled ? 'currentColor' : 'none'} />
    </InlineIcon>
  );
}

export function BellIcon({ size = 12 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <path d="M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15z" />
      <path d="M10 20.5a2 2 0 0 0 4 0" />
    </InlineIcon>
  );
}

export function ClockIcon({ size = 12 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </InlineIcon>
  );
}
