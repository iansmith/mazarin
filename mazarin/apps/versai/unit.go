// Package main implements versai, the primary text editor for mazzy.
//
// A versai.Unit is the core editing component: a VSplitter containing
// a MultiLineText (left, 48%), a VerticalLine separator (center, 4%),
// and a BoxesAndGlueInteractor (right, remainder).
package main

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	mfont "mazzy/shared/font"
	"mazzy/mazarin/mancini/std"
)

// Unit is the core editing component of versai. It is a VSplitter
// that arranges its three children horizontally:
//
//	[MultiLineText 48%]  [VerticalLine 4%]  [BoxesAndGlueInteractor 48%]
//
// The MultiLineText sets its own height to 400px. The
// BoxesAndGlueInteractor's height is constrained to match the
// MultiLineText's height. The VerticalLine's height is constrained
// to the VSplitter's height.
type Unit struct {
	Split     *std.VSplitter
	Editor    *VersaiEditor
	Separator *std.VerticalLine
	BAG       *std.BoxesAndGlueInteractor
}

// NewUnit creates a versai Unit wired to the constraint system.
// parentName is the constraint-system name of the parent interactor.
func NewUnit(parentName string, theme mancini.Theme,
	pal mancini.Palette) *Unit {

	u := &Unit{}

	// VSplitter: three children at 48/4/48.
	u.Split = std.NewVSplitter("versai_split", parentName, pal)

	// Left child: MultiLineText wrapped in VersaiEditor.
	mte := std.NewMultiLineTextNamed(
		"versai_editor", "versai_split", theme,
		"", 20, 0, 400)
	u.Editor = NewVersaiEditor("versai_editor", mte)

	// Editor's height URI — used to constrain BAG height.
	editorHeightURI := u.Editor.GetLayout().Height.URI()

	// Center child: VerticalLine with height constrained to VSplitter height.
	u.Separator = std.NewVerticalLineConstrained(
		"versai_separator", "versai_split", theme,
		u.Split.HeightURI())

	// Right child: BoxesAndGlueInteractor with height constrained to editor.
	bagLH := mancini.NewLayoutAttributesBase("versai_bag", "versai_split")
	bagLH.Width = attr.ValueI64(
		mancini.LayoutURI("versai_bag", mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	bagLH.Height = attr.ConstraintI64(
		mancini.LayoutURI("versai_bag", mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(editorHeightURI))
	bagLH.InitBounds("versai_bag")
	u.BAG = std.NewBoxesAndGlueInteractorWithLayout("versai_bag", bagLH, theme, 10)
	u.BAG.SetFontFamily(mfont.LatinModernRoman)

	// Text length constraint: eager, equal to the editor's text length.
	// When the editor updates its shared page and sets textLen, this
	// constraint fires dirtyCh, causing a redraw that reads the new text.
	bagTextLenAttr := attr.ConstraintI64(
		mancini.LayoutURI("versai_bag", mancini.DataTypeInt64, mancini.LayoutTextLen),
		mancini.EqualI64(u.Editor.TextLenURI()))
	bagTextLenAttr.SetEager(true)

	// Wire the shared page to the BAG.
	u.BAG.SetTextSource(u.Editor.TextPageAddr(), bagTextLenAttr)

	// When the VE updates the shared page, damage the BAG so the
	// vsplitter includes it in the next redraw.
	u.Editor.OnPageUpdate = func() {
		u.BAG.FullDamage()
	}

	return u
}
