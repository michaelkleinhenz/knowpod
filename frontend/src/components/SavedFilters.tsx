import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { api, Folder, Label, SavedFilter, SavedFilterInput } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { FilterContext, parseFilter } from '../lib/filterQuery';
import { BookmarkIcon, FilterIcon, PencilIcon, PinIcon, TrashIcon } from './Icons';

// SYNTAX_CHARS mark a search as meant in the filter language, so its mistakes are pointed out
// (plain words never are).
const SYNTAX_CHARS = /[:&|!()@<>"]/;

// FilterProblem explains why a query doesn't work as a filter, or names the labels and
// folders it names that don't exist; null when it is fine.
function useFilterProblem(query: string, ctx: FilterContext, always = false): string | null {
  const { t } = useTranslation();
  return useMemo(() => {
    const q = query.trim();
    if (!q || (!always && !SYNTAX_CHARS.test(q))) return null;
    const parsed = parseFilter(q, ctx);
    if (!parsed.ok) return t(`filters.errors.${parsed.error}`, { at: parsed.at + 1 });
    if (parsed.unknown.length > 0) return t('filters.unknown', { names: parsed.unknown.join(', '), count: parsed.unknown.length });
    return null;
  }, [query, ctx, always, t]);
}

// FilterBar sits below the search box: the pinned filters to narrow the list with, a hint
// when the search isn't a working filter, and a button that saves the search as a filter.
export function FilterBar({ query, setQuery, active, setActive }: { query: string; setQuery: (q: string) => void; active: string | null; setActive: (id: string | null) => void }) {
  const { t } = useTranslation();
  const { filters, filterContext, reloadFilters } = useNotes();
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const problem = useFilterProblem(query, filterContext);
  const q = query.trim();
  const canSave = q !== '' && parseFilter(q, filterContext).ok;
  const shown = (filters ?? []).filter((f) => f.pinned || f.id === active);

  useEffect(() => {
    if (!canSave) setSaving(false);
  }, [canSave]);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const f = await api.createFilter({ name, query: q, pinned: true });
      await reloadFilters();
      setActive(f.id);
      setQuery('');
      setSaving(false);
      setName('');
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  if (shown.length === 0 && !q) return null;
  return (
    <div className="filter-bar">
      {shown.length > 0 && (
        <div className="filter-chips" role="group" aria-label={t('filters.pinned')}>
          {shown.map((f) => (
            <button
              key={f.id}
              type="button"
              className={`filter-chip${f.id === active ? ' active' : ''}`}
              aria-pressed={f.id === active}
              title={f.query}
              onClick={() => setActive(f.id === active ? null : f.id)}
            >
              <FilterIcon size={12} />
              {f.name}
            </button>
          ))}
        </div>
      )}
      {problem && <p className="filter-hint">{problem}</p>}
      {canSave && !saving && (
        <button type="button" className="link-button filter-save" onClick={() => setSaving(true)}>
          <BookmarkIcon size={14} /> {t('filters.saveSearch')}
        </button>
      )}
      {saving && (
        <form className="filter-save-form" onSubmit={save}>
          <input autoFocus required maxLength={60} value={name} onChange={(e) => setName(e.target.value)} placeholder={t('filters.namePlaceholder')} aria-label={t('filters.name')} />
          <button type="submit" className="small-button" disabled={busy || !name.trim()}>
            {t('common.save')}
          </button>
          <button type="button" className="small-button secondary-button" onClick={() => setSaving(false)}>
            {t('common.cancel')}
          </button>
          {error && <p className="error">{error}</p>}
        </form>
      )}
    </div>
  );
}

// FilterHelp lists the terms of the filter language.
export function FilterHelp() {
  const { t } = useTranslation();
  const rows: [string, string][] = [
    ['call anna', 'text'],
    ['#12', 'number'],
    ['label:Task · @Work · @"Deep work"', 'label'],
    ['folder:Projects', 'folder'],
    ['due:today · due:tomorrow · due:overdue', 'dueDay'],
    ['due:week · due:month', 'dueRange'],
    ['due:none · due:any · due:2026-10-01', 'dueSome'],
    ['due<2026-10-01 · due>=today', 'dueCompare'],
    ['done · task · repeat · estimate', 'flags'],
    ['p1 · p2 · p3 · priority:none', 'priority'],
    ['type:text · type:audio · type:document · type:board', 'type'],
    ['a & b · a b · a | b · !a · (a | b) & c', 'combine'],
  ];
  return (
    <details className="filter-help">
      <summary>{t('filters.help.title')}</summary>
      <dl>
        {rows.map(([code, key]) => (
          <div key={key}>
            <dt>
              <code>{code}</code>
            </dt>
            <dd>{t(`filters.help.${key}`)}</dd>
          </div>
        ))}
      </dl>
    </details>
  );
}

// FilterForm creates or edits a saved filter, checking its query as it is typed.
function FilterForm(props: { initial: SavedFilterInput; ctx: FilterContext; submitLabel: string; onSubmit: (f: SavedFilterInput) => Promise<void>; onCancel?: () => void }) {
  const { t } = useTranslation();
  const [value, setValue] = useState<SavedFilterInput>(props.initial);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const problem = useFilterProblem(value.query, props.ctx, true);
  const broken = !!value.query.trim() && !parseFilter(value.query, props.ctx).ok;

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await props.onSubmit(value);
      setValue(props.initial);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="form filter-form" onSubmit={handleSubmit}>
      <label>
        {t('filters.name')}
        <input required maxLength={60} value={value.name} onChange={(e) => setValue({ ...value, name: e.target.value })} placeholder={t('filters.namePlaceholder')} />
      </label>
      <label>
        {t('filters.query')}
        <input required maxLength={500} className="mono" value={value.query} onChange={(e) => setValue({ ...value, query: e.target.value })} placeholder="label:Task & due:week & !done" spellCheck={false} />
      </label>
      {problem && <p className={broken ? 'error' : 'muted'}>{problem}</p>}
      <label className="checkbox">
        <input type="checkbox" checked={value.pinned} onChange={(e) => setValue({ ...value, pinned: e.target.checked })} />
        {t('filters.pinInList')}
      </label>
      {error && <p className="error">{error}</p>}
      <div className="button-row">
        <button type="submit" disabled={busy || broken}>
          {busy ? t('common.saving') : props.submitLabel}
        </button>
        {props.onCancel && (
          <button type="button" className="secondary-button" onClick={props.onCancel}>
            {t('common.cancel')}
          </button>
        )}
      </div>
    </form>
  );
}

