package std

import (
	"image"
	"image/color"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// DynamicLabel is a label whose font size is driven by an attribute.
// When the font size attribute changes (e.g., from a
// [CornerRadialChooser]'s ValueAttr), the label rebuilds its text face
// and updates its height automatically.
//
// Create a DynamicLabel with [NewDynamicLabel] or [NewDynamicLabelBold],
// passing the URI of the font size source. The label creates an
// identity constraint binding to that URI so it reacts to changes
// through the constraint network.
type DynamicLabel struct {
	impl.ThemedInteractor

	Text        string
	Color       color.NRGBA
	Bold        bool
	Transparent bool
	textFace    mancini.LatinTextFace

	// padXAttr / padYAttr drive interior padding in pixels. Both default to
	// value-attrs with 0; call SetPaddingAttrs to bind them to a shared source.
	padXAttr *attr.Attribute[int64]
	padYAttr *attr.Attribute[int64]

	fontSizeAttr *attr.Attribute[int64]
	lastFontSize int64
	align        mancini.TextAlignmentParams
}

// NewDynamicLabel creates a DynamicLabel whose font size is bound to
// fontSizeURI via an identity constraint. The label is left-aligned
// and transparent (parent paints background).
func NewDynamicLabel(myName, parent string, theme mancini.Theme,
	text string, fontSizeURI string) *DynamicLabel {

	if myName == "" {
		myName = mancini.DefaultName("dynlabel")
	}

	// Bind font size to the source URI.
	prog := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", fontSizeURI)
	fsURI := mancini.LayoutURI(myName, mancini.DataTypeInt64,
		mancini.LayoutProp("fontSize"))
	fontSizeAttr := attr.ConstraintI64(fsURI, prog)

	// Read initial font size.
	fontSize := fontSizeAttr.Get()
	if fontSize <= 0 {
		fontSize = 14
	}

	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Height.Set(fontSize + 4)

	align := mancini.TextAlignmentParams{HAlign: mancini.HAlignLeft}
	fc := theme.Font(mancini.None, fontSize)

	padXURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("padX"))
	padYURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("padY"))

	dl := &DynamicLabel{
		Text:         text,
		Color:        theme.Palette().Text(),
		Transparent:  true,
		fontSizeAttr: fontSizeAttr,
		lastFontSize: fontSize,
		align:        align,
		textFace:     impl.NewLatinTextFace(fc, false, fontSize, align),
		padXAttr:     attr.ValueI64(padXURI, 0),
		padYAttr:     attr.ValueI64(padYURI, 0),
	}
	dl.ThemedInteractor.Initialize(dl, lh, theme)

	// Measure width if font resolver is available.
	if fc != nil && fc.LoadFace != nil {
		if face := fc.LoadFace(false, fontSize); face != nil {
			lh.Width.Set(mancini.MeasureTextWidth(face, text) + 8)
		}
	}

	return dl
}

// NewDynamicLabelBold creates a bold DynamicLabel.
func NewDynamicLabelBold(myName, parent string, theme mancini.Theme,
	text string, fontSizeURI string) *DynamicLabel {

	dl := NewDynamicLabel(myName, parent, theme, text, fontSizeURI)
	dl.Bold = true

	fontSize := dl.lastFontSize
	fc := theme.Font(mancini.Bold, fontSize)
	dl.textFace = impl.NewLatinTextFace(fc, true, fontSize, dl.align)

	return dl
}

// rebuildFace checks if the font size attribute has changed and
// rebuilds the text face if needed.
func (dl *DynamicLabel) rebuildFace() {
	fontSize := dl.fontSizeAttr.Get()
	if fontSize <= 0 {
		fontSize = 14
	}
	if fontSize == dl.lastFontSize {
		return
	}
	dl.lastFontSize = fontSize

	theme := dl.Theme()
	feature := mancini.None
	if dl.Bold {
		feature = mancini.Bold
	}
	fc := theme.Font(feature, fontSize)
	dl.textFace = impl.NewLatinTextFace(fc, dl.Bold, fontSize, dl.align)

	// Update height to match new font size.
	lh := dl.GetLayout()
	lh.Height.Set(fontSize + 4)
}

// SetPaddingAttrs binds the label's X and Y interior padding to the given
// attribute URIs via identity constraints, making padding constrainable from
// any source in the constraint network. Empty URI leaves that axis unchanged.
func (dl *DynamicLabel) SetPaddingAttrs(padXURI, padYURI string) {
	if padXURI != "" {
		attr.SwapToConstraint(dl.padXAttr,
			mancini.BindStrings(mancini.ProgIdentityI64, "_source_", padXURI))
	}
	if padYURI != "" {
		attr.SwapToConstraint(dl.padYAttr,
			mancini.BindStrings(mancini.ProgIdentityI64, "_source_", padYURI))
	}
}

// SetAlign replaces the label's text alignment.
func (dl *DynamicLabel) SetAlign(align mancini.TextAlignmentParams) {
	dl.align = align
	dl.rebuildFace()
}

// Draw implements mancini.NewDrawer. Checks for font size changes,
// then renders text.
func (dl *DynamicLabel) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	tl := nanotime()

	// Check for font size change from the constraint network.
	dl.rebuildFace()

	if !dl.Transparent {
		dl.ThemedInteractor.Draw(self, x, y, w, h, damage)
	}

	if dl.textFace == nil {
		drawPerf.LabelNs.Add(nanotime() - tl)
		drawPerf.LabelCount.Add(1)
		return
	}

	dc := self.DC()
	dc.SetColor(dl.Color)
	dl.textFace.SetText(dl.Text)
	px := dl.padXAttr.Get()
	py := dl.padYAttr.Get()
	dl.textFace.DrawFace(dc, float64(x+px), float64(y+py), float64(w-2*px), float64(h-2*py))
	dl.SnapshotDamage()
	dl.ClearDamage()
	drawPerf.LabelNs.Add(nanotime() - tl)
	drawPerf.LabelCount.Add(1)
}
