package remarkable

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	// XHTML, as EPUB needs; raw HTML in the text is left out.
	goldmark.WithRendererOptions(html.WithXHTML()),
)

// The tablet's reader doesn't show form controls, so checklist boxes become characters.
var (
	checkedBox   = regexp.MustCompile(`<input checked="" disabled="" type="checkbox" ?/?>\s*`)
	uncheckedBox = regexp.MustCompile(`<input disabled="" type="checkbox" ?/?>\s*`)
)

// epubStyle keeps the text readable on the tablet's reader.
const epubStyle = `body { font-family: serif; line-height: 1.4; }
h1 { font-size: 1.6em; margin: 0 0 0.8em; }
h2 { font-size: 1.3em; } h3 { font-size: 1.1em; }
table { border-collapse: collapse; } th, td { border: 1px solid #888; padding: 0.2em 0.4em; }
pre, code { font-family: monospace; font-size: 0.9em; }
blockquote { margin-left: 1em; padding-left: 0.8em; border-left: 3px solid #888; }
`

// NoteEPUB makes an EPUB of a note: its title as the heading and its Markdown text. The
// same note always gives the same bytes, so an unchanged note isn't sent again.
func NoteEPUB(id, title, markdownText, lang string, modified time.Time) ([]byte, error) {
	var body bytes.Buffer
	if err := markdown.Convert([]byte(markdownText), &body); err != nil {
		return nil, fmt.Errorf("epub: %w", err)
	}
	text := checkedBox.ReplaceAll(body.Bytes(), []byte("☑ "))
	text = uncheckedBox.ReplaceAll(text, []byte("☐ "))
	if lang == "" {
		lang = "en"
	}
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	t, l, uid := esc(title), esc(lang), esc("urn:uuid:"+id)
	stamp := modified.UTC().Format("2006-01-02T15:04:05Z")

	page := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="%[2]s" lang="%[2]s">
<head><meta charset="UTF-8"/><title>%[1]s</title><link rel="stylesheet" type="text/css" href="style.css"/></head>
<body>
<h1>%[1]s</h1>
%[3]s</body>
</html>
`, t, l, text)
	opf := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="%[2]s">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="uid">%[3]s</dc:identifier>
<dc:title>%[1]s</dc:title>
<dc:language>%[2]s</dc:language>
<dc:creator>knowpod</dc:creator>
<meta property="dcterms:modified">%[4]s</meta>
</metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="style" href="style.css" media-type="text/css"/>
<item id="note" href="note.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine toc="ncx">
<itemref idref="note"/>
</spine>
</package>
`, t, l, uid, stamp)
	nav := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="%[2]s" lang="%[2]s">
<head><meta charset="UTF-8"/><title>%[1]s</title></head>
<body><nav epub:type="toc"><ol><li><a href="note.xhtml">%[1]s</a></li></ol></nav></body>
</html>
`, t, l)
	ncx := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<head><meta name="dtb:uid" content="%[2]s"/></head>
<docTitle><text>%[1]s</text></docTitle>
<navMap><navPoint id="note" playOrder="1"><navLabel><text>%[1]s</text></navLabel><content src="note.xhtml"/></navPoint></navMap>
</ncx>
`, t, uid)
	container := `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
`

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	// A fixed file time keeps the bytes the same for the same note.
	fixed := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	files := []struct {
		name, data string
		store      bool
	}{
		{"mimetype", "application/epub+zip", true}, // first and uncompressed, as EPUB requires
		{"META-INF/container.xml", container, false},
		{"OEBPS/content.opf", opf, false},
		{"OEBPS/nav.xhtml", nav, false},
		{"OEBPS/toc.ncx", ncx, false},
		{"OEBPS/style.css", epubStyle, false},
		{"OEBPS/note.xhtml", page, false},
	}
	for _, f := range files {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: fixed}
		if f.store {
			h.Method = zip.Store
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.data)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
