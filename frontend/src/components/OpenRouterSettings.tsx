import { FormEvent, useEffect, useMemo, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { api, ModelOption, OpenRouterSettings as Settings } from '../api/client';
import { locale } from '../i18n';
import { errorText } from '../lib/errors';

const SUGGESTED = 'google/gemini-2.5-flash';

// usePrice formats OpenRouter's USD-per-token price as USD per million tokens.
function usePrice() {
  const { t } = useTranslation();
  return (p?: string): string => {
    const n = Number(p);
    if (!p || !Number.isFinite(n) || n < 0) return '?';
    if (n === 0) return t('settings.ai.free');
    const perM = n * 1_000_000;
    return new Intl.NumberFormat(locale(), { style: 'currency', currency: 'USD', maximumFractionDigits: perM < 10 ? 2 : 0 }).format(perM);
  };
}

// ModelPicker is a filterable select of OpenRouter models.
function ModelPicker(props: {
  id: string;
  label: string;
  hint: string;
  models: ModelOption[] | null;
  value: string;
  audio: boolean;
  // emptyLabel names the empty choice when it means a default rather than "not set".
  emptyLabel?: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const price = usePrice();
  const { id, label, hint, models, value, audio, emptyLabel, onChange } = props;
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

  const optionLabel = (m: ModelOption) => {
    const cost = audio && m.audioPrice ? t('settings.ai.priceAudio', { price: price(m.audioPrice) }) : t('settings.ai.priceIn', { price: price(m.promptPrice) });
    return `${m.name} — ${cost}, ${t('settings.ai.priceOut', { price: price(m.completionPrice) })}`;
  };

  return (
    <div className="model-picker">
      <label htmlFor={id}>{label}</label>
      <p className="muted field-hint">{hint}</p>
      <input
        type="search"
        placeholder={models ? t('settings.ai.filterModels', { count: models.length }) : t('settings.ai.loadingModels')}
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        aria-label={label}
      />
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{emptyLabel ?? t('settings.ai.selectModel')}</option>
        {visible.map((m) => (
          <option key={m.id} value={m.id}>
            {optionLabel(m)}
          </option>
        ))}
      </select>
      {value && <code className="model-id">{value}</code>}
    </div>
  );
}

// OpenRouterSettings is the administrators' configuration of AI processing.
export function OpenRouterSettings() {
  const { t } = useTranslation();
  const [current, setCurrent] = useState<Settings | null>(null);
  const [models, setModels] = useState<{ transcription: ModelOption[]; summary: ModelOption[]; document: ModelOption[] } | null>(null);
  const [modelsError, setModelsError] = useState<string | null>(null);
  const [apiKey, setAPIKey] = useState('');
  const [transcriptionModel, setTranscriptionModel] = useState('');
  const [summaryModel, setSummaryModel] = useState('');
  const [documentModel, setDocumentModel] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.openRouterSettings().then(
      (s) => {
        setCurrent(s);
        setTranscriptionModel(s.transcriptionModel);
        setSummaryModel(s.summaryModel);
        setDocumentModel(s.documentModel ?? '');
      },
      (e) => setError(errorText(e, t)),
    );
    api.aiModels().then(
      (m) => {
        setModels(m);
        // Suggest a model that handles both tasks well when nothing is chosen yet.
        setTranscriptionModel((v) => v || (m.transcription.some((x) => x.id === SUGGESTED) ? SUGGESTED : ''));
        setSummaryModel((v) => v || (m.summary.some((x) => x.id === SUGGESTED) ? SUGGESTED : ''));
      },
      (e) => setModelsError(errorText(e, t)),
    );
  }, [t]);

  async function save(update: { apiKey?: string; transcriptionModel?: string; summaryModel?: string; documentModel?: string }) {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      setCurrent(await api.saveOpenRouterSettings(update));
      setAPIKey('');
      setSaved(true);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const update: { apiKey?: string; transcriptionModel?: string; summaryModel?: string; documentModel?: string } = {
      transcriptionModel,
      summaryModel,
      documentModel,
    };
    if (apiKey.trim()) update.apiKey = apiKey.trim();
    save(update);
  }

  const active = !!current?.apiKeyConfigured && !!current.transcriptionModel && !!current.summaryModel;

  return (
    <>
      <p className="muted">
        <Trans
          i18nKey="settings.ai.intro"
          components={{
            1: <a href="https://openrouter.ai" target="_blank" rel="noreferrer" />,
            3: <a href="https://openrouter.ai/settings/keys" target="_blank" rel="noreferrer" />,
          }}
        />
      </p>
      {current && (
        <p>
          <span className={`status-pill ${active ? 'ok' : 'bad'}`}>{active ? t('settings.ai.active') : t('settings.ai.notConfigured')}</span>{' '}
          {!active && <span className="muted">{t('settings.ai.waitHint')}</span>}
        </p>
      )}

      <form onSubmit={handleSubmit} className="form">
        <label>
          {t('settings.ai.apiKey')}
          <input
            type="password"
            autoComplete="off"
            placeholder={current?.apiKeyConfigured ? t('settings.ai.apiKeyPlaceholderSet', { hint: current.apiKeyHint ?? '…' }) : 'sk-or-v1-…'}
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
              onClick={() => window.confirm(t('settings.ai.removeKeyConfirm')) && save({ apiKey: '' })}
            >
              {t('settings.ai.removeKey')}
            </button>
          </p>
        )}

        {modelsError && <p className="error">{t('settings.ai.modelsError', { error: modelsError })}</p>}
        <ModelPicker
          id="transcription-model"
          label={t('settings.ai.transcriptionModel')}
          hint={t('settings.ai.transcriptionHint')}
          models={models?.transcription ?? null}
          value={transcriptionModel}
          audio
          onChange={setTranscriptionModel}
        />
        <ModelPicker
          id="summary-model"
          label={t('settings.ai.summaryModel')}
          hint={t('settings.ai.summaryHint')}
          models={models?.summary ?? null}
          value={summaryModel}
          audio={false}
          onChange={setSummaryModel}
        />
        <ModelPicker
          id="document-model"
          label={t('settings.ai.documentModel')}
          hint={t('settings.ai.documentHint')}
          models={models?.document ?? null}
          value={documentModel}
          audio={false}
          emptyLabel={t('settings.ai.sameAsTranscription')}
          onChange={setDocumentModel}
        />

        {error && <p className="error">{error}</p>}
        {saved && <p className="success">{t('settings.ai.saved')}</p>}
        <button type="submit" disabled={busy}>
          {busy ? t('common.saving') : t('common.save')}
        </button>
      </form>
      <p className="muted settings-footnote">{t('settings.ai.footnote')}</p>
    </>
  );
}
