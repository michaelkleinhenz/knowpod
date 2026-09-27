package remarkable

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"strconv"
	"unicode/utf16"

	"golang.org/x/image/vector"
)

// highlighterAlpha is the opacity of highlighter and shader strokes.
const highlighterAlpha = 0.35

// palette holds the colors of the pens by color ID.
var palette = []color.RGBA{
	{0, 0, 0, 255},       // black
	{144, 144, 144, 255}, // gray
	{255, 255, 255, 255}, // white
	{251, 247, 25, 255},  // yellow
	{0, 176, 80, 255},    // green
	{255, 128, 170, 255}, // pink
	{78, 105, 201, 255},  // blue
	{179, 62, 57, 255},   // red
	{125, 125, 125, 255}, // gray (overlap)
	{255, 237, 117, 255}, // highlight
	{161, 216, 125, 255}, // green 2
	{139, 208, 229, 255}, // cyan
	{183, 130, 205, 255}, // magenta
	{247, 232, 81, 255},  // yellow 2
}

// strokeColor returns the color a stroke is drawn in (without its opacity).
func strokeColor(s *Stroke) color.RGBA {
	if s.ARGB != 0 {
		return color.RGBA{uint8(s.ARGB >> 16), uint8(s.ARGB >> 8), uint8(s.ARGB), 255}
	}
	if s.Color >= 0 && s.Color < len(palette) {
		c := palette[s.Color]
		if s.Highlighter() && s.Color == 0 {
			return palette[3] // highlighters without a color are yellow
		}
		return c
	}
	return palette[0]
}

// width returns the drawn width of a point: at least one pixel.
func width(p Point) float64 { return math.Max(float64(p.Width), 1) }

// bounds returns the area of a page: the screen, grown to hold strokes outside it (notebook
// pages can be scrolled down, and strokes can reach past the edges).
func bounds(strokes []Stroke) (minX, minY, maxX, maxY float64) {
	minX, minY, maxX, maxY = 0, 0, PageWidth, PageHeight
	for _, s := range strokes {
		for _, p := range s.Points {
			r := width(p) / 2
			minX = math.Min(minX, float64(p.X)-r)
			minY = math.Min(minY, float64(p.Y)-r)
			maxX = math.Max(maxX, float64(p.X)+r)
			maxY = math.Max(maxY, float64(p.Y)+r)
		}
	}
	return math.Floor(minX), math.Floor(minY), math.Ceil(maxX), math.Ceil(maxY)
}

// ordered returns the strokes with highlighters first, so that ink is drawn over them.
func ordered(strokes []Stroke) []Stroke {
	out := make([]Stroke, 0, len(strokes))
	for _, s := range strokes {
		if s.Highlighter() {
			out = append(out, s)
		}
	}
	for _, s := range strokes {
		if !s.Highlighter() {
			out = append(out, s)
		}
	}
	return out
}

// --- PDF ---

// pdfScale converts screen pixels (226 dpi) to PDF points (72 per inch).
const pdfScale = 72.0 / 226

// WritePDF writes the pages as a PDF with one page per notebook page and the given title.
// Strokes are vector paths, so the PDF stays sharp at any zoom.
func WritePDF(w io.Writer, pages [][]Stroke, title string) error {
	if len(pages) == 0 {
		pages = [][]Stroke{nil}
	}
	pw := &pdfWriter{w: bufio.NewWriter(w)}
	pw.printf("%%PDF-1.4\n%%\xe2\xe3\xcf\xd3\n")

	n := len(pages)
	kids := make([]byte, 0, n*10)
	for i := 0; i < n; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R ", 4+2*i)...)
	}
	pw.object(1, "<< /Type /Catalog /Pages 2 0 R >>")
	pw.object(2, fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", n, kids))
	pw.object(3, fmt.Sprintf("<< /Type /ExtGState /ca %.2f /CA %.2f >>", highlighterAlpha, highlighterAlpha))
	for i, strokes := range pages {
		minX, minY, maxX, maxY := bounds(strokes)
		wPt, hPt := (maxX-minX)*pdfScale, (maxY-minY)*pdfScale
		content, err := pageContent(strokes, minX, minY, hPt)
		if err != nil {
			return err
		}
		pw.object(4+2*i, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Resources << /ExtGState << /GS1 3 0 R >> >> /Contents %d 0 R >>",
			num(wPt), num(hPt), 5+2*i))
		pw.stream(5+2*i, content)
	}
	info := 4 + 2*n
	pw.object(info, "<< /Title "+pdfText(title)+" /Producer (knowpod) >>")
	pw.finish(info)
	return pw.err
}

