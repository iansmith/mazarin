// Package impl provides the base "classes" that concrete interactors embed.
// These types are not used directly by application code — they exist to
// factor out the common machinery of layout, drawing, theming, and child
// management.
//
// # Embedding Hierarchy
//
// The four types form a layered hierarchy via Go struct embedding:
//
//	[Interactor]                       — root base; stores backpointer, layout, DC
//	    └─ [ThemedInteractor]          — adds [mancini.Theme] (palette, fonts)
//	[Parent]                           — mixin for child discovery
//	[Decorator]                        — embeds Interactor + Parent; single-child wrapper
//
// Concrete interactor types in [mazzy/mazarin/mancini/std] embed one of:
//
//   - [ThemedInteractor] for leaf interactors and simple controls
//     (e.g., Button, Label, Checkbox, Scrollbar)
//   - [Interactor] + [Parent] for container layouts
//     (e.g., Column, Row, ColumnOutsideIn)
//   - [Decorator] for single-child wrappers with visual decoration
//     (e.g., NeuBox, NeuCircle, AppWindow, FreeFloatingWindow)
//
// # The Backpointer Pattern
//
// Go's struct embedding promotes methods but does not support virtual
// dispatch: if ThemedInteractor.Draw calls self.DC(), the call resolves
// to Interactor.DC(), not to an override on the concrete type. Mancini
// solves this by storing a backpointer to the outermost concrete type.
//
// [Interactor.Initialize] takes an "owner" parameter of type [mancini.Interactor]:
//
//	func (i *Interactor) Initialize(owner mancini.Interactor, layout *mancini.LayoutAttributes)
//
// Every concrete type passes itself as the owner during construction:
//
//	b := &Button{Depth: Raised, Radius: 8.0}
//	b.ThemedInteractor.Initialize(b, layout, theme)  // b is the owner
//
// The Draw protocol then passes this backpointer as the "self" parameter:
//
//	func (b *Button) Draw(self mancini.Interactor, x, y, w, h int64)
//
// This ensures that self.DC(), self.Visible(), etc. always resolve
// through the concrete type, enabling correct polymorphic behavior
// across the embedding chain.
//
// # Construction Sequence
//
// Depending on which base is embedded, the required initialization calls are:
//
// For [ThemedInteractor] embedders:
//
//	t.ThemedInteractor.Initialize(t, layout, theme)
//
// This calls [Interactor.Initialize] internally, which registers the
// interactor in the global registry.
//
// For [Interactor] + [Parent] embedders:
//
//	c.Interactor.Initialize(c, layout)
//	c.Parent.Initialize(true, &c.Interactor)
//
// [Parent.Initialize] must be called after [Interactor.Initialize]
// because it needs the Interactor's layout name for child discovery.
//
// For [Decorator] embedders:
//
//	d.Decorator.Initialize(d, layout, top, right, bottom, left)
//
// This calls both [Interactor.Initialize] and [Parent.Initialize]
// internally.
package impl
