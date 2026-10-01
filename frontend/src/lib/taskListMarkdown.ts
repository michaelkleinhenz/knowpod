// Line breaks (Shift+Enter) in checklist items. Tiptap writes the lines after a break without
// indentation ("- [ ] Test  \n123") and reads only the first line as the item's text, so on
// reload the other lines fell out of the list. Here the lines are written indented under the
// item, and lines that continue the item's text are read back into it; lines that lost their
// indentation (as notes saved before were written) are taken back in too.
import { renderNestedMarkdownContent, type JSONContent, type MarkdownRendererHelpers, type MarkdownToken, type MarkdownTokenizer } from '@tiptap/core';

const ITEM = /^(\s*)[-+*]\s+\[[ xX]\]\s+/;
// A line ending in a line break: two spaces or a backslash.
const BREAK = /( {2,}|\\)$/;
// A line that starts a block of its own, so it can't continue an item's text.
const BLOCK = /^\s*([-+*]\s|\d+[.)]\s|#|>|```|~~~|\|)/;

// renderTaskItem writes a checklist item like Tiptap does, with the lines of its text after the
// first indented under it.
export function renderTaskItem(node: JSONContent, h: MarkdownRendererHelpers): string {
  const prefix = `- [${node.attrs?.checked ? 'x' : ' '}] `;
  const out = renderNestedMarkdownContent(node, h, prefix);
  const first = node.content?.[0];
  const lines = first ? h.renderChildren([first]).split('\n').length : 1;
  return out
    .split('\n')
    .map((line, i) => (i > 0 && i < lines && line ? h.indent(line) : line))
    .join('\n');
}

// indentLazyLines indents the lines that continue a checklist item's text after a line break
// but aren't indented under it.
function indentLazyLines(lines: string[]): string[] {
  let indent = -1; // the item text's indentation, while in it
  return lines.map((line, i) => {
    const item = ITEM.exec(line);
    if (item) {
      indent = item[1].length + 2;
      return line;
    }
    if (!line.trim()) {
      indent = -1;
      return line;
    }
    if (indent < 0 || !BREAK.test(lines[i - 1]) || BLOCK.test(line)) {
      indent = -1;
      return line;
    }
    const own = line.length - line.trimStart().length;
    return own >= indent ? line : ' '.repeat(indent) + line.trimStart();
  });
}

type Lexer = Parameters<MarkdownTokenizer['tokenize']>[2];

// joinContinuations takes a paragraph that directly follows an item's first line (the item's
// next lines) into the item's text, in the items and the lists nested in them.
function joinContinuations(tokens: MarkdownToken[] | undefined, lexer: Lexer) {
  for (const token of tokens ?? []) {
    if (token.type === 'taskList') {
      for (const item of (token.items ?? []) as MarkdownToken[]) {
        const next = item.nestedTokens?.[0];
        if (next?.type === 'paragraph') {
          item.text = `${item.text}\n${next.text}`;
          item.tokens = lexer.inlineTokens(item.text);
          item.nestedTokens = item.nestedTokens.slice(1);
        }
        joinContinuations(item.nestedTokens, lexer);
      }
    }
  }
}

// taskListTokenizer is Tiptap's checklist tokenizer, reading items of several lines.
export function taskListTokenizer(base: MarkdownTokenizer): MarkdownTokenizer {
  return {
    ...base,
    tokenize(src, tokens, lexer) {
      const lines = src.split('\n');
      const token = base.tokenize(indentLazyLines(lines).join('\n'), tokens, lexer);
      if (!token) return token;
      // The lazy lines were indented, so the source read is as many lines of src.
      const raw = token.raw ?? '';
      const read = raw.replace(/\n$/, '').split('\n').length;
      token.raw = lines.slice(0, read).join('\n') + (raw.endsWith('\n') ? '\n' : '');
      joinContinuations([token], lexer);
      return token;
    },
  };
}
