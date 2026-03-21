package mancini

// InitLayout creates layout handles for the ColumnOutsideIn.
// Width and Height are value handles (NOT constraints) — the parent
// sets them imperatively via publishLayout during Draw.
//
// Height is pre-set to the sum of children's preferred heights so that
// parent constraint programs (e.g. AppWindow's decorationHeight) can
// read an intrinsic content size before the first Draw pass.
func (c *ColumnOutsideIn) InitLayout(parent string) {
	if c.Name == "" {
		c.Name = defaultName("col_oi")
	}
	c.Layout = newLayoutHandles(c.Name, parent)

	// Publish intrinsic height from children's preferred heights.
	totalH := 0.0
	for _, child := range c.Children {
		if s, ok := child.(Sizer); ok {
			totalH += s.PreferredHeight()
		} else {
			totalH += 20 // fallback per child
		}
	}
	if totalH > 0 {
		c.Layout.Height.Set(int64(totalH))
	}
}
