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

// Feedback is called by the mouse state machine during a press-drag-release.
// active=true means the mouse is inside (preview the next face).
// active=false means the mouse dragged outside (revert to original face).
func (c *Clock) Feedback(active bool) {
	if len(c.Faces) < 2 {
		return
	}
	if active {
		if !c.interacting {
			c.prePressIdx = c.faceIdx
			c.interacting = true
		}
		c.faceIdx = (c.prePressIdx + 1) % len(c.Faces)
	} else {
		c.faceIdx = c.prePressIdx
	}
	c.Face = c.Faces[c.faceIdx]
}

// Complete is called when the button is released.
// success=true means released inside (commit). success=false means released
// outside (cancel — face was already reverted by Feedback(false)).
func (c *Clock) Complete(success bool) {
	c.interacting = false
}
