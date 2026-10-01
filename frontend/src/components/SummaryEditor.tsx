import { Editor, Extension, InputRule, isTextSelection, Range } from '@tiptap/core';
import { Node as PMNode } from '@tiptap/pm/model';
import { Plugin, PluginKey } from '@tiptap/pm/state';
import { Decoration, DecorationSet } from '@tiptap/pm/view';
import Image from '@tiptap/extension-image';
import { TaskItem, TaskList } from '@tiptap/extension-list';
import { TableKit } from '@tiptap/extension-table';
import { Placeholder } from '@tiptap/extensions';
import { Markdown } from '@tiptap/markdown';
import { EditorContent, useEditor, useEditorState } from '@tiptap/react';
import { BubbleMenu } from '@tiptap/react/menus';
import StarterKit from '@tiptap/starter-kit';
import Suggestion, { SuggestionKeyDownProps, SuggestionProps } from '@tiptap/suggestion';
import { ReactNode, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { DUE_MARK, dueLabel, dueMarkFor, dueState } from '../lib/dueMarks';
import { imageWidth, MIN_IMAGE_WIDTH, withImageWidth } from '../lib/imageWidth';
import { errorText } from '../lib/errors';
import { matchNotes, NOTE_REF, noteByNumber } from '../lib/noteRefs';
import { iconKind, title } from '../lib/recordings';
import { fillTemplate, loadTemplate, Template } from '../lib/templates';
import { AiPanel, AiTargetExtension, aiTargetKey, insertMarkdown } from './AiWriter';
import { NoteIcon, SparkleIcon } from './Icons';

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
  // onConvertTask makes a task of a checklist item (title, and the full text when it is too
  // long for a title). The task is created next to the note; when it resolves, the item is
  // removed from the text.
  onConvertTask?: (title: string, markdown: string) => Promise<void>;
  // aiEnabled offers the AI's help with writing (the summary model is set up).
  aiEnabled?: boolean;
  // templates are offered by "/template"; their text goes in at the cursor, with {{title}}
  // filled in with title.
  templates?: Template[];
  title?: string;
}

// cleanMarkdown drops the "&nbsp;" lines that empty paragraphs become: Markdown has no
// empty paragraphs, and the lines would show up as text elsewhere.
function cleanMarkdown(md: string): string {
  return md
    .replace(/\n+&nbsp;(?=\n|$)/g, '')
    .replace(/^(&nbsp;\n+)+/, '')
    .trim();
}

// --- Images -------------------------------------------------------------------------------

// ResizableImage is the picture node with a handle in its bottom-right corner to drag it to
// another width; the width is stored in the src (see lib/imageWidth).
const ResizableImage = Image.extend({
  addNodeView() {
    return ({ node: initial, editor, getPos }) => {
      let node = initial;
      const dom = document.createElement('div');
      dom.className = 'img-resize';
      const img = document.createElement('img');
      const handle = document.createElement('span');
      handle.className = 'img-resize-handle';
      dom.append(img, handle);
      const apply = () => {
        img.src = node.attrs.src;
        img.alt = node.attrs.alt ?? '';
        const w = imageWidth(node.attrs.src);
        img.style.width = w ? `${w}px` : '';
        handle.style.display = editor.isEditable ? '' : 'none';
      };
      apply();
      handle.addEventListener('pointerdown', (down) => {
        if (!editor.isEditable) return;
        down.preventDefault();
        down.stopPropagation();
        const startX = down.clientX;
        const startWidth = img.getBoundingClientRect().width;
        const max = dom.parentElement?.clientWidth ?? Infinity;
        let width = startWidth;
        const move = (e: PointerEvent) => {
          width = Math.min(max, Math.max(MIN_IMAGE_WIDTH, startWidth + e.clientX - startX));
          img.style.width = `${width}px`;
        };
        const up = () => {
          window.removeEventListener('pointermove', move);
          window.removeEventListener('pointerup', up);
          const pos = getPos();
          if (pos === undefined || width === startWidth) return;
          editor.view.dispatch(editor.state.tr.setNodeMarkup(pos, undefined, { ...node.attrs, src: withImageWidth(node.attrs.src, width) }));
        };
        window.addEventListener('pointermove', move);
        window.addEventListener('pointerup', up);
      });
      return {
        dom,
        update(n) {
          if (n.type !== node.type) return false;
          node = n;
          apply();
          return true;
        },
        stopEvent: (e) => e.target === handle,
        ignoreMutation: () => true,
      };
    };
  },
});

