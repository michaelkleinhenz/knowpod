import { FormEvent, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { NoteIcon } from '../components/Icons';
import { useNotes } from '../context/NotesContext';
import { createMarkdownNote, isMarkdown } from '../lib/markdownFile';
import { errorText } from '../lib/errors';
import { formatBytes } from '../lib/recordings';

// What another app shared with the installed app, as public/share-sw.js keeps it.
interface SharedFile {
  key: string;
  name: string;
  type: string;
  size: number;
  lastModified?: number;
}

interface Shared {
  title: string;
  text: string;
  url: string;
  files: SharedFile[];
}

const SHARE_CACHE = 'knowpod-share';

async function readShared(): Promise<Shared | null> {
  if (typeof caches === 'undefined') return null;
  const cache = await caches.open(SHARE_CACHE);
  const meta = await cache.match('/share-data/meta');
  return meta ? ((await meta.json()) as Shared) : null;
}

async function sharedFile(f: SharedFile): Promise<File | null> {
  const res = await (await caches.open(SHARE_CACHE)).match(f.key);
  if (!res) return null;
  return new File([await res.blob()], f.name || 'shared', { type: f.type, lastModified: f.lastModified });
}

async function clearShared() {
  if (typeof caches !== 'undefined') await caches.delete(SHARE_CACHE);
}

const URL_LINE = /^https?:\/\/\S+$/;

// sharedText makes the Markdown of a shared link or text: lines that are just a link become
// Markdown links, and the link comes last when it isn't in the text already.
function sharedText(s: Shared): string {
  const link = (u: string) => `[${u}](${u})`;
  const lines = s.text
    .replace(/\r/g, '')
    .split('\n')
    .map((l) => (URL_LINE.test(l.trim()) ? link(l.trim()) : l));
  if (s.url && !s.text.includes(s.url)) lines.push('', link(s.url));
  return lines.join('\n').trim();
}

// sharedTitle names a shared text: the title the app gave, else its first line, else the
// link's site.
function sharedTitle(s: Shared, fallback: string): string {
  if (s.title.trim()) return s.title.trim().slice(0, 200);
  const first = s.text.split('\n').find((l) => l.trim() && !URL_LINE.test(l.trim()));
  if (first) return first.trim().slice(0, 80);
  const u = s.url || s.text.split(/\s/).find((w) => URL_LINE.test(w));
  try {
    return u ? new URL(u).hostname.replace(/^www\./, '') : fallback;
  } catch {
    return fallback;
  }
}

const fileKind = (f: SharedFile) =>
  f.type.startsWith('image/') ? 'photo' : f.type === 'application/pdf' ? 'pdf' : /\.(md|markdown)$/i.test(f.name) || f.type === 'text/markdown' ? 'text' : 'audio';

// Share saves what another app shared with the installed app (Share → knowpod): a link or
// text becomes a text note, photos, PDFs and audio files are uploaded like files picked here.
export function Share() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const notes = useNotes();
  const [shared, setShared] = useState<Shared | null | undefined>(undefined);
  const [title, setTitle] = useState('');
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<Record<string, number>>({});
  const [errors, setErrors] = useState<string[]>([]);
  // The items saved already (text, file keys) and their notes, so trying again after a
  // failure saves only the rest.
  const [saved, setSaved] = useState<Record<string, Recording>>({});

  useEffect(() => {
    readShared().then(
      (s) => {
        setShared(s);
        if (s) {
          setTitle(sharedTitle(s, t('share.untitled')));
          setText(sharedText(s));
        }
      },
      () => setShared(null),
    );
  }, [t]);

  const hasText = !!shared && !!(shared.text.trim() || shared.url.trim());

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!shared) return;
    setBusy(true);
    setErrors([]);
    const done = { ...saved };
    const failed: string[] = [];
    if (hasText && !done.text) {
      try {
        done.text = await api.createTextNote(title.trim() || t('share.untitled'), text);
      } catch (err) {
        failed.push(errorText(err, t));
      }
    }
    for (const f of shared.files) {
      if (done[f.key]) continue;
      try {
        const file = await sharedFile(f);
        if (!file) throw new Error(t('share.fileGone', { name: f.name }));
        done[f.key] = isMarkdown(file)
          ? await createMarkdownNote(file, t('conversations.markdownTooLong', { name: file.name }))
          : await api.uploadRecording(file, (p) => setProgress((m) => ({ ...m, [f.key]: p })));
      } catch (err) {
        failed.push(`${f.name}: ${errorText(err, t)}`);
      }
    }
    const list = Object.values(done);
    list.forEach(notes.upsert);
    setSaved(done);
    setBusy(false);
    if (failed.length > 0) {
      setErrors(failed);
      return;
    }
    await clearShared();
    navigate(list.length === 1 ? `/conversations/${list[0].id}` : '/', { replace: true });
  }

  async function discard() {
    await clearShared();
    navigate('/', { replace: true });
  }

  if (shared === undefined) return <p className="muted share-page">{t('common.loading')}</p>;
  if (!shared || (!hasText && shared.files.length === 0)) {
    return (
      <div className="share-page">
        <h1>{t('share.title')}</h1>
        {params.get('missed') && <p className="notice bad">{t('share.missed')}</p>}
        <p className="muted">{t('share.howTo')}</p>
      </div>
    );
  }
  return (
    <form className="share-page form" onSubmit={save}>
      <h1>{t('share.title')}</h1>
      <p className="muted">{t('share.intro')}</p>
      {hasText && (
        <>
          <label>
            {t('share.noteTitle')}
            <input value={title} maxLength={200} onChange={(e) => setTitle(e.target.value)} required disabled={!!saved.text} />
          </label>
          <label>
            {t('share.noteText')}
            <textarea rows={6} value={text} onChange={(e) => setText(e.target.value)} disabled={!!saved.text} />
          </label>
        </>
      )}
      {shared.files.length > 0 && (
        <ul className="share-files">
          {shared.files.map((f) => (
            <li key={f.key}>
              <NoteIcon type={fileKind(f)} />
              <span className="share-file-name">{f.name}</span>
              <span className="muted">{formatBytes(f.size)}</span>
              {saved[f.key] ? <span className="success">{t('share.saved')}</span> : progress[f.key] !== undefined && <progress max={1} value={progress[f.key]} />}
            </li>
          ))}
        </ul>
      )}
      {errors.map((err) => (
        <p key={err} className="error">
          {err}
        </p>
      ))}
      <div className="button-row">
        <button type="submit" disabled={busy}>
          {busy ? t('common.saving') : t('share.save')}
        </button>
        <button type="button" className="secondary-button" disabled={busy} onClick={() => void discard()}>
          {t('share.discard')}
        </button>
      </div>
    </form>
  );
}
