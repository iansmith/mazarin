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
// over a larger virtual surface. Children declare virtual Y positions
// (set by LayoutChildren or constraints); Scroller renders only the
// children that intersect the current viewport.
//
// The offscreen buffer is viewport-sized (Width × Height), NOT
// virtual-sized. On each draw the scroller walks children in Y order,
// skips those entirely outside the viewport, and clips partially
// visible children to the viewport intersection. This keeps memory
// proportional to the visible area regardless of virtual content size.
//
// True size (Width, Height) comes from external constraints — typically
// bound to the available viewport space. Virtual size (VirtualWidth,
// VirtualHeight) is the total content extent.
//
// ScrollNeededX / ScrollNeededY are boolean attributes updated each
// frame, useful for driving scrollbar visibility via constraints.
//
// MaxScrollY is a constrained attribute: VirtualHeight - Height. It
// represents the maximum scroll range. When a scrollbar is wired,
// VirtualY can be constrained to the scrollbar's value attribute.
type Scroller struct {
	impl.Interactor
	impl.Parent

	Pal mancini.Palette // background fill color (Surface)

	VirtualWidth  *attr.Attribute[int64]
	VirtualHeight *attr.Attribute[int64]
	VirtualX      *attr.Attribute[int64]
	VirtualY      *attr.Attribute[int64]

	MaxScrollY *attr.Attribute[int64] // constrained: VirtualHeight - Height

	ScrollNeededX *attr.Attribute[bool]
	ScrollNeededY *attr.Attribute[bool]

	offBuf     *image.RGBA
	offDC      mancini.DrawContext
	offW, offH int // current offscreen buffer dimensions (viewport size)

	// ScrollValueY is the upstream value attribute that drives VirtualY
	// when VirtualY is a constraint. ScrollTo writes here instead of
	// directly to VirtualY. nil when VirtualY is a plain value.
	ScrollValueY *attr.Attribute[int64]
}

// NewScroller creates a Scroller wired to the constraint system.
// Width and Height must be provided by the caller — typically
// constraints bound to the parent's dimensions. virtualY is optional:
// if non-nil, it is used as the VirtualY attribute (e.g., a constraint
// wired to a scrollbar's value); if nil, a value attribute is created.
// The child is not passed here; it declares the Scroller as its parent
// via its own layout attributes.
func NewScroller(myName, parent string, pal mancini.Palette,
	width, height *attr.Attribute[int64],
	virtualY *attr.Attribute[int64]) *Scroller {
	if myName == "" {
		myName = mancini.DefaultName("scroller")
	}
	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = width
	lh.Height = height
	lh.InitBounds(myName)

	s := &Scroller{Pal: pal}

	s.VirtualX = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "VirtualX"), 0)
	if virtualY != nil {
		s.VirtualY = virtualY
	} else {
		s.VirtualY = attr.ValueI64(
			mancini.LayoutURI(myName, mancini.DataTypeInt64, "VirtualY"), 0)
	}
	s.VirtualWidth = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "VirtualWidth"), 0)
	s.VirtualHeight = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "VirtualHeight"), 0)

	s.MaxScrollY = attr.ConstraintI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "MaxScrollY"),
		mancini.SubI64(s.VirtualHeight.URI(), lh.Height.URI()))

	s.ScrollNeededX = attr.ValueBool(
		mancini.LayoutURI(myName, mancini.DataTypeBool, "ScrollNeededX"), false)
	s.ScrollNeededY = attr.ValueBool(
		mancini.LayoutURI(myName, mancini.DataTypeBool, "ScrollNeededY"), false)

	s.Interactor.Initialize(s, lh)
	s.Parent.Initialize(true, &s.Interactor)
	return s
}

// HeightURI returns the URI for the scroller's Height attribute.
// Useful for callers that need to build the constraint before creating
// the Scroller.
func ScrollerHeightURI(myName string) string {
	return mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight)
}

// MaxScrollYURI returns the URI of the MaxScrollY attribute.
func (s *Scroller) MaxScrollYURI() string {
	return s.MaxScrollY.URI()
}