// pdfText encodes a text string as UTF-16 with a byte order mark, in hex.
func pdfText(s string) string {
	b := []byte{0xfe, 0xff}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u>>8), byte(u))
	}
	return fmt.Sprintf("<%X>", b)
}

// pageContent builds a page's drawing operators, zlib-compressed. Screen coordinates are
// mapped to PDF space (origin bottom left) by the initial transformation.
func pageContent(strokes []Stroke, minX, minY, hPt float64) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "1 J 1 j %s 0 0 %s %s %s cm\n", num(pdfScale), num(-pdfScale), num(-minX*pdfScale), num(hPt+minY*pdfScale))
	for _, s := range ordered(strokes) {
		c := strokeColor(&s)
		b.WriteString("q ")
		if s.Highlighter() {
			b.WriteString("/GS1 gs ")
		}
		fmt.Fprintf(&b, "%s %s %s RG\n", num(float64(c.R)/255), num(float64(c.G)/255), num(float64(c.B)/255))
		// Consecutive segments of about the same width share one path.
		pts := s.Points
		cur := -1.0
		for i := range pts {
			wd := math.Round(width(pts[i])*2) / 2
			if i == 0 || wd != cur {
				if i > 0 {
					fmt.Fprintf(&b, "%s %s l S\n", num(float64(pts[i].X)), num(float64(pts[i].Y)))
				}
				cur = wd
				fmt.Fprintf(&b, "%s w %s %s m\n", num(wd), num(float64(pts[i].X)), num(float64(pts[i].Y)))
				if len(pts) == 1 {
					fmt.Fprintf(&b, "%s %s l\n", num(float64(pts[0].X)), num(float64(pts[0].Y)))
				}
				continue
			}
			fmt.Fprintf(&b, "%s %s l\n", num(float64(pts[i].X)), num(float64(pts[i].Y)))
		}
		b.WriteString("S Q\n")
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(b.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return z.Bytes(), nil
}

func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	if s == "-0.00" {
		return "0"
	}
	return s
}

// pdfWriter writes numbered objects and remembers their offsets for the cross-reference
// table.
type pdfWriter struct {
	w       *bufio.Writer
	n       int64
	offsets map[int]int64
	err     error
}

func (p *pdfWriter) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	n, err := fmt.Fprintf(p.w, format, args...)
	p.n += int64(n)
	p.err = err
}

func (p *pdfWriter) write(b []byte) {
	if p.err != nil {
		return
	}
	n, err := p.w.Write(b)
	p.n += int64(n)
	p.err = err
}

func (p *pdfWriter) object(id int, body string) {
	if p.offsets == nil {
		p.offsets = map[int]int64{}
	}
	p.offsets[id] = p.n
	p.printf("%d 0 obj\n%s\nendobj\n", id, body)
}

func (p *pdfWriter) stream(id int, data []byte) {
	if p.offsets == nil {
		p.offsets = map[int]int64{}
	}
	p.offsets[id] = p.n
	p.printf("%d 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", id, len(data))
	p.write(data)
	p.printf("\nendstream\nendobj\n")
}

