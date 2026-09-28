// Vite gives the URL of the pdf.js worker file for the ?url import (src/components/PdfViewer.tsx).
declare module 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url' {
  const url: string;
  export default url;
}
