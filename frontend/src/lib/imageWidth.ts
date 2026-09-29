// A picture's width is kept in the Markdown as a fragment of its URL, ![](/api/…/images/ID#w=320),
// which browsers ignore when loading the picture and other Markdown readers still understand.

export const MIN_IMAGE_WIDTH = 40;

// imageWidth returns the width in pixels stored in the URL, or undefined for the natural size.
export function imageWidth(src: string): number | undefined {
  const m = /#w=(\d{1,5})$/.exec(src);
  const w = m ? Number(m[1]) : 0;
  return w >= MIN_IMAGE_WIDTH ? w : undefined;
}

// withImageWidth returns the URL with the width set (or removed when undefined).
export function withImageWidth(src: string, width?: number): string {
  const base = src.replace(/#w=\d*$/, '');
  return width ? `${base}#w=${Math.max(MIN_IMAGE_WIDTH, Math.round(width))}` : base;
}
