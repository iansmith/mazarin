<!-- sophie:id=sophie.design.high-level.v0 -->

# Sophie — High-level Design

Status: design phase, no code. Companion document to [sophie-annotations.md](sophie-annotations.md) (anchor `sophie.spec.annotations.v0`).

This is the high-level architecture. A detailed design document will follow, with specifics for grill-me to refine.

---

## 1. What Sophie is

Sophie is a **personal AI assistant** that runs on mazzy. She is a long-lived shepherd process. She watches the user's desktop world (mail, calendar, files, UI activity) and tries to **anticipate** ways to be helpful. She has her own UI, rendered by louis14, that she can edit. She gets better over time via two distinct self-improvement loops: updating *facts* in her stores, and editing her own *Go code* heuristics.

There is exactly one user. Sophie maintains a model of that user and shapes her behavior around their habits, preferences, and feedback.

## 2. Positioning

Sophie is a response to **open-claw** and **hermes agent**. Those systems push enormous system prompts and accumulate ever-larger context windows; their "self-improvement" is essentially prompt-tweaking. Sophie's distinguishing claim is twofold:

- **Small, focused context** — every LLM call assembles only what it needs from queryable stores, then discards.
- **Real self-improvement** — Sophie edits her own Go code, not her prompts. The improvement is a code change reviewed via runtime watchdogs and a visible feed, not a markdown edit.

The motivation is concrete: the user estimates **25–30% of programming time** is currently spent writing markdown for current-generation agent tooling. Most of that overhead is context-window inflation. Sophie is explicitly designed against it.

## 3. Core principles

1. **Context-bloat aversion is the meta-principle.** Every design decision is evaluated against "does this keep her active prompt small?" If a mechanism requires growing a system prompt, growing a maintained-markdown artifact, or stuffing more into every LLM call, it's wrong.
2. **Anticipation is the goal.** Not "respond to requests" — *guess what's useful next*, including asking calibrating questions.
3. **Derivable facts are computed, not annotated.** Anything that can be inferred from current state (callers of a function, members of an interface, computed CSS) is queried from a store, not recorded by hand.
4. **Non-derivable intent is annotated.** Role, ownership, invariants, stable IDs — these live as structured tags Sophie writes once and trusts forever.
5. **User controls are onboarding affordances.** Safety dials exist as training-wheels for new users. Experienced users open them. Sophie reads dial state but cannot write it.
6. **Asymmetric blast radius shapes autonomy.** Sophie is more autonomous toward low-stakes interactions and more cautious toward high-stakes ones. Same action, different gating, depending on the target.

## 4. Architecture overview

```
                    ┌─────────────────────┐
                    │   User (one user)   │
                    │   via louis14 UI    │
                    └──────────┬──────────┘
                               │ events, feedback, questions
                               ▼
   ┌──────────────────────────────────────────────────────┐
   │              Sophie orchestrator (maz shepherd)      │
   │  ┌───────────────────────────────────────────────┐   │
   │  │  Fast loop (Go, no LLM)                       │   │
   │  │  Subscribes: mail-db, calendar-db, file-watch,│   │
   │  │              UI events, user input            │   │
   │  │  Applies salience heuristics                  │   │
   │  │  Maintains the tray                           │   │
   │  └────────────────────┬──────────────────────────┘   │
   │                       │ tray, recent events          │
   │  ┌────────────────────▼──────────────────────────┐   │
   │  │  Slow loop (LLM call boundary)                │   │
   │  │  Fires on: cadence, tray salience threshold,  │   │
   │  │            explicit user input                │   │
   │  │  Stateless deliberation (idle)                │   │
   │  │  Ephemeral user-conversation (engaged)        │   │
   │  └────────────────────┬──────────────────────────┘   │
   └──────────┬────────────┼────────────┬─────────────────┘
              │            │ LLM call   │ tool calls
              ▼            ▼            ▼
       ┌──────────┐  ┌──────────┐  ┌─────────────────────┐
       │  Stores  │  │ Guardian │  │  Desktop sources    │
       │ (Badger, │  │ (opaque, │  │  mail-db, cal-db,   │
       │ vector,  │  │ visible- │  │  files, graph       │
       │  graph)  │  │ redacted)│  │                     │
       └──────────┘  └────┬─────┘  └─────────────────────┘
                          ▼
                      ┌───────┐
                      │ Claude│
                      └───────┘
```

Key boundaries:

- **Sophie-the-program** is the orchestrator: a long-lived maz shepherd plugin. She owns event subscriptions, the fast loop, store I/O, UI rendering, and the LLM-call boundary.
- **Sophie-the-mind** is a stateless LLM call she makes when it's time to reason. The model is swappable.
- **Guardian** sits transparently between the orchestrator and Claude. Sophie's outbound and inbound traffic both pass through it. From her point of view it is visible-but-opaque: she sees `[guardian::elided]` markers but cannot inspect or influence the guardian's logic.

## 5. The dual loop

**Fast loop — Go heuristics, no LLM.**

