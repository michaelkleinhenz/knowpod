import { Editor, Extension, JSONContent } from '@tiptap/core';
import { Plugin, PluginKey } from '@tiptap/pm/state';
import { Decoration, DecorationSet } from '@tiptap/pm/view';
import { FormEvent, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, WriteAction, WriteInput } from '../api/client';
import i18n, { locale } from '../i18n';
import { errorText } from '../lib/errors';
import { SparkleIcon } from './Icons';
import { Markdown } from './Markdown';

// AiTarget is the part of the text the AI works on: the selected text (from < to), or the
// cursor (from === to), where new text goes.
export interface AiTarget {
  from: number;
  to: number;
}

// aiTargetKey holds the AI's target while its panel is open. The target follows changes of
// the text (someone else's edit arriving), and a selection stays marked while the panel has
// the focus. A transaction with a target (or null) as meta sets it.
export const aiTargetKey = new PluginKey<AiTarget | null>('aiTarget');

export const AiTargetExtension = Extension.create({
  name: 'aiTarget',
  addProseMirrorPlugins() {
    return [
      new Plugin<AiTarget | null>({
        key: aiTargetKey,
        state: {
          init: () => null,
          apply: (tr, value) => {
            const meta = tr.getMeta(aiTargetKey) as AiTarget | null | undefined;
            if (meta !== undefined) return meta;
            if (!value || !tr.docChanged) return value;
            if (value.from === value.to) {
              const at = tr.mapping.map(value.from);
              return { from: at, to: at };
            }
            const from = tr.mapping.map(value.from, 1);
            const to = tr.mapping.map(value.to, -1);
            return from < to ? { from, to } : { from: to, to };
          },
        },
        props: {
          decorations: (state) => {
            const v = aiTargetKey.getState(state);
            if (!v || v.from === v.to) return null;
            return DecorationSet.create(state.doc, [Decoration.inline(v.from, v.to, { class: 'ai-target' })]);
          },
        },
      }),
    ];
  },
});

// markdownBetween returns the text between two positions as Markdown.
export function markdownBetween(editor: Editor, from: number, to: number): string {
  const doc = editor.state.doc;
  try {
    if (editor.markdown) return editor.markdown.serialize(doc.cut(from, to).toJSON()).trim();
  } catch {
    // fall back to the plain text
  }
  return doc.textBetween(from, to, '\n\n').trim();
}

// tableDepth returns the depth of the table a position is in, or 0.
function tableDepth(editor: Editor, pos: number): number {
  const $pos = editor.state.doc.resolve(pos);
  for (let d = $pos.depth; d > 0; d--) if ($pos.node(d).type.name === 'table') return d;
  return 0;
}

// inlineContent returns the text of Markdown that is a single paragraph, as inline content
// (text with marks), or null for anything else.
function inlineContent(editor: Editor, markdown: string): JSONContent[] | null {
  try {
    const doc = editor.markdown?.parse(markdown);
    const blocks = doc?.content ?? [];
    return blocks.length === 1 && blocks[0].type === 'paragraph' ? (blocks[0].content ?? []) : null;
  } catch {
    return null;
  }
}

// putMarkdown puts Markdown into the text in place of range (from < to: replaced; from ===
// to: inserted there; an empty line there is replaced by it). A table cell only takes text:
// other Markdown (lists, tables, several paragraphs) goes below the table instead, since
// Markdown can't keep it in a cell.
export function putMarkdown(editor: Editor, range: { from: number; to: number }, markdown: string) {
  const doc = editor.state.doc;
  const depth = tableDepth(editor, range.from);
  if (depth) {
    const inline = inlineContent(editor, markdown);
    if (inline) {
      editor.chain().focus().insertContentAt(range, inline).run();
      return;
    }
    const $pos = doc.resolve(range.from);
    editor.chain().focus().insertContentAt($pos.after(depth), markdown, { contentType: 'markdown' }).run();
    return;
  }
  const $pos = doc.resolve(Math.min(range.from, doc.content.size));
  const empty = range.from === range.to && $pos.depth > 0 && $pos.parent.isTextblock && $pos.parent.content.size === 0;
  const at = empty ? { from: $pos.before(), to: $pos.after() } : range.from === range.to ? range.from : range;
  editor.chain().focus().insertContentAt(at, markdown, { contentType: 'markdown' }).run();
}

