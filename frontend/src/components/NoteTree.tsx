import { ComponentProps, ReactNode, useMemo } from 'react';
import { Recording } from '../api/client';
import { subNotes, withSubNotes } from '../lib/folders';
import { setOpen, useOpen } from '../lib/treeOpen';
import { NoteRow } from './NoteRow';

// MAX_DEPTH stops walking notes whose parents loop (which the server doesn't allow).
const MAX_DEPTH = 32;

export interface NoteTree {
  // subs maps each note ID to its sub-notes, by title.
  subs: Map<string, Recording[]>;
  // roots are the notes that are no sub-note of a known note.
  roots: Recording[];
  // shown counts the listed notes in each note: itself and its sub-notes at any depth.
  // Notes counting 0 are hidden.
  shown: Map<string, number>;
}

// useNoteTree builds the tree of notes and their sub-notes from all notes, so that sub-notes
// found by a search show under their parents; listed are the notes to show (those found,
// or all).
export function useNoteTree(all: Recording[], listed: Recording[]): NoteTree {
  return useMemo(() => {
    const subs = subNotes(all);
    const withParent = new Set([...subs.values()].flat().map((r) => r.id));
    const roots = all.filter((r) => !withParent.has(r.id));
    const ids = new Set(listed.map((r) => r.id));
    const shown = new Map<string, number>();
    const count = (r: Recording, depth: number): number => {
      const below = depth < MAX_DEPTH ? (subs.get(r.id) ?? []) : [];
      const n = (ids.has(r.id) ? 1 : 0) + below.reduce((s, c) => s + count(c, depth + 1), 0);
      shown.set(r.id, n);
      return n;
    };
    for (const r of roots) count(r, 0);
    return { subs, roots, shown };
  }, [all, listed]);
}

type RowProps = Pick<ComponentProps<typeof NoteRow>, 'onDragStart' | 'lineProps' | 'drop'>;

interface Props {
  list: Recording[];
  tree: NoteTree;
  // searching opens all notes, to show the sub-notes found.
  searching: boolean;
  activeId?: string;
  aiReady: boolean;
  // meta is the text at the end of a note's row, at its depth (0 for list's notes).
  meta: (r: Recording, depth: number) => string;
  // taskDate shows the due date on the tasks of list (their sub-notes always show it).
  taskDate?: boolean;
  onSetDone: (r: Recording, done: boolean) => void;
  onNewSub?: (parent: Recording) => void;
  onTrash?: (r: Recording) => void;
  // rowProps adds to each row, e.g. to drag it or drop notes onto it.
  rowProps?: (r: Recording) => RowProps;
  depth?: number;
}

// NoteTreeRows lists notes with their sub-notes, which open and close like folders (in all
// of the sidebar's views together).
export function NoteTreeRows({ list, tree, searching, activeId, aiReady, meta, taskDate, onSetDone, onNewSub, onTrash, rowProps, depth = 0 }: Props): ReactNode {
  const open = useOpen();
  return list.map((r) => {
    if (!tree.shown.get(r.id)) return null;
    const kids = depth < MAX_DEPTH ? (tree.subs.get(r.id) ?? []).filter((c) => tree.shown.get(c.id)) : [];
    const isOpen = searching || open.has(r.id);
    return (
      <NoteRow
        key={r.id}
        rec={r}
        active={r.id === activeId}
        aiReady={aiReady}
        meta={meta(r, depth)}
        taskDate={depth === 0 ? taskDate : true}
        onSetDone={onSetDone}
        onNewSub={onNewSub}
        onTrash={onTrash}
        sub={
          kids.length > 0
            ? {
                count: kids.length,
                open: isOpen,
                // Alt+click opens or closes the note's whole tree.
                onToggle: (e) => setOpen(e.altKey ? withSubNotes(r.id, tree.subs) : [r.id], !isOpen),
              }
            : undefined
        }
        {...rowProps?.(r)}
      >
        {kids.length > 0 && isOpen && (
          <ul className="tree-children sub-note-list">
            <NoteTreeRows
              list={kids}
              tree={tree}
              searching={searching}
              activeId={activeId}
              aiReady={aiReady}
              meta={meta}
              taskDate={taskDate}
              onSetDone={onSetDone}
              onNewSub={onNewSub}
              onTrash={onTrash}
              rowProps={rowProps}
              depth={depth + 1}
            />
          </ul>
        )}
      </NoteRow>
    );
  });
}
