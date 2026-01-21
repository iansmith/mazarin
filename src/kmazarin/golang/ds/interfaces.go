package ds

// Ider - Interface for types that have an ID
type Ider interface {
	Id() int32
}

// Sizer - Interface for types that report their size/count
type Sizer interface {
	Size() int
}

// Finder - Interface for types that can find elements by ID
// ⚠️  WARNING: Returns pointer to LIVE internal data - modify with care!
type Finder[T any] interface {
	FindById(id int32) *T
}
