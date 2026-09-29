import type { TFunction } from 'i18next';
import { api, Recording } from '../api/client';
import { locale } from '../i18n';
import { title as titleOf } from './recordings';

// Template is a starting point for a new note: a title and Markdown text with placeholders
// ({{date}}, {{time}}, {{weekday}}, {{title}}; see fillTemplate). Built-in templates come
// with the app; the user's own are their text notes marked as templates (noteId).
export interface Template {
  id: string;
  name: string;
  title: string;
  // The text of built-in templates; the user's own are loaded when chosen (loadTemplate).
  markdown?: string;
  noteId?: string;
}

// BUILT_IN names the built-in templates; their texts are translated (templates.builtIn.*).
const BUILT_IN = ['meeting', 'oneOnOne', 'project', 'journal', 'weekly'] as const;

// RAW keeps i18next from filling in the templates' own {{placeholders}}.
const RAW = { interpolation: { prefix: '\u0000', suffix: '\u0000' } };

export function builtInTemplates(t: TFunction): Template[] {
  return BUILT_IN.map((id) => ({
    id: `builtin:${id}`,
    name: t(`templates.builtIn.${id}.name`),
    title: t(`templates.builtIn.${id}.title`, RAW),
    markdown: t(`templates.builtIn.${id}.markdown`, RAW),
  }));
}

// PLACEHOLDERS lists the placeholders fillTemplate knows, for help texts.
export const PLACEHOLDERS = '{{date}}, {{isoDate}}, {{time}}, {{weekday}}, {{title}}';

// ownTemplates are the notes the user marked as templates (not in the trash), by name.
export function ownTemplates(notes: Recording[] | null | undefined, exceptId?: string): Template[] {
  return (notes ?? [])
    .filter((r) => r.template && !r.deletedAt && r.id !== exceptId)
    .map((r) => ({ id: `note:${r.id}`, name: titleOf(r), title: titleOf(r), noteId: r.id }))
    .sort((a, b) => a.name.localeCompare(b.name, locale()));
}

// loadTemplate returns the template's text: the note's current text for the user's own.
export async function loadTemplate(tpl: Template): Promise<string> {
  if (tpl.markdown !== undefined || !tpl.noteId) return tpl.markdown ?? '';
  const rec = await api.recording(tpl.noteId);
  return rec.summary?.markdown ?? '';
}

// fillTemplate fills in the placeholders of a template's title or text: {{date}} (e.g.
// "September 29, 2026"), {{isoDate}} (2026-09-29), {{time}}, {{weekday}} and {{title}} (the
// new note's title). Unknown placeholders are left as they are.
export function fillTemplate(text: string, vars: { title?: string; now?: Date } = {}): string {
  const now = vars.now ?? new Date();
  const pad = (n: number) => String(n).padStart(2, '0');
  const values: Record<string, string> = {
    date: now.toLocaleDateString(locale(), { year: 'numeric', month: 'long', day: 'numeric' }),
    isodate: `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`,
    time: now.toLocaleTimeString(locale(), { hour: 'numeric', minute: '2-digit' }),
    weekday: now.toLocaleDateString(locale(), { weekday: 'long' }),
    title: vars.title ?? '',
  };
  return text.replace(/\{\{\s*(\w+)\s*\}\}/g, (m, name: string) => values[name.toLowerCase()] ?? m);
}
