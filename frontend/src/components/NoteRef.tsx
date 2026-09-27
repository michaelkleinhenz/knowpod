import { Link } from 'react-router-dom';
import { useNotesIfAny } from '../context/NotesContext';
import { noteByNumber, noteRefPath } from '../lib/noteRefs';
import { title } from '../lib/recordings';

// NoteRef is a "#12" reference to another note in a note's text: a link to the note, named
// by its title on hover. Numbers of notes not in the list still link (they are looked up).
export function NoteRef({ number }: { number: number }) {
  const notes = useNotesIfAny();
  const note = noteByNumber(notes?.recordings, number);
  const unknown = notes?.recordings && notes.recordings.length > 0 && !note;
  return (
    <Link to={note ? `/conversations/${note.id}` : noteRefPath(number)} className={`note-ref${unknown ? ' unknown' : ''}`} title={note ? title(note) : undefined}>
      #{number}
    </Link>
  );
}
