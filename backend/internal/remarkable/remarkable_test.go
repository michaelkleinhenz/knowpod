package remarkable

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math"
	"path/filepath"
	"strings"
	"testing"

	rt "github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable/remarkabletest"
)

// v6Page encodes a version 6 page file with the given lines.
func v6Page(lines ...testLine) []byte {
	ls := make([]rt.Line, len(lines))
	for i, l := range lines {
		ls[i] = rt.Line{Tool: l.tool, Color: l.color, Points: l.pts, Deleted: l.deleted, ARGB: l.argb}
	}
	return rt.Page(ls...)
}

type testLine struct {
	tool, color int
	pts         [][3]float32 // x (centered), y, width
	deleted     bool
	argb        uint32
}

// enc writes little-endian values (for the version 5 test file).
type enc struct{ b bytes.Buffer }

func (e *enc) u32(v uint32)  { _ = binary.Write(&e.b, binary.LittleEndian, v) }
func (e *enc) f32(v float32) { e.u32(math.Float32bits(v)) }

func TestParseLinesV6(t *testing.T) {
	data := v6Page(
		testLine{tool: 15, color: 0, pts: [][3]float32{{-700, 10, 2}, {0, 20, 2.5}}},
		testLine{tool: 15, deleted: true},
		testLine{tool: toolEraser, pts: [][3]float32{{0, 0, 10}}},
		testLine{tool: toolHighlighter2, color: 9, argb: 0xff00ff00, pts: [][3]float32{{100, 100, 30}}},
	)
	strokes, err := ParseLines(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(strokes) != 2 {
		t.Fatalf("got %d strokes, want 2 (deleted and eraser dropped): %+v", len(strokes), strokes)
	}
	s := strokes[0]
	if s.Tool != 15 || len(s.Points) != 2 || s.Points[0].X != 2 || s.Points[1].X != PageWidth/2 || s.Points[1].Y != 20 || s.Points[1].Width != 2.5 {
		t.Errorf("stroke 1: %+v", s)
	}
	if !strokes[1].Highlighter() || strokes[1].ARGB != 0xff00ff00 {
		t.Errorf("stroke 2: %+v", strokes[1])
	}
	if c := strokeColor(&strokes[1]); c.G != 255 || c.R != 0 {
		t.Errorf("highlighter color %v", c)
	}
}

func TestParseLinesV5(t *testing.T) {
	var e enc
	e.b.WriteString(fmt.Sprintf("%-43s", headerPrefix+"5"))
	e.u32(1) // layers
	e.u32(1) // lines
	e.u32(2) // tool
	e.u32(7) // color
	e.u32(0)
	e.f32(2)
	e.u32(0)
	e.u32(2) // points
	for _, x := range []float32{10, 20} {
		e.f32(x)
		e.f32(30)
		e.f32(0)
		e.f32(0)
		e.f32(3)
		e.f32(0)
	}
	strokes, err := ParseLines(e.b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(strokes) != 1 || strokes[0].Color != 7 || strokes[0].Points[1] != (Point{X: 20, Y: 30, Width: 3}) {
		t.Errorf("got %+v", strokes)
	}
}

func TestParseLinesRejectsGarbage(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("hello"), []byte(fmt.Sprintf("%-43s", headerPrefix+"9"))} {
		if _, err := ParseLines(data); err == nil {
			t.Errorf("%q: no error", data)
		}
	}
	// A block claiming more bytes than the file has.
	data := v6Page(testLine{tool: 15, pts: [][3]float32{{0, 0, 1}}})
	if _, err := ParseLines(data[:len(data)-3]); err == nil {
		t.Error("truncated file: no error")
	}
}

func TestParseIndex(t *testing.T) {
	h := strings.Repeat("a", 64)
	v3 := "3\n" + h + ":80000000:doc-1:4:1234\n\n" + h + ":80000000:doc-2:3:99\n"
	v4 := "4\n0:.:2:1333\n" + h + ":80000000:doc-1:4:1234\n" + h + ":0:doc-1.metadata:0:99\n"
	for name, in := range map[string]string{"v3": v3, "v4": v4} {
		es, err := ParseIndex([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(es) != 2 || es[0] != (Entry{Hash: h, Type: "80000000", ID: "doc-1", Subfiles: 4, Size: 1234}) {
			t.Errorf("%s: %+v", name, es)
		}
	}
	for _, bad := range []string{"", "5\n", "3\nnot:enough\n", "3\n" + h + ":0:x:y:z\n"} {
		if _, err := ParseIndex([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestPageOrder(t *testing.T) {
	var c Content
	raw := `{"fileType":"notebook","cPages":{"pages":[
		{"id":"b","idx":{"value":"bb"}},
		{"id":"gone","idx":{"value":"ba"},"deleted":{"value":1}},
		{"id":"a","idx":{"value":"ba"}}]}}`
	if err := jsonUnmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.PageIDs(), ","); got != "a,b" {
		t.Errorf("pages %q", got)
	}
	if c.Kind() != KindNotebook {
		t.Errorf("kind %q", c.Kind())
	}
	old := Content{FileType: "PDF", Pages: []string{"x", "y"}}
	if strings.Join(old.PageIDs(), ",") != "x,y" || old.Kind() != KindPDF {
		t.Errorf("legacy content: %v %v", old.PageIDs(), old.Kind())
	}
}

func TestMetadataTimestamps(t *testing.T) {
	var m Metadata
	if err := jsonUnmarshal(`{"visibleName":"N","lastModified":"1700000000000","createdTime":1600000000000}`, &m); err != nil {
		t.Fatal(err)
	}
	if m.LastModified.Time().UnixMilli() != 1700000000000 || m.CreatedTime.Time().UnixMilli() != 1600000000000 {
		t.Errorf("%+v", m)
	}
	if err := jsonUnmarshal(`{"lastModified":"soon"}`, &m); err != nil || m.LastModified.Time() != nil {
		t.Errorf("bad timestamp: %v %+v", err, m)
	}
}

func TestWritePDF(t *testing.T) {
	strokes, _ := ParseLines(v6Page(
		testLine{tool: 15, pts: [][3]float32{{0, 10, 2}, {10, 20, 2}, {20, 30, 4}}},
		testLine{tool: 15, pts: [][3]float32{{0, 2500, 2}}}, // below the screen
		testLine{tool: toolHighlighter1, color: 3, pts: [][3]float32{{0, 10, 30}, {50, 10, 30}}},
	))
	var buf bytes.Buffer
	if err := WritePDF(&buf, [][]Stroke{strokes, nil}, "Idées"); err != nil {
		t.Fatal(err)
	}
	pdf := buf.String()
	if !strings.HasPrefix(pdf, "%PDF-1.4") || !strings.HasSuffix(pdf, "%%EOF\n") || !strings.Contains(pdf, "/Count 2") {
		t.Fatalf("not a two-page PDF: %q…", pdf[:min(200, len(pdf))])
	}
	// The title is UTF-16 with a byte order mark.
	if !strings.Contains(pdf, "/Title <FEFF0049006400E900650073>") || !strings.Contains(pdf, "/Info 8 0 R") {
		t.Errorf("no title in %q", pdf[strings.LastIndex(pdf, "endobj"):])
	}
	// The page grows to hold the stroke below the screen.
	if !strings.Contains(pdf, "/MediaBox [0 0 447.29 798.37]") {
		t.Errorf("unexpected page size in %q", pdf)
	}
	// The cross-reference table points at the objects.
	i := strings.LastIndex(pdf, "startxref\n")
	var start int
	fmt.Sscanf(pdf[i+len("startxref\n"):], "%d", &start)
	if !strings.HasPrefix(pdf[start:], "xref\n0 9\n") {
		t.Errorf("startxref %d points at %q", start, pdf[start:min(start+20, len(pdf))])
	}
	for _, line := range strings.Split(pdf[start:], "\n")[3:11] {
		var off int
		fmt.Sscanf(line, "%d", &off)
		if !strings.Contains(pdf[off:off+12], " 0 obj") {
			t.Errorf("xref entry %q points at %q", line, pdf[off:off+12])
		}
	}
}

func TestRenderPNG(t *testing.T) {
	strokes, _ := ParseLines(v6Page(testLine{tool: 15, color: 7, pts: [][3]float32{{-100, 100, 10}, {100, 100, 10}}}))
	data, err := RenderPNG(strokes, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != PageWidth/2 || b.Dy() != PageHeight/2 {
		t.Errorf("size %v", b)
	}
	// On the line (x = 702/2, y = 50) it is red; above it white.
	if r, g, _, _ := img.At(351, 50).RGBA(); r>>8 < 150 || g>>8 > 100 {
		t.Errorf("line pixel %v", img.At(351, 50))
	}
	if r, g, b, _ := img.At(351, 20).RGBA(); r>>8 != 255 || g>>8 != 255 || b>>8 != 255 {
		t.Errorf("background pixel %v", img.At(351, 20))
	}
}

// --- client against a fake cloud ---

func TestClientPairAndDownload(t *testing.T) {
	cloud := rt.New()
	defer cloud.Close()
	cloud.AddCode("abcdefgh", "device-1")
	page := v6Page(testLine{tool: 15, pts: [][3]float32{{0, 0, 2}, {5, 5, 2}}})
	cloud.Set(rt.Item{ID: "f1", Name: "reMarkable", Folder: true})
	cloud.Set(rt.Item{ID: "d1", Name: "Ideas", Parent: "f1", LastModified: 1700000000000,
		Content: `{"fileType":"notebook","cPages":{"pages":[{"id":"p1","idx":{"value":"ba"}}]}}`,
		Files:   map[string][]byte{"p1.rm": page, ".thumbnails/p1.png": []byte("png")}})
	c := NewClient(cloud.URL, cloud.URL)
	ctx := context.Background()

	if _, err := c.Pair(ctx, "zzzzzzzz"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("wrong code: %v", err)
	}
	if _, err := c.Pair(ctx, "short"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("short code: %v", err)
	}
	dev, err := c.Pair(ctx, " abcdefgh ")
	if err != nil || dev != "device-1" {
		t.Fatalf("pair: %q %v", dev, err)
	}
	s, err := c.Open(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.Root(ctx)
	if err != nil || root.Hash == "" || root.Generation != 7 {
		t.Fatalf("root: %+v %v", root, err)
	}
	entries, err := s.Index(ctx, root.Hash, "root.docSchema")
	if err != nil || len(entries) != 2 || entries[0].ID != "d1" {
		t.Fatalf("root index: %+v %v", entries, err)
	}
	it, err := s.ReadItem(ctx, entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if it.Metadata.VisibleName != "Ideas" || it.Metadata.Parent != "f1" || it.Metadata.Type != TypeDocument || len(it.Files) != 4 {
		t.Errorf("item: %+v", it)
	}

	dst := filepath.Join(t.TempDir(), "doc.zip")
	if _, err := s.Download(ctx, it, dst, 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("size limit: %v", err)
	}
	cloud.Requests()
	if _, err := s.Download(ctx, it, dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cloud.Requests(), "\n")
	if !strings.Contains(got, " d1/p1.rm") || !strings.Contains(got, " d1.content") || strings.Contains(got, "thumbnails") {
		t.Errorf("downloaded files (thumbnail left out):\n%s", got)
	}
	a, err := OpenArchive(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	pages, err := a.Pages()
	if err != nil || len(pages) != 1 || len(pages[0]) != 1 || a.Kind() != KindNotebook || a.Metadata.VisibleName != "Ideas" {
		t.Errorf("archive: %v %v %v", pages, err, a.Kind())
	}
	if _, _, err := a.Original(); err == nil {
		t.Error("a notebook has no original file")
	}

	// Renaming changes the item's hash but not its content hash.
	before := it.ContentHash()
	cloud.Set(rt.Item{ID: "d1", Name: "Renamed", Parent: "f1", LastModified: 1700000000001,
		Content: `{"fileType":"notebook","cPages":{"pages":[{"id":"p1","idx":{"value":"ba"}}]}}`,
		Files:   map[string][]byte{"p1.rm": page, ".thumbnails/p1.png": []byte("png")}})
	root, _ = s.Root(ctx)
	entries, _ = s.Index(ctx, root.Hash, "root.docSchema")
	it2, err := s.ReadItem(ctx, entries[0])
	if err != nil || it2.Hash == it.Hash || it2.ContentHash() != before {
		t.Errorf("rename: %v %v %v", err, it2.Hash == it.Hash, it2.ContentHash() == before)
	}

	cloud.Revoke(dev)
	if _, err := c.Open(ctx, dev); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("revoked: %v", err)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func TestParseText(t *testing.T) {
	// Text typed in pieces: an insertion in the middle, two insertions at the start (ordered
	// by ID), a deleted character and an inline formatting item that isn't text.
	data := rt.TextPage(&rt.Text{Items: []rt.TextItem{
		{ID: 10, Text: "Hello\nworld"},
		{ID: 30, Left: 14, Right: 15, Text: " there"},
		{ID: 61, Right: 10, Text: "B"},
		{ID: 60, Right: 10, Text: "A"},
		{ID: 40, Left: 20, Deleted: 1},
		{ID: 50, Left: 15, Right: 16, Format: true},
	}}, rt.Line{Tool: 15, Points: [][3]float32{{0, 0, 2}}})
	text, err := ParseText(data)
	if err != nil || text != "ABHello there\n\nworld" {
		t.Errorf("text %q, %v", text, err)
	}
	if strokes, err := ParseLines(data); err != nil || len(strokes) != 1 {
		t.Errorf("strokes next to text: %+v, %v", strokes, err)
	}

	// Paragraph styles become Markdown; empty paragraphs are left out.
	data = rt.TextPage(&rt.Text{
		Items:  []rt.TextItem{{ID: 1, Text: "Plan\nmilk\neggs\n\n Done "}},
		Styles: map[uint64]int{0: styleHeading, 5: styleBullet, 10: styleCheckbox},
	})
	if text, err := ParseText(data); err != nil || text != "# Plan\n\n- milk\n- [ ] eggs\n\nDone" {
		t.Errorf("styled text %q, %v", text, err)
	}

	if text, err := ParseText(v6Page(testLine{tool: 15, pts: [][3]float32{{0, 0, 2}}})); err != nil || text != "" {
		t.Errorf("page without text: %q, %v", text, err)
	}
	if _, err := ParseText([]byte("nope")); err == nil {
		t.Error("garbage accepted")
	}
}
