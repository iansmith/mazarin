// Package main implements versai, the primary text editor for mazzy.
//
// A versai.Unit is the core editing component: a MarginParent containing
// a UnitContainer with a VersaiEditor (top, 2/3 width, 3 lines) and a
// BoxesAndGlueInteractor (below, offset 20% from left, 400px tall).
package main

import (
	"fmt"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	"mazzy/shared/dlist"
	mfont "mazzy/shared/font"
)

// UnitEntry pairs a Unit with a dlist node handle for O(1) removal.
type UnitEntry struct {
	Unit *Unit
	Node *dlist.Node[*UnitEntry] // set after PushBack
}

// UnitList is a doubly-linked list of UnitEntry pointers, supporting O(1)
// insertion and removal. Used by the main loop to track live units.
type UnitList = dlist.List[*UnitEntry]

// NewUnitList creates an empty unit list.
func NewUnitList() *UnitList {
	return dlist.New[*UnitEntry]()
}

// Unit is the core editing component of versai. It embeds MarginParent
// to provide margins, border, and label around a UnitContainer that
// arranges its children vertically:
//
//	[VersaiEditor]                — (0,0), 2/3 width, 3 text lines
//	[BoxesAndGlueInteractor]     — (20% width, VE.bottom+2), 400px
//
// A ThrobberRadialChooser is pinned to the lower-right corner of the BAG.
type Unit struct {
	*std.MarginParent // Unit IS a MarginParent

	Container *UnitContainer
	VERow     *std.Row
	VELabel   *std.Label
	Editor    *VersaiEditor
	BAG       *std.BoxesAndGlueInteractor
	Throbber  *std.ThrobberRadialChooser

	// Focused is a value attribute indicating whether this Unit currently
	// has in-app keyboard focus. Exactly one Unit should be true at a time.
	Focused *attr.Attribute[bool]

	// BagTextLen is the eager constraint that mirrors editor text length
	// into the BAG. Stored here so CleanConstraints can delete it.
	BagTextLen *attr.Attribute[int64]

	// In-app focus: set by the application after construction.
	KeyFocusAgent  *mancini.KeyAgent
	KeyFocusTarget mancini.Interactor
	FocusPeers     []*Unit
}

