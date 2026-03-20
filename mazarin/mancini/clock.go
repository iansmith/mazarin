package mancini

import (
	"math"
	"time"

)

// Clock is a leaf interactor that draws an analog clock using a ClockFace.
// The ClockFace controls the visual appearance and timezone. Clock handles
// layout, time conversion from UTC, and delegates rendering to the face.
//
// If Faces is populated, clicking the clock cycles through face styles.
// Clock implements MousePressHandler for this purpose.
type Clock struct {
	Theme   *Theme
	Name    string
	Size    float64                    // preferred side length (logical pixels)
	Face    ClockFace                  // current active face
	Faces   []ClockFace                // ordered list of faces to cycle through on click
	faceIdx     int                    // index into Faces of the current face
	prePressIdx int                    // saved faceIdx before press (for cancel/revert)
	interacting bool                   // true while in a press-drag-release interaction
	UTCFunc    func() (sec, nanos int64) // returns current UTC time
	FaceLabel  Drawer                    // label showing face name (hidden in steady state)
	FaceSpacer Drawer                    // spacer matching label height (visible in steady state)
	Layout     *LayoutHandles
}

func (c *Clock) GetLayout() *LayoutHandles { return c.Layout }

// PreferredWidth returns the clock's preferred side length.
func (c *Clock) PreferredWidth() float64 {
	s := c.Size
	if s <= 0 {
		s = 50
	}
	return s
}

// PreferredHeight returns the clock's preferred side length.
func (c *Clock) PreferredHeight() float64 { return c.PreferredWidth() }

// Draw implements the Drawer interface.
func (c *Clock) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(c.Layout, x, y, math.Round(w), math.Round(h))

	cx := x + w/2
	cy := y + h/2
	rad := math.Min(w, h) / 2

	// Get UTC time and convert to the face's local timezone.
	var hour, minute, second, millis int
	if c.UTCFunc != nil && c.Face != nil {
		sec, nanos := c.UTCFunc()
		millis = int(nanos / 1_000_000)
		t := time.Unix(sec, 0).In(c.Face.Location())
		hour, minute, second = t.Hour(), t.Minute(), t.Second()
	}

	if c.Face != nil {
		c.Face.DrawFace(dc, c.Theme, cx, cy, rad, hour, minute, second, millis)
	}
}
