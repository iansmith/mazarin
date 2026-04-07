package std

import (
	"fmt"
	"image/color"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/sys"
)

// OutputFormat controls how Display() formats a numeric value.
type OutputFormat int

const (
	FormatDecimal OutputFormat = iota
	FormatHex
	FormatBinary
)

// hexDisplayPad is horizontal padding (pixels) inside the display background.
const hexDisplayPad = 2.0

// HexDisplay is a parent interactor that arranges a row of FourteenSeg
// children, one per character. It fills the available width from its
// parent, dynamically adjusting the number of digit positions when the
// width changes (e.g. on window resize).
//
// HexDisplay embeds [impl.Interactor] + [impl.Parent] so it participates
// in the interactor tree and the constraint network discovers its
// FourteenSeg children automatically.
type HexDisplay struct {
	impl.Interactor
	impl.Parent

	Format   OutputFormat   // controls Display() number formatting
	HPadding float64        // horizontal padding inset for background fill
	digits   []*FourteenSeg // ordered array: digits[0] is leftmost position
	bgColor  color.NRGBA
	onColor  color.NRGBA
	cellW    float64
	cellH    float64
	gap      float64
	lastNumC int    // number of characters last time we painted
	myName   string // constraint-system name for creating children
}

// NewHexDisplay creates a hex display whose width is constrained to equal
// widthSource's width. The number of FourteenSeg digit children is computed
// dynamically from the resolved width so they fill the display without
// clipping the last character.
// widthSource is the constraint-system name of a sibling whose width this
// display should match (e.g. "btn_row").
// cellW and cellH control each character cell size.
// bgColor is painted as the background behind all digits.
func NewHexDisplay(myName, parent, widthSource string,
	cellW, cellH float64, onColor, bgColor color.NRGBA) *HexDisplay {

	if myName == "" {
		myName = mancini.DefaultName("hexdisp")
	}

	gap := cellW * 0.5
	totalH := cellH + 4 // small vertical padding

	// Width = widthSource's width (sibling constraint).
	sourceWidthURI := mancini.LayoutURI(widthSource, mancini.DataTypeInt64, mancini.LayoutWidth)
	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ConstraintI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.EqualI64(sourceWidthURI))
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight),
		int64(totalH))
	lh.InitBounds(myName)

	hd := &HexDisplay{
		bgColor: bgColor,
		onColor: onColor,
		cellW:   cellW,
		cellH:   cellH,
		gap:     gap,
		myName:  myName,
	}
	hd.Interactor.Initialize(hd, lh)
	hd.Parent.Initialize(true, &hd.Interactor)

	// Create initial children directly (fast path — no AddChildFirst needed
	// since construction order matches display order).
	n := hd.computeNumChars()
	hd.digits = make([]*FourteenSeg, n)
	for i := 0; i < n; i++ {
		chName := mancini.DefaultName("hexdig")
		seg := NewFourteenSeg(chName, myName,
			" ", cellW, cellH, onColor)
		hd.digits[i] = seg
	}
	hd.lastNumC = n

	sys.UartWriteString(fmt.Sprintf("[hexdisp] created %d digits, width=%d\n",
		n, lh.Width.Get()))

	return hd
}

// computeNumChars returns how many character cells fit in the current width.
func (hd *HexDisplay) computeNumChars() int {
	lh := hd.GetLayout()
	if lh == nil || lh.Width == nil {
		return 1
	}
	availW := float64(lh.Width.Get()) - 2*(hd.HPadding+hexDisplayPad)
	if availW < hd.cellW {
		availW = hd.cellW
	}
	// n*cellW + (n-1)*gap <= availW  =>  n <= (availW + gap) / (cellW + gap)
	n := int((availW + hd.gap) / (hd.cellW + hd.gap))
	if n < 1 {
		n = 1
	}
	return n
}

