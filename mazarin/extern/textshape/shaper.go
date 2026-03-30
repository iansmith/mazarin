package textshape

// Shaper performs text shaping. The implementation is HarfBuzzShaper.
type Shaper interface {
	Shape(params ShapingParams) (ShapedRun, error)
}
