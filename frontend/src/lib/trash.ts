import { TRASH_DAYS } from '../api/client';

const DAY_MS = 24 * 60 * 60 * 1000;

// purgeDate is when a note moved to the trash at deletedAt is deleted for good.
export function purgeDate(deletedAt: string): Date {
  return new Date(new Date(deletedAt).getTime() + TRASH_DAYS * DAY_MS);
}

// daysLeft is how many days (at least 1) a note stays in the trash before it is deleted.
export function daysLeft(deletedAt: string, now = Date.now()): number {
  return Math.max(1, Math.ceil((purgeDate(deletedAt).getTime() - now) / DAY_MS));
}