// imageFiles returns the pictures among the files of a paste or drop.
function imageFiles(files: FileList | null | undefined): File[] {
  return Array.from(files ?? []).filter((f) => /^image\/(jpeg|png|webp|gif)$/.test(f.type));
}

// --- Slash commands ----------------------------------------------------------------------

// SlashUI opens the editor's menus from a slash command.
interface SlashUI {
  openAI: () => void;
  openTemplates: () => void;
  // offers says whether a command is available (the AI is set up, there are templates).
  offers: (id: string) => boolean;
}

interface SlashItem {
  id: string;
  icon: string;
  run: (editor: Editor, range: Range, ui: SlashUI) => void;
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
  { id: 'table', icon: '▦', run: (e, r) => e.chain().focus().deleteRange(r).insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run() },
  {
    id: 'template',
    icon: '❏',
    run: (e, r, ui) => {
      e.chain().focus().deleteRange(r).run();
      ui.openTemplates();
    },
  },
  {
    id: 'ai',
    icon: '✦',
    run: (e, r, ui) => {
      e.chain().focus().deleteRange(r).run();
      ui.openAI();
    },
  },
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

function slashCommands(bridge: MenuBridge<SlashItem>, label: (id: string) => string, ui: () => SlashUI) {
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
            return SLASH_ITEMS.filter((item) => ui().offers(item.id) && (!q || label(item.id).toLowerCase().includes(q) || item.id.toLowerCase().includes(q)));
          },
          command: ({ editor, range, props }) => props.run(editor, range, ui()),
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

// --- Due marks ("[today]") ---------------------------------------------------------------

// dueMarkDecorations colors the due marks in the text (not in code) by when they are due,
// with the date spelled out as tooltip.
function dueMarkDecorations(doc: PMNode): DecorationSet {
  const decos: Decoration[] = [];
  const now = new Date();
  doc.descendants((node, pos, parent) => {
    if (!node.isText || !node.text || parent?.type.spec.code || node.marks.some((m) => m.type.spec.code)) return;
    for (const m of node.text.matchAll(new RegExp(DUE_MARK))) {
      const from = pos + m.index!;
      decos.push(Decoration.inline(from, from + m[0].length, { class: `due-mark ${dueState(m[1], now)}`, title: dueLabel(m[1], m[2], now) }));
    }
  });
  return DecorationSet.create(doc, decos);
}

const dueMarksKey = new PluginKey<DecorationSet>('dueMarks');

// DueMarks turns a date typed in brackets ("[today]", "[fri]", "[5.10.]") into a due mark
// ("[2026-10-01]") as the "]" is typed, and colors the marks: overdue, today, this week. Other
// words in brackets stay as they are; Backspace right after undoes the change.
const DueMarks = Extension.create({
  name: 'dueMarks',
  addInputRules() {
    return [
      new InputRule({
        find: /\[([^[\]\n]{1,30})\]$/,
        handler: ({ state, range, match }) => {
          const mark = dueMarkFor(match[1]);
          if (!mark || mark === match[0]) return null;
          state.tr.insertText(mark, range.from, range.to);
        },
      }),
    ];
  },
  addProseMirrorPlugins() {
    return [
      new Plugin<DecorationSet>({
        key: dueMarksKey,
        state: {
          init: (_, state) => dueMarkDecorations(state.doc),
          apply: (tr, old) => (tr.docChanged ? dueMarkDecorations(tr.doc) : old),
        },
        props: {
          decorations: (state) => dueMarksKey.getState(state),
        },
      }),
    ];
  },
});

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

// TemplateMenu lists the templates after "/template"; the chosen one's text is inserted.
function TemplateMenu({ templates, rect, onChoose, onClose }: { templates: Template[]; rect: DOMRect; onChoose: (tpl: Template) => void; onClose: () => void }) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState(0);
  const { menu, pos } = useMenuPosition(rect, templates.length, selected);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
      else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') setSelected((i) => (i + (e.key === 'ArrowDown' ? 1 : -1) + templates.length) % templates.length);
      else if (e.key === 'Enter') onChoose(templates[selected]);
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    const onDown = (e: MouseEvent) => !menu.current?.contains(e.target as Node) && onClose();
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('mousedown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.removeEventListener('mousedown', onDown);
    };
  }, [templates, selected, onChoose, onClose, menu]);
  return (
    <div ref={menu} className="slash-menu" style={{ top: pos.top, left: pos.left }} role="listbox" aria-label={t('editor.templates.label')}>
      {templates.map((tpl, i) => (
        <button
          key={tpl.id}
          type="button"
          role="option"
          aria-selected={i === selected}
          className={i === selected ? 'selected' : ''}
          onMouseEnter={() => setSelected(i)}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onChoose(tpl)}
        >
          <span className="slash-icon" aria-hidden="true">
            ❏
          </span>
          <span className="slash-text">
            <span className="slash-title">{tpl.name}</span>
            <span className="slash-desc">{t(tpl.noteId ? 'templates.ownGroup' : 'templates.builtInGroup')}</span>
          </span>
        </button>
      ))}
    </div>
  );
}

