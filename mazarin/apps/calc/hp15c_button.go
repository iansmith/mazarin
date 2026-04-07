// hp15c_button.go — HP 15C calculator button interactors.
//
// Three button types:
//   - HP15CShiftButton: the f and g shift keys with latching on/off state.
//   - HP15CFunctionButton: every other key. Reads f.on/g.on to pick label/color.
//   - HP15CButton: legacy wrapper (to be removed once migration is complete).
package main

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
)

// ShiftState controls which label the button displays.
type ShiftState int

const (
	ShiftNone ShiftState = iota // Normal: show primary label
	ShiftF                      // f-shift (gold/amber): show fLabel
	ShiftG                      // g-shift (blue): show gLabel
)

// --- HP15CShiftButton ---

// HP15CShiftButton is the f or g shift key. It has a latching on/off state.
// When on, it draws with a bright color; when off, a muted version.
type HP15CShiftButton struct {
	std.Button

	on       bool        // latching state
	onColor  color.NRGBA // bright color when on
	offColor color.NRGBA // muted color when off
	label    string
	fontID   int32
	other    *HP15CShiftButton // the other shift button (for mutual exclusion)
}

// NewHP15CShiftButton creates a shift button (f or g).
func NewHP15CShiftButton(myName, parent string, theme mancini.Theme,
	width, height int64, label string, fontID int32,
	onColor, offColor, textColor color.NRGBA) *HP15CShiftButton {

	b := &HP15CShiftButton{
		on:       false,
		onColor:  onColor,
		offColor: offColor,
		label:    label,
		fontID:   fontID,
	}

	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(width)
	lh.Height.Set(height)

	b.Button = std.Button{
		Depth:  mancini.Raised,
		Radius: 4.0,
	}
	b.Button.ThemedInteractor.Initialize(b, lh, theme)
	return b
}

// SetOther links the two shift buttons for mutual exclusion.
func (b *HP15CShiftButton) SetOther(other *HP15CShiftButton) {
	b.other = other
}

// IsOn returns true if this shift button is currently latched on.
func (b *HP15CShiftButton) IsOn() bool {
	return b.on
}

// Press handles a click on the shift button.
// If the other shift is on, turn it off first. Then toggle self.
func (b *HP15CShiftButton) Press() {
	if b.other != nil && b.other.on {
		b.other.TurnOff()
	}
	b.on = !b.on
	b.FullDamage()
}

// TurnOff clears the shift state. If already off, does nothing.
// If transitioning from on to off, forces a full redraw.
func (b *HP15CShiftButton) TurnOff() {
	if !b.on {
		return
	}
	b.on = false
	b.FullDamage()
}

// Draw renders the shift button with bright or muted fill.
func (b *HP15CShiftButton) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	fillColor := b.offColor
	if b.on {
		fillColor = b.onColor
	}

	pal := b.Theme().Palette()
	b.Theme().Style().DrawBox(pal, dc, b.Depth, mancini.LightWeight,
		fx, fy, fx+fw, fy+fh, b.Radius, fillColor)

	// Label always drawn in white.
	dc.SetColor(colWhite)
	dc.DrawStringAnchored(b.label, b.fontID, fx+fw/2, fy+fh/2, 0.5, 0.5)
}

// --- functionHandler ---

// functionHandler is the interface for button-specific logic.
// which: 0=normal, 1=f-shift, 2=g-shift.
type functionHandler interface {
	Function(which int)
}

// --- HP15CFunctionButton ---

// HP15CFunctionButton is any HP 15C key except f and g.
// It reads the shift buttons' state to pick which label/color to display,
// and calls ClearShiftButtons() after executing its function.
type HP15CFunctionButton struct {
	std.Button

	primaryLabel string
	fLabel       string
	gLabel       string

	primaryFontID int32
	shiftFontID   int32

	normalColor color.NRGBA
	fColor      color.NRGBA
	gColor      color.NRGBA
	fillColor   color.NRGBA

	fBtn    *HP15CShiftButton // reference to f shift button
	gBtn    *HP15CShiftButton // reference to g shift button
	handler functionHandler   // concrete function logic (via backpointer)
}

// NewHP15CFunctionButton creates a function button.
// handler may be nil for keys with no shifted functions (uses default no-op).
func NewHP15CFunctionButton(myName, parent string, theme mancini.Theme,
	width, height int64,
	primaryLabel, fLabel, gLabel string,
	primaryFontID, shiftFontID int32,
	fillColor, normalColor, fColor, gColor color.NRGBA,
	fBtn, gBtn *HP15CShiftButton,
	handler functionHandler) *HP15CFunctionButton {

	b := &HP15CFunctionButton{
		primaryLabel:  primaryLabel,
		fLabel:        fLabel,
		gLabel:        gLabel,
		primaryFontID: primaryFontID,
		shiftFontID:   shiftFontID,
		normalColor:   normalColor,
		fColor:        fColor,
		gColor:        gColor,
		fillColor:     fillColor,
		fBtn:          fBtn,
		gBtn:          gBtn,
		handler:       handler,
	}

	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(width)
	lh.Height.Set(height)

	b.Button = std.Button{
		Depth:  mancini.Raised,
		Radius: 4.0,
	}
	b.Button.ThemedInteractor.Initialize(b, lh, theme)
	return b
}

// ShiftWhich returns the current shift state: 0=normal, 1=f, 2=g.
func (b *HP15CFunctionButton) ShiftWhich() int {
	if b.fBtn != nil && b.fBtn.IsOn() {
		return 1
	}
	if b.gBtn != nil && b.gBtn.IsOn() {
		return 2
	}
	return 0
}

