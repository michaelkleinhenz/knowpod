// Column widths of tables. Markdown tables have no widths, so the widths the user dragged the
// columns to are kept in a comment on the line above the table, "<!-- colwidths: 120 0 240 -->"
// (in pixels, 0 for a column of natural width), which other Markdown readers don't show.
import { renderTableToMarkdown, Table } from '@tiptap/extension-table';
import type { JSONContent, MarkdownToken, MarkdownTokenizer } from '@tiptap/core';
import { Plugin, PluginKey } from '@tiptap/pm/state';
import { columnResizingPluginKey, ResizeState, TableMap } from '@tiptap/pm/tables';
import type { EditorView } from '@tiptap/pm/view';

// COLWIDTHS finds the comment at the start of the text, with the line break after it.
export const COLWIDTHS = /^ {0,3}<!-- ?colwidths:((?: \d{1,5})+) ?-->[ \t]*\n/;
// MIN_COLUMN_WIDTH is the narrowest a column can be dragged to, in pixels.
export const MIN_COLUMN_WIDTH = 48;

// parseColWidths returns the widths in a colwidths comment (0 for natural width).
export function parseColWidths(list: string): number[] {
  return list
    .trim()
    .split(/\s+/)
    .map((w) => {
      const n = Number(w);
      return n >= MIN_COLUMN_WIDTH ? n : 0;
    });
}

// colWidthsComment writes the comment for the widths, or "" when no column has one.
export function colWidthsComment(widths: number[]): string {
  if (!widths.some((w) => w > 0)) return '';
  return `<!-- colwidths: ${widths.map((w) => Math.round(w || 0)).join(' ')} -->`;
}

// tableWidthsTokenizer is Tiptap's table tokenizer, also reading the comment above the table.
function tableWidthsTokenizer(base: MarkdownTokenizer): MarkdownTokenizer {
  const baseStart = base.start;
  return {
    ...base,
    start: (src) => {
      const comment = src.search(/<!-- ?colwidths:/);
      const table = typeof baseStart === 'function' ? baseStart(src) : typeof baseStart === 'string' ? src.indexOf(baseStart) : -1;
      return comment < 0 ? table : table < 0 ? comment : Math.min(comment, table);
    },
    tokenize(src, tokens, lexer) {
      const comment = COLWIDTHS.exec(src);
      if (!comment) return base.tokenize(src, tokens, lexer);
      // Only the table (up to the next blank line) is read, as Tiptap's tokenizer does: it
      // leaves most tables to the one built into marked, which this calls through blockTokens.
      const rest = src.slice(comment[0].length);
      const blank = rest.indexOf('\n\n');
      const token = lexer.blockTokens(blank >= 0 ? rest.slice(0, blank) : rest)[0];
      if (token?.type !== 'table' || !token.raw) return undefined;
      const lines = token.raw.replace(/\n$/, '').split('\n').length;
      const raw = rest.split('\n').slice(0, lines).join('\n');
      return { ...token, raw: comment[0] + raw, colwidths: parseColWidths(comment[1]) };
    },
  };
}

// columnWidths returns the width of each column of a table node (0 for natural width).
function columnWidths(node: JSONContent): number[] {
  const widths: number[] = [];
  for (const row of node.content ?? []) {
    let col = 0;
    for (const cell of row.content ?? []) {
      const span = Number(cell.attrs?.colspan) || 1;
      const set = cell.attrs?.colwidth as number[] | null | undefined;
      for (let j = 0; j < span; j++, col++) if (!widths[col] && set?.[j]) widths[col] = set[j];
    }
    for (let i = 0; i < col; i++) widths[i] ??= 0;
  }
  return Array.from(widths, (w) => w ?? 0);
}

// freezeColumns gives every column of the table with the cell at cellPos the width it is shown
// at, before one of them is dragged: otherwise the columns not dragged would shift around as
// the browser lays out the table anew.
function freezeColumns(view: EditorView, cellPos: number) {
  const $cell = view.state.doc.resolve(cellPos);
  const table = $cell.node(-1);
  const start = $cell.start(-1);
  const map = TableMap.get(table);
  const widths: number[] = [];
  for (let col = 0; col < map.width; col++) {
    const at = map.map[col];
    const cell = table.nodeAt(at);
    const dom = view.nodeDOM(start + at);
    if (!cell || !(dom instanceof HTMLElement)) return;
    const rect = map.findCell(at);
    widths.push(Math.max(MIN_COLUMN_WIDTH, Math.round(dom.offsetWidth / (rect.right - rect.left))));
  }
  const tr = view.state.tr;
  for (const at of new Set(map.map)) {
    const cell = table.nodeAt(at)!;
    const rect = map.findCell(at);
    const own = (cell.attrs.colwidth as number[] | null) ?? [];
    const set = widths.slice(rect.left, rect.right).map((w, i) => own[i] || w);
    if (set.some((w, i) => w !== own[i])) tr.setNodeMarkup(start + at, undefined, { ...cell.attrs, colwidth: set });
  }
  if (tr.docChanged) view.dispatch(tr);
}

// freezeColumnsKey names the plugin that freezes the columns when one is about to be dragged.
const freezeColumnsKey = new PluginKey('freezeColumns');

// TableWithWidths is the table, written and read as Markdown with its columns' widths.
export const TableWithWidths = Table.extend({
  markdownTokenizer: tableWidthsTokenizer(Table.config.markdownTokenizer!),
  parseMarkdown: (token: MarkdownToken, h) => {
    const node = Table.config.parseMarkdown!(token, h) as JSONContent;
    const widths = token.colwidths as number[] | undefined;
    if (widths?.some((w) => w > 0)) {
      for (const row of node.content ?? []) {
        row.content?.forEach((cell, i) => {
          if (widths[i]) cell.attrs = { ...cell.attrs, colwidth: [widths[i]] };
        });
      }
    }
    return node;
  },
  renderMarkdown: (node, h) => {
    const table = renderTableToMarkdown(node, h);
    const comment = colWidthsComment(columnWidths(node));
    // The table starts with a line break; the comment goes right above its first row.
    return comment ? table.replace(/^\n?/, `\n${comment}\n`) : table;
  },
  addProseMirrorPlugins() {
    // Runs before the plugin that drags the column (in the table's plugins).
    const freeze = new Plugin({
      key: freezeColumnsKey,
      props: {
        handleDOMEvents: {
          mousedown: (view) => {
            const resize = columnResizingPluginKey.getState(view.state) as ResizeState | undefined;
            if (view.editable && resize && resize.activeHandle > -1 && !resize.dragging) freezeColumns(view, resize.activeHandle);
            return false;
          },
        },
      },
    });
    return [freeze, ...(this.parent?.() ?? [])];
  },
});
