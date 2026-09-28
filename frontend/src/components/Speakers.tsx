import { FormEvent, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { errorText } from '../lib/errors';

// SPEAKER matches a transcript line starting with a speaker label, after an optional time
// stamp ("[1:05] Speaker 2: …"), as the server reads them.
const SPEAKER = /^\s*(?:\[(?:\d{1,2}:)?\d{1,3}:\d{2}\]\s*)?([^:\n[\]]{1,40}):\s/;

// speakerLabels returns the speaker labels of a transcript in the order they first speak.
export function speakerLabels(text: string): string[] {
  const out: string[] = [];
  for (const line of text.split('\n')) {
    const label = SPEAKER.exec(line)?.[1]?.trim();
    if (label && !out.includes(label)) out.push(label);
  }
  return out;
}

// SpeakerRow names one speaker: a name typed in, or the one the AI recognized.
function SpeakerRow({ rec, label, suggestion, setRec }: { rec: Recording; label: string; suggestion?: string; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function rename(to: string) {
    to = to.trim();
    if (!to || to === label) return;
    setBusy(true);
    setError(null);
    try {
      setRec(await api.renameSpeaker(rec.id, label, to));
      setName('');
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li>
      <form
        className="speaker-row"
        onSubmit={(e: FormEvent) => {
          e.preventDefault();
          void rename(name);
        }}
      >
        <span className="speaker">{label}</span>
        <input
          value={name}
          maxLength={40}
          placeholder={t('speakers.namePlaceholder')}
          aria-label={t('speakers.nameLabel', { label })}
          onChange={(e) => setName(e.target.value)}
          disabled={busy}
        />
        <button type="submit" className="pill-button" disabled={busy || !name.trim()}>
          {t('speakers.rename')}
        </button>
        {suggestion && (
          <button type="button" className="pill-button speaker-suggestion" disabled={busy} title={t('speakers.suggestionTitle')} onClick={() => void rename(suggestion)}>
            {t('speakers.useSuggestion', { name: suggestion })}
          </button>
        )}
      </form>
      {error && <p className="error">{error}</p>}
    </li>
  );
}

// Speakers names the speakers of a recording's transcript ("Speaker 1" becomes "Anna" in the
// transcript, the summary and its action items). Names the AI recognized in the conversation
// are offered with one click. It opens by itself while there are such suggestions.
export function Speakers({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const labels = useMemo(() => speakerLabels(rec.transcript?.text ?? ''), [rec.transcript?.text]);
  const suggestions = new Map((rec.summary?.speakers ?? []).map((s) => [s.label, s.name]));
  const suggested = labels.some((l) => suggestions.has(l));
  if (labels.length === 0) return null;
  return (
    <details className="speakers" open={suggested || undefined}>
      <summary>
        {t('speakers.title', { count: labels.length })}
        {suggested && <span className="speakers-hint"> · {t('speakers.suggested')}</span>}
      </summary>
      <p className="muted field-note">{t('speakers.hint')}</p>
      <ul className="speaker-list">
        {labels.map((label) => (
          <SpeakerRow key={label} rec={rec} label={label} suggestion={suggestions.get(label)} setRec={setRec} />
        ))}
      </ul>
    </details>
  );
}