// --- Selection bubble --------------------------------------------------------------------

// The formatting bubble (on a text selection) and the table bubble (in a table), named so
// they can be hidden by a transaction.
const formatMenuKey = new PluginKey('formatMenu');
const tableMenuKey = new PluginKey('tableMenu');

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

// TASK_TITLE_MAX is the longest task title, in characters.
const TASK_TITLE_MAX = 200;

const CONVERT_ICON =
  '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 3h4v4M13 3L7.5 8.5M11 9.5V12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h2.5"/></svg>';

// checklistItemText returns the text of a checklist item that can be made a task: an open
// item of one line, without sub-items. Others give "".
function checklistItemText(item: PMNode): string {
  if (item.attrs.checked || item.childCount !== 1) return '';
  return item.textContent.trim();
}

// taskItemWithConvert is the checklist item with a button at its right that makes a task of
// it (shown on hover, see .task-convert in styles.css). It builds on the item's own view.
function taskItemWithConvert(convert: (item: PMNode, getPos: () => number | undefined) => void, label: string) {
  return TaskItem.extend({
    addNodeView() {
      const parent = this.parent?.();
      return (props) => {
        const view = parent?.(props);
        if (!view) return {} as never;
        let item = props.node;
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'task-convert';
        button.contentEditable = 'false';
        button.title = label;
        button.setAttribute('aria-label', label);
        button.innerHTML = CONVERT_ICON;
        button.addEventListener('mousedown', (e) => e.preventDefault());
        button.addEventListener('click', (e) => {
          e.preventDefault();
          convert(item, props.getPos);
        });
        const sync = () => {
          button.hidden = !checklistItemText(item);
        };
        sync();
        view.dom.append(button);
        const update = view.update?.bind(view);
        view.update = (node, ...rest) => {
          const ok = update ? update(node, ...rest) : false;
          if (ok) {
            item = node;
            sync();
          }
          return ok;
        };
        return view;
      };
    },
  });
}

