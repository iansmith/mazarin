package std

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Scroller is a single-child parent that provides a scrollable viewport
// over a larger virtual surface. The child draws into a virtual-sized
// offscreen buffer, and Scroller blits only the visible portion to the
// screen.
//
// The offscreen buffer is the full virtual size so that content can be
// rendered once and scrolling only changes the blit source offset.
// Content is re-rendered only when MarkContentDirty is called (typically
// on time ticks or other content changes). Scroll position changes
// (ScrollTo, ScrollBy) do NOT trigger content re-rendering — they only
// change which portion of the buffer is blitted.
//
// True size (Width, Height) comes from external constraints — typically
// bound to the available viewport space. Virtual size (VirtualWidth,
// VirtualHeight) is the size the child thinks it has.
//
// ScrollNeededX / ScrollNeededY are boolean attributes updated each
// frame, useful for driving scrollbar visibility via constraints.
type Scroller struct {
	impl.Interactor
	impl.Parent

	Pal mancini.Palette // background fill color (Surface)

	VirtualWidth  int64 // child's virtual surface width
	VirtualHeight int64 // child's virtual surface height
	VirtualX      int64 // scroll position X (0 = left edge visible)
	VirtualY      int64 // scroll position Y (0 = top edge visible)

	ScrollNeededX *attr.Attribute[bool]
	ScrollNeededY *attr.Attribute[bool]

	offBuf       *image.RGBA
	offDC        mancini.DrawContext
	offW, offH   int  // current offscreen buffer dimensions (virtual size)
	contentDirty bool // true = need to re-render children into offBuf
}

// NewScroller creates a Scroller wired to the constraint system.
// Width is created as a value attribute (set imperatively by the caller).
// Height must be provided by the caller — typically a constraint bound
// to the child's height so the viewport tracks the content height.
// The child is not passed here; it declares the Scroller as its parent
// via its own layout attributes.
func NewScroller(myName, parent string, pal mancini.Palette, height *attr.Attribute[int64]) *Scroller {
	if myName == "" {
		myName = mancini.DefaultName("scroller")
	}
	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	lh.Height = height
	lh.InitBounds(myName)

	s := &Scroller{Pal: pal, contentDirty: true}
	s.ScrollNeededX = attr.ValueBool(
		mancini.LayoutURI(myName, mancini.DataTypeBool, "ScrollNeededX"), false)
	s.ScrollNeededY = attr.ValueBool(
		mancini.LayoutURI(myName, mancini.DataTypeBool, "ScrollNeededY"), false)

	s.Interactor.Initialize(s, lh)
	s.Parent.Initialize(false, &s.Interactor)
	return s
}

// HeightURI returns the URI for the scroller's Height attribute.
// Useful for callers that need to build the constraint before creating
// the Scroller.
func ScrollerHeightURI(myName string) string {
	return mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight)
}

// ScrollTo sets the scroll position to absolute virtual coordinates.
// Values are clamped to the valid range on the next Draw.
// Does NOT mark content dirty — only changes the blit offset.
func (s *Scroller) ScrollTo(vx, vy int64) {
	if vx != s.VirtualX || vy != s.VirtualY {
		s.VirtualX = vx
		s.VirtualY = vy
		s.FullDamage()
	}
}

// ScrollBy adds a delta to the current scroll position.
func (s *Scroller) ScrollBy(dx, dy int64) {
	s.ScrollTo(s.VirtualX+dx, s.VirtualY+dy)
}

// SetVirtualSize changes the virtual surface dimensions.
// Forces buffer reallocation and content re-render on the next Draw.
func (s *Scroller) SetVirtualSize(vw, vh int64) {
	if vw != s.VirtualWidth || vh != s.VirtualHeight {
		s.VirtualWidth = vw
		s.VirtualHeight = vh
		s.offBuf = nil // force reallocation
		s.offDC = nil
		s.contentDirty = true
		s.FullDamage()
	}
}

// MarkContentDirty signals that the child content has changed and needs
// to be re-rendered into the offscreen buffer on the next Draw.
// Call this when time ticks, data updates, or any other non-scroll
// content change occurs.
func (s *Scroller) MarkContentDirty() {
	s.contentDirty = true
}