// rebuildDigits tears down existing FourteenSeg children and creates n
// new ones. Children are created in reverse order and added via
// AddChildFirst so that digits[0] is the leftmost position.
func (hd *HexDisplay) rebuildDigits(n int) {
	sys.UartWriteString(fmt.Sprintf("[hexdisp] rebuild: %d -> %d digits\n",
		hd.lastNumC, n))

	// Remove all existing children.
	hd.DeleteAllChildren()

	hd.digits = make([]*FourteenSeg, n)
	// Create children in reverse so AddChildFirst puts them in order:
	// the last-created child (index 0) will be first after all swaps.
	for i := n - 1; i >= 0; i-- {
		chName := mancini.DefaultName("hexdig")
		seg := NewFourteenSeg(chName, "",
			" ", hd.cellW, hd.cellH, hd.onColor)
		hd.Parent.AddChildFirst(seg)
		hd.digits[i] = seg
	}
	hd.lastNumC = n
}

// NumDigits returns the number of digit positions in this display.
func (hd *HexDisplay) NumDigits() int {
	return len(hd.digits)
}

// Display shows the value x on the 14-segment display using the current
// OutputFormat. Characters are loaded right-to-left; if the formatted
// string is longer than the available digit positions, leading characters
// are discarded. Unused leading positions show dim segments.
func (hd *HexDisplay) Display(x int) {
	var text string
	switch hd.Format {
	case FormatHex:
		text = fmt.Sprintf("%X", uint32(x))
	case FormatBinary:
		text = fmt.Sprintf("%b", uint32(x))
	default: // FormatDecimal
		text = fmt.Sprintf("%d", x)
	}
	n := len(hd.digits)

	// Fill from the right: peel characters off the end of text and load
	// them into successive positions from the right side of the array.
	pos := n - 1
	for i := len(text) - 1; i >= 0 && pos >= 0; i-- {
		hd.digits[pos].SetText(string(text[i]))
		pos--
	}
	// Blank any remaining leading positions (shows dim segments).
	for ; pos >= 0; pos-- {
		hd.digits[pos].SetText(" ")
	}
}

// SetText updates all digit children to show new text (right-aligned).
// If the new text is shorter than the display, leading positions show space.
// If longer, leading characters are truncated.
func (hd *HexDisplay) SetText(text string) {
	n := len(hd.digits)
	// Right-align: pad with spaces on the left.
	for len(text) < n {
		text = " " + text
	}
	// If text is longer, take the rightmost n characters.
	if len(text) > n {
		text = text[len(text)-n:]
	}
	for i, seg := range hd.digits {
		seg.SetText(string(text[i]))
	}
}

// Draw paints the background and arranges FourteenSeg children left-to-right.
// If the width has changed since the last draw, the digit children are
// rebuilt to match the new available space.
func (hd *HexDisplay) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	// Check if the number of characters needs to change.
	numC := hd.computeNumChars()
	if numC != hd.lastNumC {
		hd.rebuildDigits(numC)
	}

	// Paint background (inset by HPadding to align with padded content below).
	pad := hd.HPadding
	dc.SetColor(hd.bgColor)
	dc.DrawRoundedRectangle(float64(x)+pad, float64(y), float64(w)-2*pad, float64(h), 3)
	dc.Fill()

	// Center digits vertically; pad horizontally.
	digitY := y + (h-int64(hd.cellH))/2
	cx := float64(x) + pad + hexDisplayPad
	for _, seg := range hd.digits {
		if cs, ok := mancini.Interactor(seg).(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		segLH := seg.GetLayout()
		if segLH != nil {
			segLH.X.Set(int64(cx))
			segLH.Y.Set(digitY)
			if !segLH.Width.IsConstraint() {
				segLH.Width.Set(int64(hd.cellW))
			}
			if !segLH.Height.IsConstraint() {
				segLH.Height.Set(int64(hd.cellH))
			}
		}
		seg.Draw(seg, int64(cx), digitY, int64(hd.cellW), int64(hd.cellH))
		cx += hd.cellW + hd.gap
	}
}