// SummaryEditor shows a summary as an always-editable document: clicking into the text
// places the cursor there, like in a word processor. There is no fixed toolbar: typing "/"
// at the start of a line opens a block menu, and selecting text shows a formatting bubble.
// The text is loaded from and read as Markdown, which is how summaries are stored; saving
// is done by the caller (useAutosave). It is loaded on demand (see Conversation.tsx).
export default function SummaryEditor({ markdown, onReady, onChange, onSaveShortcut, notes, noteId, onOpenNote, readOnly = false, onConvertTask, aiEnabled = false, templates, title: noteTitle }: Props) {
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
  const editableRef = useRef(!readOnly);
  editableRef.current = !readOnly;
  // Pictures pasted or dropped into the text are uploaded to the note; imagesRef is set once
  // the editor exists, since the handlers below are created with it.
  const [uploading, setUploading] = useState(0);
  const [imageError, setImageError] = useState('');
  const [convertError, setConvertError] = useState('');
  const convertTaskRef = useRef(onConvertTask);
  convertTaskRef.current = onConvertTask;
  // Set once the editor exists, like insertImagesRef.
  const convertItemRef = useRef<(item: PMNode, getPos: () => number | undefined) => void>(() => {});
  const insertImagesRef = useRef<(files: File[], pos?: number) => void>(() => {});
  // The AI panel and the template menu are opened from slash commands and shortcuts.
  const [aiOpen, setAiOpen] = useState(false);
  const aiOpenRef = useRef(false);
  aiOpenRef.current = aiOpen;
  const [templateMenu, setTemplateMenu] = useState<{ pos: number; rect: DOMRect } | null>(null);
  const slashUI = useRef<SlashUI>({ openAI: () => {}, openTemplates: () => {}, offers: () => true });

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: { levels: [1, 2, 3, 4] },
        underline: false, // not representable in Markdown
        link: { openOnClick: false, autolink: true, protocols: ['http', 'https', 'mailto'] },
      }),
      TaskList,
      taskItemWithConvert((item, getPos) => convertItemRef.current(item, getPos), t('editor.convertTask')).configure({ nested: true }),
      Placeholder.configure({ placeholder: t('editor.placeholder'), showOnlyCurrent: true }),
      ResizableImage.configure({ allowBase64: false }),
      // Column widths can't be kept in Markdown, so columns aren't resized.
      TableKit.configure({ table: { resizable: false } }),
      Markdown,
      AiTargetExtension,
      slashCommands(slash.bridge, (id) => t(`editor.slash.${id}`), () => slashUI.current),
      DueMarks,
      noteLinks(noteMenu.bridge, { notes: () => notesRef.current, noteId: () => noteIdRef.current }, t('noteRefs.openHint', { key: /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : t('noteRefs.ctrl') })),
    ],
    content: markdown,
    contentType: 'markdown',
    editable: !readOnly,
    editorProps: {
      attributes: { class: 'prose editor-content', 'aria-label': t('editor.content') },
      handleDOMEvents: {
        // "#12" opens on Ctrl/Cmd+click; a plain click places the cursor. Links open on a click
        // (unless text is being selected).
        click: (view, event) => {
          const ref = (event.target as HTMLElement).closest<HTMLElement>('.note-ref');
          if (ref && (event.metaKey || event.ctrlKey) && openNoteRef.current) {
            openNoteRef.current(Number(ref.dataset.note));
            return true;
          }
          const a = (event.target as HTMLElement).closest('a');
          if (a && (event.metaKey || event.ctrlKey || view.state.selection.empty)) {
            window.open(a.href, '_blank', 'noopener,noreferrer');
            return true;
          }
          return false;
        },
      },
      // A picture pasted (a screenshot, a copied image) or dropped (from the desktop) is
      // uploaded and shown in the text. Text that comes with it (Word) is pasted as usual.
      handlePaste: (_view, event) => {
        const files = imageFiles(event.clipboardData?.files);
        if (!files.length || !editableRef.current || event.clipboardData?.getData('text/plain')) return false;
        event.preventDefault();
        insertImagesRef.current(files);
        return true;
      },
      handleDrop: (view, event) => {
        const files = imageFiles(event.dataTransfer?.files);
        if (!files.length || !editableRef.current) return false;
        event.preventDefault();
        insertImagesRef.current(files, view.posAtCoords({ left: event.clientX, top: event.clientY })?.pos);
        return true;
      },
      handleKeyDown: (_view, event) => {
        if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
          event.preventDefault();
          onSaveShortcut();
          return true;
        }
        // Ctrl/Cmd+J asks the AI about the selected text, or to write at the cursor.
        if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'j' && slashUI.current.offers('ai')) {
          event.preventDefault();
          slashUI.current.openAI();
          return true;
        }
        return false;
      },
    },
    onCreate: ({ editor: e }) => onReady(() => cleanMarkdown(e.getMarkdown())),
    onUpdate: () => onChange(),
  });

  insertImagesRef.current = (files, pos) => {
    const id = noteIdRef.current;
    if (!id) return;
    setImageError('');
    setUploading((n) => n + files.length);
    void (async () => {
      let at = pos;
      for (const file of files) {
        try {
          const { url } = await api.uploadNoteImage(id, file);
          if (editor.isDestroyed) return;
          const chain = editor.chain().focus();
          if (at !== undefined) chain.insertContentAt(Math.min(at, editor.state.doc.content.size), { type: 'image', attrs: { src: url, alt: '' } }).run();
          else chain.setImage({ src: url, alt: '' }).run();
          at = undefined; // further pictures go after the cursor
        } catch (err) {
          setImageError(t('editor.imageFailed', { detail: errorText(err, t) }));
        } finally {
          setUploading((n) => n - 1);
        }
      }
    })();
  };

  // Makes a task of a checklist item, then takes the item out of the list. The text may have
  // changed while the task was being created, so the item is looked up again.
  convertItemRef.current = (item, getPos) => {
    const convert = convertTaskRef.current;
    const text = checklistItemText(item);
    if (!convert || !text || !editor.isEditable) return;
    const chars = Array.from(text);
    setConvertError('');
    void (async () => {
      try {
        await convert(chars.slice(0, TASK_TITLE_MAX).join(''), chars.length > TASK_TITLE_MAX ? text : '');
      } catch (err) {
        setConvertError(t('editor.convertFailed', { detail: errorText(err, t) }));
        return;
      }
      if (editor.isDestroyed) return;
      let at = getPos();
      if (at === undefined || !editor.state.doc.nodeAt(at)?.eq(item)) {
        at = undefined;
        editor.state.doc.descendants((n, pos) => {
          if (at !== undefined) return false;
          if (n.eq(item)) at = pos;
          return at === undefined;
        });
      }
      if (at === undefined) return; // the item was edited or removed meanwhile; the task stays
      // deleteRange also removes the list when this was its last item.
      editor.view.dispatch(editor.state.tr.deleteRange(at, at + item.nodeSize).scrollIntoView());
    })();
  };

  slashUI.current = {
    // Markdown can't keep a table in a table, so none is offered in one.
    offers: (id) => (id === 'ai' ? aiEnabled && editor.isEditable : id === 'template' ? !!templates?.length : id === 'table' ? !editor.isActive('table') : true),
    openAI: () => {
      if (!aiEnabled || !editor.isEditable) return;
      const { from, to } = editor.state.selection;
      aiOpenRef.current = true;
      // The bubbles give way to the panel (they would stay, as it holds the focus).
      editor.view.dispatch(editor.state.tr.setMeta(aiTargetKey, { from, to }).setMeta(formatMenuKey, 'hide').setMeta(tableMenuKey, 'hide'));
      setTemplateMenu(null);
      setAiOpen(true);
    },
    openTemplates: () => {
      const pos = editor.state.selection.from;
      const c = editor.view.coordsAtPos(pos);
      setAiOpen(false);
      setTemplateMenu({ pos, rect: new DOMRect(c.left, c.top, 0, c.bottom - c.top) });
    },
  };

  // insertTemplate puts a template's text in where "/template" was typed.
  const [templateError, setTemplateError] = useState('');
  async function insertTemplate(tpl: Template, pos: number) {
    setTemplateMenu(null);
    setTemplateError('');
    try {
      const text = fillTemplate(await loadTemplate(tpl), { title: noteTitle });
      if (!editor.isDestroyed && text) insertMarkdown(editor, pos, text);
    } catch (err) {
      setTemplateError(t('editor.templates.failed', { detail: errorText(err, t) }));
    }
  }

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
      headerRow: e.isActive('tableHeader'),
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
      <BubbleMenu
        editor={editor}
        pluginKey={formatMenuKey}
        className="bubble-menu"
        options={{ placement: 'top' }}
        // As by default (a text selection in the focused editor), but not over the AI panel.
        shouldShow={({ editor: e, view, state, from, to, element }) => {
          if (aiOpenRef.current || !e.isEditable || state.selection.empty) return false;
          const emptyText = !state.doc.textBetween(from, to).length && isTextSelection(state.selection);
          return !emptyText && (view.hasFocus() || element.contains(document.activeElement));
        }}
      >
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
        {aiEnabled && (
          <>
            <span className="bubble-sep" />
            <BubbleButton label={t('editor.ai.button')} onClick={() => slashUI.current.openAI()}>
              <span className="bubble-ai">
                <SparkleIcon size={14} /> {t('editor.ai.button')}
              </span>
            </BubbleButton>
          </>
        )}
      </BubbleMenu>
      <BubbleMenu
        editor={editor}
        pluginKey={tableMenuKey}
        className="bubble-menu table-menu"
        options={{ placement: 'top-start' }}
        shouldShow={({ editor: e, state }) => e.isEditable && state.selection.empty && e.isActive('table') && !aiOpen}
        getReferencedVirtualElement={() => {
          const at = editor.view.domAtPos(editor.state.selection.from).node;
          const table = (at instanceof Element ? at : at.parentElement)?.closest('table');
          return table ? { getBoundingClientRect: () => table.getBoundingClientRect() } : null;
        }}
      >
        <span className="table-menu-label">{t('editor.table.label')}</span>
        <BubbleButton label={t('editor.table.addRowBefore')} onClick={() => chain().addRowBefore().run()}>
          {t('editor.table.addRowBefore')}
        </BubbleButton>
        <BubbleButton label={t('editor.table.addRowAfter')} onClick={() => chain().addRowAfter().run()}>
          {t('editor.table.addRowAfter')}
        </BubbleButton>
        <BubbleButton label={t('editor.table.addColumnBefore')} onClick={() => chain().addColumnBefore().run()}>
          {t('editor.table.addColumnBefore')}
        </BubbleButton>
        <BubbleButton label={t('editor.table.addColumnAfter')} onClick={() => chain().addColumnAfter().run()}>
          {t('editor.table.addColumnAfter')}
        </BubbleButton>
        <span className="bubble-sep" />
        <BubbleButton label={t('editor.table.deleteRow')} onClick={() => chain().deleteRow().run()}>
          {t('editor.table.deleteRow')}
        </BubbleButton>
        <BubbleButton label={t('editor.table.deleteColumn')} onClick={() => chain().deleteColumn().run()}>
          {t('editor.table.deleteColumn')}
        </BubbleButton>
        <BubbleButton label={t('editor.table.deleteTable')} onClick={() => chain().deleteTable().run()}>
          {t('editor.table.deleteTable')}
        </BubbleButton>
      </BubbleMenu>
      <EditorContent editor={editor} />
      {uploading > 0 && <p className="editor-status">{t('editor.imageUploading')}</p>}
      {imageError && (
        <p className="editor-status error" role="alert">
          {imageError}
        </p>
      )}
      {convertError && (
        <p className="editor-status error" role="alert">
          {convertError}
        </p>
      )}
      {templateError && (
        <p className="editor-status error" role="alert">
          {templateError}
        </p>
      )}
      {aiOpen && <AiPanel editor={editor} onClose={() => setAiOpen(false)} />}
      {templateMenu && templates && (
        <TemplateMenu templates={templates} rect={templateMenu.rect} onChoose={(tpl) => void insertTemplate(tpl, templateMenu.pos)} onClose={() => setTemplateMenu(null)} />
      )}
      {slash.state && <SlashMenu state={slash.state} onHover={slash.hover} />}
      {noteMenu.state && <NoteMenu state={noteMenu.state} onHover={noteMenu.hover} />}
    </div>
  );
}
