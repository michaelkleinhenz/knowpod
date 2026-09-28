import { Editor, Extension, Range } from '@tiptap/core';
import { Node as PMNode } from '@tiptap/pm/model';
import { Plugin, PluginKey } from '@tiptap/pm/state';
import { Decoration, DecorationSet } from '@tiptap/pm/view';
import { TaskItem, TaskList } from '@tiptap/extension-list';
import { Placeholder } from '@tiptap/extensions';
import { Markdown } from '@tiptap/markdown';
import { EditorContent, useEditor, useEditorState } from '@tiptap/react';
import { BubbleMenu } from '@tiptap/react/menus';
import StarterKit from '@tiptap/starter-kit';
import Suggestion, { SuggestionKeyDownProps, SuggestionProps } from '@tiptap/suggestion';
import { ReactNode, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Recording } from '../api/client';
import { matchNotes, NOTE_REF, noteByNumber } from '../lib/noteRefs';
import { iconKind, title } from '../lib/recordings';
import { NoteIcon } from './Icons';

interface Props {
  markdown: string;
  // onReady hands over a function that returns the current text as Markdown.
  onReady: (getMarkdown: () => string) => void;
  // onChange is called after every edit.
  onChange: () => void;
  // onSaveShortcut is called for Ctrl/Cmd+S.
  onSaveShortcut: () => void;
  // notes are the user's notes, offered after "#" is typed; noteId is the note being edited.
  notes?: Recording[] | null;
  noteId?: string;
  // onOpenNote opens the note with the number ("#12" is Ctrl/Cmd+clicked).
  onOpenNote?: (n: number) => void;
  // readOnly shows the text without letting it be changed (a note shared for viewing).
  readOnly?: boolean;
}

// cleanMarkdown drops the "&nbsp;" lines that empty paragraphs become: Markdown has no
// empty paragraphs, and the lines would show up as text elsewhere.
function cleanMarkdown(md: string): string {
  return md
    .replace(/\n+&nbsp;(?=\n|$)/g, '')
    .replace(/^(&nbsp;\n+)+/, '')
    .trim();
}

// --- Slash commands ----------------------------------------------------------------------

interface SlashItem {
  id: string;
  icon: string;
  run: (editor: Editor, range: Range) => void;
}

const SLASH_ITEMS: SlashItem[] = [
  { id: 'heading1', icon: 'H₁', run: (e, r) => e.chain().focus().deleteRange(r).setNode('heading', { level: 1 }).run() },
  { id: 'heading2', icon: 'H₂', run: (e, r) => e.chain().focus().deleteRange(r).setNode('heading', { level: 2 }).run() },
  { id: 'heading3', icon: 'H₃', run: (e, r) => e.chain().focus().deleteRange(r).setNode('heading', { level: 3 }).run() },
  { id: 'bulletList', icon: '•≡', run: (e, r) => e.chain().focus().deleteRange(r).toggleBulletList().run() },
  { id: 'orderedList', icon: '1≡', run: (e, r) => e.chain().focus().deleteRange(r).toggleOrderedList().run() },
  // A checklist is part of the text ("- [ ]" / "- [x]" in the Markdown); it doesn't create tasks.
  { id: 'checklist', icon: '☑', run: (e, r) => e.chain().focus().deleteRange(r).toggleTaskList().run() },
  { id: 'codeBlock', icon: '</>', run: (e, r) => e.chain().focus().deleteRange(r).toggleCodeBlock().run() },
  { id: 'quote', icon: '❝', run: (e, r) => e.chain().focus().deleteRange(r).toggleBlockquote().run() },
  { id: 'divider', icon: '—', run: (e, r) => e.chain().focus().deleteRange(r).setHorizontalRule().run() },
];

// MenuState is an open suggestion menu: its entries, where the cursor is, and the entry
// chosen with the arrow keys.
interface MenuState<T> {
  items: T[];
  rect: DOMRect | null;
  selected: number;
  choose: (item: T) => void;
}

type SlashState = MenuState<SlashItem>;

// MenuBridge connects a suggestion plugin (outside React) with its menu component.
interface MenuBridge<T> {
  set: (s: MenuState<T> | null) => void;
  get: () => MenuState<T> | null;
}

