//go:build linux

package mancini

// InitLayout creates layout handles for the Clock.
func (c *Clock) InitLayout(parent string) {
	if c.Name == "" {
		c.Name = defaultName("clock")
	}
	c.Layout = newLayoutHandles(c.Name, parent)
}