// ScrollTo sets the scroll position to absolute virtual coordinates.
// Values are clamped to the valid range on the next Draw.
// Does NOT mark content dirty — only changes the blit offset.
// When VirtualY is a constraint (wired to a scrollbar), writes to
// ScrollValueY instead, which flows through the constraint.
func (s *Scroller) ScrollTo(vx, vy int64) {
	if vx != s.VirtualX.Get() || vy != s.VirtualY.Get() {
		s.VirtualX.Set(vx)
		if s.ScrollValueY != nil {
			s.ScrollValueY.Set(vy)
		} else {
			s.VirtualY.Set(vy)
		}
		s.FullDamage()
	}
}

// ScrollBy adds a delta to the current scroll position.
func (s *Scroller) ScrollBy(dx, dy int64) {
	s.ScrollTo(s.VirtualX.Get()+dx, s.VirtualY.Get()+dy)
}

// SetVirtualSize changes the virtual surface dimensions.
// Forces buffer reallocation on the next Draw.
func (s *Scroller) SetVirtualSize(vw, vh int64) {
	if vw != s.VirtualWidth.Get() || vh != s.VirtualHeight.Get() {
		s.VirtualWidth.Set(vw)
		s.VirtualHeight.Set(vh)
		s.FullDamage()
	}
}

// MarkContentDirty signals that the child content has changed and needs
// to be re-rendered on the next Draw.
func (s *Scroller) MarkContentDirty() {
	s.FullDamage()
}

// DeleteChild removes a child from this Scroller's child list.
func (s *Scroller) DeleteChild(child mancini.Interactor) {
	s.Parent.DeleteChild(child)
}

