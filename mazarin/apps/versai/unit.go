// Package main implements versai, the primary text editor for mazzy.
//
// A versai.Unit is the core editing component: a VSplitter containing
// a MultiLineText (left, 48%), a VerticalLine separator (center, 4%),
// and a WebInteractor (right, remainder).
package main

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
)

// Unit is the core editing component of versai. It is a VSplitter
// that arranges its three children horizontally:
//
//	[MultiLineText 48%]  [VerticalLine 4%]  [WebInteractor 48%]
//
// The MultiLineText sets its own height to 400px. The WebInteractor's
// height is constrained to match the MultiLineText's height. The
// VerticalLine's height is constrained to the VSplitter's height.
type Unit struct {
	Split     *std.VSplitter
	Editor    *std.MultiLineText
	Separator *std.VerticalLine
	Web       *std.WebInteractor
}

// NewUnit creates a versai Unit wired to the constraint system.
// parentName is the constraint-system name of the parent interactor.
func NewUnit(parentName string, theme mancini.Theme,
	pal mancini.Palette, engine std.WebRenderEngine) *Unit {

	u := &Unit{}

	// VSplitter: three children at 48/4/48.
	u.Split = std.NewVSplitter("versai_split", parentName, pal)

	// Left child: MultiLineText with height = 400.
	u.Editor = std.NewMultiLineTextNamed(
		"versai_editor", "versai_split", theme,
		"", 20, 0, 400)

	// Editor's height URI — used to constrain WebInteractor height.
	editorHeightURI := u.Editor.GetLayout().Height.URI()

	// Center child: VerticalLine with height constrained to VSplitter height.
	u.Separator = std.NewVerticalLineConstrained(
		"versai_separator", "versai_split", theme,
		u.Split.HeightURI())

	// Right child: WebInteractor with height constrained to editor height.
	webLH := mancini.NewLayoutAttributesBase("versai_web", "versai_split")
	webLH.Width = attr.ValueI64(
		mancini.LayoutURI("versai_web", mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	webLH.Height = attr.ConstraintI64(
		mancini.LayoutURI("versai_web", mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(editorHeightURI))
	webLH.InitBounds("versai_web")
	u.Web = std.NewWebInteractorWithLayout("versai_web", webLH, engine)

	return u
}
