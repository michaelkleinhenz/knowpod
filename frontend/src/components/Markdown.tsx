import { Fragment, ReactNode } from 'react';
import { NOTE_REF } from '../lib/noteRefs';
import { NoteRef } from './NoteRef';

// safeHref allows only web and mail links, so Markdown can't smuggle in javascript: URLs.
function safeHref(url: string): string | null {
  return /^(https?:\/\/|mailto:)/i.test(url.trim()) ? url.trim() : null;
}

// inline renders `code`, **bold**, *italic* / _italic_, ~~strike~~ and [links](https://…);
// with noteLinks, "#12" links to the user's note 12.
export function inline(text: string, noteLinks = false): ReactNode[] {
  const parts = text.split(/(`[^`]+`|\*\*[^*]+\*\*|~~[^~]+~~|\[[^\]]+\]\([^)\s]+\)|\*[^*\s][^*]*\*|_[^_\s][^_]*_)/);
  const inl = (t: string) => inline(t, noteLinks);
  return parts.map((part, i) => {
    if (part.length > 1 && part.startsWith('`') && part.endsWith('`')) return <code key={i}>{part.slice(1, -1)}</code>;
    if (part.length > 3 && part.startsWith('**') && part.endsWith('**')) return <strong key={i}>{inl(part.slice(2, -2))}</strong>;
    if (part.length > 3 && part.startsWith('~~') && part.endsWith('~~')) return <s key={i}>{inl(part.slice(2, -2))}</s>;
    const link = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(part);
    if (link) {
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
    return <Fragment key={i}>{noteLinks ? linkNotes(part) : part}</Fragment>;
  });
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
  | { kind: 'ul' | 'ol'; start: number; items: ListItem[] };

const HEADING = /^(#{1,6})\s+(.*)$/;
const LIST = /^(\s*)([-*+•]|\d+[.)])\s+(.*)$/;
const FENCE = /^\s*(```|~~~)/;
const TASK = /^\[([ xX])\]\s+(.*)$/;
const RULE = /^\s*([-*_])(\s*\1){2,}\s*$/;
const indentOf = (line: string) => line.length - line.trimStart().length;
const startsBlock = (line: string) => HEADING.test(line.trim()) || LIST.test(line) || FENCE.test(line) || RULE.test(line) || /^\s*>/.test(line);

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
    for (; i < lines.length && lines[i].trim() && (para.length === 0 || !startsBlock(lines[i])); i++) para.push(lines[i].trim());
    blocks.push({ kind: 'p', text: para.join(' ') });
  }
  return blocks;
}

function render(blocks: Block[], noteLinks: boolean): ReactNode[] {
  return blocks.map((b, i) => {
    switch (b.kind) {
      case 'h': {
        const level = Math.min(b.level + 1, 4); // the page title is the only h1
        const Tag = `h${level}` as 'h2' | 'h3' | 'h4';
        return <Tag key={i}>{inline(b.text, noteLinks)}</Tag>;
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
        return <blockquote key={i}>{render(b.children, noteLinks)}</blockquote>;
      case 'ul':
      case 'ol': {
        let tasks = b.kind === 'ul';
        const items = b.items.map((item, j) => {
          const sub = item.children.some((c) => c.trim()) ? render(parse(item.children), noteLinks) : null;
          const task = TASK.exec(item.text);
          if (!task) tasks = false;
          if (task && b.kind === 'ul') {
            const done = task[1] !== ' ';
            return (
              <li key={j} className={done ? 'done' : undefined}>
                <input type="checkbox" checked={done} disabled aria-label={task[2]} />
                {inline(task[2], noteLinks)}
                {sub}
              </li>
            );
          }
          return (
            <li key={j}>
              {inline(item.text, noteLinks)}
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
      default:
        return <p key={i}>{inline(b.text, noteLinks)}</p>;
    }
  });
}

// Markdown renders Markdown text; with noteLinks, "#12" links to the user's note 12.
export function Markdown({ text, noteLinks = false }: { text: string; noteLinks?: boolean }) {
  return <>{render(parse(text.replace(/\r/g, '').split('\n')), noteLinks)}</>;
}
