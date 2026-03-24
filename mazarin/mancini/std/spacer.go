package std

import (
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Spacer is a leaf interactor that draws nothing but occupies space.
// Its Width and Height are set as value handles at construction time.
type Spacer struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()
}

// NewSpacer creates a Spacer with the given fixed dimensions.
func NewSpacer(myName, parent string, w, h int64) *Spacer {
	if myName == "" {
		myName = mancini.DefaultName("spacer")
	}

	lh := mancini.NewLayoutHandles(myName, parent)
	lh.Width.Set(w)
	lh.Height.Set(h)

	s := &Spacer{}
	s.Interactor.Init(s, lh)
	return s
}

// Draw implements mancini.NewDrawer. Spacer draws nothing.
func (s *Spacer) Draw(self mancini.Interactor, x, y, w, h int64) {
	// intentionally empty
}
