import { Markdown } from '@tiptap/markdown';
import { EditorContent, useEditor, useEditorState } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { errorText } from '../lib/errors';

// Changes are saved this long after the last keystroke …
const AUTOSAVE_DELAY_MS = 2_000;
// … and at least this often while typing continues.
const AUTOSAVE_MAX_WAIT_MS = 10_000;
// A failed save is retried after this long (or as soon as the browser is back online).
const RETRY_MS = 10_000;

type Sync = 'saved' | 'dirty' | 'saving' | 'error' | 'offline';

interface Props {
  recordingId: string;
  title: string;
  markdown: string;
  // onSaved receives the recording after every successful save.
  onSaved: (rec: Recording) => void;
  onClose: () => void;
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

// SummaryEditor edits a summary as rich text. The text is loaded from and saved as Markdown,
// which is how summaries are stored. Changes are saved automatically; the sync state is
// always shown. It is loaded on demand (see Conversation.tsx).
export default function SummaryEditor({ recordingId, title: initialTitle, markdown, onSaved, onClose }: Props) {
  const { t } = useTranslation();
  const [title, setTitle] = useState(initialTitle);
  const [sync, setSync] = useState<Sync>('saved');
  const [error, setError] = useState<string | null>(null);

  // What the server has, the save in flight, and the timers.
  const saved = useRef({ title: initialTitle, markdown });
  const inFlight = useRef<Promise<boolean> | null>(null);
  const debounce = useRef<number>();
  const maxWait = useRef<number>();
  const retry = useRef<number>();
  const titleRef = useRef(title);
  titleRef.current = title;

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
    editorProps: { attributes: { class: 'prose editor-content', 'aria-label': t('editor.content') } },
  });

  const current = useCallback(() => ({ title: titleRef.current.trim(), markdown: editor.getMarkdown() }), [editor]);
  const isDirty = useCallback(() => {
    const c = current();
    return c.title !== saved.current.title.trim() || c.markdown !== saved.current.markdown;
  }, [current]);

  const clearTimers = () => {
    window.clearTimeout(debounce.current);
    window.clearTimeout(maxWait.current);
    window.clearTimeout(retry.current);
    debounce.current = maxWait.current = retry.current = undefined;
  };

  // save sends the current text if it differs from what the server has. Saves never
  // overlap: a change made during a save is sent by the next one. It resolves to whether
  // everything is saved.
  const save = useCallback(async (): Promise<boolean> => {
    clearTimers();
    if (inFlight.current) {
      await inFlight.current;
      if (!isDirty()) return true;
    }
    if (!isDirty()) {
      setSync('saved');
      return true;
    }
    const c = current();
    if (!c.title) {
      setSync('error');
      setError(t('editor.titleRequired'));
      return false;
    }
    setSync('saving');
    setError(null);
    const run = (async () => {
      try {
        const rec = await api.editSummary(recordingId, c.title, c.markdown);
        saved.current = c;
        onSaved(rec);
        return true;
      } catch (err) {
        const offline = !navigator.onLine;
        setSync(offline ? 'offline' : 'error');
        setError(offline ? null : errorText(err, t));
        retry.current = window.setTimeout(() => void save(), RETRY_MS);
        return false;
      }
    })();
    inFlight.current = run;
    const ok = await run;
    inFlight.current = null;
    if (!ok) return false;
    if (isDirty()) {
      setSync('dirty');
      schedule(); // typed during the save
      return false;
    }
    setSync('saved');
    return true;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, isDirty, recordingId, onSaved, t]);

  // schedule saves after a pause in typing, and at least every AUTOSAVE_MAX_WAIT_MS.
  const schedule = useCallback(() => {
    window.clearTimeout(debounce.current);
    debounce.current = window.setTimeout(() => void save(), AUTOSAVE_DELAY_MS);
    if (maxWait.current === undefined) maxWait.current = window.setTimeout(() => void save(), AUTOSAVE_MAX_WAIT_MS);
  }, [save]);

  const changed = useCallback(() => {
    if (!isDirty()) return;
    setSync((s) => (s === 'saving' ? s : 'dirty'));
    schedule();
  }, [isDirty, schedule]);

  useEffect(() => {
    editor.on('update', changed);
    return () => {
      editor.off('update', changed);
    };
  }, [editor, changed]);

  // Retry right away when the connection comes back; warn before closing the tab with
  // unsaved changes; save what's left when the editor goes away (e.g. navigating off).
  useEffect(() => {
    const online = () => isDirty() && void save();
    const beforeUnload = (e: BeforeUnloadEvent) => {
      if (isDirty() || inFlight.current) e.preventDefault();
    };
    window.addEventListener('online', online);
    window.addEventListener('beforeunload', beforeUnload);
    return () => {
      window.removeEventListener('online', online);
      window.removeEventListener('beforeunload', beforeUnload);
    };
  }, [isDirty, save]);
  useEffect(
    () => () => {
      if (!editor.isDestroyed && isDirty()) void save();
      clearTimers();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

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

  async function done() {
    if (await save()) onClose();
  }

  const chain = () => editor.chain().focus();
  const syncText =
    sync === 'saved'
      ? t('editor.sync.saved')
      : sync === 'dirty'
        ? t('editor.sync.dirty')
        : sync === 'saving'
          ? t('editor.sync.saving')
          : sync === 'offline'
            ? t('editor.sync.offline')
            : t('editor.sync.error', { error: error ?? '' });

  return (
    <div className="summary-editor">
      <div className="editor-bar">
        <div className={`sync-state ${sync}`} role="status" aria-live="polite">
          <span className="sync-dot" aria-hidden="true" />
          <span>{syncText}</span>
          {(sync === 'error' || sync === 'offline') && (
            <button type="button" className="link-button" onClick={() => void save()}>
              {t('editor.sync.retry')}
            </button>
          )}
        </div>
        <button type="button" className="primary-button" onClick={() => void done()} disabled={sync === 'saving'}>
          {t('editor.done')}
        </button>
      </div>
      <label className="editor-title">
        <span>{t('editor.title')}</span>
        <input
          required
          maxLength={200}
          value={title}
          onChange={(e) => {
            titleRef.current = e.target.value;
            setTitle(e.target.value);
            changed();
          }}
        />
      </label>
      <div className="editor-frame">
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
    </div>
  );
}
