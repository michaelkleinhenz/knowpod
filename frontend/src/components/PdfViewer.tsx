import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { PDFDocumentProxy } from 'pdfjs-dist';

// PdfViewer draws a PDF's pages itself with pdf.js. Browsers on phones don't show a PDF in a
// frame (Android shows nothing, iOS only the first page), so the document can't be left to
// the browser's own viewer. Pages are drawn as they scroll into view, at the width of the
// viewer; the link below opens the file in the browser's viewer for searching or zooming.
// pdf.js (the legacy build, for older mobile browsers) is only loaded when a PDF is shown.
async function loadPdfjs() {
  const [pdfjs, worker] = await Promise.all([
    import('pdfjs-dist/legacy/build/pdf.mjs'),
    import('pdfjs-dist/legacy/build/pdf.worker.min.mjs?url'),
  ]);
  pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
  return pdfjs;
}

export function PdfViewer({ url, title }: { url: string; title: string }) {
  const { t } = useTranslation();
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let destroy: (() => void) | null = null;
    setDoc(null);
    setFailed(false);
    loadPdfjs()
      .then((pdfjs) => {
        if (cancelled) return null;
        const task = pdfjs.getDocument({ url });
        destroy = () => void task.destroy();
        return task.promise;
      })
      .then((d) => d && !cancelled && setDoc(d))
      .catch(() => !cancelled && setFailed(true));
    return () => {
      cancelled = true;
      destroy?.();
    };
  }, [url]);

  return (
    <div className="pdf-viewer" role="document" aria-label={title}>
      {failed ? (
        <p className="error">{t('conversation.documentLoadFailed')}</p>
      ) : !doc ? (
        <p className="muted">{t('common.loading')}</p>
      ) : (
        <div className="pdf-pages">
          {Array.from({ length: doc.numPages }, (_, i) => (
            <PdfPage key={i} doc={doc} number={i + 1} />
          ))}
        </div>
      )}
      <p>
        <a href={url} target="_blank" rel="noreferrer">
          {t('conversation.openDocument')}
        </a>
      </p>
    </div>
  );
}

// PdfPage keeps the page's space (from its proportions) and draws it while it is near the
// screen, again when the viewer's width changes.
function PdfPage({ doc, number }: { doc: PDFDocumentProxy; number: number }) {
  const boxRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [ratio, setRatio] = useState(Math.SQRT2);
  const [visible, setVisible] = useState(false);
  const [width, setWidth] = useState(0);

  useEffect(() => {
    let cancelled = false;
    doc.getPage(number).then((page) => {
      const vp = page.getViewport({ scale: 1 });
      if (!cancelled) setRatio(vp.height / vp.width);
    });
    return () => {
      cancelled = true;
    };
  }, [doc, number]);

  useEffect(() => {
    const box = boxRef.current;
    if (!box) return;
    const io = new IntersectionObserver(([e]) => setVisible(e.isIntersecting), { rootMargin: '1200px 0px' });
    io.observe(box);
    const ro = new ResizeObserver(([e]) => setWidth(Math.round(e.contentRect.width)));
    ro.observe(box);
    return () => {
      io.disconnect();
      ro.disconnect();
    };
  }, []);

  useEffect(() => {
    if (!visible || !width) {
      // Phones allow only so much canvas memory: pages far off screen give theirs back.
      const canvas = canvasRef.current;
      if (canvas) canvas.width = canvas.height = 0;
      return;
    }
    let task: { cancel: () => void } | null = null;
    let cancelled = false;
    doc.getPage(number).then((page) => {
      const canvas = canvasRef.current;
      if (cancelled || !canvas) return;
      const base = page.getViewport({ scale: 1 });
      const scale = (width / base.width) * Math.min(window.devicePixelRatio || 1, 2);
      // At most about 8 megapixels a page, well under what mobile browsers allow a canvas.
      const vp = page.getViewport({ scale: Math.min(scale, Math.sqrt(8e6 / (base.width * base.height))) });
      const off = document.createElement('canvas');
      off.width = Math.floor(vp.width);
      off.height = Math.floor(vp.height);
      const render = page.render({ canvas: off, viewport: vp });
      task = render;
      render.promise
        .then(() => {
          if (cancelled) return;
          canvas.width = off.width;
          canvas.height = off.height;
          canvas.getContext('2d')?.drawImage(off, 0, 0);
        })
        .catch(() => {});
    });
    return () => {
      cancelled = true;
      task?.cancel();
    };
  }, [doc, number, visible, width]);

  return (
    <div ref={boxRef} className="pdf-page" style={{ aspectRatio: `1 / ${ratio}` }}>
      <canvas ref={canvasRef} aria-hidden="true" />
    </div>
  );
}
