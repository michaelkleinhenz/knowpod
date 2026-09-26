import type { TFunction } from 'i18next';
import { ApiError } from '../api/client';

// errorText turns an error into a message in the current language: known API error codes
// are translated, everything else falls back to the server's (English) message.
export function errorText(err: unknown, t: TFunction): string {
  if (err instanceof ApiError && err.code) {
    // Messages like "invalid input: name must be 1-60 characters" carry a useful detail.
    const detail = err.message.includes(': ') ? err.message.slice(err.message.indexOf(': ') + 2) : err.message;
    const key = `errors.${err.code}`;
    const text = t(key, { detail, defaultValue: '' });
    if (text && text !== key) return text;
  }
  if (err instanceof TypeError) return t('errors.network');
  return err instanceof Error ? err.message : String(err);
}
