import { KeyboardEvent, useMemo, useState } from 'react';
import { parseTask, ParsedTask, shiftSpans, Span } from './dateParse';

// useTaskParse reads a task's date and priority from text as it is typed, like parseTask.
// Backspace right after a recognized phrase keeps it as plain text instead of deleting its
// last character, as in Todoist: "Sperrmüll 15.10.", Backspace, " 12.10." is due on 12.10.
// with "Sperrmüll 15.10." as the title. Pass onKeyDown on to the input.
export function useTaskParse(text: string, enabled = true): { parsed: ParsedTask | null; onKeyDown: (e: KeyboardEvent<HTMLInputElement>) => boolean } {
  const [state, setState] = useState<{ text: string; keep: Span[] }>({ text, keep: [] });
  let keep = state.keep;
  if (state.text !== text) {
    // The kept phrases move along with edits elsewhere in the text.
    keep = text.trim() ? shiftSpans(state.keep, state.text, text) : [];
    setState({ text, keep });
  }
  const parsed = useMemo(() => (enabled ? parseTask(text, new Date(), keep) : null), [text, keep, enabled]);

  // onKeyDown reports whether it handled the key.
  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (!parsed || e.key !== 'Backspace' || e.nativeEvent.isComposing || e.altKey || e.ctrlKey || e.metaKey) return false;
    const input = e.currentTarget;
    const caret = input.selectionStart;
    if (caret === null || caret !== input.selectionEnd) return false;
    const span = parsed.spans.find((s) => s.end === caret);
    if (!span) return false;
    e.preventDefault();
    setState({ text, keep: [...keep, span] });
    return true;
  }
  return { parsed, onKeyDown };
}
