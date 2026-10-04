import { FormEvent, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { errorText } from '../lib/errors';

// SPEAKER matches a transcript line starting with a speaker label, after an optional time
// stamp ("[1:05] Speaker 2: …", also "[0:3]"), as the server reads them.
const SPEAKER = /^\s*(?:\[(?:\d{1,2}:)?\d{1,3}:\d{1,2}\]\s*)?([^:\n[\]]{1,40}):\s/;

// speakerLabels returns the speaker labels of a transcript in the order they first speak.
export function speakerLabels(text: string): string[] {
  const out: string[] = [];
  for (const line of text.split('\n')) {
    const label = SPEAKER.exec(line)?.[1]?.trim();
    if (label && !out.includes(label)) out.push(label);
  }
  return out;
}

// Speakers names the speakers of a recording's transcript in the note's sidebar: "Speaker 1"
// becomes "Anna" in the transcript. Then the summary is made again from the renamed
// transcript, so it uses the names throughout; without that, the labels are replaced where
// the summary and its action items mention them. Names the AI recognized in the conversation
// are offered with one click. Naming two speakers the same merges them.
export function Speakers({
  rec,
  setRec,
  onRegenerate,
}: {
  rec: Recording;
  setRec: (r: Recording) => void;
  // onRegenerate runs a request that makes the summary again (asking first, as for regenerating it).
  onRegenerate: (request: () => Promise<unknown>) => Promise<void>;
}) {
  const { t } = useTranslation();
  const labels = useMemo(() => speakerLabels(rec.transcript?.text ?? ''), [rec.transcript?.text]);
  const suggestions = new Map((rec.summary?.speakers ?? []).map((s) => [s.label, s.name]));
  const [names, setNames] = useState<Record<string, string>>({});
  const [resummarize, setResummarize] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (labels.length === 0) return null;

  const processing = rec.status !== 'summarized' && rec.status !== 'failed';
  const changes = labels.map((label) => ({ from: label, to: (names[label] ?? '').trim() })).filter((n) => n.to && n.to !== n.from);
  const canResummarize = !!rec.summary;

  async function save(e: FormEvent) {
    e.preventDefault();
    if (changes.length === 0) return;
    setBusy(true);
    setError(null);
    try {
      if (resummarize && canResummarize) {
        await onRegenerate(() => api.nameSpeakers(rec.id, changes, true));
      } else {
        setRec(await api.nameSpeakers(rec.id, changes, false));
      }
      setNames({});
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="speakers">
      <h2>{t('speakers.title')}</h2>
      <p className="muted field-note">{t('speakers.hint')}</p>
      <form onSubmit={(e) => void save(e)}>
        <fieldset className="view-only-fieldset" disabled={busy || processing}>
          <ul className="speaker-list">
            {labels.map((label) => {
              const suggestion = suggestions.get(label);
              return (
                <li key={label} className="speaker-row">
                  <span className="speaker">{label}</span>
                  <input
                    value={names[label] ?? ''}
                    maxLength={40}
                    placeholder={t('speakers.namePlaceholder')}
                    aria-label={t('speakers.nameLabel', { label })}
                    onChange={(e) => setNames({ ...names, [label]: e.target.value })}
                  />
                  {suggestion && names[label] !== suggestion && (
                    <button
                      type="button"
                      className="pill-button speaker-suggestion"
                      title={t('speakers.suggestionTitle')}
                      onClick={() => setNames({ ...names, [label]: suggestion })}
                    >
                      {t('speakers.useSuggestion', { name: suggestion })}
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
          {canResummarize && (
            <label className="check-row">
              <input type="checkbox" checked={resummarize} onChange={(e) => setResummarize(e.target.checked)} />
              {t('speakers.resummarize')}
            </label>
          )}
          <button type="submit" className="pill-button" disabled={changes.length === 0}>
            {resummarize && canResummarize ? t('speakers.saveAndResummarize') : t('speakers.save')}
          </button>
        </fieldset>
      </form>
      {processing && <p className="muted field-note">{t('speakers.processing')}</p>}
      {error && <p className="error">{error}</p>}
    </section>
  );
}