// menuRender drives a suggestion menu through its bridge: arrow keys choose an entry, Enter
// or Tab takes it, Escape closes the menu.
function menuRender<T>(bridge: MenuBridge<T>) {
  return () => {
    const update = (p: SuggestionProps<T>) =>
      bridge.set({
        items: p.items,
        rect: p.clientRect?.() ?? null,
        selected: Math.min(bridge.get()?.selected ?? 0, Math.max(p.items.length - 1, 0)),
        choose: (item) => p.command(item),
      });
    return {
      onStart: (p: SuggestionProps<T>) => {
        bridge.set(null);
        update(p);
      },
      onUpdate: update,
      onKeyDown: ({ event }: SuggestionKeyDownProps) => {
        const s = bridge.get();
        if (!s) return false;
        if (event.key === 'Escape') {
          bridge.set(null);
          return true;
        }
        if (!s.items.length) return false;
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          const step = event.key === 'ArrowDown' ? 1 : -1;
          bridge.set({ ...s, selected: (s.selected + step + s.items.length) % s.items.length });
          return true;
        }
        if (event.key === 'Enter' || event.key === 'Tab') {
          s.choose(s.items[s.selected]);
          return true;
        }
        return false;
      },
      onExit: () => bridge.set(null),
    };
  };
}

// useMenuBridge keeps a suggestion menu's state for React and for the plugin.
function useMenuBridge<T>() {
  const [state, setState] = useState<MenuState<T> | null>(null);
  const ref = useRef<MenuState<T> | null>(null);
  const bridge = useRef<MenuBridge<T>>({
    set: (s) => {
      ref.current = s;
      setState(s);
    },
    get: () => ref.current,
  });
  const hover = (i: number) => {
    const s = ref.current;
    if (s) bridge.current.set({ ...s, selected: i });
  };
  return { state, bridge: bridge.current, hover };
}

function slashCommands(bridge: MenuBridge<SlashItem>, label: (id: string) => string) {
  return Extension.create({
    name: 'slashCommands',
    addProseMirrorPlugins() {
      return [
        Suggestion<SlashItem>({
          editor: this.editor,
          char: '/',
          startOfLine: true, // "/" at the start of a line, as in word processors
          items: ({ query }) => {
            const q = query.toLowerCase();
            return SLASH_ITEMS.filter((item) => !q || label(item.id).toLowerCase().includes(q) || item.id.toLowerCase().includes(q));
          },
          command: ({ editor, range, props }) => props.run(editor, range),
          render: menuRender(bridge),
        }),
      ];
    },
  });
}

// --- Links to other notes ("#12") -------------------------------------------------------

interface NoteLinkOptions {
  // notes returns the user's notes, noteId the note being edited.
  notes: () => Recording[];
  noteId: () => string | undefined;
}

// noteRefDecorations marks "#12" in the text (not in code) as a link to note 12, with the
// note's title as tooltip.
function noteRefDecorations(doc: PMNode, notes: Recording[], hint: string): DecorationSet {
  const decos: Decoration[] = [];
  doc.descendants((node, pos, parent) => {
    if (!node.isText || !node.text || parent?.type.spec.code || node.marks.some((m) => m.type.spec.code)) return;
    for (const m of node.text.matchAll(new RegExp(NOTE_REF))) {
      const n = Number(m[1]);
      const note = noteByNumber(notes, n);
      const from = pos + m.index!;
      decos.push(
        Decoration.inline(from, from + m[0].length, {
          class: `note-ref${note || notes.length === 0 ? '' : ' unknown'}`,
          'data-note': String(n),
          title: note ? `${title(note)} · ${hint}` : hint,
        }),
      );
    }
  });
  return DecorationSet.create(doc, decos);
}

// noteLinksKey names the link decorations; a transaction with it as meta redraws them.
const noteLinksKey = new PluginKey<DecorationSet>('noteLinks');

// noteLinks offers the user's notes after "#" is typed (filtered by number or title as the
// user types) and inserts the chosen note's "#12"; references in the text are shown as links.
function noteLinks(bridge: MenuBridge<Recording>, opts: NoteLinkOptions, hint: string) {
  return Extension.create({
    name: 'noteLinks',
    addProseMirrorPlugins() {
      return [
        Suggestion<Recording>({
          editor: this.editor,
          pluginKey: new PluginKey('noteLinkSuggestion'),
          char: '#',
          items: ({ query }) => matchNotes(opts.notes(), query, opts.noteId()),
          command: ({ editor, range, props }) =>
            editor
              .chain()
              .focus()
              .insertContentAt(range, { type: 'text', text: `#${props.number} ` })
              .run(),
          render: menuRender(bridge),
        }),
        new Plugin<DecorationSet>({
          key: noteLinksKey,
          state: {
            init: (_, state) => noteRefDecorations(state.doc, opts.notes(), hint),
            apply: (tr, old) => (tr.docChanged || tr.getMeta(noteLinksKey) ? noteRefDecorations(tr.doc, opts.notes(), hint) : old),
          },
          props: {
            decorations: (state) => noteLinksKey.getState(state),
          },
        }),
      ];
    },
  });
}

