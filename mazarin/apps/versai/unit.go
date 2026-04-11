// Package main implements versai, the primary text editor for mazzy.
//
// A versai.Unit is the core editing component: a VSplitter containing
// a MultiLineText (left, 48%), a VerticalLine separator (center, 4%),
// and a BoxesAndGlueInteractor (right, remainder).
package main

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	"mazzy/shared/dlist"
	mfont "mazzy/shared/font"
)

// UnitEntry pairs a Unit with its MarginParent wrapper and a dlist
// node handle for O(1) removal.
type UnitEntry struct {
	Unit *Unit
	MP   *std.MarginParent
	Node *dlist.Node[*UnitEntry] // set after PushBack
}

// UnitList is a doubly-linked list of UnitEntry pointers, supporting O(1)
// insertion and removal. Used by the main loop to track live units.
type UnitList = dlist.List[*UnitEntry]

// NewUnitList creates an empty unit list.
func NewUnitList() *UnitList {
	return dlist.New[*UnitEntry]()
}

// Unit is the core editing component of versai. It is a VSplitter
// that arranges its three children horizontally:
//
//	[MultiLineText 48%]  [VerticalLine 4%]  [BoxesAndGlueInteractor 48%]
//
// The MultiLineText sets its own height to 400px. The
// BoxesAndGlueInteractor's height is constrained to match the
// MultiLineText's height. The VerticalLine's height is constrained
// to the VSplitter's height.
//
// A ThrobberRadialChooser is pinned to the lower-right corner of the
// VSplitter, providing a "Small"/"Large" mode chooser overlay.
type Unit struct {
	Split     *std.VSplitter
	Editor    *VersaiEditor
	Separator *std.VerticalLine
	BAG       *std.BoxesAndGlueInteractor
	Throbber  *std.ThrobberRadialChooser

	// Focused is a value attribute indicating whether this Unit currently
	// has in-app keyboard focus. Exactly one Unit should be true at a time.
	Focused *attr.Attribute[bool]

	// BagTextLen is the eager constraint that mirrors editor text length
	// into the BAG. Stored here so CleanConstraints can delete it.
	BagTextLen *attr.Attribute[int64]
}

