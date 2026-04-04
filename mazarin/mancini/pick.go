package mancini

// pickDebug enables verbose pick tracing. Set via SetPickDebug; read
// from impl via PickDebugEnabled.
var pickDebug bool

// SetPickDebug enables or disables verbose pick tracing globally.
func SetPickDebug(v bool) { pickDebug = v }

// PickDebugEnabled returns true when verbose pick tracing is active.
// Called from [impl.Interactor.Pick] to avoid an import cycle.
func PickDebugEnabled() bool { return pickDebug }

// PickTree walks the mancini interactor tree via the [Picker] interface
// and returns all interactors whose bounds contain (x, y). Coordinates
// are in the app-local frame (the same frame as dispatch event coordinates).
//
// Each interactor's [Picker.Pick] method handles its own bounds check,
// child traversal, and coordinate conversion. Concrete types like
// [std.Scroller] override Pick to apply virtual-space transforms.
//
// Results are front-to-back: deepest children first, then ancestors.
func PickTree(x, y int64, root Interactor) []Interactor {
	l, ok := root.(Layouter)
	if !ok {
		return nil
	}
	lh := l.GetLayout()
	if lh == nil {
		return nil
	}
	// Convert from app-local to root-local coordinates.
	localX := x - lh.X.Get()
	localY := y - lh.Y.Get()

	if picker, ok := root.(Picker); ok {
		return picker.Pick(localX, localY)
	}
	return nil
}

// InsideBounds checks whether (x, y) in app-local coordinates falls
// within the interactor's layout bounds. Used by agents (e.g.,
// ClickDragAgent) to test whether the cursor is inside the target.
func InsideBounds(inter Interactor, x, y int64) bool {
	l, ok := inter.(Layouter)
	if !ok {
		return false
	}
	lh := l.GetLayout()
	if lh == nil {
		return false
	}
	ix, iy := lh.X.Get(), lh.Y.Get()
	iw, ih := lh.Width.Get(), lh.Height.Get()
	return x >= ix && x < ix+iw && y >= iy && y < iy+ih
}
