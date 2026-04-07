// hp15c_button.go — HP 15C calculator button interactor.
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
