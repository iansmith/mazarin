package std

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"strings"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// FourteenSeg is a 14-segment LED display interactor. It renders a string
// of characters using the classic 14-segment geometry (like vintage LED
// calculator/scoreboard displays). Each character uses a 14-bit mask to
// select which segments are lit.
//
// Width and Height are computed from the character count and segment cell
// size. FourteenSeg does not paint a background — the parent is expected
// to handle that.
type FourteenSeg struct {
	impl.Interactor

	text     string
	onColor  color.NRGBA // lit segment color
	dimColor color.NRGBA // unlit segment color (dim ghost)

	cellW float64 // width of one character cell
	cellH float64 // height of one character cell
	gap   float64 // gap between characters
	segW  float64 // segment line thickness
	pad   float64 // padding inside cell
}

// fourteenSegMinWidth computes the segment stroke width, clamped to an
// integer pixel to avoid a divide-by-zero in x/image/vector's fixed-point
// rasterizer. Fractional stroke widths (e.g. 1.76) can produce degenerate
// stroke-expansion geometry where two vertices land at the same fixed-point
// x but in different pixel columns, making twoOverS==0 in fixedLineTo.
func fourteenSegMinWidth(cellW float64) float64 {
	w := max(cellW*0.08, 1.5)
	rounded := math.Ceil(w)
	if rounded != w {
		fmt.Fprintf(os.Stderr, "[fourteen_seg] WARNING: segW %.2f rounded to %.0f to work around x/image/vector fixedLineTo divide-by-zero\n", w, rounded)
	}
	return rounded
}

// NewFourteenSeg creates a 14-segment display showing the given text.
// cellW and cellH control the size of each character cell.
func NewFourteenSeg(myName, parent string,
	text string, cellW, cellH float64, onColor color.NRGBA) *FourteenSeg {

	if myName == "" {
		myName = mancini.DefaultName("14seg")
	}

	n := len(text)
	if n == 0 {
		n = 1
	}
	gap := cellW * 0.15
	totalW := float64(n)*cellW + float64(n-1)*gap
	totalH := cellH

	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(int64(totalW))
	lh.Height.Set(int64(totalH))

	// Dim color: same hue as on, but very low alpha.
	dimColor := onColor
	dimColor.A = 25

	seg := &FourteenSeg{
		text:     strings.ToUpper(text),
		onColor:  onColor,
		dimColor: dimColor,
		cellW:    cellW,
		cellH:    cellH,
		gap:      gap,
		segW:     fourteenSegMinWidth(cellW),
		pad:      max(cellW*0.08, 1.0),
	}
	seg.Interactor.Initialize(seg, lh)
	return seg
}

// SetText updates the displayed text.
func (s *FourteenSeg) SetText(text string) {
	s.text = strings.ToUpper(text)
	// Update width for new character count.
	n := len(s.text)
	if n == 0 {
		n = 1
	}
	totalW := float64(n)*s.cellW + float64(n-1)*s.gap
	lh := s.GetLayout()
	if lh != nil {
		lh.Width.Set(int64(totalW))
	}
}

// SetOnColor changes the lit segment color.
func (s *FourteenSeg) SetOnColor(c color.NRGBA) {
	s.onColor = c
	s.dimColor = c
	s.dimColor.A = 25
}

// Draw renders the 14-segment display. No background is painted.
func (s *FourteenSeg) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	cx := float64(x)
	cy := float64(y)

	for _, ch := range s.text {
		if ch == '.' {
			s.drawDot(dc, cx, cy)
		} else {
			mask := seg14CharMap[ch]
			s.drawChar(dc, cx, cy, mask)
		}
		cx += s.cellW + s.gap
	}
}

// drawChar renders one 14-segment character at position (x, y).
func (s *FourteenSeg) drawChar(dc mancini.DrawContext, x, y float64, mask uint16) {
	w := s.cellW
	h := s.cellH
	p := s.pad
	sw := s.segW

	// Key coordinates within the cell.
	left := x + p
	right := x + w - p
	top := y + p
	bot := y + h - p
	midX := x + w/2
	midY := y + h/2
	hLen := right - left // horizontal segment length

	dc.SetLineWidth(sw)

	// --- Horizontal segments ---
	s.drawSeg(dc, mask, segA, left+sw/2, top, left+hLen-sw/2, top)         // top
	s.drawSeg(dc, mask, segG1, left+sw/2, midY, midX-sw/4, midY)           // mid-left
	s.drawSeg(dc, mask, segG2, midX+sw/4, midY, right-sw/2, midY)          // mid-right
	s.drawSeg(dc, mask, segD, left+sw/2, bot, left+hLen-sw/2, bot)         // bottom

	// --- Vertical segments ---
	s.drawSeg(dc, mask, segF, left, top+sw/2, left, midY-sw/2)             // upper-left
	s.drawSeg(dc, mask, segB, right, top+sw/2, right, midY-sw/2)           // upper-right
	s.drawSeg(dc, mask, segE, left, midY+sw/2, left, bot-sw/2)             // lower-left
	s.drawSeg(dc, mask, segC, right, midY+sw/2, right, bot-sw/2)           // lower-right
	s.drawSeg(dc, mask, segI, midX, top+sw/2, midX, midY-sw/2)            // upper-center
	s.drawSeg(dc, mask, segL, midX, midY+sw/2, midX, bot-sw/2)            // lower-center

	// --- Diagonal segments ---
	s.drawSeg(dc, mask, segH, left+sw, top+sw, midX-sw/2, midY-sw/2)      // upper-left diag
	s.drawSeg(dc, mask, segJ, right-sw, top+sw, midX+sw/2, midY-sw/2)     // upper-right diag
	s.drawSeg(dc, mask, segM, left+sw, bot-sw, midX-sw/2, midY+sw/2)      // lower-left diag
	s.drawSeg(dc, mask, segK, right-sw, bot-sw, midX+sw/2, midY+sw/2)     // lower-right diag
}

