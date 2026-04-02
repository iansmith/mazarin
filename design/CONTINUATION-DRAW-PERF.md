# Continuation: Clocks Draw Performance Investigation

## Problem
Clocks' initial `app.Draw()` takes ~41 seconds. Expected: <1s.
This is 80-100x too slow for the actual rendering work involved.

## What We Know
- Draw is CPU-bound (134 SVCs in 90s, GOMAXPROCS=1)
- All font loads succeed (tier-1 cache hits, no IPC during draw)
- GC pauses are trivial (<5ms each)
- Window size: 966x379, 6 neumorphic clock faces
- Per-SID trace removed (was adding ~17s, so real draw time ~41s without it)

## Investigation Plan

### Step 1: Instrument `app.Draw()` phases
Add `nanotime()` checkpoints in `flock/cmd/clocks/main.go` around each
phase of the draw to isolate which operation dominates:

```
t0: dc.SetColor + dc.FillRectangle (background fill)
t1: app.SetDC
t2: app.Draw(...)   ← this is where most time goes, break it down
t3: done
```

### Step 2: Instrument neumorphic rendering in `mazarin/mancini/std/draw.go`
Add timing inside `neuRaised` and `neuCircleRaised`:
- Time spent in `shadowLayer` (gg.NewContext + Draw + rgbaToNRGBA + gaussianBlur)
- Time spent in `draw.Draw` / `draw.DrawMask` compositing
- Time spent in `dc.DrawRoundedRectangle` + `dc.Fill` (the face fill)

This tells us: is it blur, gg rasterization, compositing, or something else?

### Step 3: Measure Gaussian blur directly
Add timing around `gaussianBlurNRGBA` calls in `shadowLayer`:
```go
t0 := nanotime()
result := gaussianBlurNRGBA(nrgba, blur)
dt := (nanotime() - t0) / 1e6
rawPuts("[blur] " + size + " sigma=" + sigma + " " + dt + "ms\n")
```

### Step 4: Check demand paging cost
Each `image.NewRGBA(...)` allocates Go heap memory. With demand paging,
first access to each new page triggers a page fault → SVC → kernel maps
page. Count page faults during draw from kernel stats:
- Record `pf=` value before and after draw
- If delta is large (thousands), demand paging is a major factor

### Step 5: Check text shaping / glyph rendering
The draw calls `DrawText` which uses HarfBuzz for shaping + glyph cache
for rendering. If the V2 cache binary search is slow or HarfBuzz shaping
per string is expensive, text could dominate.
- Instrument `DrawText` / `textshape.ShapeAndMeasure` with timing

### Step 6: Check `gg.NewContextForRGBA` overhead
The `gg` library (fogleman/gg) creates a cairo-like context. If
construction involves expensive initialization (freetype rasterizer
setup per context), creating many contexts could be costly.
- Count how many gg contexts are created during one draw
- Time context creation vs. actual drawing

## Likely Suspects (ordered by probability)
1. **Demand paging** — thousands of page faults for heap allocations
2. **gg context/rasterizer overhead** — many contexts created per draw
3. **Text shaping** — HarfBuzz called per string per draw
4. **Something in the interactor tree walk** — unexpected O(n^2) or blocking
5. **Gaussian blur** — unlikely to be >0.5s total, but measure to confirm

## Files to Modify
- `flock/cmd/clocks/main.go` — phase timing around draw
- `mazarin/mancini/std/draw.go` — neuRaised/neuCircleRaised timing
- `mazarin/mancini/draw_context.go` — DrawText timing (if needed)
