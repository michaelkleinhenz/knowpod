import { useState } from 'react';

export function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable (e.g. plain HTTP); the value is still selectable.
    }
  }
  return (
    <button type="button" className="small-button" onClick={copy}>
      {copied ? 'Copied' : label}
    </button>
  );
}
