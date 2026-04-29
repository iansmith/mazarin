package std

import (
	"fmt"
	"image"
	"math"
	"time"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Clock is a leaf interactor that draws an analog clock face. The
// [mancini.ClockFace] controls visual appearance and timezone. Clock
// handles layout, time conversion from UTC, and delegates rendering
// to the active face.
//
// Clock embeds [impl.Interactor] directly (not [impl.ThemedInteractor])
// because it receives its [mancini.Palette] and [mancini.FontConfig]
// explicitly rather than from a [mancini.Theme].
//
// Clock implements [mancini.Clickable] to cycle through normal faces
// (Classic, Roman, Movado, Digit) and [mancini.DoubleClickable] to
// cycle through weird faces (Metric, Polar).
//
// Clock is typically wrapped in a [NeuCircle] decorator for neumorphic
// circular shadows.
type Clock struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()

	// Pal is the palette used by the active face for rendering.
	Pal mancini.Palette
	// Fonts is the font configuration passed to the active face.
	Fonts   *mancini.FontConfig
	Size    int64                        // preferred side length (logical pixels)
	Face    mancini.ClockFace            // current active face
	UTCFunc func() (sec, nanos int64)    // returns current UTC time

	FaceNameAttr *attr.Attribute[string]  // constraint attribute for current face name

	// Two face groups: single click cycles normal, double click cycles weird.
	NormalFaces []mancini.ClockFace
	WeirdFaces  []mancini.ClockFace
	normalIdx   int
	weirdIdx    int
	weirdMode   bool // true when showing a weird face

	// Layout attributes for face name label and spacer visibility toggling.
	// Set after construction by the caller (they're sibling interactors).
	FaceLabelLayout  *mancini.LayoutAttributes
	FaceSpacerLayout *mancini.LayoutAttributes
}

// NewClock creates a Clock wired to the constraint system.
// parent is the constraint name of the wrapping NeuCircle (or other parent).
// normalFaces are cycled by single click; weirdFaces by double click.
func NewClock(myName, parent string, pal mancini.Palette, fonts *mancini.FontConfig,
	size int64, utcFunc func() (int64, int64),
	normalFaces, weirdFaces []mancini.ClockFace) *Clock {

	if myName == "" {
		myName = mancini.DefaultName("clock")
	}

	c := &Clock{
		Pal:         pal,
		Fonts:       fonts,
		Size:        size,
		UTCFunc:     utcFunc,
		NormalFaces: normalFaces,
		WeirdFaces:  weirdFaces,
	}
	if len(normalFaces) > 0 {
		c.Face = normalFaces[0]
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

	c.Interactor.Initialize(c, lh)
	c.FullDamage()
	return c
}

// Draw implements mancini.NewDrawer.
func (c *Clock) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
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
		tcf := nanotime()
		c.Face.DrawFace(dc, c.Fonts, c.Pal, cx, cy, rad, hour, minute, second, millis)
		drawPerf.ClockFaceNs.Add(nanotime() - tcf)
	}
}

// --- Mouse interaction (Henry-Hudson protocol) ---

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

// Click implements mancini.Clickable — cycles through normal faces
// (Classic, Roman, Movado, Digit).
func (c *Clock) Click(ev *mancini.InputEvent) bool {
	if len(c.NormalFaces) == 0 {
		return false
	}
	c.weirdMode = false
	c.normalIdx = (c.normalIdx + 1) % len(c.NormalFaces)
	c.Face = c.NormalFaces[c.normalIdx]
	fmt.Printf("[clock] Click → face %d: %s\n", c.normalIdx, c.Face.FaceName())
	c.publishFaceName()
	c.FullDamage()
	return true
}

// DoubleClick implements mancini.DoubleClickable — cycles through
// weird faces (Metric, Polar).
func (c *Clock) DoubleClick(ev *mancini.InputEvent) bool {
	if len(c.WeirdFaces) == 0 {
		return false
	}
	if !c.weirdMode {
		c.weirdMode = true
		c.weirdIdx = 0
	} else {
		c.weirdIdx = (c.weirdIdx + 1) % len(c.WeirdFaces)
	}
	c.Face = c.WeirdFaces[c.weirdIdx]
	c.publishFaceName()
	c.FullDamage()
	return true
}

func (c *Clock) publishFaceName() {
	if c.FaceNameAttr != nil && c.Face != nil {
		c.FaceNameAttr.Set(c.Face.FaceName())
	}
	// Show face name label briefly.
	if c.FaceLabelLayout != nil {
		c.FaceLabelLayout.Visible.Set(true)
	}
	if c.FaceSpacerLayout != nil {
		c.FaceSpacerLayout.Visible.Set(false)
	}
}