// NewUnit creates a versai Unit wired to the constraint system.
// myName may be empty, in which case a unique name is generated
// automatically. parentName is the constraint-system name of the
// parent interactor. label is the text drawn in the top margin border.
// rachelSID is the shepherd ID for rachel (overlay IPC).
// mainDC may be nil initially and set later via Unit.Throbber.SetMainDC.
func NewUnit(myName, parentName string, theme mancini.Theme,
	pal mancini.Palette, rachelSID int, mainDC mancini.DrawContext,
	label string) *Unit {

	if myName == "" {
		myName = mancini.DefaultName("unit")
	}
	contName := myName + "_cont"
	editorName := myName + "_editor"
	bagName := myName + "_bag"

	// Create the MarginParent that Unit embeds.
	mp := std.NewMarginParent(myName, parentName,
		16, 6, 6, 6, // top, right, bottom, left
		1,            // border width
		label,
		theme, pal)

	u := &Unit{MarginParent: mp}

	// Re-register so dispatch sees Unit, not MarginParent.
	u.ReInitializeOwner(u)

	// Focused attribute — initially false.
	u.Focused = attr.ValueBool(
		mancini.LayoutURI(myName, mancini.DataTypeBool, "Focused"), false)

	// UnitContainer: parent is this Unit.
	const veFontSize = 20
	const veLines = 1
	rowName := myName + "_verow"
	labelName := myName + "_velbl"
	u.Container = NewUnitContainer(contName, myName, theme, pal, veFontSize, veLines)

	// When the container computes its final height, update the
	// MarginParent's height = containerHeight + top + bottom margins.
	u.Container.OnHeightChanged = func(containerH int64) {
		u.GetLayout().Height.Set(containerH + mp.Top + mp.Bottom)
	}

	// VE row: label "[1]" + VE, top-aligned.
	u.VERow = std.NewRow(rowName, contName, pal, 0, mancini.AxisMinimum, 0)
	u.VELabel = std.NewLabelNamed(labelName, rowName, theme, "[1]", veFontSize)
	u.VELabel.Transparent = true

	// VE: parent is the VE row.
	mte := std.NewMultiLineTextNamed(
		editorName, rowName, theme,
		"", veFontSize, 0, 100) // height=100 placeholder, recomputed at first draw
	tint := pal.SurfaceTint()
	mte.BgColor = &tint
	u.Editor = NewVersaiEditor(editorName, mte)

	// Wire VE reference so UnitContainer can compute VE height from font metrics.
	u.Container.VE = u.Editor

	// BAG: parent is the container.
	bagLH := mancini.NewLayoutAttributesBase(bagName, contName)
	bagLH.Width = attr.ValueI64(
		mancini.LayoutURI(bagName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	bagLH.Height = attr.ValueI64(
		mancini.LayoutURI(bagName, mancini.DataTypeInt64, mancini.LayoutHeight), 200)
	bagLH.InitBounds(bagName)
	u.BAG = std.NewBoxesAndGlueInteractorWithLayout(bagName, bagLH, theme, 10)
	u.BAG.SetFontFamily(mfont.LatinModernRoman)

	// Text length constraint: eager, equal to the editor's text length.
	u.BagTextLen = attr.ConstraintI64(
		mancini.LayoutURI(bagName, mancini.DataTypeInt64, mancini.LayoutTextLen),
		mancini.EqualI64(u.Editor.TextLenURI()))
	// BagTextLen is NOT eager — damage propagates up to AppWindow's
	// DamageRect which is the single eager attribute driving redraws.

	// Wire the shared page to the BAG.
	u.BAG.SetTextSource(u.Editor.TextPageAddr(), u.BagTextLen)

	// When the VE updates the shared page, damage the BAG.
	u.Editor.OnPageUpdate = func() {
		u.BAG.FullDamage()
	}

	// ThrobberRadialChooser: parent is the container (drawn after BAG).
	throbName := myName + "_throb"
	u.Throbber = std.NewThrobberRadialChooser(
		throbName, contName, theme, pal,
		rachelSID, mainDC,
		[]string{"Small", "Medium", "Large"}, 0,
	)

	// Wire radial chooser selections to the BAG font size.
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

// SetFocusToSelf claims in-app keyboard focus for this Unit.
// It walks FocusPeers, setting each peer's Focused to false except
// this one (true), and directs the KeyAgent to deliver keyboard events
// to the editor.
func (u *Unit) SetFocusToSelf() {
	if u.KeyFocusAgent == nil || u.KeyFocusTarget == nil {
		return
	}
	for _, peer := range u.FocusPeers {
		isSelf := peer == u
		if peer.Focused != nil {
			peer.Focused.Set(isSelf)
		}
		if peer.KeyFocusTarget != nil {
			if f, ok := peer.KeyFocusTarget.(interface{ SetFocused(bool) }); ok {
				f.SetFocused(isSelf)
			}
		}
	}
	u.KeyFocusAgent.SetFocus(u.KeyFocusTarget)
	fmt.Printf("[unit] SetFocusToSelf\n")
}

// Click implements mancini.Clickable. Catches clicks that fall through
// from non-interactive children and claims focus.
func (u *Unit) Click(ev *mancini.InputEvent) bool {
	u.SetFocusToSelf()
	return true
}

// DeleteUnitEntry removes a UnitEntry from the unit list, detaches the
// Unit from its parent container, and tears down all constraint
// attributes. After this call the entry and its Unit should not be used.
func DeleteUnitEntry(entry *UnitEntry, units *UnitList, col *ColumnEdgeToEdge) {
	// 1. Remove from the linked list.
	units.Remove(entry.Node)

	// 2. Detach the Unit from the scroller.
	col.SV.Scroller.DeleteChild(entry.Unit)

	// 3. Tear down all constraints.
	entry.Unit.CleanConstraints()

	// 4. Re-layout remaining children.
	col.LayoutChildren()
}

// CleanConstraints tears down all constraint attributes owned by this
// Unit in dependency order. After this call the Unit should not be used.
func (u *Unit) CleanConstraints() {
	// Phase 0: Unit-level attributes.
	attr.Delete(u.Focused)
	u.Focused = nil

	// Phase 1: Cross-child constraints.
	attr.Delete(u.BagTextLen)
	u.BagTextLen = nil

	// Phase 2: Throbber.
	u.Throbber.GetLayout().CleanConstraints()

	// Phase 3: Leaf children.
	u.BAG.GetLayout().CleanConstraints()
	u.Editor.GetLayout().CleanConstraints()
	u.VELabel.GetLayout().CleanConstraints()

	// Phase 3.5: VE Row.
	u.VERow.GetLayout().CleanConstraints()

	// Phase 4: UnitContainer.
	u.Container.GetLayout().CleanConstraints()

	// Phase 5: MarginParent (self).
	u.MarginParent.GetLayout().CleanConstraints()
}
