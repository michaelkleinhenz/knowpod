// The filter language of the search box and saved filters, like Todoist's filters:
//
//   label:Task & due:week & !done      @Work | folder:"Side projects"      (p1 | p2) overdue
//
// Terms: words (found in the title, "quoted" for phrases or keywords), #12 (note number),
// label:Name or @Name, folder:Name (and the folders in it), due:today|tomorrow|overdue|week
// |month|none|any|2026-10-01, due<2026-10-01 (also <=, >, >= with a date, today, tomorrow or
// yesterday), done, task, p1-p3 (priority:none for none), repeat, estimate, type:text|audio|
// document|board, and today, tomorrow, overdue on their own. They combine with & (or just a
// space), | and !, grouped with parentheses; & binds tighter than |.
import type { Folder, Label, Recording } from '../api/client';
import { isoDate } from './dateParse';
import { isTask, labelName } from './labels';
import { noteType, title } from './recordings';
import { overdue } from './tasks';

// FilterContext is what terms are resolved against.
export interface FilterContext {
  labels: Label[];
  folders: Folder[];
  // notes lets sub-notes count as in their parent's folder.
  notes?: Recording[];
  now?: Date;
}

export type Matcher = (r: Recording) => boolean;

export type ParsedFilter =
  | { ok: true; match: Matcher; unknown: string[] }
  | { ok: false; error: string; at: number };

type Token = { kind: 'op'; op: '&' | '|' | '!' | '(' | ')'; at: number } | { kind: 'word'; text: string; quoted: boolean; at: number };

class FilterSyntaxError extends Error {
  constructor(
    message: string,
    public at: number,
  ) {
    super(message);
  }
}

const OPS = new Set(['&', '|', '!', '(', ')']);

function tokenize(q: string): Token[] {
  const out: Token[] = [];
  let i = 0;
  while (i < q.length) {
    const c = q[i];
    if (/\s/.test(c)) {
      i++;
      continue;
    }
    // "!" negates only at the start of a term, so words like "hi!" stay words.
    if (OPS.has(c)) {
      out.push({ kind: 'op', op: c as '&', at: i });
      i++;
      continue;
    }
    const at = i;
    let text = '';
    let quoted = false;
    while (i < q.length && !/\s/.test(q[i]) && !(OPS.has(q[i]) && q[i] !== '!')) {
      if (q[i] === '"') {
        if (text === '') quoted = true;
        const end = q.indexOf('"', i + 1);
        if (end < 0) throw new FilterSyntaxError('unclosedQuote', i);
        text += q.slice(i + 1, end);
        i = end + 1;
      } else {
        text += q[i++];
      }
    }
    out.push({ kind: 'word', text, quoted, at });
  }
  return out;
}

const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);

// relativeDay reads a date word: today, tomorrow, yesterday or YYYY-MM-DD.
function relativeDay(v: string, now: Date): string | null {
  switch (v) {
    case 'today':
      return isoDate(now);
    case 'tomorrow':
      return isoDate(addDays(now, 1));
    case 'yesterday':
      return isoDate(addDays(now, -1));
  }
  return /^\d{4}-\d{2}-\d{2}$/.test(v) && !Number.isNaN(new Date(`${v}T12:00:00`).getTime()) ? v : null;
}

const lower = (s: string) => s.toLocaleLowerCase();

// Compiler turns tokens into a matcher, resolving names against the context.
class Compiler {
  private pos = 0;
  unknown: string[] = [];
  private folderOfNote: (r: Recording) => string | undefined;
  private folderById: Map<string, Folder>;

  constructor(
    private tokens: Token[],
    private ctx: FilterContext,
    private now: Date,
    private end: number,
  ) {
    this.folderById = new Map(ctx.folders.map((f) => [f.id, f]));
    const byId = new Map((ctx.notes ?? []).map((n) => [n.id, n]));
    // A sub-note is in the folder of the topmost note above it.
    this.folderOfNote = (r) => {
      let n = r;
      for (let i = 0; n.parentId && byId.has(n.parentId) && i < 64; i++) n = byId.get(n.parentId)!;
      return n.folderId;
    };
  }

  private peek(): Token | undefined {
    return this.tokens[this.pos];
  }

  private isOp(op: string): boolean {
    const t = this.peek();
    return t?.kind === 'op' && t.op === op;
  }

  parse(): Matcher {
    if (this.tokens.length === 0) return () => true;
    const m = this.or();
    const t = this.peek();
    if (t) throw new FilterSyntaxError(t.kind === 'op' && t.op === ')' ? 'unopenedParen' : 'unexpected', t.at);
    return m;
  }

  private or(): Matcher {
    const parts = [this.and()];
    while (this.isOp('|')) {
      this.pos++;
      parts.push(this.and());
    }
    return parts.length === 1 ? parts[0] : (r) => parts.some((m) => m(r));
  }

  private and(): Matcher {
    const parts = [this.unary()];
    for (;;) {
      if (this.isOp('&')) {
        this.pos++;
        parts.push(this.unary());
        continue;
      }
      // A term right after another means "and" too.
      const t = this.peek();
      if (t && (t.kind === 'word' || t.op === '!' || t.op === '(')) {
        parts.push(this.unary());
        continue;
      }
      break;
    }
    return parts.length === 1 ? parts[0] : (r) => parts.every((m) => m(r));
  }

  private unary(): Matcher {
    if (this.isOp('!')) {
      this.pos++;
      const m = this.unary();
      return (r) => !m(r);
    }
    return this.primary();
  }

