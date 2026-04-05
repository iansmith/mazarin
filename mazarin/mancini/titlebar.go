package mancini

// WindowState describes the focus state of a window.
type WindowState int

const (
	Active   WindowState = iota // Window has focus.
	Inactive                    // Window does not have focus.
)

func (s WindowState) String() string {
	switch s {
	case Active:
		return "Active"
	case Inactive:
		return "Inactive"
	}
	return "?"
}

// WindowType describes the kind of window a title bar belongs to.
type WindowType int

const (
	AppWindowType         WindowType = iota // Root application window.
	FreeStandingWindowType                   // Non-root floating panel.
	GadgetType                               // Small utility or accessory window.
)

func (t WindowType) String() string {
	switch t {
	case AppWindowType:
		return "AppWindow"
	case FreeStandingWindowType:
		return "FreeStandingWindow"
	case GadgetType:
		return "Gadget"
	}
	return "?"
}

// TitleBar draws a window title bar into a DrawContext.
// Implementations vary the appearance based on the window's focus
// state and type. The title bar is drawn into the rectangle
// (x, y, w, h) using the provided DrawContext.
type TitleBar interface {
	DrawTitleBar(dc DrawContext, title string, state WindowState, wtype WindowType, x, y, w, h float64)
}
