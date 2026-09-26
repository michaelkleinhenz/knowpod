import { useState } from 'react';
import { useTranslation } from 'react-i18next';

export function CopyButton({ text, label, className = 'small-button' }: { text: string; label?: string; className?: string }) {
  const { t } = useTranslation();
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
    <button type="button" className={className} onClick={copy}>
      {copied ? t('common.copied') : (label ?? t('common.copy'))}
    </button>
  );
}
