import { ReactNode, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';

// Images are waited for at most this long before the print dialog opens.
const IMAGE_WAIT_MS = 5_000;

export type PrintDoc = { title: string; meta: ReactNode; body: ReactNode };

// PrintSheet prints a note: it puts the title, a line of details and the body on a sheet of
// its own, and while the print dialog is open only that sheet is printed (see .print-sheet in
// styles.css), without the app around it. Pictures are loaded first; onDone is called after
// printing or cancelling.
export function PrintSheet({ doc, onDone }: { doc: PrintDoc | null; onDone: () => void }) {
  const sheet = useRef<HTMLDivElement>(null);
  const onDoneRef = useRef(onDone);
  onDoneRef.current = onDone;

  useEffect(() => {
    if (!doc) return;
    let cancelled = false;
    // Pictures in notes load lazily, which a hidden sheet would never do.
    const images = Array.from(sheet.current?.querySelectorAll('img') ?? []);
    images.forEach((img) => (img.loading = 'eager'));
    const loaded = Promise.all(
      images.map((img) =>
        img.complete
          ? null
          : new Promise((resolve) => {
              img.addEventListener('load', resolve, { once: true });
              img.addEventListener('error', resolve, { once: true });
            }),
      ),
    );
    const finish = () => {
      document.body.classList.remove('printing');
      window.removeEventListener('afterprint', finish);
      if (!cancelled) onDoneRef.current();
    };
    void Promise.race([loaded, new Promise((resolve) => setTimeout(resolve, IMAGE_WAIT_MS))]).then(() => {
      if (cancelled) return;
      document.body.classList.add('printing');
      window.addEventListener('afterprint', finish);
      window.print();
    });
    return () => {
      cancelled = true;
      window.removeEventListener('afterprint', finish);
      document.body.classList.remove('printing');
    };
  }, [doc]);

  if (!doc) return null;
  return createPortal(
    <div ref={sheet} className="print-sheet">
      <h1>{doc.title}</h1>
      <p className="print-meta">{doc.meta}</p>
      {doc.body}
    </div>,
    document.body,
  );
}