// ClearShiftButtons resets both shift buttons to off.
func (b *HP15CFunctionButton) ClearShiftButtons() {
	if b.fBtn != nil {
		b.fBtn.TurnOff()
	}
	if b.gBtn != nil {
		b.gBtn.TurnOff()
	}
}

// Press handles a click on the function button.
// Reads shift state, dispatches to the handler, then clears shift.
func (b *HP15CFunctionButton) Press() {
	which := b.ShiftWhich()
	if b.handler != nil {
		b.handler.Function(which)
	}
	b.ClearShiftButtons()
}

// activeLabel returns the label and color for the current shift state.
func (b *HP15CFunctionButton) activeLabel() (string, int32, color.NRGBA) {
	if b.fBtn != nil && b.fBtn.IsOn() {
		if b.fLabel != "" {
			return b.fLabel, b.shiftFontID, b.fColor
		}
	}
	if b.gBtn != nil && b.gBtn.IsOn() {
		if b.gLabel != "" {
			return b.gLabel, b.shiftFontID, b.gColor
		}
	}
	return b.primaryLabel, b.primaryFontID, b.normalColor
}

// Draw renders the function button with the appropriate label for shift state.
func (b *HP15CFunctionButton) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	pal := b.Theme().Palette()
	b.Theme().Style().DrawBox(pal, dc, b.Depth, mancini.LightWeight,
		fx, fy, fx+fw, fy+fh, b.Radius, b.fillColor)

	text, fontID, col := b.activeLabel()
	if text == "" {
		return
	}
	dc.SetColor(col)
	dc.DrawStringAnchored(text, fontID, fx+fw/2, fy+fh/2, 0.5, 0.5)
}

// --- Legacy HP15CButton (kept for transition) ---

// HP15CFace implements mancini.Face for the HP 15C button. It draws
// the currently active label (primary, f, or g) centered on the button
// face, in the appropriate color for the current shift state.
type HP15CFace struct {
	PrimaryLabel string
	FLabel       string
	GLabel       string
	Shift        *ShiftState // points to shared shift state from engine

	// Font IDs (set once after fonts are opened).
	PrimaryFontID int32
	ShiftFontID   int32 // used for f/g labels when active on the face

	// Colors.
	NormalColor color.NRGBA // primary label color (normal mode)
	FColor      color.NRGBA // amber/gold (f-shift mode)
	GColor      color.NRGBA // blue (g-shift mode)
}

// DrawFace implements mancini.Face. Draws the active label centered
// in the button rectangle.
func (f *HP15CFace) DrawFace(dc mancini.DrawContext, x, y, w, h float64) {
	var text string
	var fontID int32
	var col color.NRGBA

	switch *f.Shift {
	case ShiftF:
		if f.FLabel != "" {
			text = f.FLabel
			fontID = f.ShiftFontID
			col = f.FColor
		} else {
			text = f.PrimaryLabel
			fontID = f.PrimaryFontID
			col = f.NormalColor
		}
	case ShiftG:
		if f.GLabel != "" {
			text = f.GLabel
			fontID = f.ShiftFontID
			col = f.GColor
		} else {
			text = f.PrimaryLabel
			fontID = f.PrimaryFontID
			col = f.NormalColor
		}
	default:
		text = f.PrimaryLabel
		fontID = f.PrimaryFontID
		col = f.NormalColor
	}

	if text == "" {
		return
	}
	dc.SetColor(col)
	dc.DrawStringAnchored(text, fontID, x+w/2, y+h/2, 0.5, 0.5)
}

// HP15CButton is an HP 15C calculator key. It embeds std.Button and
// overrides Draw to render the neumorphic box with face content.
type HP15CButton struct {
	std.Button

	Shift     *ShiftState
	IsEnter   bool        // true for the tall ENTER key
	FillColor color.NRGBA // button face fill (overrides pal.Surface())
}

// NewHP15CButton creates an HP 15C button wired to the constraint system.
func NewHP15CButton(myName, parent string, theme mancini.Theme,
	width, height int64, key *hp15cKey, shift *ShiftState,
	primaryFontID, shiftFontID int32,
	fillColor, normalColor, fColor, gColor color.NRGBA) *HP15CButton {

	b := &HP15CButton{
		Shift:     shift,
		IsEnter:   key.rowSpan == 2,
		FillColor: fillColor,
	}

	// Build layout.
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(width)
	lh.Height.Set(height)

	// Initialize embedded Button.
	b.Button = std.Button{
		Depth:  mancini.Raised,
		Radius: 4.0,
		Face: &HP15CFace{
			PrimaryLabel:  key.label,
			FLabel:        key.fLabel,
			GLabel:        key.gLabel,
			Shift:         shift,
			PrimaryFontID: primaryFontID,
			ShiftFontID:   shiftFontID,
			NormalColor:   normalColor,
			FColor:        fColor,
			GColor:        gColor,
		},
	}
	b.Button.ThemedInteractor.Initialize(b, lh, theme)
	return b
}

// Draw renders the HP 15C button: neumorphic box with face content.
func (b *HP15CButton) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	// Neumorphic button box with custom fill color.
	pal := b.Theme().Palette()
	b.Theme().Style().DrawBox(pal, dc, b.Depth, mancini.LightWeight,
		fx, fy, fx+fw, fy+fh, b.Radius, b.FillColor)

	// Face content (label text).
	if b.Face != nil {
		b.Face.DrawFace(dc, fx, fy, fw, fh)
	}
}
