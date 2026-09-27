package remarkable

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Page size of the reMarkable 1 and 2 screens in its own coordinates (226 dpi).
const (
	PageWidth  = 1404
	PageHeight = 1872
)

// Pen tools that matter for drawing. Erasers are not drawn.
const (
	toolHighlighter1 = 5
	toolEraser       = 6
	toolEraseArea    = 8
	toolHighlighter2 = 18
	toolShader       = 23
)

// Point is a sample of a stroke. X runs from 0 (left edge) to PageWidth, Y from 0 (top)
// downwards; notebooks can be longer than a screen. Width is the drawn line width.
type Point struct {
	X, Y  float32
	Width float32
}

// Stroke is a line drawn with a pen.
type Stroke struct {
	Tool  int
	Color int
	// ARGB is the exact color of newer highlighters and shaders; 0 when not given.
	ARGB   uint32
	Points []Point
}

// Highlighter reports whether the stroke is translucent (highlighter or shader).
func (s *Stroke) Highlighter() bool {
	return s.Tool == toolHighlighter1 || s.Tool == toolHighlighter2 || s.Tool == toolShader
}

// eraser reports whether the stroke erases rather than draws.
func (s *Stroke) eraser() bool { return s.Tool == toolEraser || s.Tool == toolEraseArea }

const headerPrefix = "reMarkable .lines file, version="

// headerLen is the length of the (space-padded) header of every .rm file.
const headerLen = 43

