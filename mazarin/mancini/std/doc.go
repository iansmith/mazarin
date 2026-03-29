// Package std provides the standard library of concrete interactor types
// for the Mancini UI toolkit. Every interactor here embeds one of the base
// types from [mazzy/mazarin/mancini/impl] and satisfies [mancini.NewDrawer].
//
// # Interactor Catalog
//
// Leaf interactors (no children, embed [impl.ThemedInteractor]):
//
//   - [Button] — neumorphic push button with configurable depth and face content
//   - [Checkbox] — toggle box (Inset ↔ Raised with checkmark)
//   - [CheckboxWithLabel] — [Checkbox] paired with a text [Label]
//   - [Label] — single-line static or dynamic text
//   - [ConsoleLabel] — left-aligned monospaced text with per-instance background
//   - [SingleLineText] — editable text input field with cursor and auto-scroll
//   - [Scrollbar] — vertical or horizontal scrollbar with thumb and optional arrows
//   - [NOfMChooser] — strip-based N-of-M multi-select with grooves and selection
//   - [RadialNOfMChooser] — arc-shaped annular segment N-of-M chooser
//   - [RadialMenu] — full-circle annular menu with selectable segments
//
// Leaf interactors (no children, embed [impl.Interactor] directly):
//
//   - [Clock] — analog clock face with configurable styles and click cycling
//   - [Spacer] — invisible fixed-size placeholder
//
// Container interactors (embed [impl.Interactor] + [impl.Parent]):
//
//   - [Column] — vertical layout with inside-out sizing, spacing, cross-axis alignment
//   - [Row] — horizontal layout with inside-out sizing, spacing, cross-axis alignment
//   - [ColumnOutsideIn] — vertical layout with clamped height and visibility toggling
//
// Decorator interactors (embed [impl.Decorator]):
//
//   - [NeuBox] — single-child wrapper with rectangular neumorphic shadows
//   - [NeuCircle] — single-child wrapper with circular neumorphic shadows
//   - [AppWindow] — root application window with title bar and content clipping
//   - [FreeFloatingWindow] — floating panel with title and groove separator
//
// Title bar renderers (not interactors; used as callbacks for [AppWindow]):
//
//   - [GradientTitle] — animated horizontal gradient with oscillating peak
//   - [StripedTitle] — classic Mac OS horizontal pinstripes
//
// # Neumorphic Rendering Helpers
//
// The package also exports rendering helpers used by interactors and
// available for custom interactor implementations:
//
//   - [NeuBoxWith] — draws a neumorphic rounded rectangle at any depth
//   - [NeuCircleWith] — draws a neumorphic circle at any depth
//   - [NeuGroove] — draws a thin horizontal inset groove separator
//
// # Nil-Safe Neumorphic Parameters
//
// All interactors handle nil [mancini.NeuParams] gracefully. When the
// [mancini.Theme]'s [mancini.NeumorphicParams] returns nil for Heavy() or
// Light(), interactors fall back to flat rendering: filled shapes with face
// content, but no shadows, grooves, or selection effects. This allows themes
// to disable neumorphic rendering for specific weight classes.
package std
