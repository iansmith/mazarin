# louis14 — temporary font integration plan

This document describes the changes needed in `~/louis14` to use the
new `OpenTemporaryFont` / `CloseTemporaryFont` API once the mazzy-side
infrastructure lands. **Do not apply these changes until the user
confirms.** The mazzy-side work introduces the new API as additive
(existing `OpenFont` continues to work unchanged), so louis14 will
keep building and rendering correctly in the meantime — just without
benefiting from temp-font slot recycling for HTML renders.

## Background — how louis14 opens fonts today

louis14's font path goes through two files:

- **`pkg/render/render.go`**: `Renderer.openFont(fontPath, fontSize)`
  is the central place CSS-resolved fonts get opened during paint.
  Caches `(fontPath, size) → fontID` in `r.fontCache` for the
  Renderer's lifetime. Calls `r.dc.OpenFont(family, variant, size)`
  through the DrawContext indirection (so IPC-only providers see a
  family name, not a filesystem path).

- **`pkg/text/measure.go`**: `getLayout().OpenFont(...)` is called
  during text measurement (layout phase, before paint). The shared
  `sharedLayout` is a `textshape.HarfBuzzTextLayout` whose
  `OpenFont` forwards to its underlying `GlyphProvider`. The same
  fontIDs returned during measurement are reused at paint, so
  measurement and rendering must share font state.

- **`pkg/text/fontcache.go`**: `RegisterFontFace(...)` calls
  `CurrentProvider().RegisterBuffer(family, variant, data)` for CSS
  `@font-face` srcs. This adds the (family, variant) → bytes entry
  to the provider's `registered` map; it doesn't allocate a slot
  yet. The slot is allocated later when `OpenFont` is called for
  that family/variant.

## What needs to change

### 1. `pkg/render/render.go`

The `Renderer` is currently long-lived (one per output target — image
or DC). For HTML render where we want temp-font lifecycle, we need
to scope font opens to **a single render pass** so we can close
everything at the end.

Add to `Renderer`:

```go
// tempFontIDs lists fontIDs opened during the current Render call.
// Closed via dc.CloseTemporaryFont in the deferred cleanup at end of
// Render. Reset to empty at the start of each Render so fonts opened
// for one paint don't carry over.
tempFontIDs []int32
```

In `openFont` (around line 192):

```go
func (r *Renderer) openFont(fontPath string, fontSize float64) int32 {
    size := int32(math.Round(fontSize))
    key := fontCacheKey{path: fontPath, size: size}
    r.fontCacheMu.Lock()
    defer r.fontCacheMu.Unlock()
    if id, ok := r.fontCache[key]; ok {
        return id
    }
    family, variant := text.FontPathToFamilyVariant(fontPath)
    metrics, err := r.dc.OpenTemporaryFont(family, variant, size)  // ← was OpenFont
    if err != nil {
        return -1
    }
    r.fontCache[key] = metrics.FontID
    r.tempFontIDs = append(r.tempFontIDs, metrics.FontID)  // ← track for close
    return metrics.FontID
}
```

In each public render entry point (`Render`, `RenderEmbedded`, etc.),
add a deferred cleanup at the start of the function:

```go
func (r *Renderer) Render(boxes []*layout.Box) {
    r.tempFontIDs = r.tempFontIDs[:0]  // reset scope
    defer r.closeTempFonts()
    // ... existing render body
}

func (r *Renderer) closeTempFonts() {
    for _, id := range r.tempFontIDs {
        _ = r.dc.CloseTemporaryFont(id)
    }
    r.tempFontIDs = r.tempFontIDs[:0]
    // Drop fontCache entries for the closed fontIDs so the next
    // render re-opens. Permanent fontIDs (from the permanent-pool
    // hit inside OpenTemporaryFont) close to no-op, but we still
    // want a fresh cache so the next render sees the current state.
    r.fontCache = map[fontCacheKey]int32{}
}
```

**Open question for the user:** if `Renderer` is used outside an HTML
context (e.g., a long-lived custom drawing surface), the close-on-render
defer will retire fonts that the caller might want to keep cached. We
have two options:

- (a) Always temp + always close at end of Render. Simpler and matches
  HTML usage; standalone callers re-open per render (small cost).
- (b) Add a flag `Renderer.useTemporaryFonts bool` that defaults to
  true for HTML renderers and false for whatever else exists. More
  flexible, more state.

**Recommendation:** (a). louis14's primary consumer is HTML rendering;
any other path should be examined and explicitly opt out only if it
demonstrates a measurable cost.

### 2. `pkg/text/measure.go`

Text measurement runs during layout, BEFORE paint, and the fonts it
opens must remain valid through paint. Two options:

- **Option I — measurement uses permanent `OpenFont`**: layout-time
  fonts go to the permanent pool. Paint-time fonts also use the
  permanent pool (or hit it via `OpenTemporaryFont`'s permanent-first
  check). Net effect: HTML rendering doesn't actually exercise the
  temp pool unless `@font-face` introduces unique faces not in
  permanent.

  This is a *very* attractive option because it minimizes change:
  measure.go doesn't change at all. Only render.go switches to
  `OpenTemporaryFont`, and that call hits the permanent-first check
  and returns the same fontIDs measurement allocated. No close
  discipline needed for fonts that came back from the permanent pool
  (CloseTemporaryFont is a no-op for those).

  The temp pool is used **only** when the requested face genuinely
  isn't in permanent — i.e., a CSS `@font-face` with custom bytes.
  Those *are* per-page lifecycle, exactly the workload we're solving.

