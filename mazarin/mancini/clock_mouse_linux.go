//go:build linux

package mancini

import "math"

// DetailedHit returns true if (localX, localY) is inside the clock's
// circular face. Uses the actual layout dimensions from constraints
// to compute center and radius.
func (c *Clock) DetailedHit(localX, localY int64) bool {
	w := float64(c.Layout.Width.Get())
	h := float64(c.Layout.Height.Get())
	cx := w / 2
	cy := h / 2
	rad := math.Min(w, h) / 2
	dx := float64(localX) - cx
	dy := float64(localY) - cy
	return dx*dx+dy*dy <= rad*rad
}

// HandleMousePress cycles to the next ClockFace in the Faces list.
func (c *Clock) HandleMousePress(x, y int64) {
	if len(c.Faces) < 2 {
		return
	}
	c.faceIdx = (c.faceIdx + 1) % len(c.Faces)
	c.Face = c.Faces[c.faceIdx]
}
