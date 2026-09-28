package remarkable

import (
	"errors"
	"slices"
	"strings"
)

// blockRootText is the block of a version 6 page file that holds the page's typed text.
const blockRootText = 0x07

// Paragraph styles of typed text.
const (
	styleHeading         = 2
	styleBold            = 3
	styleBullet          = 4
	styleBullet2         = 5
	styleCheckbox        = 6
	styleCheckboxChecked = 7
)

// maxTextChars bounds the characters (including deleted ones) of a page's text.
const maxTextChars = 1 << 19

// ParseText reads the typed text of a page file (typed on the tablet's keyboard or in the
// reMarkable apps) as Markdown: headings, bullets and checkboxes keep their paragraph
// style. Page files before version 6 hold no typed text.
func ParseText(data []byte) (string, error) {
	v, err := fileVersion(data)
	if err != nil {
		return "", err
	}
	if v != "6" {
		return "", nil
	}
	var parts []string
	err = eachBlock(data[headerLen:], func(typ, _ uint8, b []byte) error {
		if typ != blockRootText {
			return nil
		}
		t, err := parseRootText(b)
		if err != nil {
			return err
		}
		if t != "" {
			parts = append(parts, t)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.Join(parts, "\n\n"), nil
}

// textChar is a character of typed text. Text is a sequence in which every character
// names its neighbours when it was inserted; deleted characters stay as placeholders, and
// formatting items (inline bold and italics) take a place without being text.
type textChar struct {
	id, left, right crdtID
	r               rune
	text            bool
}

// parseRootText reads a root text block: the text's characters, then the paragraph styles
// keyed by the character starting the paragraph (the newline before it, or the zero ID for
// the first paragraph). The position and width of the text box that follow are not needed.
func parseRootText(b []byte) (string, error) {
	r := &reader{data: b}
	r.expect(1, tagID)
	r.id()
	body := r.sub(2)
	items := body.sub(1).sub(1)
	n := items.varuint()
	var chars []textChar
	for i := uint64(0); i < n && items.err == nil; i++ {
		var err error
		if chars, err = readTextItem(items.sub(0), chars); err != nil {
			return "", err
		}
	}
	if items.err != nil {
		return "", items.err
	}
	// The styles are optional: without them, all paragraphs are plain.
	styles := readTextStyles(body)
	return textMarkdown(orderText(chars), styles), nil
}

// readTextItem reads an item of the text sequence and appends its characters.
func readTextItem(r *reader, out []textChar) ([]textChar, error) {
	r.expect(2, tagID)
	id := r.id()
	r.expect(3, tagID)
	left := r.id()
	r.expect(4, tagID)
	right := r.id()
	r.expect(5, tagByte4)
	deleted := uint64(r.u32())
	var value []rune
	format := false
	if r.err == nil && r.remaining() > 0 {
		s := r.sub(6)
		n := s.varuint()
		s.u8() // is ASCII (always set)
		// (A length past the end fails without converting a huge length to int.)
		value = []rune(string(s.take(int(min(n, uint64(s.remaining()+1))))))
		format = s.remaining() > 0 && s.peek(2, tagByte4)
		if s.err != nil {
			return nil, s.err
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	switch {
	case format:
		return append(out, textChar{id: id, left: left, right: right}), nil
	case deleted > 0:
		if uint64(len(out))+deleted > maxTextChars {
			return nil, errors.New("typed text too long")
		}
		value = make([]rune, deleted)
	case len(value) == 0:
		return out, nil
	}
	if len(out)+len(value) > maxTextChars {
		return nil, errors.New("typed text too long")
	}
	// An item of several characters stands for one character after the other, with
	// consecutive IDs.
	for i, c := range value {
		next := crdtID{id.author, id.counter + 1}
		ch := textChar{id: id, left: left, right: next, r: c, text: deleted == 0}
		if i == len(value)-1 {
			ch.right = right
		}
		out = append(out, ch)
		left, id = id, next
	}
	return out, nil
}

// readTextStyles reads the paragraph styles that follow the text; it returns what it could
// read.
func readTextStyles(body *reader) map[crdtID]int {
	styles := map[crdtID]int{}
	if body.err != nil || !body.peek(2, tagLength4) {
		return styles
	}
	r := body.sub(2).sub(1)
	n := r.varuint()
	for i := uint64(0); i < n && r.err == nil; i++ {
		char := r.id() // untagged
		r.expect(1, tagID)
		r.id() // timestamp
		v := r.sub(2)
		v.u8() // always 17
		style := v.u8()
		if r.err != nil || v.err != nil {
			break
		}
		styles[char] = int(style)
	}
	return styles
}

// orderText puts the characters in reading order: each character comes after its left and
// before its right neighbour; characters that become ready at the same time (inserted at the
// same place) are ordered by ID. The zero ID stands for the start (as a left neighbour) or
// the end (as a right one). Characters caught in a cycle are put at the end.
func orderText(chars []textChar) []textChar {
	// A character listed twice counts once, as listed last.
	index := make(map[crdtID]int, len(chars))
	var text []textChar
	for _, c := range chars {
		if i, ok := index[c.id]; ok {
			text[i] = c
			continue
		}
		index[c.id] = len(text)
		text = append(text, c)
	}
	// The nodes are the characters, the start, the end, and the IDs of neighbours that
	// aren't in the text.
	n := len(text)
	start, end, total := n, n+1, n+2
	node := func(id crdtID, zero int) int {
		if id == (crdtID{}) {
			return zero
		}
		i, ok := index[id]
		if !ok {
			i = total
			index[id] = i
			total++
		}
		return i
	}
	lefts, rights := make([]int, n), make([]int, n)
	for i, c := range text {
		lefts[i], rights[i] = node(c.left, start), node(c.right, end)
	}
	// Each character waits for its left neighbour, and each node for the characters naming
	// it as their right neighbour. The characters waiting for a node are a linked list.
	deps := make([]int, total)
	first := make([]int, total)
	for i := range first {
		first[i] = -1
	}
	next := make([]int, n)
	for i := range text {
		deps[i]++
		next[i], first[lefts[i]] = first[lefts[i]], i
		deps[rights[i]]++
	}
	var ready []int
	for i, d := range deps {
		if d == 0 {
			ready = append(ready, i)
		}
	}
	out := make([]textChar, 0, n)
	done := make([]bool, n)
	take := func(ids []crdtID) {
		slices.SortFunc(ids, compareIDs)
		for _, id := range ids {
			i := index[id]
			out = append(out, text[i])
			done[i] = true
		}
	}
	release := func(i int, to *[]int) {
		if deps[i]--; deps[i] == 0 {
			*to = append(*to, i)
		}
	}
	var ids []crdtID
	for len(ready) > 0 {
		ids = ids[:0]
		var later []int
		for _, x := range ready {
			if x < n {
				ids = append(ids, text[x].id)
				release(rights[x], &later)
			}
			for c := first[x]; c >= 0; c = next[c] {
				release(c, &later)
			}
		}
		take(ids)
		ready = later
	}
	ids = ids[:0]
	for i, c := range text {
		if !done[i] {
			ids = append(ids, c.id)
		}
	}
	take(ids)
	return out
}

func compareIDs(a, b crdtID) int {
	switch {
	case a.less(b):
		return -1
	case b.less(a):
		return 1
	}
	return 0
}

// textMarkdown writes the characters as Markdown, a paragraph per line. Empty paragraphs
// are left out; a blank line separates paragraphs except within lists.
func textMarkdown(chars []textChar, styles map[crdtID]int) string {
	type paragraph struct {
		style int
		text  strings.Builder
	}
	paras := []*paragraph{{style: styles[crdtID{}]}}
	for _, c := range chars {
		switch {
		case !c.text:
		case c.r == '\n':
			paras = append(paras, &paragraph{style: styles[c.id]})
		default:
			paras[len(paras)-1].text.WriteRune(c.r)
		}
	}
	var b strings.Builder
	prevList := false
	for _, p := range paras {
		text := strings.TrimSpace(p.text.String())
		if text == "" {
			continue
		}
		prefix, list := "", true
		switch p.style {
		case styleHeading:
			prefix, list = "# ", false
		case styleBold:
			prefix, list = "## ", false
		case styleBullet:
			prefix = "- "
		case styleBullet2:
			prefix = "  - "
		case styleCheckbox:
			prefix = "- [ ] "
		case styleCheckboxChecked:
			prefix = "- [x] "
		default:
			list = false
		}
		if b.Len() > 0 {
			if list && prevList {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(prefix + text)
		prevList = list
	}
	return b.String()
}