- **Option II — measurement uses `OpenTemporaryFont` too**, with the
  Renderer (or a layout-render coordinator) tracking temp fontIDs
  across both phases. More state-passing required.

**Recommendation:** Option I. measure.go stays as-is. The
permanent-first check in `OpenTemporaryFont` does the right thing for
filesystem-resolved fonts (returns the existing permanent fontID),
and only `@font-face` custom bytes go into the temp pool.

### 3. `pkg/text/fontcache.go`

`RegisterFontFace` calls `RegisterBuffer` on the current provider —
this stores the bytes locally on the client (in `FontSvcGlyphProvider`'s
`registered` map). It doesn't yet send bytes to fontsvc.

Once mazzy's `FontSvcGlyphProvider.OpenTemporaryFont` is wired to
share font bytes with fontsvc on first temp open of a `@font-face`
family/variant, this file probably doesn't need changes. The flow
becomes:

1. CSS parser sees `@font-face`, fetches bytes, calls `RegisterFontFace`.
2. `RegisterFontFace` → `provider.RegisterBuffer(family, variant, data)`
   — bytes now stored on the client side.
3. Render starts. Renderer.openFont sees a CSS rule using the
   `@font-face` family. Calls `OpenTemporaryFont(family, variant, size)`.
4. `FontSvcGlyphProvider.OpenTemporaryFont` checks the permanent pool
   (miss), then checks `registered` (hit — bytes are local). Allocates
   pages, copies bytes, SharePagesWithTarget(fontsvc), sends
   `RequestOpenTemporaryFont` IPC with the page reference.
5. fontsvc parses Face from shared bytes, allocates temp slot, builds
   tier-1 cache, returns `0x1000 | idx`.
6. End of render: Renderer closes the temp fontID via
   `CloseTemporaryFont`. Provider sends `RequestCloseTemporaryFont`,
   fontsvc unmaps the bytes pages and frees the slot. Provider then
   `FreePages` on its side.

So `pkg/text/fontcache.go` itself is unchanged — the byte-sharing
machinery lives below the surface in `FontSvcGlyphProvider`.

### 4. `pkg/resource/web_render_engine.go` and `pkg/resource/renderer.go`

These wire a `GlyphProvider` into the rendering pipeline. They
already pass through to the underlying provider for OpenFont /
RegisterBuffer / etc. After the interface gains `OpenTemporaryFont`
and `CloseTemporaryFont`, the WebEngine's render pass will go through
`Renderer` (covered above), so no direct changes here unless these
files re-implement the render path.

A grep for `OpenFont(` in `pkg/resource/` should be confirmed clean
before declaring this point closed.

## Coordination checkpoints

The user (Ian) is coordinating the louis14 changes. The proposed order:

1. **Mazzy lands**: GlyphProvider interface gains the two methods
   (additive, no breaking change to OpenFont). All three mazzy
   providers implement them. fontsvc gets the temp pool + IPCs.
   Build mazzy clean.
2. **Verify mazzy alone**: run a 90s session, confirm no regression
   in current OpenFont path. Permanent pool fills moderately, no
   `[fontsvc] no free font slots` (because nothing yet uses the temp
   pool).
3. **louis14 changes**: render.go switches to `OpenTemporaryFont` +
   close-on-render-exit defer. Per the recommendation above,
   measure.go and fontcache.go stay as-is.
4. **Integration test**: mail-app session with many distinct HTML
   messages, verify temp pool slot recycling via fontsvc memstats /
   per-shepherd page counts.

## Risks / unknowns

- **`Renderer` reuse pattern in louis14 standalone vs. mazzy.** The
  HTML render path may not always go through a single `Renderer`
  instance — need to verify there isn't a separate code path that
  bypasses `Renderer.openFont` (e.g., layout-time font opens that
  don't touch the Renderer). If yes, those need similar lifecycle
  treatment.
- **`Renderer.fontCache` semantics across repeated renders.** Today
  the cache persists. Closing temp fontIDs at end of render means
  the cache needs to be invalidated for those entries (or cleared).
  The `closeTempFonts` helper above clears the whole cache; if that
  hurts perf for repeated paints of the same content (no font
  changes), we can refine to clear only entries pointing at temp
  fontIDs.
- **Fonts shared across renders.** If two consecutive renders use
  the same `@font-face` set, the temp open/close pair runs twice —
  re-parses, rebuilds tier-1 cache. The user explicitly accepted
  this cost ("more disk traffic" amortized vs. permanent pool
  pressure). If profiling shows it's actually a problem, we could
  add a fontsvc-side LRU cache of recently-closed temp slots.

## Out of scope for this work

- DirectGlyphProvider / standalone louis14 testing harness changes —
  the harness already uses `ResetOpenedFonts` which still works. If
  we want surgical close in the harness (no need to reset everything
  between tests), update visualtest harness to call
  `CloseTemporaryFont` per test. Not required for the mazzy work.
- Refactoring the existing measure-then-paint coupling. The current
  shared-layout model works fine.
