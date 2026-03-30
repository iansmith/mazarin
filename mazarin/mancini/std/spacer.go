package std

import (
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Spacer is a leaf interactor that draws nothing but occupies space in
// a [Column] or [Row] layout. Its Width and Height are set as value
// attributes at construction time.
//
// Spacer embeds [impl.Interactor] directly. It has no theme or visual
// rendering — its only purpose is to participate in the constraint
// network as a fixed-size gap.
type Spacer struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()
}

// NewSpacer creates a Spacer with the given fixed dimensions.
func NewSpacer(myName, parent string, w, h int64) *Spacer {
	if myName == "" {
		myName = mancini.DefaultName("spacer")
	}

	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(w)
	lh.Height.Set(h)

	s := &Spacer{}
	s.Interactor.Initialize(s, lh)
	return s
}

// Draw implements mancini.NewDrawer. Spacer draws nothing.
func (s *Spacer) Draw(self mancini.Interactor, x, y, w, h int64) {
	// intentionally empty
}