// NoteMenu lists the notes offered after typing "#": their type, number and title.
function NoteMenu({ state, onHover }: { state: MenuState<Recording>; onHover: (i: number) => void }) {
  const { t } = useTranslation();
  const { menu, pos } = useMenuPosition(state.rect, state.items.length, state.selected);
  return (
    <div ref={menu} className="slash-menu note-menu" style={{ top: pos.top, left: pos.left }} role="listbox" aria-label={t('noteRefs.menu')}>
      {state.items.length === 0 && <div className="slash-empty">{t('noteRefs.noMatches')}</div>}
      {state.items.map((r, i) => (
        <button
          key={r.id}
          type="button"
          role="option"
          aria-selected={i === state.selected}
          className={i === state.selected ? 'selected' : ''}
          onMouseEnter={() => onHover(i)}
          onMouseDown={(e) => e.preventDefault()} // keep the editor's focus
          onClick={() => state.choose(r)}
        >
          <NoteIcon type={iconKind(r)} />
          <span className="note-menu-number">#{r.number}</span>
          <span className="note-menu-title">{title(r)}</span>
        </button>
      ))}
    </div>
  );
}

// useMenuPosition places a suggestion menu below the cursor, or above it when there's no
// room, and keeps the selected entry in view.
function useMenuPosition(rect: DOMRect | null, count: number, selected: number) {
  const menu = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number }>({ top: -9999, left: -9999 });
  useLayoutEffect(() => {
    const r = rect;
    const el = menu.current;
    if (!r || !el) return;
    const h = el.offsetHeight;
    const w = el.offsetWidth;
    const below = r.bottom + 6 + h <= window.innerHeight;
    setPos({
      top: below ? r.bottom + 6 : Math.max(8, r.top - h - 6),
      left: Math.max(8, Math.min(r.left, window.innerWidth - w - 8)),
    });
  }, [rect, count]);
  useEffect(() => {
    menu.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [selected]);
  return { menu, pos };
}

// SlashMenu is the block menu shown after typing "/" at the start of a line.
function SlashMenu({ state, onHover }: { state: SlashState; onHover: (i: number) => void }) {
  const { t } = useTranslation();
  const { menu, pos } = useMenuPosition(state.rect, state.items.length, state.selected);

  return (
    <div ref={menu} className="slash-menu" style={{ top: pos.top, left: pos.left }} role="listbox" aria-label={t('editor.slash.label')}>
      {state.items.length === 0 && <div className="slash-empty">{t('common.noMatches')}</div>}
      {state.items.map((item, i) => (
        <button
          key={item.id}
          type="button"
          role="option"
          aria-selected={i === state.selected}
          className={i === state.selected ? 'selected' : ''}
          onMouseEnter={() => onHover(i)}
          onMouseDown={(e) => e.preventDefault()} // keep the editor's focus
          onClick={() => state.choose(item)}
        >
          <span className="slash-icon" aria-hidden="true">
            {item.icon}
          </span>
          <span className="slash-text">
            <span className="slash-title">{t(`editor.slash.${item.id}`)}</span>
            <span className="slash-desc">{t(`editor.slash.${item.id}Hint`)}</span>
          </span>
        </button>
      ))}
    </div>
  );
}

// --- Selection bubble --------------------------------------------------------------------

function BubbleButton(props: { label: string; active?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      className={`bubble-button${props.active ? ' active' : ''}`}
      title={props.label}
      aria-label={props.label}
      aria-pressed={props.active}
      onMouseDown={(e) => e.preventDefault()}
      onClick={props.onClick}
    >
      {props.children}
    </button>
  );
}

