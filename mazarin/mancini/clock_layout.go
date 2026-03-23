package mancini

import "mazzy/mazarin/attr"

// InitLayout creates layout handles for the Clock.
func (c *Clock) InitLayout(parent string) {
	if c.Name == "" {
		c.Name = DefaultName("clock")
	}
	c.Layout = newLayoutHandles(c.Name, parent)

	// Publish intrinsic size so parent constraints can bootstrap.
	size := c.Size
	if size <= 0 {
		size = 50
	}
	c.Layout.Width.Set(int64(size))
	c.Layout.Height.Set(int64(size))

	// Publish current face name as a string attribute.
	faceName := ""
	if c.Face != nil {
		faceName = c.Face.FaceName()
	}
	faceURI := layoutURI(c.Name, "str", "FaceName")
	c.FaceNameHandle = attr.ValueStr(faceURI, faceName)
}
