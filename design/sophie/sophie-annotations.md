<!-- sophie:id=sophie.spec.annotations.v0 -->

# Sophie annotation spec v0

A spec this small fits in Sophie's system prompt. Resist growing it — every new tag is a new rule she has to follow correctly forever.

## Principles

1. **Never duplicate what the graph computes.** No `callers=`, `callees=`, `implements=`, `refs=`. Those come from `go/types`/SSA, the DOM, the CSS parser.
2. **IDs are the only required tag.** Everything else is optional intent.
3. **IDs are stable under rename.** Renaming a symbol/element/rule does *not* change its `sophie:id`. The ID is the contract; the name is incidental.
4. **One ID per anchored thing.** Not per file, not per package. Per definition.
5. **Free-text fields are short.** One line. Multi-line intent belongs in regular doc comments.

## Syntax per language

**Go** — directive comments, Go convention (`//go:build` style):

```go
//sophie:id=ui.radial.chooser
//sophie:role=entry-point
//sophie:invariant=must run on render goroutine
func NewChooser(...) *Chooser { ... }
```

Directives sit immediately above the declaration, no blank line between. Multiple directives are separate `//sophie:` lines, not comma-jammed.

**HTML** — `data-sophie-*` attributes:

```html
<div data-sophie-id="ui.statusbar.root"
     data-sophie-role="status-bar"
     data-sophie-owner="renderer">
  ...
</div>
```

**CSS** — block comment immediately preceding the rule:

```css
/* sophie:id=ui.statusbar.style
   sophie:role=status-bar-skin
   sophie:invariant=do not set position; layout owns it */
.statusbar { ... }
```

**Markdown / docs / tickets** — inline HTML comment:

```markdown
<!-- sophie:id=design.radial-menu.rationale -->
```

Same key=value vocabulary across all four. Sophie writes one parser per surface and gets uniform semantics.

## Tag vocabulary

Six tags. That's the whole spec.

| Tag | Required? | Meaning | Value shape |
|---|---|---|---|
| `id` | **Required** on every anchored thing | Stable cross-store handle | `domain.subdomain.name` — dotted, lowercase, ASCII, kebab segments allowed (`ui.radial-menu.chooser`) |
| `role` | Optional | What this *is* in the system's ontology | Short kebab token from a controlled vocabulary Sophie maintains: `entry-point`, `render-hot-path`, `config-loader`, `status-bar`, etc. |
| `owner` | Optional | Which subsystem owns it | Short kebab token: `renderer`, `constraint-vm`, `ui`, `memory` |
| `invariant` | Optional, repeatable | A rule that must hold; not derivable | One line of free text |
| `see` | Optional, repeatable | Reference to another `sophie:id` | A bare ID (no URL) |
| `status` | Optional | Lifecycle | One of: `experimental`, `stable`, `deprecated` |

Repeatable tags appear on separate directive lines (or repeated attributes in HTML, but Go-style is one per line).

## ID format rules

- Lowercase ASCII, dots as hierarchy separators, kebab within a segment.
- Three segments minimum (`domain.area.name`) to avoid collisions; deeper if useful.
- Generated once and **never edited** except via an explicit rename ceremony (see below).
- IDs are *not* paths. `ui.radial.chooser` doesn't imply a file location; the graph resolves ID → current location.
- Reserved namespaces: `sophie.*` for Sophie-internal anchors, `user.*` for user-facing customization points.

## Allowed placements

| Tag | Go | HTML | CSS | Markdown |
|---|---|---|---|---|
| `id` | top-level decls (func, type, var, const), package doc | any element | any rule or `@`-rule | section headers, callouts |
| `role` | same | same | same | discouraged |
| `owner` | same | same | rare | yes |
| `invariant` | functions, types, rules | rare | yes | yes |
| `see` | anywhere | anywhere | anywhere | anywhere |
| `status` | top-level decls | top-level elements | top-level rules | rarely |

"Discouraged" / "rare" means: not banned, but Sophie should default to leaving it off.

## Rename ceremony

When an ID *must* change (e.g. a subsystem reorganization):

1. Add a `sophie:see=old.id` on the new ID's anchor.
2. Search-and-replace `old.id` → `new.id` across all stores (graph rebuild + vector store metadata update + ticket text). This is a tool, not a manual step.
3. Old ID never reused.

Renames are expected to be rare. Most "renames" are just symbol renames in Go, which don't touch the ID.

## What is explicitly *not* in the spec

- No structural facts (callers, callees, implementers, refs, types).
- No timestamps. Git knows.
- No author. Sophie is the only author.
- No free-form JSON. If a future need wants structured data, add a tag with a tight value grammar; don't open the door to JSON blobs.
- No nesting. Tags are flat.

## Parser contract

Two lines for Sophie's tool implementer:

> A Sophie tag is a `key=value` pair where key matches `[a-z][a-z-]*` and value is everything to the end of the line/attribute, trimmed. Multiple tags on one anchor live on separate directive lines (Go/CSS) or as separate attributes (HTML). Parsing is line-oriented; no quoting, no escapes, no continuations.

That's the whole grammar. It stays cheap to parse in any language and cheap for Sophie to write correctly.

## Versioning

The spec itself has the anchor `sophie.spec.annotations.v0` (see top of file). If it changes incompatibly, bump to `v1` and migrate. Sophie can read the spec by ID like any other anchor.
