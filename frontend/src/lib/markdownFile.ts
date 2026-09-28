import { api, Recording } from '../api/client';

// MARKDOWN_ACCEPT are the file types and extensions of Markdown files, for file pickers.
export const MARKDOWN_ACCEPT = '.md,.markdown,text/markdown';

// MAX_MARKDOWN is the most text a note holds (the server's limit, in bytes of UTF-8).
const MAX_MARKDOWN = 100_000;
const MAX_TITLE = 200;

// isMarkdown tells a Markdown file by its extension or type.
export function isMarkdown(file: File): boolean {
  return /\.(md|markdown)$/i.test(file.name) || file.type === 'text/markdown';
}

// markdownNote makes a note's title and text of a Markdown file: YAML front matter is left
// out (its "title:" is used), and a first-level heading at the top becomes the title;
// otherwise the file's name (without the extension) is the title.
export function markdownNote(fileName: string, content: string): { title: string; markdown: string } {
  let text = content.replace(/^﻿/, '').replace(/\r\n?/g, '\n');
  let title = '';
  const front = /^---\n([\s\S]*?)\n---(?:\n|$)/.exec(text);
  if (front) {
    const t = /^title:\s*(.+)$/m.exec(front[1]);
    if (t) title = t[1].trim().replace(/^(['"])(.*)\1$/, '$2');
    text = text.slice(front[0].length);
  }
  text = text.replace(/^\s*\n/, '');
  const heading = /^#\s+(.+?)\s*#*\s*(?:\n|$)/.exec(text);
  if (heading && (!title || heading[1] === title)) {
    title = heading[1];
    text = text.slice(heading[0].length);
  }
  title = (title || fileName.replace(/\.(md|markdown)$/i, '')).trim() || fileName;
  return { title: [...title].slice(0, MAX_TITLE).join(''), markdown: text.trim() };
}

// createMarkdownNote makes a text note of a Markdown file, in folderId (the top level when
// absent). A file too long for a note is refused with the message tooLong.
export async function createMarkdownNote(file: File, tooLong: string, folderId?: string): Promise<Recording> {
  const { title, markdown } = markdownNote(file.name, await file.text());
  if (new TextEncoder().encode(markdown).length > MAX_MARKDOWN) throw new Error(tooLong);
  return api.createTextNote(title, markdown, undefined, undefined, folderId);
}
