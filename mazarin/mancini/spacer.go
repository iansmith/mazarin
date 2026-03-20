package mancini


// Spacer is a leaf interactor that occupies space but draws nothing.
// It participates in the constraint system so that container constraint
// programs (Column height, Row width, etc.) can see its dimensions.
type Spacer struct {
	Name       string
	PreferredW float64
	PreferredH float64
	Layout     *LayoutHandles
}

func (s *Spacer) GetLayout() *LayoutHandles    { return s.Layout }
func (s *Spacer) PreferredWidth() float64       { return s.PreferredW }
func (s *Spacer) PreferredHeight() float64      { return s.PreferredH }

func (s *Spacer) Draw(_ DrawContext, x, y, w, h float64) {
	if !isVisible(s) {
		return
	}
	publishLayout(s.Layout, x, y, w, h)
}