// finish writes the cross-reference table and trailer for objects 1…last; the last one is
// the document information.
func (p *pdfWriter) finish(last int) {
	start := p.n
	p.printf("xref\n0 %d\n0000000000 65535 f \n", last+1)
	for id := 1; id <= last; id++ {
		p.printf("%010d 00000 n \n", p.offsets[id])
	}
	p.printf("trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", last+1, last, start)
	if p.err == nil {
		p.err = p.w.Flush()
	}
}

// --- PNG ---

// maxImageHeight caps rendered images of very long pages.
const maxImageHeight = 8000

// RenderPNG draws a page on white at the given scale (1 = screen pixels) and encodes it as
// PNG. It is used to let a vision model read the handwriting.
func RenderPNG(strokes []Stroke, scale float64) ([]byte, error) {
	minX, minY, maxX, maxY := bounds(strokes)
	if h := (maxY - minY) * scale; h > maxImageHeight {
		scale *= maxImageHeight / h
	}
	w, h := int(math.Ceil((maxX-minX)*scale)), int(math.Ceil((maxY-minY)*scale))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	// Strokes of the same color and opacity are rasterized together: overlapping shapes
	// of one pass don't darken each other, and the rasterizer is cleared once per pass.
	type pass struct {
		c     color.RGBA
		alpha bool
	}
	var order []pass
	groups := map[pass][]Stroke{}
	for _, s := range ordered(strokes) {
		k := pass{strokeColor(&s), s.Highlighter()}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], s)
	}
	z := vector.NewRasterizer(w, h)
	tr := func(p Point) (float64, float64) {
		return (float64(p.X) - minX) * scale, (float64(p.Y) - minY) * scale
	}
	for _, k := range order {
		z.Reset(w, h)
		for _, s := range groups[k] {
			for i, p := range s.Points {
				x1, y1 := tr(p)
				r := width(p) * scale / 2
				disc(z, x1, y1, r)
				if i > 0 {
					x0, y0 := tr(s.Points[i-1])
					segment(z, x0, y0, x1, y1, r)
				}
			}
		}
		src := k.c
		if k.alpha {
			a := uint8(math.Round(highlighterAlpha * 255))
			src = color.RGBA{uint8(uint16(src.R) * uint16(a) / 255), uint8(uint16(src.G) * uint16(a) / 255), uint8(uint16(src.B) * uint16(a) / 255), a}
		}
		z.DrawOp = draw.Over
		z.Draw(img, img.Bounds(), image.NewUniform(src), image.Point{})
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// The rasterizer adds up signed coverage, so every shape is drawn counter-clockwise (in
// image coordinates, y down): overlapping shapes then add up instead of cancelling out.

// segment adds the rectangle covering a line of width 2r from (x0,y0) to (x1,y1).
func segment(z *vector.Rasterizer, x0, y0, x1, y1, r float64) {
	dx, dy := x1-x0, y1-y0
	l := math.Hypot(dx, dy)
	if l == 0 {
		return
	}
	nx, ny := -dy/l*r, dx/l*r
	polygon(z, [][2]float64{{x0 + nx, y0 + ny}, {x1 + nx, y1 + ny}, {x1 - nx, y1 - ny}, {x0 - nx, y0 - ny}})
}

// disc adds a circle (the round joins and caps of a line).
func disc(z *vector.Rasterizer, x, y, r float64) {
	n := 8
	if r > 4 {
		n = 16
	}
	pts := make([][2]float64, n)
	for i := range pts {
		a := 2 * math.Pi * float64(i) / float64(n)
		pts[i] = [2]float64{x + r*math.Cos(a), y + r*math.Sin(a)}
	}
	polygon(z, pts)
}

// polygon adds a closed polygon, reversing it if needed so all shapes share one winding.
func polygon(z *vector.Rasterizer, pts [][2]float64) {
	area := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		area += pts[i][0]*pts[j][1] - pts[j][0]*pts[i][1]
	}
	if area < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	z.MoveTo(float32(pts[0][0]), float32(pts[0][1]))
	for _, p := range pts[1:] {
		z.LineTo(float32(p[0]), float32(p[1]))
	}
	z.ClosePath()
}