// SummaryEditor shows a summary as an always-editable document: clicking into the text
// places the cursor there, like in a word processor. There is no fixed toolbar: typing "/"
// at the start of a line opens a block menu, and selecting text shows a formatting bubble.
// The text is loaded from and read as Markdown, which is how summaries are stored; saving
// is done by the caller (useAutosave). It is loaded on demand (see Conversation.tsx).
export default function SummaryEditor({ markdown, onReady, onChange, onSaveShortcut, notes, noteId, onOpenNote, readOnly = false }: Props) {
  const { t } = useTranslation();
  const slash = useMenuBridge<SlashItem>();
  const noteMenu = useMenuBridge<Recording>();
  // The plugins read the latest notes through these.
  const notesRef = useRef<Recording[]>(notes ?? []);
  notesRef.current = notes ?? [];
  const noteIdRef = useRef(noteId);
  noteIdRef.current = noteId;
  const openNoteRef = useRef(onOpenNote);
  openNoteRef.current = onOpenNote;

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: { levels: [1, 2, 3, 4] },
        underline: false, // not representable in Markdown
        link: { openOnClick: false, autolink: true, protocols: ['http', 'https', 'mailto'] },
      }),
      TaskList,
      TaskItem.configure({ nested: true }),
      Placeholder.configure({ placeholder: t('editor.placeholder'), showOnlyCurrent: true }),
      Markdown,
      slashCommands(slash.bridge, (id) => t(`editor.slash.${id}`)),
      noteLinks(noteMenu.bridge, { notes: () => notesRef.current, noteId: () => noteIdRef.current }, t('noteRefs.openHint', { key: /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : t('noteRefs.ctrl') })),
    ],
    content: markdown,
    contentType: 'markdown',
    editable: !readOnly,
    editorProps: {
      attributes: { class: 'prose editor-content', 'aria-label': t('editor.content') },
      handleDOMEvents: {
        // Links and "#12" open on Ctrl/Cmd+click; a plain click places the cursor.
        click: (_view, event) => {
          const ref = (event.target as HTMLElement).closest<HTMLElement>('.note-ref');
          if (ref && (event.metaKey || event.ctrlKey) && openNoteRef.current) {
            openNoteRef.current(Number(ref.dataset.note));
            return true;
          }
          const a = (event.target as HTMLElement).closest('a');
          if (a && (event.metaKey || event.ctrlKey)) {
            window.open(a.href, '_blank', 'noopener,noreferrer');
            return true;
          }
          return false;
        },
      },
      handleKeyDown: (_view, event) => {
        if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
          event.preventDefault();
          onSaveShortcut();
          return true;
        }
        return false;
      },
    },
    onCreate: ({ editor: e }) => onReady(() => cleanMarkdown(e.getMarkdown())),
    onUpdate: () => onChange(),
  });

  // The owner can make a note editable or read-only for the user while it is open.
  useEffect(() => {
    if (!editor.isDestroyed && editor.isEditable === readOnly) editor.setEditable(!readOnly, false);
  }, [editor, readOnly]);

  // Redraw the "#12" links when the notes (their titles, which exist) change.
  const notesKey = (notes ?? []).map((r) => `${r.number}:${title(r)}`).join('|');
  useEffect(() => {
    if (!editor.isDestroyed) editor.view.dispatch(editor.state.tr.setMeta(noteLinksKey, true));
  }, [editor, notesKey]);

  const marks = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e.isActive('bold'),
      italic: e.isActive('italic'),
      strike: e.isActive('strike'),
      code: e.isActive('code'),
      link: e.isActive('link'),
      bulletList: e.isActive('bulletList'),
      orderedList: e.isActive('orderedList'),
      checklist: e.isActive('taskList'),
    }),
  });

  function setLink() {
    const previous = editor.getAttributes('link').href as string | undefined;
    const url = window.prompt(t('editor.linkPrompt'), previous ?? 'https://');
    if (url === null) return;
    const chain = editor.chain().focus().extendMarkRange('link');
    if (!url.trim() || url.trim() === 'https://') chain.unsetLink().run();
    else chain.setLink({ href: url.trim() }).run();
  }

  const chain = () => editor.chain().focus();
  return (
    <div className="doc-editor">
      <BubbleMenu editor={editor} className="bubble-menu" options={{ placement: 'top' }}>
        <BubbleButton label={t('editor.bold')} active={marks.bold} onClick={() => chain().toggleBold().run()}>
          <b>B</b>
        </BubbleButton>
        <BubbleButton label={t('editor.italic')} active={marks.italic} onClick={() => chain().toggleItalic().run()}>
          <i>I</i>
        </BubbleButton>
        <BubbleButton label={t('editor.strike')} active={marks.strike} onClick={() => chain().toggleStrike().run()}>
          <s>S</s>
        </BubbleButton>
        <BubbleButton label={t('editor.code')} active={marks.code} onClick={() => chain().toggleCode().run()}>
          {'</>'}
        </BubbleButton>
        <span className="bubble-sep" />
        <BubbleButton label={t('editor.bulletList')} active={marks.bulletList} onClick={() => chain().toggleBulletList().run()}>
          •≡
        </BubbleButton>
        <BubbleButton label={t('editor.orderedList')} active={marks.orderedList} onClick={() => chain().toggleOrderedList().run()}>
          1≡
        </BubbleButton>
        <BubbleButton label={t('editor.checklist')} active={marks.checklist} onClick={() => chain().toggleTaskList().run()}>
          ☑
        </BubbleButton>
        <span className="bubble-sep" />
        <BubbleButton label={t('editor.link')} active={marks.link} onClick={setLink}>
          {t('editor.link')}
        </BubbleButton>
      </BubbleMenu>
      <EditorContent editor={editor} />
      {slash.state && <SlashMenu state={slash.state} onHover={slash.hover} />}
      {noteMenu.state && <NoteMenu state={noteMenu.state} onHover={noteMenu.hover} />}
    </div>
  );
}
