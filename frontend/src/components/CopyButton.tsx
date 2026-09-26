import { ReactNode, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CheckIcon } from './Icons';

// CopyButton copies text to the clipboard. With an icon it shows only the icon (the label
// becomes the tooltip) and a check mark once copied.
export function CopyButton({ text, label, className = 'small-button', icon }: { text: string; label?: string; className?: string; icon?: ReactNode }) {
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
  const name = label ?? t('common.copy');
  if (icon) {
    return (
      <button type="button" className={className} onClick={copy} title={copied ? t('common.copied') : name} aria-label={name}>
        {copied ? <CheckIcon /> : icon}
      </button>
    );
  }
  return (
    <button type="button" className={className} onClick={copy}>
      {copied ? t('common.copied') : name}
    </button>
  );
}