// drawSeg draws one segment: lit (onColor) if the bit is set, dim otherwise.
func (s *FourteenSeg) drawSeg(dc mancini.DrawContext, mask uint16, bit uint16,
	x1, y1, x2, y2 float64) {
	// Skip zero-length segments — the vector rasterizer divides by
	// the line span and will panic on zero-length lines.
	if x1 == x2 && y1 == y2 {
		return
	}
	if mask&bit != 0 {
		dc.SetColor(s.onColor)
	} else {
		dc.SetColor(s.dimColor)
	}
	dc.DrawLine(x1, y1, x2, y2)
	dc.Stroke()
}

// drawDot renders a decimal point as a small filled circle at the
// bottom-center of the character cell.
func (s *FourteenSeg) drawDot(dc mancini.DrawContext, x, y float64) {
	r := max(s.segW*0.8, 1.0)
	cx := x + s.cellW/2
	cy := y + s.cellH - s.pad
	dc.SetColor(s.onColor)
	dc.DrawCircle(cx, cy, r)
	dc.Fill()
}

// NewFourteenSegValue creates a 14-segment display with its text backed
// by a string constraint attribute. The display updates when the attribute
// changes.
func NewFourteenSegValue(myName, parent string,
	text string, cellW, cellH float64, onColor color.NRGBA) (*FourteenSeg, *attr.Attribute[string]) {

	seg := NewFourteenSeg(myName, parent, text, cellW, cellH, onColor)
	textAttr := attr.ValueStr(
		mancini.LayoutURI(myName, "string", "Text"), text)
	return seg, textAttr
}

// --- Segment constants and character map ---

const (
	segA  uint16 = 1 << 0
	segB  uint16 = 1 << 1
	segC  uint16 = 1 << 2
	segD  uint16 = 1 << 3
	segE  uint16 = 1 << 4
	segF  uint16 = 1 << 5
	segG1 uint16 = 1 << 6
	segG2 uint16 = 1 << 7
	segH  uint16 = 1 << 8
	segI  uint16 = 1 << 9
	segJ  uint16 = 1 << 10
	segK  uint16 = 1 << 11
	segL  uint16 = 1 << 12
	segM  uint16 = 1 << 13
)

var seg14CharMap = map[rune]uint16{
	'0': segA | segB | segC | segD | segE | segF | segJ | segM,
	'1': segB | segC,
	'2': segA | segB | segD | segE | segG1 | segG2,
	'3': segA | segB | segC | segD | segG2,
	'4': segB | segC | segF | segG1 | segG2,
	'5': segA | segC | segD | segF | segG1 | segG2,
	'6': segA | segC | segD | segE | segF | segG1 | segG2,
	'7': segA | segB | segC,
	'8': segA | segB | segC | segD | segE | segF | segG1 | segG2,
	'9': segA | segB | segC | segD | segF | segG1 | segG2,

	'A': segA | segB | segC | segE | segF | segG1 | segG2,
	'B': segA | segB | segC | segD | segG2 | segI | segL,
	'C': segA | segD | segE | segF,
	'D': segA | segB | segC | segD | segI | segL,
	'E': segA | segD | segE | segF | segG1,
	'F': segA | segE | segF | segG1,
	'G': segA | segC | segD | segE | segF | segG2,
	'H': segB | segC | segE | segF | segG1 | segG2,
	'I': segA | segD | segI | segL,
	'J': segB | segC | segD | segE,
	'K': segE | segF | segG1 | segJ | segK,
	'L': segD | segE | segF,
	'M': segB | segC | segE | segF | segH | segJ,
	'N': segB | segC | segE | segF | segH | segK,
	'O': segA | segB | segC | segD | segE | segF,
	'P': segA | segB | segE | segF | segG1 | segG2,
	'Q': segA | segB | segC | segD | segE | segF | segK,
	'R': segA | segB | segE | segF | segG1 | segG2 | segK,
	'S': segA | segC | segD | segF | segG1 | segG2,
	'T': segA | segI | segL,
	'U': segB | segC | segD | segE | segF,
	'V': segE | segF | segJ | segM,
	'W': segB | segC | segE | segF | segK | segM,
	'X': segH | segJ | segK | segM,
	'Y': segH | segJ | segL,
	'Z': segA | segD | segJ | segM,

	' ': 0,
	'-': segG1 | segG2,
	'.': 0, // dot would need separate handling
	'+': segG1 | segG2 | segI | segL,
	'*': segG1 | segG2 | segH | segI | segJ | segK | segL | segM,
	'/': segJ | segM,
}