// ParseLines reads the strokes of a page file (.rm), in format version 6 (firmware 3.0 and
// later) or the older versions 3 and 5.
func ParseLines(data []byte) ([]Stroke, error) {
	if len(data) < headerLen || !strings.HasPrefix(string(data), headerPrefix) {
		return nil, errors.New("not a reMarkable page file")
	}
	var strokes []Stroke
	var err error
	switch v := strings.TrimSpace(string(data[len(headerPrefix):headerLen])); v {
	case "6":
		strokes, err = parseV6(data[headerLen:])
	case "5", "3":
		strokes, err = parseV5(data[headerLen:], v == "5")
	default:
		return nil, fmt.Errorf("unsupported page file version %q", v)
	}
	if err != nil {
		return nil, err
	}
	out := strokes[:0]
	for _, s := range strokes {
		if !s.eraser() && len(s.Points) > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

// --- version 3 and 5 ---

// parseV5 reads the fixed layout of versions 3 and 5: layers of lines of points.
func parseV5(data []byte, v5 bool) ([]Stroke, error) {
	r := &reader{data: data}
	layers := r.u32()
	var out []Stroke
	for l := uint32(0); l < layers && r.err == nil; l++ {
		lines := r.u32()
		for i := uint32(0); i < lines && r.err == nil; i++ {
			s := Stroke{Tool: int(r.u32()), Color: int(r.u32())}
			r.u32() // padding
			r.f32() // base width
			if v5 {
				r.u32() // unknown
			}
			n := r.u32()
			if uint64(n)*24 > uint64(r.remaining()) {
				return nil, errors.New("page file: truncated stroke")
			}
			s.Points = make([]Point, n)
			for j := range s.Points {
				x, y := r.f32(), r.f32()
				r.f32() // speed
				r.f32() // direction
				w := r.f32()
				r.f32() // pressure
				s.Points[j] = Point{X: x, Y: y, Width: w}
			}
			out = append(out, s)
		}
	}
	if r.err != nil {
		return nil, fmt.Errorf("page file: %w", r.err)
	}
	return out, nil
}

// --- version 6 ---

// Block types of version 6 files that are read here.
const blockLineItem = 0x05

// Tag types of version 6 fields: the low four bits of a field's tag.
const (
	tagByte1   = 0x1
	tagByte4   = 0x4
	tagByte8   = 0x8
	tagLength4 = 0xC
	tagID      = 0xF
)

// parseV6 reads the scene of a version 6 file: a sequence of typed blocks, of which the line
// items hold the strokes. Deleted items keep their block but have no value. Other blocks
// (the scene tree, layers, typed text) are skipped.
func parseV6(data []byte) ([]Stroke, error) {
	var out []Stroke
	pos := 0
	for pos+8 <= len(data) {
		length := int(binary.LittleEndian.Uint32(data[pos:]))
		version := data[pos+6]
		typ := data[pos+7]
		start := pos + 8
		end := start + length
		if length < 0 || end > len(data) {
			return nil, errors.New("page file: truncated block")
		}
		if typ == blockLineItem {
			s, err := parseLineItem(data[start:end], version)
			if err != nil {
				return nil, fmt.Errorf("page file: %w", err)
			}
			if s != nil {
				out = append(out, *s)
			}
		}
		pos = end
	}
	return out, nil
}

// parseLineItem reads a scene item block holding a line: the item's IDs, then (unless the
// item was deleted) the line's pen, color and points.
func parseLineItem(b []byte, version uint8) (*Stroke, error) {
	r := &reader{data: b}
	for idx := uint32(1); idx <= 4; idx++ { // parent, item, left and right IDs
		r.expect(idx, tagID)
		r.crdtID()
	}
	r.expect(5, tagByte4)
	deleted := r.u32()
	if r.err != nil {
		return nil, r.err
	}
	if deleted > 0 || !r.peek(6, tagLength4) {
		return nil, nil
	}
	r.expect(6, tagLength4)
	valueLen := int(r.u32())
	if r.err != nil || valueLen > r.remaining() {
		return nil, errors.New("truncated line item")
	}
	v := &reader{data: r.data[r.pos : r.pos+valueLen]}
	if v.u8() != 0x03 { // item type: line
		return nil, v.err
	}
	s := &Stroke{}
	v.expect(1, tagByte4)
	s.Tool = int(v.u32())
	v.expect(2, tagByte4)
	s.Color = int(v.u32())
	v.expect(3, tagByte8)
	v.f64() // thickness scale
	v.expect(4, tagByte4)
	v.f32() // starting length
	v.expect(5, tagLength4)
	n := int(v.u32())
	if v.err != nil {
		return nil, v.err
	}
	if n > v.remaining() {
		return nil, errors.New("truncated points")
	}
	pts := &reader{data: v.data[v.pos : v.pos+n]}
	v.pos += n
	size := 14
	if version < 2 {
		size = 24
	}
	s.Points = make([]Point, 0, n/size)
	for pts.remaining() >= size {
		p := Point{X: pts.f32() + PageWidth/2, Y: pts.f32()}
		if version < 2 {
			pts.f32() // speed
			pts.f32() // direction
			p.Width = pts.f32()
			pts.f32() // pressure
		} else {
			pts.u16() // speed
			p.Width = float32(pts.u16()) / 4
			pts.u8() // direction
			pts.u8() // pressure
		}
		s.Points = append(s.Points, p)
	}
	// Newer firmware appends the exact color of highlighters (index 8); the timestamp
	// (6) and move ID (7) that come before it are skipped.
	for v.err == nil && v.remaining() > 0 {
		idx, typ := v.tag()
		switch {
		case v.err != nil:
		case idx == 8 && typ == tagByte4:
			s.ARGB = v.u32()
		default:
			v.skip(typ)
		}
	}
	return s, nil
}

// reader decodes little-endian values; after the first error every read returns zero.
type reader struct {
	data []byte
	pos  int
	err  error
}

func (r *reader) remaining() int { return len(r.data) - r.pos }

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n > r.remaining() {
		r.err = errors.New("unexpected end of data")
		return nil
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b
}

func (r *reader) u8() uint8 {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

func (r *reader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }

func (r *reader) f64() float64 {
	if b := r.take(8); b != nil {
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	}
	return 0
}

func (r *reader) varuint() uint64 {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b := r.u8()
		if r.err != nil {
			return 0
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v
		}
	}
	r.err = errors.New("varuint too long")
	return 0
}

// crdtID reads an item ID: an author byte and a counter.
func (r *reader) crdtID() {
	r.u8()
	r.varuint()
}

func (r *reader) tag() (index uint32, typ uint8) {
	t := r.varuint()
	return uint32(t >> 4), uint8(t & 0xf)
}

// expect reads a tag and fails unless it has the given index and type.
func (r *reader) expect(index uint32, typ uint8) {
	i, t := r.tag()
	if r.err == nil && (i != index || t != typ) {
		r.err = fmt.Errorf("expected field %d of type %#x, found %d of type %#x", index, typ, i, t)
	}
}

// peek reports whether the next tag has the given index and type, without consuming it.
func (r *reader) peek(index uint32, typ uint8) bool {
	if r.err != nil {
		return false
	}
	pos := r.pos
	i, t := r.tag()
	ok := r.err == nil && i == index && t == typ
	r.pos, r.err = pos, nil
	return ok
}

// skip jumps over a field value of the given type.
func (r *reader) skip(typ uint8) {
	switch typ {
	case tagByte1:
		r.take(1)
	case tagByte4:
		r.take(4)
	case tagByte8:
		r.take(8)
	case tagLength4:
		r.take(int(r.u32()))
	case tagID:
		r.crdtID()
	default:
		r.err = fmt.Errorf("unknown field type %#x", typ)
	}
}