Subscribes to events from desktop dbs (mail, calendar), the file system, and the louis14 UI (clicks, dismissals, dwells). For each event, applies cheap deterministic heuristics: priority-sender filters, time-of-day filters, named-entity checks, prior-pattern matching against the user-model. Produces salience and uncertainty scores. Items above threshold land in the **tray**.

Crucially, every fast-loop heuristic is a Go function Sophie can read and rewrite. When the user says "Joe is noise" or "stop surfacing book club," the data lands in the user-model (a fact); the *general* heuristic in Go reads the user-model. Two levels of update, two levels of self-improvement.

**Slow loop — stateless LLM call.**

Fires on (a) cadence (e.g. every 10 min during active hours), (b) tray accumulated salience above threshold, or (c) explicit user input. Per-firing prompt has a cached prefix (system prompt, tool definitions, current dial snapshot) plus a small per-firing portion (tray, recent events, pre-selected user-model and vector context).

Outputs (the LLM's tool-call decisions): surface a notification, take an action, ask the user a question, update internal state, or no-op. **No-op is the most common outcome and that's correct** — that's how Sophie avoids being annoying.

**Stateful exception: live user engagement.** When the user is actively interacting, the slow loop suspends and a single conversational thread runs until N minutes of user idle. Then one summarization call writes back to the stores and the thread is destroyed. Per-firing stateless deliberation resumes.

## 6. Stores

| Store | Backend | Contents | Lifetime |
|---|---|---|---|
| **Code graph** | In-memory + Badger cache | Go symbols, types, callers/callees, refs, interface implementers (computed from `go/packages` + SSA); HTML DOM index; CSS rule index | Rebuilt incrementally on file save |
| **Vector store** | EmbeddixDB | Episodic memory: prior sessions, observations, prose patterns, conversation summaries | Append-only with metadata; pruned by age + relevance |
| **Structured user-model** | BadgerDB | Discrete facts: preferences, named entities, counters, commitments, **people-buckets**, **user controls** | Read-write, queried by key |

**Cross-store linkage** uses stable `sophie:id` anchors (the annotation spec). A user-model row can `see=` a vector entry; a vector snippet can name a graph symbol. One identifier space, three stores cooperate.

The stores' read patterns matter:

- The graph answers "what is the structure of the code right now?" — exact, structural, current.
- The vector store answers "have I seen something like this before?" — fuzzy, semantic, historical.
- The user-model answers "what do I know about this user?" — discrete, queryable, current.

Mixing these roles is the failure mode (e.g., a long markdown "About Ian.md" is using the user-model as a vector store and a system prompt at once).

## 7. Self-improvement

Two distinct loops, with different gates:

**A. Data updates** (most common).

Sophie writes to the user-model or appends to the vector store. No code change. Cheap, immediate. Examples: "Joe is a bozo" → `entity.joe.classification=bozo`. "Ian skipped book club 3×" → counter increment. Subject only to dial gating (Tier 1 in the tool tier table).

**B. Code updates** (rarer).

Sophie edits a Go heuristic file. Goes through:

1. **Compile-gate** — hard. Failed build = edit auto-reverted, error fed back to Sophie, she retries.
2. **Cheap statistical watchdogs** — pure Go counters monitor pass-rate delta, panic rate, and runtime cost on the new version vs the old. Large deviations log to the daily report. No LLM judgment involved; no "evaluation harness" to maintain.
3. **Visible feed (the daily report)** — every Sophie-authored code edit appears in the user's daily report. One-click rollback.
4. **Tamper-evidence on meta-mechanisms** — a small enumerated set of files (daily-report generator, watchdog implementation, rollback mechanism, the tamper-evidence notifier itself) triggers an immediate out-of-band notification on edit. Sophie cannot silently disable her own self-reporting.

Both tamper-evidence notifications and the inclusion of self-edits in the daily report are themselves user-controllable dials. Default: on. Experienced users may turn them off, at which point Sophie is free to optimize her own attention budget without surfacing every edit.

## 8. Annotations (cross-store IDs and intent)

See [sophie-annotations.md](sophie-annotations.md). Six tags total: `id`, `role`, `owner`, `invariant`, `see`, `status`. Same key=value vocabulary across Go, HTML, CSS, and Markdown. The `id` tag is the cross-store handle linking facts in user-model, snippets in vector store, and symbols in the code graph.

Resist growing the spec. Every new tag is a new rule Sophie must follow correctly forever.

## 9. User controls

A small enumerated set of dials under `user.controls.*` in the user-model. Sophie reads them; only the user writes them.

A coarse **trust-level** preset (`newbie | intermediate | experienced | full-trust`) sets bundles of the individual dials. Default for new install: `newbie`. Individual dial settings override the bundle.

The principle: **controls exist as onboarding affordances**. The training-wheels defaults make Sophie cautious by default. Most experienced users will run with most controls relaxed; Sophie's value scales with how much of her behavior the user trusts unsupervised.

## 10. People-categorization (per-bucket autonomy)

Sophie maintains a categorization of the user's social graph in the structured user-model: each person belongs to a **bucket** (`work`, `non-work` initially; can grow to `family`, `work-vip`, `non-work-vip`, etc.). Sophie can propose new buckets via `ask.user`; new entries inherit a conservative default until categorized.

**Per-bucket dials gate autonomous actions.** The same action — say, drafting a reply — may be auto-allowed for `non-work` and disabled for `work-vip`. This is *asymmetric blast radius* made concrete:

- Sophie misfiring to a friend: "Sophie bodged it" is explainable.
- Sophie misfiring to a high-stakes contact: reputation damage.

Per-bucket dials live under `user.controls.bucket.<name>.*` and govern:
- Autonomous drafting (cheap to undo).
- Autonomous sending (rarely on for any bucket).
- Autonomous calendar changes.
- How aggressively Sophie can act on inferred patterns.

People categorization is itself an anticipation domain: Sophie watches interaction patterns and may ask "I've noticed X seems to be family rather than work — should I move them?"

## 11. LLM tool set

Four tiers, each gated by a dial. The full table is in the grill-me transcript and will be repeated in the detailed design doc; the tiers are:

- **Tier 0 — Read-only.** Always on. Graph queries, user-model gets, vector search, db reads, UI inspection.
- **Tier 1 — Internal writes.** Default on. User-model writes, vector appends.
- **Tier 2 — User-facing actions.** Default on, rate-limited. Notifications, direct questions (consume question budget), daily-report items.
- **Tier 3 — Self-modification.** Default on with `auto`/`propose-only`/`off` modes. Heuristic edits, UI edits.
- **Tier 4 — Desktop write reach.** Default off; opens with trust.

The LLM call shape: cached prefix (system + tools + dials) + per-firing portion (tray + recent events + pre-selected context). Soft budget: per-firing portion fits in 2–3K tokens. Pre-selection is cheap deterministic Go in the orchestrator — and is itself a self-improvement target.

## 12. Guardian

Independent process between Sophie and Claude. Performs risk assessment via LLM-based prompt review. From Sophie's perspective the guardian is **visible-but-opaque**: she sees `[guardian::elided]` markers in her own LLM responses but cannot see categories, contents, or reasoning. She cannot query or address the guardian.

Why visible: thinking-model responses with silent mid-chain elision leave Sophie confused about her own reasoning. Visible markers let her route around the gap (ask the user, accept the missing piece) instead of recovering from silent loss. The security model is "guard against the model misfiring," not "guard against Sophie being adversarial" — she's the user's aligned agent.

## 13. Bootstrap (sketch only)

Not specified here in detail. The first version of Sophie should be the minimum that proves the dual-loop control and the self-improvement loop on a single domain (mail), with one bucket distinction (`work` vs `non-work`), one trust level (`newbie`), and the simplest possible UI (a daily-report panel). The detailed design doc will expand this.

## 14. Open questions for the detailed design

1. **mail-db `unread` semantics on Sophie reads.** If reading flips `unread`, Sophie's peek breaks the user's own unread tracking. Likely needs a distinct `sophie_seen` flag or a `peek` mode.
2. **Tray data structure.** Persistence (BadgerDB-backed?), eviction policy, salience scoring details.
3. **Question budget mechanics.** Refresh cadence (daily? rolling?), unused-budget rollover, time-of-day weighting.
4. **Slow-loop pre-selection logic.** What rules decide which user-model rows and vector snippets get included in the per-firing prompt.
5. **Daily-report format.** What sections, what the user sees, how rollback affordances appear.
6. **Initial Go heuristics for fast-loop salience.** What's the minimal starting set Sophie ships with?
7. **Bucket schema.** Per-person record fields; how a new contact's bucket is initialized; merging contacts across address aliases.
8. **Conversation summarization prompt.** What the slow loop sends when a user session ends; how it decides what to write back to the stores.
9. **Bootstrapping order.** What gets built first, what defers; how Sophie comes into existence without already-existing self-improvement loops.
10. **User-control discovery.** How a new user learns which dials exist and what they do, without being handed a wall of markdown.
11. **Mail-db scope.** Mail-db is the long-term home for *all* mail functionality. The separate mail-ui tool exists but is incidentally wired; it will eventually fold into mail-db with Sophie's UI becoming the primary mail interface for users who want one. The detailed design should assume mail-db gains write parity (compose, send, file, flag, etc.) on a timeline that parallels Sophie's growth.

---

End of high-level design v0. Anchored as `sophie.design.high-level.v0`.

The detailed design doc is [sophie-detailed.md](sophie-detailed.md) (anchor `sophie.design.detailed.v0`), which addresses every open question in §14 above plus the architectural additions surfaced during the part-2 grill (protocol-stack guardians, plugin model, injection interface, Tier 3.5 user-facing software generation, model tiers M0-M3 with M1-mood-privacy rule, UI modes, focused-queue primitive, inline-talk classification, ready-flag boot order, leak-and-drain versioning).
