package std

import (
	"io"
	"unicode/utf8"
)

// gapBuffer is a gap-buffer text store optimized for cursor-local edits.
// The gap sits at the cursor position; inserts and deletes near the cursor
// are O(1). Moving the cursor slides the gap via copy (cache-friendly memmove).
//
// All positions in the public API are byte offsets into the logical content
// (i.e. as if the gap didn't exist).
type gapBuffer struct {
	buf      []byte
	gapStart int // index of first gap byte
	gapEnd   int // index of first byte after gap
}

const defaultGapSize = 256

// newGapBuffer creates a gap buffer initialized with the given text.
func newGapBuffer(initial string) *gapBuffer {
	n := len(initial)
	gap := defaultGapSize
	buf := make([]byte, n+gap)
	copy(buf, initial)
	return &gapBuffer{
		buf:      buf,
		gapStart: n,
		gapEnd:   n + gap,
	}
}

// Len returns the logical content length in bytes (excluding the gap).
func (g *gapBuffer) Len() int {
	return len(g.buf) - (g.gapEnd - g.gapStart)
}

// gapLen returns the current gap size.
func (g *gapBuffer) gapLen() int {
	return g.gapEnd - g.gapStart
}

// GapPos returns the current gap (cursor) position as a logical byte offset.
func (g *gapBuffer) GapPos() int {
	return g.gapStart
}

// MoveTo moves the gap so that it starts at logical byte position pos.
func (g *gapBuffer) MoveTo(pos int) {
	if pos == g.gapStart {
		return
	}
	if pos < g.gapStart {
		// Slide gap left: move bytes [pos, gapStart) to end of gap.
		n := g.gapStart - pos
		copy(g.buf[g.gapEnd-n:g.gapEnd], g.buf[pos:g.gapStart])
		g.gapStart = pos
		g.gapEnd -= n
	} else {
		// Slide gap right: move bytes [gapEnd, gapEnd+(pos-gapStart)) to gapStart.
		n := pos - g.gapStart
		copy(g.buf[g.gapStart:g.gapStart+n], g.buf[g.gapEnd:g.gapEnd+n])
		g.gapStart += n
		g.gapEnd += n
	}
}

// grow ensures the gap has room for at least n bytes.
func (g *gapBuffer) grow(n int) {
	if g.gapLen() >= n {
		return
	}
	// Double the buffer or add enough for n, whichever is larger.
	needed := n - g.gapLen()
	extra := len(g.buf)
	if extra < needed {
		extra = needed
	}
	if extra < defaultGapSize {
		extra = defaultGapSize
	}
	newBuf := make([]byte, len(g.buf)+extra)
	// Copy before-gap.
	copy(newBuf, g.buf[:g.gapStart])
	// Copy after-gap to end of new buffer, leaving the expanded gap in the middle.
	newGapEnd := g.gapStart + g.gapLen() + extra
	copy(newBuf[newGapEnd:], g.buf[g.gapEnd:])
	g.buf = newBuf
	g.gapEnd = newGapEnd
}

// Insert inserts the given bytes at the gap position (cursor).
func (g *gapBuffer) Insert(b []byte) {
	g.grow(len(b))
	copy(g.buf[g.gapStart:], b)
	g.gapStart += len(b)
}

// InsertString inserts a string at the gap position.
func (g *gapBuffer) InsertString(s string) {
	g.grow(len(s))
	copy(g.buf[g.gapStart:], s)
	g.gapStart += len(s)
}

// InsertRune inserts a single rune at the gap position.
func (g *gapBuffer) InsertRune(r rune) {
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	g.Insert(buf[:n])
}

// DeleteBackward deletes n bytes before the gap. Returns bytes deleted.
func (g *gapBuffer) DeleteBackward(n int) int {
	if n > g.gapStart {
		n = g.gapStart
	}
	g.gapStart -= n
	return n
}

// DeleteForward deletes n bytes after the gap. Returns bytes deleted.
func (g *gapBuffer) DeleteForward(n int) int {
	avail := len(g.buf) - g.gapEnd
	if n > avail {
		n = avail
	}
	g.gapEnd += n
	return n
}

// ByteAt returns the byte at logical position pos.
func (g *gapBuffer) ByteAt(pos int) byte {
	if pos < g.gapStart {
		return g.buf[pos]
	}
	return g.buf[pos+g.gapLen()]
}

