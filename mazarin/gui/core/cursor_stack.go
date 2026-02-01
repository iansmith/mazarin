package core

// CursorStack manages a stack of Cursors with a fallback bottom,
// plus cursor position tracking in screen coordinates.
type CursorStack interface {
	Push(c Cursor) Cursor
	Pop() Cursor
	Bottom(c Cursor)
	Current() Cursor
	SetPosition(x, y int)
	Position() (int, int)
	Move(dx, dy int)
}
