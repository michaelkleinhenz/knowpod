// Drafts keep a note's unsaved edits (title and text) in the browser's storage until the
// server has them, so they survive anything that stops a save from getting through: the
// session ending, the connection dropping, the app being closed or reloaded. Opening the
// note again brings the edits back and saves them (see useAutosave).

export interface Draft {
  // The user who made the edits; another user's drafts are never shown.
  userId: string;
  title: string;
  markdown: string;
  // The revision of the text the edits were made on (undefined: none known).
  revision?: number;
  savedAt: string;
}

const PREFIX = 'knowpod.draft.';

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null; // storage disabled
  }
}

// readDraft returns the user's draft of a note, if one is kept.
export function readDraft(noteId: string, userId: string | undefined): Draft | null {
  if (!userId) return null;
  try {
    const raw = storage()?.getItem(PREFIX + noteId);
    if (!raw) return null;
    const d = JSON.parse(raw) as Draft;
    if (d.userId !== userId || typeof d.markdown !== 'string' || typeof d.title !== 'string') return null;
    return d;
  } catch {
    return null;
  }
}

// writeDraft keeps the user's unsaved edits of a note.
export function writeDraft(noteId: string, draft: Omit<Draft, 'savedAt'>): void {
  try {
    storage()?.setItem(PREFIX + noteId, JSON.stringify({ ...draft, savedAt: new Date().toISOString() }));
  } catch {
    // storage full or disabled: the edits stay in the open editor only
  }
}

// removeDraft forgets a note's draft, once it is saved or the edits were given up.
export function removeDraft(noteId: string): void {
  try {
    storage()?.removeItem(PREFIX + noteId);
  } catch {
    // nothing kept
  }
}

// clearDrafts forgets the drafts of every user except keepUserId (when someone else signs in
// on this device).
export function clearDrafts(keepUserId?: string): void {
  const s = storage();
  if (!s) return;
  try {
    for (let i = s.length - 1; i >= 0; i--) {
      const key = s.key(i);
      if (!key?.startsWith(PREFIX)) continue;
      let owner: string | undefined;
      try {
        owner = (JSON.parse(s.getItem(key) ?? '{}') as Partial<Draft>).userId;
      } catch {
        owner = undefined;
      }
      if (!keepUserId || owner !== keepUserId) s.removeItem(key);
    }
  } catch {
    // storage disabled
  }
}
