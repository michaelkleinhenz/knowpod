import { useTranslation } from 'react-i18next';
import type { Person, Recording } from '../api/client';
import { useNotesIfAny } from '../context/NotesContext';
import { avatarHue, initials, madeBy } from '../lib/people';

// Avatar shows a person as their initials on a color of their own.
export function Avatar({ person, size = 18, title }: { person: Pick<Person, 'email' | 'userId'>; size?: number; title?: string }) {
  return (
    <span className={`avatar hue-${avatarHue(person.userId)}`} style={{ width: size, height: size, fontSize: size * 0.48 }} title={title ?? person.email} role="img" aria-label={title ?? person.email}>
      {initials(person) || '?'}
    </span>
  );
}

// noteOrigin is who a note comes from when that isn't the user: the owner of a note shared
// with them, or whoever made it in the user's own shared note. Null for the user's own.
export function noteOrigin(r: Recording, userId: string | undefined): { userId: string; kind: 'sharedBy' | 'madeBy' } | null {
  if (!userId) return null;
  if (r.ownerId && r.ownerId !== userId) return { userId: r.ownerId, kind: 'sharedBy' };
  const maker = madeBy(r);
  if (maker && maker !== userId) return { userId: maker, kind: 'madeBy' };
  return null;
}

// OriginBadge marks a note that comes from someone else with their avatar: "Shared by bob",
// or "Added by bob" for a note bob made in one of the user's shared notes. Nothing for the
// user's own notes.
export function OriginBadge({ rec, size = 16 }: { rec: Recording; size?: number }) {
  const { t } = useTranslation();
  const notes = useNotesIfAny();
  const origin = noteOrigin(rec, notes?.filterContext.userId);
  if (!origin) return null;
  const person = notes?.people.find((p) => p.userId === origin.userId) ?? { userId: origin.userId, email: '' };
  const maker = madeBy(rec);
  let title = t(`people.${origin.kind}`, { name: person.email || t('people.someone') });
  if (origin.kind === 'sharedBy' && maker && maker !== origin.userId) {
    const m = notes?.people.find((p) => p.userId === maker);
    title += ' · ' + t('people.madeBy', { name: m ? (m.self ? t('people.you') : m.email) : t('people.someone') });
  }
  return (
    <span className="origin-badge">
      <Avatar person={person} size={size} title={title} />
    </span>
  );
}
