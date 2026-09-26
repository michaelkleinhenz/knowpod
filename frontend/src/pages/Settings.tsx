import { FormEvent, useEffect, useMemo, useState } from 'react';
import { api, ModelOption, OpenRouterSettings } from '../api/client';

const SUGGESTED = 'google/gemini-2.5-flash';

// price formats OpenRouter's USD-per-token price as USD per million tokens.
function price(p?: string): string {
  const n = Number(p);
  if (!p || !Number.isFinite(n) || n < 0) return '?';
  if (n === 0) return 'free';
  const perM = n * 1_000_000;
  return `$${perM < 1 ? perM.toFixed(2) : perM.toFixed(perM < 10 ? 2 : 0)}`;
}

function optionLabel(m: ModelOption, audio: boolean): string {
  const cost = audio && m.audioPrice ? `${price(m.audioPrice)}/M audio` : `${price(m.promptPrice)}/M in`;
  return `${m.name} — ${cost}, ${price(m.completionPrice)}/M out`;
}

// ModelPicker is a filterable select of OpenRouter models.
function ModelPicker(props: {
  id: string;
  label: string;
  hint: string;
  models: ModelOption[] | null;
  value: string;
  audio: boolean;
  onChange: (v: string) => void;
}) {
  const { id, label, hint, models, value, audio, onChange } = props;
  const [filter, setFilter] = useState('');
  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const list = (models ?? []).filter((m) => !q || m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q));
    // Keep the current choice selectable even if it is filtered out or no longer listed.
    if (value && !list.some((m) => m.id === value)) {
      const current = models?.find((m) => m.id === value);
      list.unshift(current ?? { id: value, name: value, contextLength: 0, promptPrice: '', completionPrice: '' });
    }
    return list;
  }, [models, filter, value]);

  return (
    <div className="model-picker">
      <label htmlFor={id}>{label}</label>
      <p className="muted field-hint">{hint}</p>
      <input
        type="search"
        placeholder={models ? `Filter ${models.length} ${models.length === 1 ? 'model' : 'models'}` : 'Loading models…'}
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        aria-label={`Filter ${label.toLowerCase()} list`}
      />
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">— Select a model —</option>
        {visible.map((m) => (
          <option key={m.id} value={m.id}>
            {optionLabel(m, audio)}
          </option>
        ))}
      </select>
      {value && <code className="model-id">{value}</code>}
    </div>
  );
}

export function Settings() {
  const [current, setCurrent] = useState<OpenRouterSettings | null>(null);
  const [models, setModels] = useState<{ transcription: ModelOption[]; summary: ModelOption[] } | null>(null);
  const [modelsError, setModelsError] = useState<string | null>(null);
  const [apiKey, setAPIKey] = useState('');
  const [transcriptionModel, setTranscriptionModel] = useState('');
  const [summaryModel, setSummaryModel] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.openRouterSettings().then(
      (s) => {
        setCurrent(s);
        setTranscriptionModel(s.transcriptionModel);
        setSummaryModel(s.summaryModel);
      },
      (e: Error) => setError(e.message),
    );
    api.openRouterModels().then(
      (m) => {
        setModels(m);
        // Suggest a model that handles both tasks well when nothing is chosen yet.
        setTranscriptionModel((v) => v || (m.transcription.some((x) => x.id === SUGGESTED) ? SUGGESTED : ''));
        setSummaryModel((v) => v || (m.summary.some((x) => x.id === SUGGESTED) ? SUGGESTED : ''));
      },
      (e: Error) => setModelsError(e.message),
    );
  }, []);

  async function save(update: { apiKey?: string; transcriptionModel?: string; summaryModel?: string }) {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const s = await api.saveOpenRouterSettings(update);
      setCurrent(s);
      setAPIKey('');
      setSaved(true);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const update: { apiKey?: string; transcriptionModel?: string; summaryModel?: string } = { transcriptionModel, summaryModel };
    if (apiKey.trim()) update.apiKey = apiKey.trim();
    save(update);
  }

  const active = !!current?.apiKeyConfigured && !!current.transcriptionModel && !!current.summaryModel;

  return (
    <section className="card narrow-wide">
      <h1>Settings</h1>
      <h2 className="card-title">AI processing (OpenRouter)</h2>
      <p className="muted">
        Every recording is transcribed and then summarized through{' '}
        <a href="https://openrouter.ai" target="_blank" rel="noreferrer">
          OpenRouter
        </a>
        , using the models chosen here. Create an API key at{' '}
        <a href="https://openrouter.ai/settings/keys" target="_blank" rel="noreferrer">
          openrouter.ai/settings/keys
        </a>
        ; usage is billed to that OpenRouter account.
      </p>
      {current && (
        <p>
          <span className={`status-pill ${active ? 'ok' : 'bad'}`}>{active ? 'Active' : 'Not configured'}</span>{' '}
          {!active && <span className="muted">Recordings wait until an API key and both models are set.</span>}
        </p>
      )}

      <form onSubmit={handleSubmit} className="form">
        <label>
          API key
          <input
            type="password"
            autoComplete="off"
            placeholder={current?.apiKeyConfigured ? `Configured (${current.apiKeyHint ?? 'hidden'}). Enter a new key to replace it.` : 'sk-or-v1-…'}
            value={apiKey}
            onChange={(e) => setAPIKey(e.target.value)}
          />
        </label>
        {current?.apiKeyConfigured && (
          <p className="field-hint">
            <button
              type="button"
              className="link-button danger-link"
              disabled={busy}
              onClick={() => window.confirm('Remove the OpenRouter API key? AI processing stops.') && save({ apiKey: '' })}
            >
              Remove API key
            </button>
          </p>
        )}

        {modelsError && <p className="error">Could not load the model list: {modelsError}</p>}
        <ModelPicker
          id="transcription-model"
          label="Transcription model"
          hint="Turns the audio into text. Only models that accept audio are listed."
          models={models?.transcription ?? null}
          value={transcriptionModel}
          audio
          onChange={setTranscriptionModel}
        />
        <ModelPicker
          id="summary-model"
          label="Summary model"
          hint="Writes the title and summary from the transcript."
          models={models?.summary ?? null}
          value={summaryModel}
          audio={false}
          onChange={setSummaryModel}
        />

        {error && <p className="error">{error}</p>}
        {saved && <p className="success">Settings saved.</p>}
        <button type="submit" disabled={busy}>
          {busy ? 'Saving…' : 'Save'}
        </button>
      </form>
      <p className="muted settings-footnote">
        Changing a model affects recordings processed from now on. Use “Re-transcribe” or “Re-summarize” on a conversation to
        process it again.
      </p>
    </section>
  );
}