  private primary(): Matcher {
    const t = this.peek();
    if (!t) throw new FilterSyntaxError('missingTerm', this.end);
    if (t.kind === 'op') {
      if (t.op !== '(') throw new FilterSyntaxError('missingTerm', t.at);
      this.pos++;
      const m = this.or();
      if (!this.isOp(')')) throw new FilterSyntaxError('unclosedParen', this.peek()?.at ?? this.end);
      this.pos++;
      return m;
    }
    this.pos++;
    return this.term(t.text, t.quoted, t.at);
  }

  private term(text: string, quoted: boolean, at: number): Matcher {
    const has = (s: string) => (r: Recording) => lower(title(r)).includes(lower(s));
    if (quoted) return has(text);
    const w = lower(text);
    if (w === '') throw new FilterSyntaxError('missingTerm', at);

    let m: RegExpExecArray | null;
    if ((m = /^#(\d+)$/.exec(w))) {
      const n = Number(m[1]);
      return (r) => r.number === n;
    }
    if (/^\d+$/.test(w)) {
      const n = Number(w);
      return (r) => r.number === n || has(text)(r);
    }
    if (text.startsWith('@') && text.length > 1) return this.label(text.slice(1));
    if ((m = /^due(<=|>=|<|>)(.+)$/.exec(w))) {
      const day = relativeDay(m[2], this.now);
      if (!day) throw new FilterSyntaxError('badDate', at);
      const op = m[1];
      return (r) => !!r.due && (op === '<' ? r.due.date < day : op === '<=' ? r.due.date <= day : op === '>' ? r.due.date > day : r.due.date >= day);
    }
    const colon = text.indexOf(':');
    if (colon > 0) {
      const key = w.slice(0, colon);
      const value = text.slice(colon + 1);
      const v = lower(value);
      switch (key) {
        case 'label':
          return this.label(value);
        case 'folder':
          return this.folder(value);
        case 'due':
          return this.due(v, at);
        case 'type':
          if (!['text', 'audio', 'document', 'board'].includes(v)) throw new FilterSyntaxError('badType', at);
          return (r) => noteType(r) === v;
        case 'priority':
        case 'p':
          if (v === 'none' || v === '0') return (r) => !r.priority;
          if (!/^[123]$/.test(v)) throw new FilterSyntaxError('badPriority', at);
          return (r) => r.priority === Number(v);
        case 'text':
        case 'title':
          return has(value);
      }
    }
    switch (w) {
      case 'done':
        return (r) => !!r.done;
      case 'task':
        return isTask;
      case 'repeat':
      case 'recurring':
        return (r) => !!r.due?.repeat;
      case 'estimate':
        return (r) => !!r.estimate;
      case 'today':
      case 'tomorrow':
      case 'overdue':
        return this.due(w, at);
      case 'p1':
      case 'p2':
      case 'p3': {
        const p = Number(w[1]);
        return (r) => r.priority === p;
      }
    }
    return has(text);
  }

  private label(name: string): Matcher {
    const n = lower(name);
    const ids = new Set(this.ctx.labels.filter((l) => lower(labelName(l)) === n || lower(l.name) === n || l.id === n).map((l) => l.id));
    if (ids.size === 0) this.unknown.push(`@${name}`);
    return (r) => (r.labels ?? []).some((id) => ids.has(id));
  }

  // folder matches the notes in the named folders and the folders below them.
  private folder(name: string): Matcher {
    const n = lower(name);
    const roots = new Set(this.ctx.folders.filter((f) => lower(f.name) === n).map((f) => f.id));
    if (roots.size === 0) this.unknown.push(`folder:${name}`);
    const inside = (id: string | undefined) => {
      for (let i = 0; id && i < 64; i++) {
        if (roots.has(id)) return true;
        id = this.folderById.get(id)?.parentId;
      }
      return false;
    };
    return (r) => inside(this.folderOfNote(r));
  }

  private due(v: string, at: number): Matcher {
    const today = isoDate(this.now);
    const within = (days: number) => {
      const last = isoDate(addDays(this.now, days - 1));
      return (r: Recording) => !!r.due && r.due.date >= today && r.due.date <= last;
    };
    switch (v) {
      case 'none':
        return (r) => !r.due;
      case 'any':
        return (r) => !!r.due;
      case 'overdue':
        return (r) => overdue(r, this.now);
      case 'week':
        return within(7);
      case 'month':
        return within(30);
    }
    const day = relativeDay(v, this.now);
    if (!day) throw new FilterSyntaxError('badDate', at);
    return (r) => r.due?.date === day;
  }
}

// parseFilter compiles a query against the user's labels and folders. Names that match no
// label or folder are listed in unknown (they match nothing).
export function parseFilter(query: string, ctx: FilterContext): ParsedFilter {
  try {
    const c = new Compiler(tokenize(query), ctx, ctx.now ?? new Date(), query.length);
    const match = c.parse();
    return { ok: true, match, unknown: c.unknown };
  } catch (err) {
    if (err instanceof FilterSyntaxError) return { ok: false, error: err.message, at: err.at };
    throw err;
  }
}

// searchMatcher is the search box's matcher: the query as a filter, or (while it isn't one
// yet, e.g. half typed) notes whose title contains it.
export function searchMatcher(query: string, ctx: FilterContext): Matcher {
  const q = query.trim();
  if (!q) return () => true;
  const parsed = parseFilter(q, ctx);
  if (parsed.ok) return parsed.match;
  const text = lower(q);
  return (r) => lower(title(r)).includes(text);
}
