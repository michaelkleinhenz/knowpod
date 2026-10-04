import type { ReactNode } from 'react';
import type { IconKind } from '../lib/recordings';

// Small inline icons (stroke uses currentColor).

// NoteIcon shows a note as a page; its content says the note's type: a sound wave for audio
// recordings, lines of text with a heading for text notes, the reMarkable logo for documents imported
// from the reMarkable, the Pocket recorder for notes imported from Pocket AI, a picture for photos,
// "PDF" for uploaded PDFs, columns of cards for boards.
export function NoteIcon({ type, label }: { type: IconKind; label?: string }) {
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
      {type === 'photo' ? (
        <>
          <rect x="5.5" y="9.5" width="19" height="17" rx="2" fill="none" stroke="var(--color-muted)" opacity="0.7" />
          <circle cx="11" cy="14.5" r="2" fill="var(--color-warning)" opacity="0.8" />
          <path d="M6.5 25l6-6.5 4 4 3-3 4.5 5.5z" fill="var(--color-primary)" opacity="0.8" />
        </>
      ) : type === 'pdf' ? (
        <text x="15" y="23" textAnchor="middle" fill="var(--color-error)" fontFamily="system-ui, sans-serif" fontSize="8.5" fontWeight="700" letterSpacing="0.2">
          PDF
        </text>
      ) : type === 'document' ? (
        // The reMarkable logo: its "rM" monogram.
        <text x="15" y="23.5" textAnchor="middle" fill="var(--color-text)" fontFamily="Georgia, 'Times New Roman', serif" fontSize="13" fontWeight="600" letterSpacing="-0.6">
          rM
        </text>
      ) : type === 'remarkableText' ? (
        // The reMarkable monogram with a pen: a text note on the tablet, editable here.
        <>
          <text x="13.5" y="20" textAnchor="middle" fill="var(--color-text)" fontFamily="Georgia, 'Times New Roman', serif" fontSize="12" fontWeight="600" letterSpacing="-0.6">
            rM
          </text>
          <g transform="rotate(-45 20 28)">
            <rect x="14" y="26.6" width="10" height="2.8" rx="0.6" fill="var(--color-primary)" />
            <path d="M24 26.6l2.6 1.4-2.6 1.4z" fill="var(--color-text)" />
          </g>
        </>
      ) : type === 'pocket' ? (
        // The Pocket recorder: a small rounded device with its recording light, above a sound wave.
        <>
          <rect x="9.5" y="6.5" width="11" height="16" rx="4" fill="none" stroke="var(--color-text)" strokeWidth="1.6" />
          <circle cx="15" cy="11.5" r="1.7" fill="var(--color-error)" />
          <rect x="12.5" y="16" width="5" height="2" rx="1" fill="var(--color-muted)" opacity="0.55" />
          <g stroke="var(--color-primary)" strokeWidth="1.8" strokeLinecap="round" opacity="0.8">
            <path d="M9 29v1M12 27.5v4M15 26.5v6M18 27.5v4M21 29v1" />
          </g>
        </>
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

// FullscreenIcon is four corner brackets pointing outwards, for the editor's full-screen
// focus mode; with exit, they point inwards.
export function FullscreenIcon({ exit = false }: { exit?: boolean }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path
        d={exit ? 'M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5' : 'M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5'}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

// SidebarIcon is a window with a panel on its left; with collapsed, an arrow points out of the
// panel (to show it again), otherwise into it (to hide it).
export function SidebarIcon({ collapsed = false }: { collapsed?: boolean }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d={collapsed ? 'M9 4v16M13 10l2 2-2 2' : 'M9 4v16M16 10l-2 2 2 2'} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// HomeIcon is a house, for the home page with today's briefing.
export function HomeIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M4 11l8-7 8 7M6 9.5V20h4.5v-5.5h3V20H18V9.5" strokeLinecap="round" strokeLinejoin="round" />
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

export function PrintIcon() {
  return (
    <ToolIcon>
      <path d="M7 9V4h10v5M7 17H5a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-2" />
      <rect x="7" y="14" width="10" height="6" />
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

// FinishIcon: a double check mark, for checking off a repeating task for good.
export function FinishIcon() {
  return (
    <ToolIcon>
      <path d="m2.5 12.5 4.5 4.5L16.5 7.5M12 16.5l.5.5L22 7.5" />
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

// UsbIcon is a USB plug on its cable, for copying from a Pocket recorder by USB.
export function UsbIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M8 8V3h8v5M12 17v5" />
      <rect x="6" y="8" width="12" height="9" rx="2" />
      <path d="M10.5 5.5h.01M13.5 5.5h.01" />
    </svg>
  );
}

// WifiIcon is the WiFi waves, for copying from the Pocket over its WiFi (the Android app).
export function WifiIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M2 8.5a15 15 0 0 1 20 0M5 12a10 10 0 0 1 14 0M8.5 15.5a5 5 0 0 1 7 0" />
      <path d="M12 19h.01" />
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

// Icons of the timer and saved filters.
export function PlayIcon({ size = 14 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <path d="M8 5.5v13l10-6.5z" fill="currentColor" />
    </InlineIcon>
  );
}

// ChevronDownIcon minimizes the phone's recording screen.
export function ChevronDownIcon({ size = 14 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <path d="M6 9l6 6 6-6" />
    </InlineIcon>
  );
}

export function StopIcon({ size = 14 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <rect x="6.5" y="6.5" width="11" height="11" rx="1.5" fill="currentColor" />
    </InlineIcon>
  );
}

// FocusIcon is a tomato-shaped timer for focus sessions (Pomodoro).
export function FocusIcon({ size = 14 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <circle cx="12" cy="13.5" r="7" />
      <path d="M12 6.5V4M9.5 5l2.5 1.5L14.5 5M12 10v3.5l2 1.5" />
    </InlineIcon>
  );
}

export function StopwatchIcon({ size = 14 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <circle cx="12" cy="13.5" r="7.5" />
      <path d="M12 9.5v4l2.5 1.5M10 3h4M12 3v3M18.5 6.5l1.5-1.5" />
    </InlineIcon>
  );
}

export function FilterIcon({ size = 14 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <path d="M4 5h16l-6.2 7.4V19l-3.6-1.8v-4.8z" />
    </InlineIcon>
  );
}

export function BookmarkIcon({ size = 16 }: { size?: number }) {
  return (
    <InlineIcon size={size}>
      <path d="M6.5 4h11v16L12 16l-5.5 4z" />
    </InlineIcon>
  );
}

export function PinIcon({ size = 14, filled = false }: { size?: number; filled?: boolean }) {
  return (
    <InlineIcon size={size}>
      <path d="M9 4h6l-1 6 3.5 3.5h-11L10 10zM12 13.5V20" fill={filled ? 'currentColor' : 'none'} />
    </InlineIcon>
  );
}

// ShareIcon is two people: the note is (or can be) shared with others.
export function ShareIcon({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="9" cy="8" r="3.2" />
      <path d="M3 19.5c.6-3.3 3-5.2 6-5.2s5.4 1.9 6 5.2" />
      <path d="M15.5 5.2a3 3 0 0 1 0 5.6M17.5 14.6c1.8.7 3 2.4 3.4 4.9" />
    </svg>
  );
}

export function SparkleIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z" />
      <path d="M19 15.5l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z" />
    </svg>
  );
}

export function MicIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="9" y="3" width="6" height="11" rx="3" />
      <path d="M5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21" />
    </svg>
  );
}

export function CameraIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M4 8h3l1.8-2.5h6.4L17 8h3a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1z" />
      <circle cx="12" cy="13" r="3.5" />
    </svg>
  );
}

export function PauseIcon({ size = 14 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <rect x="6" y="5" width="4" height="14" rx="1" />
      <rect x="14" y="5" width="4" height="14" rx="1" />
    </svg>
  );
}

// HistoryIcon is a clock with an arrow going back: the note's earlier versions.
export function HistoryIcon({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3.5 12a8.5 8.5 0 1 0 2.5-6" />
      <path d="M3 4v4.5h4.5" />
      <path d="M12 7.5V12l3 2" />
    </svg>
  );
}

// GlobeIcon is the web: a note published on it.
export function GlobeIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.6 3.8 5.6 3.8 9s-1.3 6.4-3.8 9c-2.5-2.6-3.8-5.6-3.8-9S9.5 5.6 12 3z" />
    </svg>
  );
}
