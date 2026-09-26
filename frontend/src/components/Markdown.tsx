import { Fragment, ReactNode } from 'react';

// inline renders **bold**, *italic* / _italic_ and `code` spans.
export function inline(text: string): ReactNode[] {
  return text.split(/(`[^`]+`|\*\*[^*]+\*\*|\*[^*\s][^*]*\*|_[^_\s][^_]*_)/).map((part, i) => {
    if (part.length > 1 && part.startsWith('`') && part.endsWith('`')) return <code key={i}>{part.slice(1, -1)}</code>;
    if (part.length > 3 && part.startsWith('**') && part.endsWith('**')) return <strong key={i}>{part.slice(2, -2)}</strong>;
    if (part.length > 2 && ((part.startsWith('*') && part.endsWith('*')) || (part.startsWith('_') && part.endsWith('_'))))
      return <em key={i}>{part.slice(1, -1)}</em>;
    return <Fragment key={i}>{part}</Fragment>;
  });
}

type Block =
  | { kind: 'h'; level: number; text: string }
  | { kind: 'ul' | 'ol'; items: string[] }
  | { kind: 'p'; text: string };

// parse splits the Markdown subset used by AI summaries and the API description into
// blocks: headings, bullet and numbered lists (with wrapped lines), and paragraphs. No raw
// HTML is ever rendered.
function parse(text: string): Block[] {
  const blocks: Block[] = [];
  let para: string[] = [];
  let list: { kind: 'ul' | 'ol'; items: string[] } | null = null;
  const flush = () => {
    if (para.length) blocks.push({ kind: 'p', text: para.join(' ') });
    if (list) blocks.push(list);
    para = [];
    list = null;
  };
  for (const raw of text.replace(/\r/g, '').split('\n')) {
    const line = raw.trim();
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    const bullet = /^[-*•]\s+(.*)$/.exec(line);
    const numbered = /^\d+[.)]\s+(.*)$/.exec(line);
    if (!line) {
      flush();
    } else if (heading) {
      flush();
      blocks.push({ kind: 'h', level: heading[1].length, text: heading[2] });
    } else if (bullet || numbered) {
      const kind = bullet ? 'ul' : 'ol';
      if (para.length || (list && list.kind !== kind)) flush();
      if (!list) list = { kind, items: [] };
      list.items.push((bullet ?? numbered)![1]);
    } else if (list && /^\s/.test(raw)) {
      list.items[list.items.length - 1] += ' ' + line; // continuation of a list item
    } else {
      if (list) flush();
      para.push(line);
    }
  }
  flush();
  return blocks;
}

export function Markdown({ text }: { text: string }) {
  return (
    <>
      {parse(text).map((b, i) => {
        switch (b.kind) {
          case 'h': {
            const level = Math.min(b.level + 1, 4); // page title is the only h1
            const Tag = `h${level}` as 'h2' | 'h3' | 'h4';
            return <Tag key={i}>{inline(b.text)}</Tag>;
          }
          case 'ul':
          case 'ol': {
            const Tag = b.kind;
            return (
              <Tag key={i}>
                {b.items.map((item, j) => (
                  <li key={j}>{inline(item)}</li>
                ))}
              </Tag>
            );
          }
          default:
            return <p key={i}>{inline(b.text)}</p>;
        }
      })}
    </>
  );
}