// insertMarkdown puts Markdown into the text at pos (see putMarkdown).
export function insertMarkdown(editor: Editor, pos: number, markdown: string) {
  putMarkdown(editor, { from: pos, to: pos }, markdown);
}

// CONTEXT_CHARS is how much of the note is sent along for context.
const CONTEXT_CHARS = 8000;

const SELECTION_ACTIONS: WriteAction[] = ['improve', 'fix', 'shorter', 'longer', 'simplify', 'professional', 'casual', 'summarize', 'tasks', 'table'];

// defaultLanguage is the language offered first for translations: the other of the app's two.
const defaultLanguage = () => (i18n.language === 'de' ? 'en-US' : 'de-DE');

// AiPanel has the AI write for the note: with text selected, it rewrites, shortens,
// translates, … it, or does what the user asks with it; at the cursor it continues the text
// or writes what the user asks for. The answer is shown first; the user replaces the
// selection with it, inserts it, tries again or discards it.
export function AiPanel({ editor, onClose }: { editor: Editor; onClose: () => void }) {
  const { t } = useTranslation();
  const target = () => aiTargetKey.getState(editor.state) ?? null;
  const [selection] = useState(() => {
    const v = target();
    return !!v && v.from < v.to;
  });
  const [instruction, setInstruction] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ markdown: string; model: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [languages, setLanguages] = useState<string[]>([]);
  const last = useRef<Omit<WriteInput, 'text' | 'context'> | null>(null);
  // Answers of a request that was cancelled or replaced are ignored.
  const request = useRef(0);
  const panel = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number }>({ top: -9999, left: -9999 });

  useEffect(() => {
    input.current?.focus();
    if (selection) api.aiLanguages().then(setLanguages, () => setLanguages([]));
  }, [selection]);

  // The panel sits below the target, or above it when there's no room.
  useLayoutEffect(() => {
    const place = () => {
      const v = target();
      const el = panel.current;
      if (!v || !el) return;
      const end = editor.view.coordsAtPos(v.to);
      const start = editor.view.coordsAtPos(v.from);
      const h = el.offsetHeight;
      const w = el.offsetWidth;
      const below = end.bottom + 8 + h <= window.innerHeight;
      setPos({
        top: below ? end.bottom + 8 : Math.max(8, start.top - h - 8),
        left: Math.max(8, Math.min(start.left, window.innerWidth - w - 8)),
      });
    };
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
    // The panel grows when the answer arrives.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result, busy, error]);

  // Escape closes the panel; a click outside closes it while nothing was written yet.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      e.stopPropagation();
      close();
    };
    const onDown = (e: MouseEvent) => {
      if (!result && !busy && !panel.current?.contains(e.target as Node)) close();
    };
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('mousedown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.removeEventListener('mousedown', onDown);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result, busy]);

  function close(refocus = true) {
    request.current++;
    if (!editor.isDestroyed) {
      editor.view.dispatch(editor.state.tr.setMeta(aiTargetKey, null));
      if (refocus) editor.commands.focus();
    }
    onClose();
  }

  async function run(req: Omit<WriteInput, 'text' | 'context'>) {
    const v = target();
    if (!v || editor.isDestroyed) return;
    last.current = req;
    const doc = editor.state.doc;
    const whole = markdownBetween(editor, 0, doc.content.size).slice(0, CONTEXT_CHARS);
    let input: WriteInput;
    if (selection) {
      input = { ...req, text: markdownBetween(editor, v.from, v.to), context: whole };
    } else if (req.action === 'continue') {
      input = { ...req, text: markdownBetween(editor, 0, v.from).slice(-CONTEXT_CHARS) };
    } else {
      input = { ...req, text: '', context: whole };
    }
    const id = ++request.current;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const out = await api.aiWrite(input);
      if (id === request.current) setResult(out);
    } catch (err) {
      if (id === request.current) setError(t('editor.ai.failed', { detail: errorText(err, t) }));
    } finally {
      if (id === request.current) setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const text = instruction.trim();
    if (!text || busy) return;
    void run({ action: selection ? 'custom' : 'write', instruction: text });
  }

  // apply puts the answer into the text: in place of the selection, below the block it ends
  // in, or at the cursor.
  function apply(how: 'replace' | 'below' | 'insert') {
    const v = target();
    if (!v || !result || editor.isDestroyed) return;
    editor.view.dispatch(editor.state.tr.setMeta(aiTargetKey, null));
    if (how === 'replace') {
      putMarkdown(editor, v, result.markdown);
    } else if (how === 'below') {
      const $to = editor.state.doc.resolve(v.to);
      const at = $to.depth > 0 ? $to.after(1) : v.to;
      editor.chain().focus().insertContentAt(at, result.markdown, { contentType: 'markdown' }).run();
    } else {
      insertMarkdown(editor, v.from, result.markdown);
    }
    close(false);
  }

  const languageName = (code: string) => {
    try {
      return new Intl.DisplayNames([locale()], { type: 'language' }).of(code) ?? code;
    } catch {
      return code;
    }
  };
  const sortedLanguages = [...languages].sort((a, b) =>
    a === defaultLanguage() ? -1 : b === defaultLanguage() ? 1 : languageName(a).localeCompare(languageName(b), locale()),
  );

  return (
    <div ref={panel} className="ai-panel" style={{ top: pos.top, left: pos.left }} role="dialog" aria-label={t('editor.ai.title')}>
      <form className="ai-ask" onSubmit={submit}>
        <SparkleIcon size={16} />
        <input
          ref={input}
          type="text"
          value={instruction}
          maxLength={1000}
          disabled={busy}
          placeholder={t(selection ? 'editor.ai.placeholderSelection' : 'editor.ai.placeholderCursor')}
          aria-label={t(selection ? 'editor.ai.placeholderSelection' : 'editor.ai.placeholderCursor')}
          onChange={(e) => setInstruction(e.target.value)}
        />
        <button type="submit" className="pill-button primary" disabled={busy || !instruction.trim()}>
          {t('editor.ai.run')}
        </button>
      </form>

      {busy && (
        <div className="ai-status" role="status">
          <span className="ai-spinner" aria-hidden="true" />
          {t('editor.ai.working')}
          <button type="button" className="pill-button" onClick={() => close()}>
            {t('editor.ai.cancel')}
          </button>
        </div>
      )}
      {error && (
        <p className="error ai-error" role="alert">
          {error}
        </p>
      )}

      {result ? (
        <div className="ai-result">
          <div className="prose ai-preview">
            <Markdown text={result.markdown} />
          </div>
          <p className="muted ai-model">{t('editor.ai.model', { model: result.model })}</p>
          <div className="ai-apply">
            {selection ? (
              <>
                <button type="button" className="pill-button primary" onClick={() => apply('replace')}>
                  {t('editor.ai.replace')}
                </button>
                <button type="button" className="pill-button" onClick={() => apply('below')}>
                  {t('editor.ai.insertBelow')}
                </button>
              </>
            ) : (
              <button type="button" className="pill-button primary" onClick={() => apply('insert')}>
                {t('editor.ai.insert')}
              </button>
            )}
            <button type="button" className="pill-button" onClick={() => last.current && void run(last.current)}>
              {t('editor.ai.retry')}
            </button>
            <button type="button" className="pill-button" onClick={() => close()}>
              {t('editor.ai.discard')}
            </button>
          </div>
        </div>
      ) : (
        !busy && (
          <div className="ai-actions">
            {selection ? (
              <>
                {SELECTION_ACTIONS.map((a) => (
                  <button key={a} type="button" className="ai-action" onClick={() => void run({ action: a })}>
                    {t(`editor.ai.actions.${a}`)}
                  </button>
                ))}
                {languages.length > 0 && (
                  <select
                    className="ai-action ai-translate"
                    value=""
                    aria-label={t('editor.ai.translate')}
                    onChange={(e) => e.target.value && void run({ action: 'translate', language: e.target.value })}
                  >
                    <option value="">{t('editor.ai.translate')}</option>
                    {sortedLanguages.map((l) => (
                      <option key={l} value={l}>
                        {languageName(l)}
                      </option>
                    ))}
                  </select>
                )}
              </>
            ) : (
              editor.state.doc.textBetween(0, target()?.from ?? 0, '\n').trim() && (
                <button type="button" className="ai-action" onClick={() => void run({ action: 'continue' })}>
                  {t('editor.ai.actions.continue')}
                </button>
              )
            )}
          </div>
        )
      )}
    </div>
  );
}