// NewUnit creates a versai Unit wired to the constraint system.
// myName may be empty, in which case a unique name is generated
// automatically. parentName is the constraint-system name of the
// parent interactor. rachelSID is the shepherd ID for rachel (overlay IPC).
// mainDC may be nil initially and set later via Unit.Throbber.SetMainDC.
func NewUnit(myName, parentName string, theme mancini.Theme,
	pal mancini.Palette, rachelSID int, mainDC mancini.DrawContext) *Unit {

	if myName == "" {
		myName = mancini.DefaultName("unit")
	}
	splitName := myName + "_split"
	editorName := myName + "_editor"
	sepName := myName + "_sep"
	bagName := myName + "_bag"

	u := &Unit{}

	// Focused attribute — initially false.
	u.Focused = attr.ValueBool(
		mancini.LayoutURI(myName, mancini.DataTypeBool, "Focused"), false)

	// VSplitter: three children at 48/4/48.
	u.Split = std.NewVSplitter(splitName, parentName, pal)

	// Left child: MultiLineText wrapped in VersaiEditor.
	mte := std.NewMultiLineTextNamed(
		editorName, splitName, theme,
		"", 20, 0, 400)
	u.Editor = NewVersaiEditor(editorName, mte)

	// Editor's height URI — used to constrain BAG height.
	editorHeightURI := u.Editor.GetLayout().Height.URI()

	// Center child: VerticalLine with height constrained to VSplitter height.
	u.Separator = std.NewVerticalLineConstrained(
		sepName, splitName, theme,
		u.Split.HeightURI())

	// Right child: BoxesAndGlueInteractor with height constrained to editor.
	bagLH := mancini.NewLayoutAttributesBase(bagName, splitName)
	bagLH.Width = attr.ValueI64(
		mancini.LayoutURI(bagName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	bagLH.Height = attr.ConstraintI64(
		mancini.LayoutURI(bagName, mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(editorHeightURI))
	bagLH.InitBounds(bagName)
	u.BAG = std.NewBoxesAndGlueInteractorWithLayout(bagName, bagLH, theme, 10)
	u.BAG.SetFontFamily(mfont.LatinModernRoman)

	// Text length constraint: eager, equal to the editor's text length.
	// When the editor updates its shared page and sets textLen, this
	// constraint fires dirtyCh, causing a redraw that reads the new text.
	u.BagTextLen = attr.ConstraintI64(
		mancini.LayoutURI(bagName, mancini.DataTypeInt64, mancini.LayoutTextLen),
		mancini.EqualI64(u.Editor.TextLenURI()))
	u.BagTextLen.SetEager(true)

	// Wire the shared page to the BAG.
	u.BAG.SetTextSource(u.Editor.TextPageAddr(), u.BagTextLen)

	// When the VE updates the shared page, damage the BAG so the
	// vsplitter includes it in the next redraw.
	u.Editor.OnPageUpdate = func() {
		u.BAG.FullDamage()
	}

	// ThrobberRadialChooser pinned to lower-right corner of the VSplitter.
	throbName := myName + "_throb"
	u.Throbber = std.NewThrobberRadialChooser(
		throbName, splitName, theme, pal,
		rachelSID, mainDC,
		[]string{"Small", "Medium", "Large"}, 0,
	)
	// Throbber position is set by VSplitter.Draw to the lower-left
	// of the BAG pane. X/Y are plain value attributes, updated each frame.

	// Wire radial chooser selections to the BAG font size.
	// "Small" (index 0) = 10pt, "Medium" (index 1) = 16pt, "Large" (index 2) = 20pt.
	u.Throbber.OnSelect = func(seg int) {
		switch seg {
		case 0:
			u.BAG.SetFontSize(10)
		case 1:
			u.BAG.SetFontSize(16)
		case 2:
			u.BAG.SetFontSize(20)
		}
	}

	return u
}

// DeleteUnitEntry removes a UnitEntry from the unit list, detaches the
// MarginParent from its parent container, and tears down all constraint
// attributes for both the Unit and MarginParent. After this call the
// entry and its Unit should not be used.
func DeleteUnitEntry(entry *UnitEntry, units *UnitList, col *ColumnEdgeToEdge) {
	// 1. Remove from the linked list.
	units.Remove(entry.Node)

	// 2. Detach the MarginParent from the scroller.
	col.SV.Scroller.DeleteChild(entry.MP)

	// 3. Tear down the Unit's constraint subgraph.
	entry.Unit.CleanConstraints()

	// 4. Tear down the MarginParent's constraint attrs.
	entry.MP.GetLayout().CleanConstraints()

	// 5. Re-layout remaining children.
	col.LayoutChildren()
}

// CleanConstraints tears down all constraint attributes owned by this
// Unit in dependency order. After this call the Unit should not be used.
func (u *Unit) CleanConstraints() {
	// Phase 0: Unit-level attributes.
	attr.Delete(u.Focused)
	u.Focused = nil

	// Phase 1: Cross-child constraints that read from sibling attrs.
	// BagTextLen depends on Editor.TextLen.
	attr.Delete(u.BagTextLen)
	u.BagTextLen = nil

	// Phase 2: Throbber.
	u.Throbber.GetLayout().CleanConstraints()

	// Phase 3: Each child's layout, leaves first.
	// BAG and Separator have constraint Heights that depend on
	// Editor.Height and Split.Height respectively — their
	// LayoutAttributes.CleanConstraints handles internal ordering.
	u.BAG.GetLayout().CleanConstraints()
	u.Separator.GetLayout().CleanConstraints()
	u.Editor.GetLayout().CleanConstraints()

	// Phase 4: VSplitter (parent of the above children).
	u.Split.GetLayout().CleanConstraints()
}