// FilterSettings manages the saved filters: create, edit, pin to the notes list, delete.
export function FilterSettings() {
  const { t } = useTranslation();
  const [filters, setFilters] = useState<SavedFilter[] | null>(null);
  const [labels, setLabels] = useState<Label[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [editing, setEditing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.filters().then(setFilters, (e) => setError(errorText(e, t)));
  }, [t]);
  useEffect(() => {
    load();
    api.labels().then(setLabels, () => undefined);
    api.folders().then(setFolders, () => undefined);
  }, [load]);
  const ctx = useMemo<FilterContext>(() => ({ labels, folders }), [labels, folders]);

  async function act(fn: () => Promise<unknown>) {
    setError(null);
    try {
      await fn();
      load();
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  const remove = (f: SavedFilter) => {
    if (window.confirm(t('filters.deleteConfirm', { name: f.name }))) void act(() => api.deleteFilter(f.id));
  };

  return (
    <>
      <p className="muted">
        <Trans i18nKey="filters.intro" components={{ 1: <code /> }} />
      </p>
      {error && <p className="error">{error}</p>}
      {!filters && !error && <p className="muted">{t('common.loading')}</p>}
      {filters && filters.length === 0 && <p className="muted">{t('filters.none')}</p>}
      {filters && filters.length > 0 && (
        <ul className="filter-list">
          {filters.map((f) =>
            editing === f.id ? (
              <li key={f.id}>
                <FilterForm
                  initial={{ name: f.name, query: f.query, pinned: f.pinned }}
                  ctx={ctx}
                  submitLabel={t('common.save')}
                  onSubmit={async (v) => {
                    await api.updateFilter(f.id, v);
                    setEditing(null);
                    load();
                  }}
                  onCancel={() => setEditing(null)}
                />
              </li>
            ) : (
              <li key={f.id} className="filter-row">
                <div className="filter-row-text">
                  <strong>{f.name}</strong>
                  <code>{f.query}</code>
                </div>
                <button
                  type="button"
                  className={`icon-button small${f.pinned ? ' active' : ''}`}
                  aria-pressed={f.pinned}
                  title={t(f.pinned ? 'filters.unpin' : 'filters.pin')}
                  aria-label={t(f.pinned ? 'filters.unpin' : 'filters.pin')}
                  onClick={() => void act(() => api.updateFilter(f.id, { name: f.name, query: f.query, pinned: !f.pinned }))}
                >
                  <PinIcon filled={f.pinned} />
                </button>
                <button type="button" className="icon-button small" title={t('common.edit')} aria-label={t('filters.editLabel', { name: f.name })} onClick={() => setEditing(f.id)}>
                  <PencilIcon />
                </button>
                <button type="button" className="icon-button small danger" title={t('common.delete')} aria-label={t('filters.deleteLabel', { name: f.name })} onClick={() => remove(f)}>
                  <TrashIcon />
                </button>
              </li>
            ),
          )}
        </ul>
      )}
      <h3 className="card-subtitle">{t('filters.new')}</h3>
      <FilterForm
        initial={{ name: '', query: '', pinned: true }}
        ctx={ctx}
        submitLabel={t('filters.create')}
        onSubmit={async (v) => {
          await api.createFilter(v);
          load();
        }}
      />
      <FilterHelp />
    </>
  );
}
