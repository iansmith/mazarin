// Package mancini is the core of the Mazzy UI toolkit. It defines the
// interfaces and types that all interactors share: layout, drawing, theming,
// and neumorphic shadow parameters.
//
// # Architecture
//
// Mancini uses an object-oriented design layered on Go's embedding. Every
// UI element ("interactor") ultimately satisfies the [Interactor] interface,
// which provides position (X, Y), size (W, H), visibility, and access to
// the current [DrawContext]. Themed interactors additionally satisfy
// [ThemedInteractor], adding palette colors and font resolution.
//
// Concrete interactor types live in the [mazzy/mazarin/mancini/std] package.
// Their implementation base types live in [mazzy/mazarin/mancini/impl]:
//
//   - [impl.Interactor] — root base "class"; stores the backpointer, layout
//     attributes, and drawing context.
//   - [impl.ThemedInteractor] — extends [impl.Interactor] with a [Theme]
//     reference; provides BgColor, FgColor, and font resolution.
//   - [impl.Parent] — mixin for interactors that have children; discovers
//     children through the constraint network.
//   - [impl.Decorator] — single-child parent with inside-out sizing and
//     customizable visual decoration.
//
// # Backpointer and Virtual Dispatch
//
// Go embedding promotes methods but does not provide virtual dispatch.
// Mancini solves this with a backpointer pattern: every [impl.Interactor]
// stores an "owner" field of type [Interactor] that points to the outermost
// concrete type. During construction, the concrete type passes itself:
//
//	b := &Button{...}
//	b.ThemedInteractor.Init(b, layout, theme)  // b is the backpointer
//
// The Draw protocol uses this backpointer as the "self" parameter:
//
//	func (b *Button) Draw(self Interactor, x, y, w, h int64) { ... }
//
// This ensures that self.DC(), self.Visible(), etc. resolve through the
// concrete type, enabling correct virtual dispatch across the embedding
// hierarchy.
//
// # Layout and Constraints
//
// Each interactor has a [LayoutAttributes] that publishes its position,
// size, and visibility as named attributes in a global constraint network.
// Container interactors ([impl.Parent]) discover children by querying
// which attributes name them as their parent. This decouples parent–child
// relationships from Go's type system — children register themselves by
// setting their Parent attribute to the parent's constraint-system name.
//
// # Neumorphic Rendering
//
// The visual system supports three depth states ([NeuDepth]):
//
//   - [Raised] — proud of the surface, casting external dark and light shadows
//   - [Flush] — level with the surface, with a thin edge outline
//   - [Inset] — recessed into the surface, with inner shadows
//
// Shadow parameters are bundled in [NeuParams] and delivered through the
// [Theme] via the [NeumorphicParams] interface. Two weight classes exist:
// Heavy (for windows) and Light (for controls). Either may return nil to
// disable neumorphic rendering entirely, falling back to flat drawing.
//
// # Drawing
//
// All rendering goes through [DrawContext], which wraps a [github.com/fogleman/gg]
// context. The [NewDrawer] interface is the standard draw protocol:
//
//	Draw(self Interactor, x, y, w, h int64)
//
// The parent passes authoritative bounds (x, y, w, h) that override any
// stale values in the layout attributes. The "self" parameter is the
// backpointer described above.
package mancini