// Draw implements mancini.NewDrawer.
//
// The offscreen buffer is viewport-sized (w × h). On each draw:
//  1. Walk children in Y order.
//  2. Skip children whose bottom (childY + childH) <= scrollY (above viewport).
//  3. Skip children whose top (childY) >= scrollY + viewportH (below viewport).
//  4. For visible children, compute the intersection of the child rect with
//     the viewport, set a clip rectangle, translate Y, and draw.
//
// No virtual-sized buffer is ever allocated.
func (s *Scroller) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	if !s.Damaged(damage) {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	defer s.SnapshotDamage()

	children := s.GetChildren()
	if len(children) == 0 {
		return
	}

	vw := s.VirtualWidth.Get()
	vh := s.VirtualHeight.Get()
	vy := s.VirtualY.Get()

	needScrollX := vw > w
	needScrollY := vh > h
	s.ScrollNeededX.Set(needScrollX)
	s.ScrollNeededY.Set(needScrollY)

	// Fast path: no scrolling needed — draw children directly to parent DC.
	if !needScrollX && !needScrollY {
		for _, child := range children {
			var cw, ch int64
			if l, ok := child.(mancini.Layouter); ok {
				clh := l.GetLayout()
				if clh != nil {
					if !clh.Width.IsConstraint() {
						clh.Width.Set(w)
					}
					cw = clh.Width.Get()
					ch = clh.Height.Get()
				}
			}
			if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
				cs.SetDC(dc)
			}
			if d, ok := child.(mancini.NewDrawer); ok {
				if cl, ok := child.(mancini.Layouter); ok {
					clh := cl.GetLayout()
					d.Draw(child, x+clh.X.Get(), y+clh.Y.Get(), cw, ch, damage)
				}
			}
		}
		return
	}

	// Scroll path: clamp scroll position.
	if vy < 0 {
		vy = 0
	}
	if maxY := vh - h; vy > maxY {
		vy = maxY
	}

	// Manage viewport-sized offscreen buffer.
	bw, bh := int(w), int(h)
	if s.offBuf == nil || s.offW != bw || s.offH != bh {
		s.offBuf = image.NewRGBA(image.Rect(0, 0, bw, bh))
		s.offDC = dc.NewChildContext(s.offBuf)
		s.offW = bw
		s.offH = bh
		fmt.Printf("[scroller] viewport buffer alloc %dx%d\n", bw, bh)
	}

	// Clear the viewport buffer.
	fillRGBA(s.offBuf, s.Pal.Surface())

	// Viewport in virtual coordinates: [vy, vy+h).
	vpTop := vy
	vpBot := vy + h

	// Walk children, drawing only those that intersect the viewport.
	for _, child := range children {
		cl, cok := child.(mancini.Layouter)
		if !cok {
			continue
		}
		clh := cl.GetLayout()
		if clh == nil {
			continue
		}

		childY := clh.Y.Get()
		childH := clh.Height.Get()
		childBot := childY + childH

		// Skip children entirely above the viewport.
		if childBot <= vpTop {
			continue
		}
		// Stop once we reach children entirely below the viewport.
		if childY >= vpBot {
			break
		}

		// Child intersects the viewport. Compute the visible portion
		// in virtual coordinates.
		visTop := childY
		if visTop < vpTop {
			visTop = vpTop
		}
		visBot := childBot
		if visBot > vpBot {
			visBot = vpBot
		}

		// Map to buffer coordinates (buffer Y=0 corresponds to virtual Y=vy).
		bufChildY := childY - vy
		bufVisTop := visTop - vy
		bufVisBot := visBot - vy

		// Set child width.
		if !clh.Width.IsConstraint() {
			clh.Width.Set(w)
		}
		cw := clh.Width.Get()

		// Set DC and clip to the visible intersection.
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(s.offDC)
		}

		// Pass a full-viewport damage rect so all children redraw on
		// the cleared buffer. We avoid forceFullDamageRecursive because
		// writing to DamageRect attributes triggers constraint cascades.
		fullDamage := image.Rect(0, 0, bw, bh)

		// Push graphics state, set clip to the visible portion, draw, pop.
		s.offDC.Push()
		s.offDC.DrawRectangle(0, float64(bufVisTop), float64(cw), float64(bufVisBot-bufVisTop))
		s.offDC.Clip()

		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, 0, bufChildY, cw, childH, fullDamage)
		}

		s.offDC.Pop()
	}

	// Blit viewport buffer to the parent DC at (x, y).
	canvas := dc.Image().(*image.RGBA)
	tx, ty := dc.TransformPoint(float64(x), float64(y))
	dstRect := image.Rect(int(tx), int(ty), int(tx)+bw, int(ty)+bh)
	srcPt := image.Point{X: 0, Y: 0}
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

	vw := s.VirtualWidth.Get()
	vh := s.VirtualHeight.Get()

	// Fast path: no scrolling — use default pick.
	if vw <= iw && vh <= ih {
		return s.Interactor.Pick(localX, localY)
	}

	// Scroll path: translate viewport-local coords to virtual-space
	// coords, then subtract each child's position to get child-local.
	var result []mancini.Interactor
	children := s.GetChildren()
	virtualX := s.VirtualX.Get()
	virtualY := s.VirtualY.Get()
	// virtualCoord = localCoord + scrollOffset
	vx := localX + virtualX
	vy := localY + virtualY
	for _, child := range children {
		cl, cok := child.(mancini.Layouter)
		if !cok {
			continue
		}
		clh := cl.GetLayout()
		if clh == nil {
			continue
		}
		childLocalX := vx - clh.X.Get()
		childLocalY := vy - clh.Y.Get()
		if picker, ok := child.(mancini.Picker); ok {
			result = append(result, picker.Pick(childLocalX, childLocalY)...)
		}
	}

	result = append(result, s)
	return result
}

// forceFullDamageRecursive marks an interactor and all its descendants
// as fully damaged. Used when a parent buffer wipe invalidates all
// descendant pixels.
func forceFullDamageRecursive(i mancini.Interactor) {
	if fd, ok := i.(interface{ FullDamage() }); ok {
		fd.FullDamage()
	}
	if p, ok := i.(interface{ GetChildren() []mancini.Interactor }); ok {
		for _, child := range p.GetChildren() {
			forceFullDamageRecursive(child)
		}
	}
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