// Draw implements mancini.NewDrawer.
func (s *Scroller) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	children := s.GetChildren()
	if len(children) == 0 {
		return
	}
	child := children[0]

	// Effective virtual size is at least the true size.
	effW := s.VirtualWidth
	if effW < w {
		effW = w
	}
	effH := s.VirtualHeight
	if effH < h {
		effH = h
	}

	needScrollX := s.VirtualWidth > w
	needScrollY := s.VirtualHeight > h
	s.ScrollNeededX.Set(needScrollX)
	s.ScrollNeededY.Set(needScrollY)

	// Fast path: no scrolling needed — pass through directly.
	if !needScrollX && !needScrollY {
		if l, ok := child.(mancini.Layouter); ok {
			lh := l.GetLayout()
			if lh != nil {
				lh.X.Set(x)
				lh.Y.Set(y)
			}
		}
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, x, y, w, h)
		}
		return
	}

	// Scroll path: clamp position.
	if s.VirtualX < 0 {
		s.VirtualX = 0
	}
	if maxX := effW - w; s.VirtualX > maxX {
		s.VirtualX = maxX
	}
	if s.VirtualY < 0 {
		s.VirtualY = 0
	}
	if maxY := effH - h; s.VirtualY > maxY {
		s.VirtualY = maxY
	}

	// Manage virtual-sized offscreen buffer.
	bw, bh := int(effW), int(effH)
	if s.offBuf == nil || s.offW != bw || s.offH != bh {
		s.offBuf = image.NewRGBA(image.Rect(0, 0, bw, bh))
		s.offDC = dc.NewChildContext(s.offBuf)
		s.offW = bw
		s.offH = bh
		s.contentDirty = true
		fmt.Printf("[scroller] buffer alloc %dx%d (virtual)\n", bw, bh)
	}

	// Re-render content only when dirty.
	if s.contentDirty {
		fillRGBA(s.offBuf, s.Pal.Surface())

		// Position child at virtual origin and draw into full virtual buffer.
		// No clip/translate needed — buffer IS virtual-sized.
		if l, ok := child.(mancini.Layouter); ok {
			lh := l.GetLayout()
			if lh != nil {
				lh.X.Set(0)
				lh.Y.Set(0)
			}
		}
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(s.offDC)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, 0, 0, effW, effH)
		}
		s.contentDirty = false
	}

	// Blit viewport-sized portion from (VX, VY) to the parent DC at (x, y).
	canvas := dc.Image().(*image.RGBA)
	tx, ty := dc.TransformPoint(float64(x), float64(y))
	vw, vh := int(w), int(h)
	dstRect := image.Rect(int(tx), int(ty), int(tx)+vw, int(ty)+vh)
	srcPt := image.Point{X: int(s.VirtualX), Y: int(s.VirtualY)}
	draw.Draw(canvas, dstRect, s.offBuf, srcPt, draw.Src)
}

// Pick performs hit testing with virtual coordinate translation.
// Overrides the default impl.Interactor.Pick to add the scroll offset
// before recursing into the child.
func (s *Scroller) Pick(localX, localY int64) []mancini.Interactor {
	lh := s.GetLayout()
	if lh == nil {
		return nil
	}
	iw, ih := lh.Width.Get(), lh.Height.Get()
	if localX < 0 || localX >= iw || localY < 0 || localY >= ih {
		return nil
	}
	if !s.Visible() {
		return nil
	}

	// Fast path: no scrolling — use default pick.
	needScrollX := s.VirtualWidth > iw
	needScrollY := s.VirtualHeight > ih
	if !needScrollX && !needScrollY {
		return s.Interactor.Pick(localX, localY)
	}

	// Scroll path: translate to virtual coords before recursing.
	var result []mancini.Interactor
	children := s.GetChildren()
	if len(children) > 0 {
		child := children[0]
		childLocalX := localX + s.VirtualX
		childLocalY := localY + s.VirtualY
		if picker, ok := child.(mancini.Picker); ok {
			result = append(result, picker.Pick(childLocalX, childLocalY)...)
		}
	}

	result = append(result, s)
	return result
}

// fillRGBA fills an RGBA image with a solid color.
func fillRGBA(img *image.RGBA, c color.Color) {
	r, g, b, a := c.RGBA()
	r8, g8, b8, a8 := byte(r>>8), byte(g>>8), byte(b>>8), byte(a>>8)
	pix := img.Pix
	for i := 0; i < len(pix); i += 4 {
		pix[i] = r8
		pix[i+1] = g8
		pix[i+2] = b8
		pix[i+3] = a8
	}
}