// RuneAt decodes and returns the rune at logical byte position pos,
// plus the rune's byte width.
func (g *gapBuffer) RuneAt(pos int) (rune, int) {
	if pos < g.gapStart {
		// Rune might span the gap boundary.
		if g.gapStart-pos >= utf8.UTFMax {
			return utf8.DecodeRune(g.buf[pos:])
		}
		// Build a small temp buffer bridging the gap.
		var tmp [utf8.UTFMax]byte
		n := 0
		for i := pos; i < g.gapStart && n < utf8.UTFMax; i++ {
			tmp[n] = g.buf[i]
			n++
		}
		for i := g.gapEnd; n < utf8.UTFMax && i < len(g.buf); i++ {
			tmp[n] = g.buf[i]
			n++
		}
		return utf8.DecodeRune(tmp[:n])
	}
	return utf8.DecodeRune(g.buf[pos+g.gapLen():])
}

// String materializes the content as a single string (two copies around gap).
func (g *gapBuffer) String() string {
	out := make([]byte, g.Len())
	copy(out, g.buf[:g.gapStart])
	copy(out[g.gapStart:], g.buf[g.gapEnd:])
	return string(out)
}

// Slice returns the logical bytes in [start, end) as a new string.
func (g *gapBuffer) Slice(start, end int) string {
	if start == end {
		return ""
	}
	n := end - start
	out := make([]byte, n)
	g.copyTo(out, start, n)
	return string(out)
}

// copyTo copies n logical bytes starting at logical position start into dst.
func (g *gapBuffer) copyTo(dst []byte, start, n int) {
	if start+n <= g.gapStart {
		copy(dst, g.buf[start:start+n])
		return
	}
	if start >= g.gapStart {
		phys := start + g.gapLen()
		copy(dst, g.buf[phys:phys+n])
		return
	}
	// Spans the gap.
	beforeGap := g.gapStart - start
	copy(dst, g.buf[start:g.gapStart])
	copy(dst[beforeGap:], g.buf[g.gapEnd:g.gapEnd+(n-beforeGap)])
}

// RuneCount returns the number of runes in the content.
func (g *gapBuffer) RuneCount() int {
	n := utf8.RuneCount(g.buf[:g.gapStart])
	n += utf8.RuneCount(g.buf[g.gapEnd:])
	return n
}

// LineCount returns the number of logical lines (1 + number of '\n' chars).
func (g *gapBuffer) LineCount() int {
	n := 1
	for i := 0; i < g.gapStart; i++ {
		if g.buf[i] == '\n' {
			n++
		}
	}
	for i := g.gapEnd; i < len(g.buf); i++ {
		if g.buf[i] == '\n' {
			n++
		}
	}
	return n
}

// ForEachByte calls fn for each logical byte in order. If fn returns false,
// iteration stops.
func (g *gapBuffer) ForEachByte(fn func(pos int, b byte) bool) {
	for i := 0; i < g.gapStart; i++ {
		if !fn(i, g.buf[i]) {
			return
		}
	}
	off := g.gapLen()
	for i := g.gapEnd; i < len(g.buf); i++ {
		if !fn(i-off, g.buf[i]) {
			return
		}
	}
}

// RuneByteOffset returns the byte offset of the n-th rune (0-based) in the
// logical content. Returns Len() if n >= RuneCount().
func (g *gapBuffer) RuneByteOffset(n int) int {
	count := 0
	// Scan before gap.
	pos := 0
	for pos < g.gapStart && count < n {
		_, sz := utf8.DecodeRune(g.buf[pos:g.gapStart])
		pos += sz
		count++
	}
	if count == n {
		return pos
	}
	// Scan after gap.
	phys := g.gapEnd
	logPos := g.gapStart
	for phys < len(g.buf) && count < n {
		_, sz := utf8.DecodeRune(g.buf[phys:])
		phys += sz
		logPos += sz
		count++
	}
	return logPos
}

// WriteTo writes the logical content into w in two chunks (before-gap,
// after-gap) with no intermediate allocation.
func (g *gapBuffer) WriteTo(w io.Writer) (int64, error) {
	n1, err := w.Write(g.buf[:g.gapStart])
	if err != nil {
		return int64(n1), err
	}
	n2, err := w.Write(g.buf[g.gapEnd:])
	return int64(n1 + n2), err
}

// ByteOffsetToRuneOffset converts a logical byte offset to a rune count.
func (g *gapBuffer) ByteOffsetToRuneOffset(byteOff int) int {
	count := 0
	if byteOff <= g.gapStart {
		pos := 0
		for pos < byteOff {
			_, sz := utf8.DecodeRune(g.buf[pos:g.gapStart])
			pos += sz
			count++
		}
		return count
	}
	// Count all runes before gap.
	count = utf8.RuneCount(g.buf[:g.gapStart])
	// Count runes in after-gap region up to the target.
	phys := g.gapEnd
	logPos := g.gapStart
	for logPos < byteOff && phys < len(g.buf) {
		_, sz := utf8.DecodeRune(g.buf[phys:])
		phys += sz
		logPos += sz
		count++
	}
	return count
}
