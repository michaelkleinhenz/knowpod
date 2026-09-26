import { CSSProperties } from 'react';
import i18n from 'i18next';
import { Label, Recording } from '../api/client';

// TASK is the built-in label that makes a note a task with a check mark.
export const TASK = 'task';

// LABEL_COLORS are the colors offered for new labels.
export const LABEL_COLORS = ['#3b5f86', '#2f6f5e', '#15803d', '#0e7490', '#6d28d9', '#be185d', '#b42318', '#c2410c', '#a16207', '#5d6b7d'];

// labelName names a label; built-in labels are translated.
export function labelName(l: Label): string {
  return l.builtIn ? i18n.t(`labels.builtIn.${l.id}`, { defaultValue: l.name }) : l.name;
}

// labelStyle passes the label's color to the chip styles.
export function labelStyle(l: Label): CSSProperties {
  return { '--label': l.color } as CSSProperties;
}

export function isTask(r: Recording): boolean {
  return r.labels?.includes(TASK) ?? false;
}

// noteLabels returns the note's labels in their order, skipping unknown IDs.
export function noteLabels(r: Recording, all: Label[] | null): Label[] {
  const byId = new Map((all ?? []).map((l) => [l.id, l]));
  return (r.labels ?? []).flatMap((id) => byId.get(id) ?? []);
}
