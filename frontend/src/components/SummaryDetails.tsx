import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, ModelOption, Recording, SummaryOptions, Theme } from '../api/client';
import { errorText } from '../lib/errors';
import { languageName } from '../lib/recordings';
import { useThemeText } from '../lib/themes';
import { SlidersIcon } from './Icons';

type View = 'main' | 'language' | 'model' | 'theme';

interface Option {
  id: string;
  label: string;
  sub?: string;
}

// OptionList is a searchable list inside the details panel.
function OptionList(props: {
  title: string;
  placeholder: string;
  options: Option[] | null;
  selected: string;
  onSelect: (id: string) => void;
  onBack: () => void;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState('');
  const filtered = useMemo(() => {
    const query = q.trim().toLowerCase();
    return (props.options ?? []).filter((o) => !query || `${o.label} ${o.sub ?? ''} ${o.id}`.toLowerCase().includes(query));
  }, [props.options, q]);
  return (
    <div className="details-list">
      <div className="details-head">
        <button type="button" className="details-back" onClick={props.onBack} aria-label={t('common.back')}>
          ‹
        </button>
        <span>{props.title}</span>
      </div>
      <input
        className="details-search"
        type="search"
        autoFocus
        placeholder={props.placeholder}
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      <ul role="listbox" aria-label={props.title}>
        {!props.options && <li className="muted details-empty">{t('common.loading')}</li>}
        {props.options && filtered.length === 0 && <li className="muted details-empty">{t('common.noMatches')}</li>}
        {filtered.map((o) => (
          <li key={o.id}>
            <button
              type="button"
              role="option"
              aria-selected={o.id === props.selected}
              className={o.id === props.selected ? 'selected' : ''}
              onClick={() => props.onSelect(o.id)}
            >
              <span className="option-text">
                <span className="option-label">{o.label}</span>
                {o.sub && <span className="option-sub">{o.sub}</span>}
              </span>
              {o.id === props.selected && <span className="check">✓</span>}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// SummaryDetails is the "Summary details" menu of a note: it shows and changes the language,
// model and theme of the summary, and regenerates it. onRegenerate runs the request; the
// page asks for confirmation and makes sure pending edits aren't saved over the new summary.
export function SummaryDetails({ rec, onRegenerate }: { rec: Recording; onRegenerate: (request: () => Promise<unknown>) => Promise<void> }) {
  const { t } = useTranslation();
  const themeText = useThemeText();
  const current: SummaryOptions = {
    language: rec.summary?.language || rec.summaryOptions?.language || 'auto',
    model: rec.summaryOptions?.model || '',
    themeId: rec.summary?.themeId || rec.summaryOptions?.themeId || 'auto',
  };
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<View>('main');
  const [pending, setPending] = useState<SummaryOptions>(current);
  const [themes, setThemes] = useState<Theme[] | null>(null);
  const [languages, setLanguages] = useState<string[] | null>(null);
  const [models, setModels] = useState<ModelOption[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const root = useRef<HTMLDivElement>(null);

  // Load the choices the first time the menu opens.
  useEffect(() => {
    if (!open) return;
    if (!themes) api.themes().then(setThemes, () => setThemes([]));
    if (!languages) api.aiLanguages().then(setLanguages, () => setLanguages([]));
    if (!models) api.aiModels().then((m) => setModels(m.summary), () => setModels([]));
  }, [open, themes, languages, models]);

  // Close on outside click and Escape.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent | TouchEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false);
    document.addEventListener('mousedown', onDown);
    document.addEventListener('touchstart', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('touchstart', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  function toggle() {
    if (!open) {
      setPending(current);
      setView('main');
      setError(null);
    }
    setOpen(!open);
  }

  async function regenerate() {
    setBusy(true);
    setError(null);
    try {
      setOpen(false);
      await onRegenerate(() => api.resummarize(rec.id, pending));
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  const themeOf = (id?: string) => themes?.find((th) => th.id === id);
  const themeLabel = (id?: string) => {
    const th = themeOf(id);
    if (th) return themeText(th).name;
    if (id && id === rec.summary?.themeId && rec.summary?.themeName) return themeText({ id, name: rec.summary.themeName, builtIn: true }).name;
    return themeText({ id: id || 'auto', name: id || 'Auto', builtIn: true }).name;
  };
  const languageLabel = (tag?: string) => (!tag || tag === 'auto' ? t('details.autoDetect') : languageName(tag));
  const modelLabel = (id?: string) => {
    if (!id) return t('details.defaultModel');
    return models?.find((m) => m.id === id)?.name ?? id;
  };
  const canRegenerate = !!rec.transcript;

  const rows: { view: View; label: string; value: string }[] = [
    { view: 'language', label: t('details.language'), value: languageLabel(pending.language) },
    { view: 'model', label: t('details.model'), value: modelLabel(pending.model) },
    { view: 'theme', label: t('details.theme'), value: themeLabel(pending.themeId) },
  ];

  return (
    <div className="summary-details" ref={root}>
      <button
        type="button"
        className={`icon-button details-trigger${open ? ' active' : ''}`}
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label={t('details.title')}
        title={`${t('details.title')}: ${themeLabel(current.themeId)}`}
        onClick={toggle}
      >
        <SlidersIcon />
      </button>
      {open && (
        <>
          <div className="details-backdrop" onClick={() => setOpen(false)} />
          <div className="details-panel" role="dialog" aria-label={t('details.title')}>
            {view === 'main' && (
              <>
                <div className="details-title">{t('details.title')}</div>
                <ul className="details-rows">
                  {rows.map((r) => (
                    <li key={r.view}>
                      <button type="button" onClick={() => setView(r.view)}>
                        <span className="row-label">{r.label}</span>
                        <span className="row-value">{r.value}</span>
                        <span className="chevron">›</span>
                      </button>
                    </li>
                  ))}
                </ul>
                {error && <p className="error details-error">{error}</p>}
                <button type="button" className="regenerate" disabled={busy || !canRegenerate} onClick={regenerate}>
                  <span aria-hidden="true">✦</span> {busy ? t('details.regenerating') : t('details.regenerate')}
                </button>
                {!canRegenerate && <p className="muted details-note">{t('conversation.needsTranscript')}</p>}
              </>
            )}
            {view === 'language' && (
              <OptionList
                title={t('details.language')}
                placeholder={t('details.searchLanguages')}
                options={
                  languages && [
                    { id: 'auto', label: t('details.autoDetect') },
                    ...languages
                      .map((tag) => ({ id: tag, label: languageName(tag), sub: tag }))
                      .sort((a, b) => a.label.localeCompare(b.label)),
                  ]
                }
                selected={pending.language || 'auto'}
                onSelect={(id) => {
                  setPending({ ...pending, language: id });
                  setView('main');
                }}
                onBack={() => setView('main')}
              />
            )}
            {view === 'model' && (
              <OptionList
                title={t('details.model')}
                placeholder={t('details.searchModels')}
                options={
                  models && [
                    { id: '', label: t('details.defaultModel') },
                    ...models.map((m) => ({ id: m.id, label: m.name, sub: m.id })),
                  ]
                }
                selected={pending.model || ''}
                onSelect={(id) => {
                  setPending({ ...pending, model: id });
                  setView('main');
                }}
                onBack={() => setView('main')}
              />
            )}
            {view === 'theme' && (
              <OptionList
                title={t('details.theme')}
                placeholder={t('details.searchThemes')}
                options={themes && themes.map((th) => ({ id: th.id, label: themeText(th).name, sub: themeText(th).description }))}
                selected={pending.themeId || 'auto'}
                onSelect={(id) => {
                  setPending({ ...pending, themeId: id });
                  setView('main');
                }}
                onBack={() => setView('main')}
              />
            )}
          </div>
        </>
      )}
    </div>
  );
}
