import { Markdown } from '@tiptap/markdown';
import { EditorContent, useEditor, useEditorState } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { ReactNode } from 'react';
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

function ToolButton(props: { label: string; active?: boolean; disabled?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      className={`tool-button${props.active ? ' active' : ''}`}
      title={props.label}
      aria-label={props.label}
      aria-pressed={props.active}
      disabled={props.disabled}
      onMouseDown={(e) => e.preventDefault()} // keep the editor's selection
      onClick={props.onClick}
    >
      {props.children}
    </button>
  );
}

// SummaryEditor shows a summary as an always-editable document: clicking into the text
// places the cursor there, like in a word processor. The text is loaded from and read as
// Markdown, which is how summaries are stored; saving is done by the caller (useAutosave).
// It is loaded on demand (see Conversation.tsx).
export default function SummaryEditor({ markdown, onReady, onChange, onSaveShortcut }: Props) {
  const { t } = useTranslation();

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: { levels: [1, 2, 3, 4] },
        underline: false, // not representable in Markdown
        link: { openOnClick: false, autolink: true, protocols: ['http', 'https', 'mailto'] },
      }),
      Markdown,
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
    onCreate: ({ editor: e }) => onReady(() => e.getMarkdown()),
    onUpdate: () => onChange(),
  });

  const state = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e.isActive('bold'),
      italic: e.isActive('italic'),
      strike: e.isActive('strike'),
      code: e.isActive('code'),
      h2: e.isActive('heading', { level: 2 }),
      h3: e.isActive('heading', { level: 3 }),
      bullet: e.isActive('bulletList'),
      ordered: e.isActive('orderedList'),
      quote: e.isActive('blockquote'),
      link: e.isActive('link'),
      canUndo: e.can().undo(),
      canRedo: e.can().redo(),
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
      <div className="editor-toolbar" role="toolbar" aria-label={t('editor.toolbar')}>
        <ToolButton label={t('editor.bold')} active={state.bold} onClick={() => chain().toggleBold().run()}>
          <b>B</b>
        </ToolButton>
        <ToolButton label={t('editor.italic')} active={state.italic} onClick={() => chain().toggleItalic().run()}>
          <i>I</i>
        </ToolButton>
        <ToolButton label={t('editor.strike')} active={state.strike} onClick={() => chain().toggleStrike().run()}>
          <s>S</s>
        </ToolButton>
        <ToolButton label={t('editor.code')} active={state.code} onClick={() => chain().toggleCode().run()}>
          {'</>'}
        </ToolButton>
        <span className="tool-sep" />
        <ToolButton label={t('editor.heading2')} active={state.h2} onClick={() => chain().toggleHeading({ level: 2 }).run()}>
          H2
        </ToolButton>
        <ToolButton label={t('editor.heading3')} active={state.h3} onClick={() => chain().toggleHeading({ level: 3 }).run()}>
          H3
        </ToolButton>
        <span className="tool-sep" />
        <ToolButton label={t('editor.bulletList')} active={state.bullet} onClick={() => chain().toggleBulletList().run()}>
          •≡
        </ToolButton>
        <ToolButton label={t('editor.orderedList')} active={state.ordered} onClick={() => chain().toggleOrderedList().run()}>
          1.
        </ToolButton>
        <ToolButton label={t('editor.quote')} active={state.quote} onClick={() => chain().toggleBlockquote().run()}>
          ❝
        </ToolButton>
        <ToolButton label={t('editor.link')} active={state.link} onClick={setLink}>
          🔗
        </ToolButton>
        <span className="tool-sep" />
        <ToolButton label={t('editor.undo')} disabled={!state.canUndo} onClick={() => chain().undo().run()}>
          ↶
        </ToolButton>
        <ToolButton label={t('editor.redo')} disabled={!state.canRedo} onClick={() => chain().redo().run()}>
          ↷
        </ToolButton>
      </div>
      <EditorContent editor={editor} />
    </div>
  );
}
