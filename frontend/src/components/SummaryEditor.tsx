import { Editor, Extension, Range } from '@tiptap/core';
import { TaskItem, TaskList } from '@tiptap/extension-list';
import { Placeholder } from '@tiptap/extensions';
import { Markdown } from '@tiptap/markdown';
import { EditorContent, useEditor, useEditorState } from '@tiptap/react';
import { BubbleMenu } from '@tiptap/react/menus';
import StarterKit from '@tiptap/starter-kit';
import Suggestion, { SuggestionKeyDownProps, SuggestionProps } from '@tiptap/suggestion';
import { ReactNode, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

interface Props {
  markdown: string;
  // onReady hands over a function that returns the current text as Markdown.
  onReady: (getMarkdown: () => string) => void;
  // onChange is called after every edit.
  onChange: () => void;
  // onSaveShortcut is called for Ctrl/Cmd+S.
  onSaveShortcut: () => void;
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
  { id: 'taskList', icon: '☑', run: (e, r) => e.chain().focus().deleteRange(r).toggleTaskList().run() },
  { id: 'codeBlock', icon: '</>', run: (e, r) => e.chain().focus().deleteRange(r).toggleCodeBlock().run() },
  { id: 'quote', icon: '❝', run: (e, r) => e.chain().focus().deleteRange(r).toggleBlockquote().run() },
  { id: 'divider', icon: '—', run: (e, r) => e.chain().focus().deleteRange(r).setHorizontalRule().run() },
];

interface SlashState {
  items: SlashItem[];
  rect: DOMRect | null;
  selected: number;
  choose: (item: SlashItem) => void;
}

// SlashBridge connects the suggestion plugin (outside React) with the menu component.
interface SlashBridge {
  set: (s: SlashState | null) => void;
  get: () => SlashState | null;
}

function slashCommands(bridge: SlashBridge, label: (id: string) => string) {
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
          render: () => {
            const update = (p: SuggestionProps<SlashItem>) =>
              bridge.set({
                items: p.items,
                rect: p.clientRect?.() ?? null,
                selected: Math.min(bridge.get()?.selected ?? 0, Math.max(p.items.length - 1, 0)),
                choose: (item) => p.command(item),
              });
            return {
              onStart: (p) => {
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
          },
        }),
      ];
    },
  });
}

// SlashMenu is the block menu shown after typing "/" at the start of a line.
function SlashMenu({ state, onHover }: { state: SlashState; onHover: (i: number) => void }) {
  const { t } = useTranslation();
  const menu = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number }>({ top: -9999, left: -9999 });

  // Below the cursor, or above it when there's no room.
  useLayoutEffect(() => {
    const r = state.rect;
    const el = menu.current;
    if (!r || !el) return;
    const h = el.offsetHeight;
    const w = el.offsetWidth;
    const below = r.bottom + 6 + h <= window.innerHeight;
    setPos({
      top: below ? r.bottom + 6 : Math.max(8, r.top - h - 6),
      left: Math.max(8, Math.min(r.left, window.innerWidth - w - 8)),
    });
  }, [state.rect, state.items.length]);

  useEffect(() => {
    menu.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [state.selected]);

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
export default function SummaryEditor({ markdown, onReady, onChange, onSaveShortcut }: Props) {
  const { t } = useTranslation();
  const [slash, setSlashState] = useState<SlashState | null>(null);
  const slashRef = useRef<SlashState | null>(null);
  const bridge = useRef<SlashBridge>({
    set: (s) => {
      slashRef.current = s;
      setSlashState(s);
    },
    get: () => slashRef.current,
  });

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
      slashCommands(bridge.current, (id) => t(`editor.slash.${id}`)),
    ],
    content: markdown,
    contentType: 'markdown',
    editorProps: {
      attributes: { class: 'prose editor-content', 'aria-label': t('editor.content') },
      handleDOMEvents: {
        // Links open on Ctrl/Cmd+click; a plain click places the cursor.
        click: (_view, event) => {
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

  const marks = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e.isActive('bold'),
      italic: e.isActive('italic'),
      strike: e.isActive('strike'),
      code: e.isActive('code'),
      link: e.isActive('link'),
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
        <BubbleButton label={t('editor.link')} active={marks.link} onClick={setLink}>
          {t('editor.link')}
        </BubbleButton>
      </BubbleMenu>
      <EditorContent editor={editor} />
      {slash && (
        <SlashMenu
          state={slash}
          onHover={(i) => {
            const s = slashRef.current;
            if (s) bridge.current.set({ ...s, selected: i });
          }}
        />
      )}
    </div>
  );
}
