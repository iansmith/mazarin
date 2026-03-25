package std

import (
	"math"
	"time"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Clock is a leaf interactor that draws an analog clock face.
// The ClockFace controls visual appearance and timezone. Clock handles
// layout, time conversion from UTC, and delegates rendering to the face.
//
// If Faces has more than one entry, clicking the clock cycles styles.
// Clock implements the mouse feedback protocol for this.
type Clock struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()

	Pal     mancini.Palette
	Fonts   *mancini.FontConfig
	Size    int64                        // preferred side length (logical pixels)
	Face    mancini.ClockFace            // current active face
	Faces   []mancini.ClockFace          // ordered list for click cycling
	UTCFunc func() (sec, nanos int64)    // returns current UTC time

	FaceNameAttr *attr.Attribute[string]  // constraint attribute for current face name

	faceIdx     int  // index into Faces of current face
	prePressIdx int  // saved faceIdx before press
	interacting bool // true during press-drag-release

	// Layout attributes for face name label and spacer visibility toggling.
	// Set after construction by the caller (they're sibling interactors).
	FaceLabelLayout  *mancini.LayoutAttributes
	FaceSpacerLayout *mancini.LayoutAttributes
}

// NewClock creates a Clock wired to the constraint system.
// parent is the constraint name of the wrapping NeuCircle (or other parent).
func NewClock(myName, parent string, pal mancini.Palette, fonts *mancini.FontConfig,
	size int64, utcFunc func() (int64, int64), faces []mancini.ClockFace) *Clock {

	if myName == "" {
		myName = mancini.DefaultName("clock")
	}

	c := &Clock{
		Pal:     pal,
		Fonts:   fonts,
		Size:    size,
		UTCFunc: utcFunc,
		Faces:   faces,
	}
	if len(faces) > 0 {
		c.Face = faces[0]
	}

	lh := mancini.NewLayoutAttributes(myName, parent)

	// Publish intrinsic size so parent constraints can bootstrap.
	if size <= 0 {
		size = 50
	}
	lh.Width.Set(size)
	lh.Height.Set(size)

	// Face name string attribute.
	faceName := ""
	if c.Face != nil {
		faceName = c.Face.FaceName()
	}
	faceURI := mancini.LayoutURI(myName, mancini.DataTypeStr, mancini.LayoutFaceName)
	c.FaceNameAttr = attr.ValueStr(faceURI, faceName)

	c.Interactor.Init(c, lh)
	return c
}

// Draw implements mancini.NewDrawer.
func (c *Clock) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	cx := fx + fw/2
	cy := fy + fh/2
	rad := math.Min(fw, fh) / 2

	// Get UTC time and convert to the face's local timezone.
	var hour, minute, second, millis int
	if c.UTCFunc != nil && c.Face != nil {
		sec, nanos := c.UTCFunc()
		millis = int(nanos / 1_000_000)
		t := time.Unix(sec, 0).In(c.Face.Location())
		hour, minute, second = t.Hour(), t.Minute(), t.Second()
	}

	if c.Face != nil {
		c.Face.DrawFace(dc, c.Fonts, c.Pal, cx, cy, rad, hour, minute, second, millis)
	}
}

// --- Mouse interaction ---

// DetailedHit returns true if (localX, localY) is inside the clock's
// circular face.
func (c *Clock) DetailedHit(localX, localY int64) bool {
	lh := c.GetLayout()
	if lh == nil {
		return false
	}
	w := float64(lh.Width.Get())
	h := float64(lh.Height.Get())
	cx := w / 2
	cy := h / 2
	rad := math.Min(w, h) / 2
	dx := float64(localX) - cx
	dy := float64(localY) - cy
	return dx*dx+dy*dy <= rad*rad
}

// Feedback is called during press-drag-release face cycling.
// active=true previews the next face; active=false reverts to original.
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

	// Publish current face name to constraint attribute.
	if c.FaceNameAttr != nil {
		c.FaceNameAttr.Set(c.Face.FaceName())
	}

	// Toggle face name label / spacer visibility.
	if c.FaceLabelLayout != nil {
		c.FaceLabelLayout.Visible.Set(active)
	}
	if c.FaceSpacerLayout != nil {
		c.FaceSpacerLayout.Visible.Set(!active)
	}
}

// Complete is called when the button is released.
// success=true means released inside (commit face change).
// success=false means released outside (face already reverted by Feedback).
func (c *Clock) Complete(success bool) {
	c.interacting = false
	if !success {
		// Cancelled: hide label, show spacer (return to steady state).
		if c.FaceLabelLayout != nil {
			c.FaceLabelLayout.Visible.Set(false)
		}
		if c.FaceSpacerLayout != nil {
			c.FaceSpacerLayout.Visible.Set(true)
		}
	}
}
