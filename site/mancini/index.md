---
layout: default
title: mancini API reference
author: iansmith
---

# Mancini API Reference

Mancini is mazarin's UI toolkit — the interactor library that every
application uses for layout, drawing, theming, and neumorphic rendering.

## Packages

| Package | Description |
|---------|-------------|
| [mancini](mancini.md) | Core interfaces and types: layout attributes, draw context, theme, palette, neumorphic parameters |
| [mancini/impl](impl.md) | Base "classes" for concrete interactors: Interactor, ThemedInteractor, Parent, Decorator |
| [mancini/std](std.md) | Standard interactor catalog: Button, Label, Column, Row, AppWindow, and more |
| [mancini/theme](theme.md) | Default implementations of Theme, Palette, and NeumorphicParams |

## Architecture at a Glance

Mancini layers object-oriented interactor design onto Go's struct embedding.
The key concepts:

- **Backpointer pattern** — every interactor stores a reference to its
  outermost concrete type, enabling virtual dispatch through the embedding
  chain. See [impl](impl.md) for details.

- **Constraint-driven layout** — position and size come from a global
  constraint network, not from preferred-size methods. Parent-child
  relationships are discovered through constraint attributes.

- **Neumorphic rendering** — three depth states (Raised, Flush, Inset)
  with shadow parameters delivered through the Theme. All interactors
  handle nil parameters gracefully, falling back to flat drawing.

- **DrawContext** — all rendering goes through a thin interface wrapping
  [fogleman/gg](https://github.com/fogleman/gg). Interactors that need
  features not in the interface (arc fills, clipping) use local buffers
  and composite via `image/draw`.
