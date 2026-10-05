// The editor's mention pills (see lib/mentions): a person ("@anna.berg@example.com" in the
// Markdown) and a date ("@2026-10-05"), each an inline node that is selected and deleted as one.
import { mergeAttributes, Node, type MarkdownToken, type MarkdownTokenizer } from '@tiptap/core';
import { DATE_MENTION, dateMentionText, MENTION, mentionName, validDate } from './mentions';

// afterWord says whether the text read before a mention ends in a word, so "@" there is part
// of it (an email address) and no mention.
function afterWord(tokens: MarkdownToken[]): boolean {
  const prev = tokens[tokens.length - 1];
  return !!prev && /[\w.%+@-]$/.test(prev.raw ?? '');
}

// tokenizer reads a mention found by re (with the value in its first group) at the start of
// the text.
function tokenizer(name: string, re: RegExp, valid: (value: string) => boolean = () => true): MarkdownTokenizer {
  const at = new RegExp(`^${re.source.replace(/^\(\?<![^)]*\)/, '')}`);
  return {
    name,
    level: 'inline',
    start: (src) => src.search(new RegExp(re.source)),
    tokenize: (src, tokens) => {
      const m = at.exec(src);
      if (!m || afterWord(tokens) || !valid(m[1])) return undefined;
      return { type: name, raw: m[0], value: m[1] };
    },
  };
}

// PersonMention is a person mentioned in the text, shown as "@anna.berg".
export const PersonMention = Node.create({
  name: 'personMention',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,
  addAttributes() {
    return {
      email: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-email') ?? '',
        renderHTML: (attrs) => ({ 'data-email': attrs.email }),
      },
    };
  },
  parseHTML: () => [{ tag: 'span[data-person-mention]' }],
  renderHTML: ({ node, HTMLAttributes }) => [
    'span',
    mergeAttributes(HTMLAttributes, { 'data-person-mention': '', class: 'mention-pill person', title: node.attrs.email }),
    mentionName(node.attrs.email),
  ],
  renderText: ({ node }) => `@${node.attrs.email}`,
  markdownTokenizer: tokenizer('personMention', MENTION),
  parseMarkdown: (token, h) => h.createNode('personMention', { email: token.value }),
  renderMarkdown: (node) => `@${node.attrs?.email ?? ''}`,
});

// DateMention is a date mentioned in the text, shown as "Mon, Oct 5, 2026"; clicking it opens
// the calendar to choose another (see SummaryEditor).
export const DateMention = Node.create({
  name: 'dateMention',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,
  addAttributes() {
    return {
      date: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-date') ?? '',
        renderHTML: (attrs) => ({ 'data-date': attrs.date }),
      },
    };
  },
  parseHTML: () => [{ tag: 'span[data-date-mention]' }],
  renderHTML: ({ node, HTMLAttributes }) => [
    'span',
    mergeAttributes(HTMLAttributes, { 'data-date-mention': '', class: 'mention-pill date', title: node.attrs.date }),
    dateMentionText(node.attrs.date),
  ],
  renderText: ({ node }) => `@${node.attrs.date}`,
  markdownTokenizer: tokenizer('dateMention', DATE_MENTION, validDate),
  parseMarkdown: (token, h) => h.createNode('dateMention', { date: token.value }),
  renderMarkdown: (node) => `@${node.attrs?.date ?? ''}`,
});
