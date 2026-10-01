import { Fragment, ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { DUE_MARK, dueLabel, dueLineState, dueState, dueText } from '../lib/dueMarks';
import { imageWidth } from '../lib/imageWidth';
import { NOTE_REF } from '../lib/noteRefs';
import { NoteRef } from './NoteRef';

// safeHref allows only web and mail links, so Markdown can't smuggle in javascript: URLs.
function safeHref(url: string): string | null {
  return /^(https?:\/\/|mailto:)/i.test(url.trim()) ? url.trim() : null;
}

// noteHref matches links to the user's notes in the app ("/conversations/…", "/n/12"),
// as briefings write them.
function noteHref(url: string): boolean {
  return /^\/(conversations\/[\w-]+|n\/\d{1,9})$/.test(url);
}

// safeImage allows only the pictures stored with notes (also through a published note's
// link), so Markdown can't load other sites.
function safeImage(url: string): boolean {
  return /^\/api\/v1\/(recordings|public)\/[\w-]+\/images\/[\w-]+(#w=\d{1,5})?$/.test(url);
}

// Cite renders a citation such as "[2]" (see Markdown's cite).
export type Cite = (n: number) => ReactNode;

// inline renders ![pictures](/api/v1/recordings/…) of notes, `code`, **bold**, *italic* / _italic_, ~~strike~~, [links](https://…)
// and [note links](/conversations/…);
// with noteLinks, "#12" links to the user's note 12; with cite, "[2]" is rendered by it.
export function inline(text: string, noteLinks = false, cite?: Cite): ReactNode[] {
  const parts = text.split(/(!\[[^\]]*\]\([^)\s]+\)|`[^`]+`|\*\*[^*]+\*\*|~~[^~]+~~|\[[^\]]+\]\([^)\s]+\)|\[\d{1,2}\](?!\()|\*[^*\s][^*]*\*|_[^_\s][^_]*_)/);
  const inl = (t: string) => inline(t, noteLinks, cite);
  return parts.map((part, i) => {
    const citation = /^\[(\d{1,2})\]$/.exec(part);
    if (citation) return <Fragment key={i}>{cite ? cite(Number(citation[1])) : part}</Fragment>;
    if (part.length > 1 && part.startsWith('`') && part.endsWith('`')) return <code key={i}>{part.slice(1, -1)}</code>;
    if (part.length > 3 && part.startsWith('**') && part.endsWith('**')) return <strong key={i}>{inl(part.slice(2, -2))}</strong>;
    if (part.length > 3 && part.startsWith('~~') && part.endsWith('~~')) return <s key={i}>{inl(part.slice(2, -2))}</s>;
    const image = /^!\[([^\]]*)\]\(([^)\s]+)\)$/.exec(part);
    if (image) return safeImage(image[2]) ? <img key={i} src={image[2]} alt={image[1]} width={imageWidth(image[2])} loading="lazy" /> : <Fragment key={i}>{image[1]}</Fragment>;
    const link = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(part);
    if (link) {
      // A note link's text has no "#12" links in it: links don't nest.
      if (noteHref(link[2]))
        return (
          <Link key={i} to={link[2]}>
            {inline(link[1], false, cite)}
          </Link>
        );
      const href = safeHref(link[2]);
      return href ? (
        <a key={i} href={href} target="_blank" rel="noreferrer noopener">
          {inl(link[1])}
        </a>
      ) : (
        <Fragment key={i}>{link[1]}</Fragment>
      );
    }
    if (part.length > 2 && ((part.startsWith('*') && part.endsWith('*')) || (part.startsWith('_') && part.endsWith('_'))))
      return <em key={i}>{inl(part.slice(1, -1))}</em>;
    return <Fragment key={i}>{plain(part, noteLinks)}</Fragment>;
  });
}

// plain renders plain text: due marks ("[2026-10-01 en]") relative to today ("[tomorrow]")
// and colored by when they are due, and with noteLinks, "#12" as links to the notes.
function plain(text: string, noteLinks: boolean): ReactNode {
  const rest = (t: string) => (noteLinks ? linkNotes(t) : t);
  const out: ReactNode[] = [];
  let last = 0;
  const now = new Date();
  for (const m of text.matchAll(new RegExp(DUE_MARK))) {
    out.push(<Fragment key={`t${m.index}`}>{rest(text.slice(last, m.index))}</Fragment>);
    out.push(
      <span key={m.index} className={`due-mark ${dueState(m[1], now)}`} title={dueLabel(m[1], m[2], now)}>
        [{dueText(m[1], m[2], m[3], now)}]
      </span>,
    );
    last = m.index! + m[0].length;
  }
  if (out.length === 0) return rest(text);
  out.push(<Fragment key="end">{rest(text.slice(last))}</Fragment>);
  return out;
}

// dueLine colors a list item's own text when it has due marks (see dueLineState).
function dueLine(text: string, content: ReactNode): ReactNode {
  const due = dueLineState(text);
  return due ? <span className={`due-line ${due}`}>{content}</span> : content;
}

// linkNotes turns "#12" in plain text into links to the notes.
function linkNotes(text: string): ReactNode {
  const out: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(new RegExp(NOTE_REF))) {
    out.push(text.slice(last, m.index));
    out.push(<NoteRef key={m.index} number={Number(m[1])} />);
    last = m.index! + m[0].length;
  }
  if (out.length === 0) return text;
  out.push(text.slice(last));
  return out;
}

interface ListItem {
  text: string;
  children: string[]; // indented lines below the item, parsed as nested Markdown
}

type Block =
  | { kind: 'h'; level: number; text: string }
  | { kind: 'p'; text: string }
  | { kind: 'hr' }
  | { kind: 'code'; text: string }
  | { kind: 'quote'; children: Block[] }
  | { kind: 'ul' | 'ol'; start: number; items: ListItem[] }
  | { kind: 'table'; align: Align[]; head: string[]; rows: string[][] };

type Align = 'left' | 'center' | 'right' | undefined;

const HEADING = /^(#{1,6})\s+(.*)$/;
const LIST = /^(\s*)([-*+•]|\d+[.)])\s+(.*)$/;
const FENCE = /^\s*(```|~~~)/;
const TASK = /^\[([ xX])\]\s+(.*)$/;
const RULE = /^\s*([-*_])(\s*\1){2,}\s*$/;
const TABLE_RULE = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const indentOf = (line: string) => line.length - line.trimStart().length;
const startsBlock = (line: string) => HEADING.test(line.trim()) || LIST.test(line) || FENCE.test(line) || RULE.test(line) || /^\s*>/.test(line);

// cells splits a table row into its cells: at the pipes that aren't escaped (\|) or in
// `code`, without the outer pipes.
function cells(line: string): string[] {
  let row = line.trim();
  if (row.startsWith('|')) row = row.slice(1);
  if (row.endsWith('|') && !row.endsWith('\\|')) row = row.slice(0, -1);
  const out: string[] = [];
  let cell = '';
  let code = false;
  for (let i = 0; i < row.length; i++) {
    const c = row[i];
    if (c === '\\' && row[i + 1] === '|') {
      cell += '|';
      i++;
    } else if (c === '`') {
      code = !code;
      cell += c;
    } else if (c === '|' && !code) {
      out.push(cell.trim());
      cell = '';
    } else {
      cell += c;
    }
  }
  out.push(cell.trim());
  return out;
}

// tableAt says whether a table starts at lines[i]: a row with pipes, then the rule under the
// header ("| --- | :-: |") with as many cells.
function tableAt(lines: string[], i: number): boolean {
  const head = lines[i];
  const rule = lines[i + 1];
  return !!rule && head.includes('|') && TABLE_RULE.test(rule) && rule.includes('-') && cells(head).length === cells(rule).length;
}

// parse splits Markdown into blocks: headings, paragraphs, nested bullet, numbered and
// task lists, quotes, rules and code blocks. No raw HTML is ever rendered.
function parse(lines: string[]): Block[] {
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();
    if (!trimmed) {
      i++;
      continue;
    }
    if (FENCE.test(line)) {
      const fence = FENCE.exec(line)![1];
      const code: string[] = [];
      for (i++; i < lines.length && !lines[i].trim().startsWith(fence); i++) code.push(lines[i]);
      i++;
      blocks.push({ kind: 'code', text: code.join('\n') });
      continue;
    }
    if (tableAt(lines, i)) {
      const head = cells(line);
      const align: Align[] = cells(lines[i + 1]).map((c) =>
        c.startsWith(':') && c.endsWith(':') ? 'center' : c.endsWith(':') ? 'right' : c.startsWith(':') ? 'left' : undefined,
      );
      const rows: string[][] = [];
      for (i += 2; i < lines.length && lines[i].trim() && lines[i].includes('|') && !startsBlock(lines[i]); i++) {
        const row = cells(lines[i]);
        rows.push(head.map((_, j) => row[j] ?? ''));
      }
      blocks.push({ kind: 'table', align, head, rows });
      continue;
    }
    const heading = HEADING.exec(trimmed);
    if (heading) {
      blocks.push({ kind: 'h', level: heading[1].length, text: heading[2].replace(/\s+#+\s*$/, '') });
      i++;
      continue;
    }
    if (RULE.test(line)) {
      blocks.push({ kind: 'hr' });
      i++;
      continue;
    }
    if (/^\s*>/.test(line)) {
      const quoted: string[] = [];
      for (; i < lines.length && /^\s*>/.test(lines[i]); i++) quoted.push(lines[i].replace(/^\s*>\s?/, ''));
      blocks.push({ kind: 'quote', children: parse(quoted) });
      continue;
    }
    const first = LIST.exec(line);
    if (first) {
      const base = first[1].length;
      const ordered = /\d/.test(first[2]);
      const list: Block = { kind: ordered ? 'ol' : 'ul', start: ordered ? parseInt(first[2], 10) : 1, items: [] };
      let contentIndent = base + first[2].length + 1;
      for (; i < lines.length; i++) {
        const l = lines[i];
        const m = LIST.exec(l);
        const items = list.items;
        if (!l.trim()) {
          // A blank line continues the list only if more of it follows.
          const next = lines.slice(i + 1).find((x) => x.trim());
          if (next === undefined || indentOf(next) < base || (indentOf(next) === base && !LIST.exec(next))) break;
          if (items.length) items[items.length - 1].children.push('');
          continue;
        }
        if (m && m[1].length === base) {
          if (/\d/.test(m[2]) !== ordered) break;
          items.push({ text: m[3], children: [] });
          contentIndent = base + m[2].length + 1;
        } else if (indentOf(l) > base && items.length) {
          items[items.length - 1].children.push(l.slice(Math.min(indentOf(l), contentIndent)));
        } else if (!startsBlock(l) && items.length && lines[i - 1]?.trim()) {
          items[items.length - 1].text += ' ' + l.trim(); // lazy continuation
        } else {
          break;
        }
      }
      blocks.push(list);
      continue;
    }
    const para: string[] = [];
    for (; i < lines.length && lines[i].trim() && (para.length === 0 || (!startsBlock(lines[i]) && !tableAt(lines, i))); i++) para.push(lines[i].trim());
    blocks.push({ kind: 'p', text: para.join(' ') });
  }
  return blocks;
}

function render(blocks: Block[], noteLinks: boolean, cite?: Cite): ReactNode[] {
  return blocks.map((b, i) => {
    switch (b.kind) {
      case 'h': {
        const level = Math.min(b.level + 1, 4); // the page title is the only h1
        const Tag = `h${level}` as 'h2' | 'h3' | 'h4';
        return <Tag key={i}>{inline(b.text, noteLinks, cite)}</Tag>;
      }
      case 'hr':
        return <hr key={i} />;
      case 'code':
        return (
          <pre key={i}>
            <code>{b.text}</code>
          </pre>
        );
      case 'quote':
        return <blockquote key={i}>{render(b.children, noteLinks, cite)}</blockquote>;
      case 'table':
        return (
          <div key={i} className="table-wrap">
            <table>
              <thead>
                <tr>
                  {b.head.map((c, j) => (
                    <th key={j} style={b.align[j] ? { textAlign: b.align[j] } : undefined}>
                      {inline(c, noteLinks, cite)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {b.rows.map((row, r) => (
                  <tr key={r}>
                    {row.map((c, j) => (
                      <td key={j} style={b.align[j] ? { textAlign: b.align[j] } : undefined}>
                        {inline(c, noteLinks, cite)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      case 'ul':
      case 'ol': {
        let tasks = b.kind === 'ul';
        const items = b.items.map((item, j) => {
          const sub = item.children.some((c) => c.trim()) ? render(parse(item.children), noteLinks, cite) : null;
          const task = TASK.exec(item.text);
          if (!task) tasks = false;
          if (task && b.kind === 'ul') {
            const done = task[1] !== ' ';
            return (
              <li key={j} className={done ? 'done' : undefined}>
                <input type="checkbox" checked={done} disabled aria-label={task[2]} />
                {dueLine(task[2], inline(task[2], noteLinks, cite))}
                {sub}
              </li>
            );
          }
          return (
            <li key={j}>
              {dueLine(item.text, inline(item.text, noteLinks, cite))}
              {sub}
            </li>
          );
        });
        return b.kind === 'ol' ? (
          <ol key={i} start={b.start !== 1 ? b.start : undefined}>
            {items}
          </ol>
        ) : (
          <ul key={i} className={tasks ? 'task-list' : undefined}>
            {items}
          </ul>
        );
      }
      default: {
        const due = dueLineState(b.text);
        return (
          <p key={i} className={due ? `due-line ${due}` : undefined}>
            {inline(b.text, noteLinks, cite)}
          </p>
        );
      }
    }
  });
}

// Markdown renders Markdown text; with noteLinks, "#12" links to the user's note 12; with
// cite, citations such as "[2]" are rendered by it.
export function Markdown({ text, noteLinks = false, cite }: { text: string; noteLinks?: boolean; cite?: Cite }) {
  return <>{render(parse(text.replace(/\r/g, '').split('\n')), noteLinks, cite)}</>;
}
