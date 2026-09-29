import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Attachment, Recording } from '../api/client';
import { errorText } from '../lib/errors';
import { formatBytes, inkAttachment } from '../lib/recordings';
import { DownloadIcon, TrashIcon, UploadIcon } from './Icons';

// Attachments lists the files attached to a note, stored with the note on the server, and
// lets an editor add and remove them.
export function Attachments({ rec, setRec, readOnly }: { rec: Recording; setRec: (r: Recording) => void; readOnly: boolean }) {
  const { t } = useTranslation();
  const input = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState<{ name: string; fraction: number } | null>(null);
  const [error, setError] = useState('');
  // The handwriting from the reMarkable is shown as a pill in the note's header instead.
  const ink = inkAttachment(rec);
  const files: Attachment[] = (rec.attachments ?? []).filter((a) => a !== ink);
  const editable = !readOnly && rec.source !== 'remarkable';

  async function add(list: FileList | null) {
    const picked = Array.from(list ?? []);
    if (input.current) input.current.value = '';
    setError('');
    for (const file of picked) {
      setUploading({ name: file.name, fraction: 0 });
      try {
        await api.uploadAttachment(rec.id, file, (fraction) => setUploading({ name: file.name, fraction }));
      } catch (err) {
        setError(t('attachments.failed', { detail: errorText(err, t) }));
        break;
      }
    }
    setUploading(null);
    // The server holds the truth (and the note's new version).
    try {
      setRec(await api.recording(rec.id));
    } catch {
      // the list refreshes with the next load
    }
  }

  async function remove(a: Attachment) {
    setError('');
    try {
      setRec(await api.deleteAttachment(rec.id, a.id));
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  if (!files.length && !editable) return null;
  return (
    <section>
      <h2>{t('attachments.title')}</h2>
      {files.length > 0 && (
        <ul className="attachment-list">
          {files.map((a) => (
            <li key={a.id}>
              <a href={api.attachmentURL(rec.id, a.id)} download={a.name} title={a.name}>
                <DownloadIcon />
                <span className="attachment-name">{a.name}</span>
                <span className="attachment-size">{formatBytes(a.size)}</span>
              </a>
              {editable && (
                <button type="button" className="icon-button" onClick={() => void remove(a)} aria-label={t('attachments.remove', { name: a.name })} title={t('attachments.remove', { name: a.name })}>
                  <TrashIcon />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {uploading && (
        <p className="editor-status" role="status">
          {t('attachments.uploading', { name: uploading.name })} {Math.round(uploading.fraction * 100)}%
        </p>
      )}
      {error && <p className="error">{error}</p>}
      {editable && (
        <>
          <input ref={input} type="file" multiple hidden onChange={(e) => void add(e.target.files)} />
          <button type="button" className="small-button attachment-add" disabled={!!uploading} onClick={() => input.current?.click()}>
            <UploadIcon /> {t('attachments.add')}
          </button>
        </>
      )}
    </section>
  );
}
