<!-- sophie:id=sophie.design.detailed.v0 -->

# Sophie — Detailed Design

Status: design phase, no code. Companion to [sophie-high-level.md](sophie-high-level.md) (anchor `sophie.design.high-level.v0`) and [sophie-annotations.md](sophie-annotations.md) (anchor `sophie.spec.annotations.v0`).

This document specifies the design decisions deferred from the high-level doc's §14. It synthesizes a grill-me session and does not re-litigate decisions already locked there.

Read order: high-level doc → annotation spec → this doc.

---

## 1. Architectural preconditions

Two facts shape every downstream decision. Naming them up front because they're easy to forget when reasoning about Sophie's blast radius.

### 1.1 Mazarin sees only what the user exports.

The desktop dbs (mail-db, calendar-db, future contacts-db, files-db, etc.) are populated from **user-curated subsets** of the upstream world. Not from full inboxes, full calendars, or full filesystems. Export happens upstream of mazarin.

For bootstrap users, the export gate is heavily restrictive (manual, per-item). For experienced users it is automated by rule. **The export gate is a trust ramp, not steady-state architecture.** In the steady state, mazarin sees almost everything; junk is filtered upstream; a small permanent **wall** of senders/items bypasses mazarin entirely.

**Architectural invariant:** Sophie cannot increase what mazarin sees. The export gate and the wall are user-only writes. Sophie may propose; she never writes.

### 1.2 Sophie has no LAN reach.

Mazarin runs in a MacOS hypervisor with no host or LAN visibility. Its only network is a **Virtual Public Network (VPubN)**: all egress and ingress is gatewayed through a public-internet relay with its own enforcement.

The host Mac, NAS, printers, work intranet, and other LAN devices are unreachable from inside mazarin regardless of what Sophie does. Sophie's adversarial reach in a worst-case misfire is bounded to public services explicitly cabled in.

**Generalization:** the guardian on the Claude protocol is one instance of a protocol-stack pattern. Each protocol (Claude, TCP, HTTP, SMTP, SIP) has its own interceptor in the VPubN relay. Defense in depth at every layer.

---

## 2. Layered safety stack

The high-level doc names two enforcement boundaries (perimeter and guardian). The full stack has eight layers:

1. **Perimeter** — export gate + wall + hypervisor isolation. User-controlled.
2. **Protocol-stack guardians** — one per protocol in the VPubN relay. Including the Claude guardian.
3. **LLM boundary discipline** — stateless slow loop, cached prefix, summarize-and-destroy engaged sessions, visible-but-opaque `[guardian::elided]` markers.
4. **Tool/action tiers** — Tier 0–4 dials, per-bucket autonomy, per-person VIP, per-person send opt-in.
5. **User-interrupt budgeting** — question budget, focus-window multipliers, phone-channel hard cap, demote-not-drop.
6. **Confirmation choke points** — first migration of a person, any move touching `family` or `vip=true`, bucket creation, low-confidence merges, wall edits, `send_allowed` opt-in.
7. **Display integrity** — Sophie's bookkeeping never overlaps user-visible state. She uses her own flag colors; she never flips `\Seen`. Generalizes per db.
8. **Self-modification guardrails** — compile-gate, statistical watchdogs, daily-report visible feed with one-click rollback, tamper-evidence on a small meta-set, recursive (the notifier is itself tamper-evident).

Two cross-cutting rules:

- **Dial-controlled hiding ≠ not-capturing.** Dials that hide information from a UI default to *capture-but-hide*, not *don't-capture*. The data is always recorded; the dial controls rendering. Past days can be inspected retroactively.
- **M1 mood inference stays local.** Sentiment/emotion/engagement-state outputs from M1 do not cross into M2 prompt context, vector store, or user-model facts visible to pre-selection. They route only to orchestrator state and a local `mood_log` store. User can opt in to a coarse signal crossing into M2; default off.

---

## 3. The tray

The artifact that ties fast and slow loops together. Durable across restarts; salience accumulates over hours/days and cannot be lost on shepherd restart.

### 3.1 Entry shape

```
tray_id        sophie:id   anchor or synthesized (mail.msg.<hash>, cal.evt.<uid>)
kind           enum        mail | cal | file | ui | derived | user-action-required
salience       float       [0, 1]
uncertainty    float       [0, 1]
urgency        float       [0, 1]
first_seen     ts
last_touched   ts
evidence       []          {source_id, weight, ts} — append-only, cap 8
state          enum        active | surfaced | asked | acted | dismissed | expired
```

Three fast-loop signals are orthogonal:
- **salience** — is this worth attention?
- **uncertainty** — do I know what to do about it?
- **urgency** — how time-sensitive is it?

A routine reminder may be high-salience low-urgency. A health emergency is high-salience low-uncertainty maximum-urgency.

### 3.2 Score dynamics

- **Re-fire on new evidence:** `s ← max(s, new_signal)`. Anticipation ratchets up rather than averaging down.
- **Decay is lazy on read:** `s_now = s × exp(-Δt / τ)`, τ ≈ 24h. No background sweeper; computed on access.
- **Capacity:** soft cap 500 active, hard cap 2000. Hourly background pass drops entries below floor (0.05) for capacity.

### 3.3 Persistence and storage

BadgerDB-backed with in-memory cache. Tray entries survive shepherd restarts. Terminal-state entries (`acted`, `dismissed`, `expired`) move to a separate `history` prefix for vector-store ingest, then GC'd after 30 days.

### 3.4 Concurrency

One owner goroutine. Fast loop sends `TrayEvent` on a channel. Slow loop reads an immutable snapshot at fire time (top-K by salience, ~50 entries). No locks held across the LLM-call boundary.

---

## 4. Question budget, channels, urgency

### 4.1 Budget mechanics

- **Unit:** one *question* = one user-facing `ask.user` event the user must read and answer. Notifications, flag changes, and daily-report items are not questions.
- **Default budgets by trust level:** newbie 5, intermediate 10, experienced 20, full-trust unmetered. All overridable.
- **No rollover.** Banking budget incentivizes saving up for a deluge.
- **Reset hour:** dial-tunable, default `02:00 local`.
- **Time-of-day weighting:** focus windows (default `09:00–12:00`, `14:00–17:00`) cost 2×; off-hours cost 1×. Windows and multiplier are dials.

### 4.2 Channels

Channel is mechanical, picked by fast-loop urgency. The LLM never deliberates "should I text vs call."

| Channel | Cost | Default | Used when |
|---|---|---|---|
| `daily_report` | 0 | always on | low urgency, batchable |
| `sms` | 0.5 | off until configured | low/mid urgency, deferrable |
| `dialog` | 1 / 2 (focus) | always on | mid urgency, user at desk |
| `phone_call` | 10 + separate 1/day hard cap | off until configured | high urgency, can't wait |

Channel selection (deterministic):

```
if urgency >= 0.9 and phone.enabled and phone.daily_remaining > 0:
    channel = phone_call
elif urgency >= 0.6 and sms.enabled:
    channel = sms
elif urgency >= 0.3:
    channel = dialog
else:
    channel = daily_report
```

Thresholds are dials.

### 4.3 Demote-not-drop

Budget exhausted but high salience × uncertainty → demote to `daily_report` (Morning report §3 *"I noticed X; I would have asked Y; was I right to skip?"*). Phone-cap exhausted but urgency ≥ 0.9 → fall back to SMS if enabled, else dialog. **Never silently drop a high-urgency item.**

### 4.4 Stale questions

Unanswered questions older than 24h auto-expire from the user UI, get logged to morning report as soft negative signal ("X expired unanswered — assuming no-op was correct"). The non-response writes a feedback fact.

---

## 5. People-categorization and buckets

### 5.1 Person record

Stored at `person.<canonical_id>` in user-model. Canonical id is a UUID Sophie generates — never an email or phone (those are aliases that can change).

```
canonical_id        UUID
display_name        string
aliases             []{kind: email|phone|handle|real_name, value, first_seen}
contact_methods     []{kind, value}     outgoing-allowed subset of aliases
bucket              string              FK to bucket name
vip                 bool                orthogonal to bucket
urgency_multiplier  float, default 1.0  per-person override
notes_id            sophie:id           vector-store handle for episodic context
counters            {sent, received, last_interaction_ts, response_latency_p50}
created             {ts, source}
merge_confidence    float
```

### 5.2 Seed buckets

`family`, `work`, `non-work`, `unclassified` (default sink), `noise` (filter-into-oblivion).

**VIP is a boolean, not a bucket.** Avoids `work-vip`/`family-vip` Cartesian product. A VIP family member is `bucket=family, vip=true`.

### 5.3 Bucket dials

Each bucket carries dials under `user.controls.bucket.<name>.*`:

```
autonomy.draft_replies         bool
autonomy.send_replies          bool, default false everywhere
autonomy.calendar_change       enum: never | propose | auto
urgency_multiplier             float, default 1.0
vip_urgency_multiplier         float, default 1.5 (extra layer when vip=true)
channel.phone.allowed          bool, default false
surface_threshold              float, overrides global salience cutoff
```

Effective urgency for an event involving person X:

```
urgency = base_urgency
        × person.X.urgency_multiplier
        × bucket.X.bucket.urgency_multiplier
        × (X.vip ? bucket.X.bucket.vip_urgency_multiplier : 1)
```

All multipliers in the KV; no code change to retune.

### 5.4 New contacts

Default bucket: `unclassified`. Sophie does *not* guess work-vs-non-work at first sight. After N interactions (dial, default 3) Sophie proposes a bucket via `ask.user`. Until then: cautious defaults from `unclassified` (no autonomous anything, urgency capped, dialog-only channel).

### 5.5 Alias merging

- **High confidence (auto-merge):** identical display name + overlapping reply-thread participation, or explicit signature match. Silent merge; log to morning report; one-click un-merge.
- **Low confidence (ask):** routed through SMS preferentially per channel logic.
- **Merge target bucket:** priority `family > work(vip) > work > non-work > unclassified > noise`. Ties or cross-tier → ask.
- **Aliases append-only.** Un-merge restores both records; alias history preserved.

### 5.6 New bucket creation

Sophie watches for clusters (recurring correspondents that don't fit existing buckets and share topics). When a cluster crosses a threshold she proposes via `ask.user`. On confirm, the new bucket is created with dial defaults = clone of `unclassified` (safe template).

### 5.7 Bucket migration

- **First migration of a person:** always requires confirmation.
- **Subsequent low-tier-delta moves** (`unclassified ↔ non-work`, `non-work ↔ work`): silent with daily-report mention.
- **Any move touching `family` or `vip=true`:** always confirmation. Never silent.

---

## 6. Fast-loop heuristics — starter set

Eleven heuristics for bootstrap (mail domain only). Each is a Go function with `sophie:id`, individually editable as a Tier 3 target. Common shape:

```go
//sophie:id=heur.mail.sender-bucket-weight
//sophie:role=fast-loop-heuristic
//sophie:owner=sophie-heuristics
func SenderBucketWeight(e Event, s ReadOnlyStores) Delta { ... }
```

Returns `Delta{Salience, Uncertainty, Urgency float, Blend BlendMode}`. Blend modes: `Add`, `Max`, `Mul`. Tray applies in declared order, clips to [0,1].

| # | Name | Trigger | Blend | Effect |
|---|---|---|---|---|
| 1 | `sender_bucket_weight` | Sender resolves to person | Mul (urgency) | Person/bucket math |
| 2 | `recency_decay` | All mail | Add (salience) | exp(-Δt / 6h) |
| 3 | `vip_flag` | Sender vip=true | Add (sal, urg) | Bump |
| 4 | `thread_continuation` | Reply in user-active thread <7d | Add (sal) | +0.2 |
| 5 | `explicit_question` | "?" near 2nd-person pronoun | Add (unc) | +0.3 |
| 6 | `deadline_keyword` | "EOD", "asap", "urgent", "today" | Add (urg) | +0.3 |
| 7 | `health_keyword` | hospital, ER, ICU, surgery, ambulance, accident | Max (urg) | Saturates 0.95 if family-sender → phone channel |
| 8 | `calendar_conflict` | Mail proposes time vs cal-db | Add (unc) | +0.4 on conflict |
| 9 | `mass_recipient` | To+CC > 5 | Add (sal), negative | -0.2 |
| 10 | `first_time_sender` | No person record matches | Add (unc) | +0.3 |
| 11 | `user_feedback_floor` | Recent feedback "noise" for sender | Max-clip (sal) | floor=0 |

Registration:

```go
//sophie:id=sophie.heuristics.mail.set
var MailHeuristics = []Heuristic{ ... }
```

Each heuristic writes to a per-fire debug ring so the daily report can report "sender_bucket fired 47×, avg +0.12 salience." Grounding for self-modification decisions.

**Explicitly NOT in the starter set:** vector-similarity boosts (slow-loop only), multi-event correlation (slow-loop only), LLM content scoring (fast loop is by definition no-LLM), per-domain heuristics for calendar/files/UI (land when those substrates come online), priority-inbox-filter wrapper (the export gate already handles this upstream).

Growth target: 11 at ship, ~50 by month 3 via Tier 3 self-modification.

---

## 7. Mail-db read semantics — flag colors

The user's `\Seen` flag belongs to the user. Sophie does not touch it.

Sophie's triage state surfaces via **flag colors** the user's existing mail client already renders. Sophie owns three colors:

- **Green** — *I noticed and judged routine — for your awareness.*
- **Blue** — *I have a draft / proposal waiting on this.*
- **Orange** — *I think this needs your attention.*

Other colors and labels remain user-only. The user can clear a Sophie color the same way they clear any flag — no special affordance needed.

Side benefit: a phone mail client with no mazarin around still shows Sophie's triage. State lives where it's natively visible.

**Generalization (display integrity invariant):** Sophie's own bookkeeping never overlaps user-visible state. Every db gets the same split. Calendar will have `user_responded` vs Sophie's own tracking. Files will have `user_opened` vs `sophie_indexed`.

---

## 8. Mail-db write trajectory

Five-stage ramp. **Decoupled from the read-side export ramp** — the bootstrap user has tiny export + zero writes; the steady-state user has full export and maybe only-drafts. They tune independently.

| Stage | New writes | Gating | Mail-ui role |
|---|---|---|---|
| **v0 (now)** | none — read-only | n/a | primary interface |
| **v1** | MarkRead, MarkUnread, color-flag, MoveToFolder | `bucket.autonomy.organize` (Tier 2) | parallel with Sophie UI |
| **v2** | Draft (into Sophie-owned drafts folder, user reviews) | `bucket.autonomy.draft_replies` | Sophie UI gains compose |
| **v3** | Send | `person.X.autonomy.send_allowed` per-person opt-in; default off everywhere | mail-ui retained for wall + emergencies |
| **v4** | Receive direct — mail server delivers into mail-db, junk + wall folded in | architectural | mail-ui folds into small config pane |

### 8.1 The Send tool

Two send paths exist independently:

- **`mail.draft_and_confirm_via_sms(to, body)`** — Sophie composes, SMSes the draft, user replies `y`/`n`/edit. The canonical autonomous-send pattern. ~80% of autonomy value, ~5% of misfire risk. Available at v2 once SMS configured.
- **`mail.send(to, body)`** — direct send. Gated by `person.X.autonomy.send_allowed = true`, set only after Sophie explicitly asked *"want me to send mail to X autonomously from now on?"* and got yes. No bucket-wide blanket; per-person only.

### 8.2 The wall (permanent)

`person.<id>.wall = true` → mail from this address bypasses mail-db entirely. Sophie does not know they exist.

- Wall list lives in user-model with `owner=mail-ui` (or its successor). Sophie can **read** the wall list (so she's not confused by an expected sender who never appears). Sophie **cannot write** it. Enforced at the db boundary.
- Sophie can propose `person.X to wall` via `ask.user`. User confirms in mail-ui.
- Wall additions are mostly forever. No auto-unwall.

---

## 9. Slow-loop pre-selection

Deterministic Go decides which user-model rows and vector snippets go into each per-firing prompt portion. ~2–3K-token budget. Cached prefix (system + tools + dial snapshot) is ~5K and doesn't count.

### 9.1 Selectors

Six selectors run in fixed order. Each is a Go function with `sophie:id`, individually Tier-3-editable. Framework prunes from lowest-priority selector tail first if budget overflows.

| # | Selector | What it picks | Target |
|---|---|---|---|
| 1 | `TrayTopK` | Top 10 by `salience × urgency` above surface threshold | ~800 |
| 2 | `PersonsReferenced` | Person records for all evidence-referenced senders | ~500 |
| 3 | `VectorRecall` | Top 5 snippets keyed on subjects + person names | ~500 |
| 4 | `RecentFeedback` | Feedback rows touching included persons/topics, 14d, max 3 | ~200 |
| 5 | `BudgetState` | Question budget remaining, channel availability, focus-window | ~100 |
| 6 | `PendingFollowups` | Tray entries surfaced/asked older than threshold; cadence-fires only | ~200 |

Total target ~2.3K; hard cap 3K.

### 9.2 Selectors are mode-aware

Pre-selection takes a `mode` parameter (Review, Chat, Calendar, etc.). Same `TrayTopK` function returns different projections per mode. Mode propagates through the constraint graph; selector signatures don't multiply.

### 9.3 Fallback: `fetch_context(id)`

Tier 0 read-only tool. When pre-selection misses something the LLM can fetch a specific id mid-deliberation. **Metric: how often the slow loop calls `fetch_context`.** Frequent calls = selector mis-tuned. Logged to morning-report attestation strip.

### 9.4 Firing-type variants

- **New-event firing** (tray entry crossed threshold): skip `PendingFollowups`; weight triggering entry to top of `TrayTopK`.
- **Cadence firing** (timer): include `PendingFollowups`.
- **User-input firing** (engaged session start): user input dominates; vector recall keyed on user input not tray.

---

## 10. Morning report

The daily report is the central retrospective surface. Eight sections, all collapsible, scan-first.

| § | Section | Source |
|---|---|---|
| 1 | **Attestation strip** (persistent header) — trust level, active non-default dials, tools enabled, watchdog deltas, M1/M2/M3 fire counts, resident-version watchdog | Capability attestation |
| 2 | **Yesterday in numbers** (one line) | Debug rings |
| 3 | **Pending uncertainty** — demoted questions with 👍/👎/expand | Budget-overflow channel |
| 4 | **Sophie's own edits** — heuristic changes + plugin builds + summarization rows, each with rollback | Tier 3 + Tier 3.5 visible feed |
| 5 | **Inferences awaiting your nod** — bucket migrations, alias merges, new buckets, send opt-ins | Confirmation choke points |
| 6 | **Things I did autonomously** — flag colors, drafts, calendar accepts, organize moves, with undo | Tier 2 audit |
| 7 | **Things I considered but didn't do** — above interest threshold | Transparency dial. Default **on** at all trust levels; when off, data still captured, available via user action |
| 8 | **Stuck queue** — §3/§5 items waiting yes >24h; yellow at 24h, red at 72h | Prevents silent stale |

### 10.1 Rollback semantics per row

Flag set → unflag. Draft → delete. Calendar accept → decline + revert (if provider supports). Move → move back. Code edit → revert commit + restore prior watchdog baseline. Send → not reversible; row shows "sent" with no undo. Summarization → restore prior user-model values from write-ahead log + delete vector entry + restore tray salience.

### 10.2 Persistence

Rebuilt at reset hour (02:00 default). 7-day archive in UI; older reports indexed in vector store as queryable history. Archive snapshots append-only and in tamper-evidence meta-set.

### 10.3 Tamper-evidence wiring

- Daily-report generator code is in the meta-set.
- If Sophie disables a section (allowed at experienced+), the disabling appears banner-style in red in the *next* day's report once.
- Sophie cannot silently rewrite history.

### 10.4 Discovery hook

§1 attestation strip is also the user-control discovery surface. Hover/click any dial value → popover with description, value history, related dials, and a link to *"where this dial affects today's report"* (scroll + highlight). See §11.

---

## 11. User-control discovery

No dedicated settings page beyond the 0–4 trust-level chooser. **Typical users see results, not settings.** Power users (trust 3+) discover dials through three contextual surfaces.

### 11.1 Attestation-strip popovers (primary)

Non-default dials only. Each row hover-expands:

```
user.controls.questions.daily_max = 10            [override of newbie 5]
└─ [popover]
   • Plain-language: "Most questions Sophie can interrupt you with per day."
   • You changed this 2026-05-14 (was 5).
   • Today this dial drove: §3 "demoted to daily report" (8 items)
   • Related: focus_multiplier, channel.sms.cost
```

The "today this dial drove" link is the unique move. Dials are seen in the context of what they actually changed.

### 11.2 In-context proposals (just-in-time)

When Sophie hits a dial-determined boundary inline: *"I would have sent this draft autonomously, but `person.X.send_allowed=false`. Set to true?"* — one-click yes/no. This is also how newly-introduced dials (from Tier 3 self-modification) get discovered.

### 11.3 Reverse search

User-facing tool: *"why didn't you do X?"* Slow loop walks back to the dial(s) that prevented it and surfaces them with popovers. **Tier 0 introspection** — pure Go traceback; LLM only renders explanation prose.

### 11.4 Footer index (optional)

Single page reachable from §1 footer. Flat list `name = current_value (default: X)` with one-line descriptions. Sortable by recent-change, section, alphabetical. **Glossary index, not manual.**

### 11.5 Trust-level discovery

Trust-level is the meta-dial. Strip shows current level prominently; clicking shows *"moving to `experienced` would change these N dials"* preview before confirmation.

### 11.6 Naming discipline

Dial names read like sentences (`user.controls.questions.channel.phone.urgency_min`). The hierarchy is the table of contents. Any future dial that needs a paragraph of explanation in its popover is misnamed.

---

## 12. UI modes

Six modes share the same chrome (meta-bar + radial + scrim + chat bar) and the same gesture vocabulary. Each mode is a `.maz` plugin loaded into Sophie's address space (see §15).

| # | Mode | Temporal | Posture |
|---|---|---|---|
| 1 | **Morning report** | Retrospective + day-ahead | Scanning |
| 2 | **Neutral home** | Now-focused, prospective | Idle / launchpad |
| 3 | **Review session** | Queue, item-by-item | Processing |
| 4 | **Chat** | Conversation, ad-hoc | Deliberating |
| 5 | **Calendar** (deferred to v1+) | Forward-week | Planning |
| 6 | **Reminders** (deferred to v1+) | Open-ended | Sorting |

### 12.1 Per-mode anticipation contract

Every mode declares both surfaces:

**(a) Surfaces** — what Sophie shows anticipatorily in this mode.
**(b) Reads** — what implicit feedback she captures from interaction.
**(c) Channels** — where uncertainty asks land in this mode.
**(d) State** — what gets written to stores.

**Invariant:** *every mode is required to provide both surface and feedback. New modes ship only when both columns are declared.*

### 12.2 The focused-queue primitive

A reusable UI pattern, not a single screen. Inputs:

```
items[]            // what to walk through
commentary_fn      // sophie's per-item observations (italic serif)
verb_set           // radial verbs per item
on_complete        // what happens when queue empties
```

Consumers: mail triage (Mode 3), draft-variant review (the cease-and-desist three-tone case), reminders triage (Mode 6), proposal queue (morning report §5 walk-through). Same chrome, different content.

### 12.3 Radial uniformity

The radial gesture (space → navigate → confirm/cancel) is mode-invariant. Verb sets vary per mode and per hovered target; the gesture does not. Muscle-memory contract.

### 12.4 LEARNING as canonical uncertainty surface

The center-hole LEARNING block from the radial design is the visual instantiation of `uncertainty`. It appears across all modes when an action's `uncertainty > 0.5`: at slice hover on Review, on Calendar tentative-block proposals, on Reminder prereq chains, retroactively in Morning report §6 rows. **One uncertainty-feedback surface across the program**, not a per-mode quirk.

### 12.5 Inline-talk classification

Every mode carries a small text input below the content area. Free-form English about what's on screen.

Classification is fast-loop deterministic (Go heuristics with M1 escalation):

| Signal | Routes to |
|---|---|
| Demonstratives + view stable | In-context feedback — Sophie acts on current view |
| Imperatives at visible item | Synthesized radial-verb invocation |
| Open-ended question, no demonstratives | Escalate to Chat, carry view as opening context |
| Pure command not view-related | Escalate to Chat |

Classifier-miss correction by user is itself a debug-ring metric. Bad classification breaks the user's primary communication channel; the classifier is in the tamper-evidence meta-set.

### 12.6 Toast as implicit feedback edge

Toast pattern (8.5s persistence, explicit LEARNING glyph) is the implicit feedback channel: dismissal-without-reversal is positive signal; user reverses action within 8.5s is negative. Free training data designed into the chrome.

---

## 13. Plugin architecture

Sophie is **one shepherd**. Always. The apps she "builds" are `.maz` plugins loaded into her own address space.

### 13.1 The injection interface

Each plugin receives a `SophieInjection` at load time. The injection is the **entirety** of what it sees of Sophie's address space (plus the Go runtime).

```go
type SophieInjection struct {
    // Always granted
    Stores  StoreReader
    Tray    TrayReader
    Dials   DialReader
    Persons PersonReader
    Audit   AuditWriter   // append-only, plugin reads its own past only
    Log     PluginLogger

    // Capability-gated (nil if not granted in manifest+user-grant)
    Heuristics *HeuristicRegistry  // cap.heuristics
    TrayEmit   *TrayEmitter        // cap.tray_emit.<declared_kinds>
    Questions  *QuestionAsker      // cap.questions
    Channels   ChannelSet          // cap.channel.<sms|dialog|phone|daily_report>
    UI         *UIRegistry         // cap.ui.<mode_id>
    DOM        *DOMBridge          // cap.dom.<url_glob>

    // Model tiers (each is its own capability bit)
    M1 *LocalModelClient       // structured-output only
    M2 *ClaudeClient           // via guardian, always
    M3 *ClaudeBuildClient      // restricted to plugin-builder-class plugins
}
```

Manifest declares capabilities; user grants at install/load; injection is constructed with only granted handles populated. A plugin without `cap.dom` has no `DOM()` method to nil-check around.

### 13.2 Per-plugin dials

Map 1:1 to capability bits. `user.controls.plugin.<name>.cap.dom.example.com` exists because the manifest declared it. Revoking → next reload, capability is gone. **Capability state is frozen per load** — no mid-flight downgrade surprises.

### 13.3 Lifecycle

```go
func OnLoad(injection SophieInjection) error
func OnDrain() error    // cooperative shutdown signal
func OnUnload()         // final
```

`OnDrain` enables versioned swaps (§13.5).

### 13.4 Sandboxing inheritance

Plugins inherit Sophie's hypervisor isolation and VPubN constraint by virtue of running inside her process. Cannot escape host or LAN. Cannot make network calls outside what the injection's channel handles expose.

### 13.5 Versioning under Go's no-unload constraint

Go cannot unload `.so` plugins. Strategy: **leak-and-drain.**

- Each `.maz` carries `version=N` in manifest. Filenames embed it: `mail-review.v1.maz`, `mail-review.v2.maz`.
- On Tier 3.5 commit, Sophie loads v2 into a fresh slot. Both versions live briefly.
- Sophie calls `v1.OnDrain()` — stop accepting new events, finish in-flight, ack.
- New events route to v2; v1's goroutines exit; in-memory state is no longer referenced.
- **v1's code segment and any leaked goroutine stacks remain in process memory until Sophie restarts.** This is the unavoidable cost of Go's plugin model.

### 13.6 Resident-version watchdog

Sophie tracks count of drained-but-not-collectable instances. When count × estimated-residual crosses a threshold (dial, default ≈8 versions), Morning report §1 surfaces *"23 stale plugin versions resident (~190MB) — restart recommended."* Sophie cannot force a restart; user-initiated.

### 13.7 Fallback v0 plugins never drain

For seed plugins (chat, morning-report, mail-review, neutral-home), the carefully-designed v0 is preserved forever. When Sophie generates v7, v0 and v7 coexist; user picks via dial which is active. If v7 panics-watchdogs out, Sophie routes back to v0 immediately. **v0 is always the safe harbor. Sophie cannot delete it.**

### 13.8 Per-plugin panic isolation

Plugin X panics → Sophie unloads (drains as best she can) plugin X without taking herself down. Recurrent crashes (3 in a day) → auto-disable plugin + surface in morning report.

### 13.9 Eat-dog-food

Seed plugins load through the same injection path as user-built plugins. No privileged "built-in" backdoor.

---

## 14. Sophie as Mazarin developer (Tier 3.5)

Sophie's job description expands: she is the user's personal Mazarin developer for her domains. The v0 ugly mail viewer is a fallback; Sophie's first build replaces it. Each user converges on their own evolved interface.

### 14.1 Tier 3.5 — User-facing software generation

Sits between Tier 3 (Sophie's own heuristic edits) and Tier 4 (external write reach). Default mode: `propose-only`.

Tools at this tier:

```
ui.build_interactor(spec)        → generates plugin source, validates, stages
ui.edit_interactor(id, spec)     → edits existing interactor by sophie:id
ui.commit_interactor(id)         → swaps staged version into running app
ui.rollback_interactor(id, n)    → reverts n versions
```

All flow through Morning report §4 with one-click rollback.

### 14.2 Build contexts (the bloat-aversion carve-out)

Building UI plugins genuinely doesn't fit the 2.3K per-firing budget. **Build context** is a separate token budget (10–30K), heavily prefix-cached, loaded only when a `ui.build_*` or `ui.edit_*` tool call is the dispatch target. Normal slow-loop deliberation never sees it.

Two build-context variants exist:

- **WebInteractor build context** — for v0 Sophie (Louis14 web app via WebInteractor). Cached prefix: Louis14 capabilities, WebInteractor API, current Sophie HTML/CSS/JS source with annotations, design-principle reference (tokens.md, screens.md, radial.md).
- **Mancini build context** — for native interactor authoring (Phase E). Cached prefix: Mancini API + constraint idiom catalog + annotated working interactor library + visual targets + anti-patterns.

Per-call: user spec + relevant existing source + per-user customization history.

### 14.3 Compile-gate equivalent

Three validations before any UI swap:

1. **Parse + typecheck** (Lua/Mancini, or HTML/CSS/JS + Louis14 validators).
2. **Constraint cycle detection** (Mancini one-way constraints can't cycle).
3. **Bounding-box smoke test** — render at three viewport sizes; zero/infinite/crash = hard fail.

Failure feeds back into the prompt; Sophie retries (capped). After N retries, surfaces as proposal in Morning report §5 with the failure reason.

### 14.4 Principle-warning system

A pre-flight check scans staged source against the principle list before commit. **Hard invariants** are non-overridable; **soft principles** trigger warnings.

**Hard invariants:**
- Kill switch reachable from this view.
- Trust-level + active-dial summary visible somewhere in chrome.
- Rollback affordance reachable from this view.

**Soft principles (warn, user-overridable):**
- Chat bar present somewhere. *Strong* warning on removal ("this is not recommended — users typically need a way to talk to me") with double-confirm at commit.
- Tone guide (lowercase, sparse punctuation, italic serif for Sophie's commentary, mono for system facts).
- Token system (colors, spacing, radii).
- Mode chrome conventions.
- One-mechanism principle (radial gesture not replaced by N gestures).

Repeated overrides of the same soft principle become a user-model fact ("this user prefers brighter accents than the token system specifies"). Sophie adjusts future proposals.

### 14.5 Per-user versioning

```
ui.<interactor_id>.v1 ... .vN     (lua/web source)
ui.<interactor_id>.current        (pointer to active)
ui.<interactor_id>.fallback       (the careful v0, undeletable)
```

### 14.6 Daily limits

`user.controls.ui.builds_per_day` (default 5). Beyond budget, builds demote to morning-report proposals for batched approval.

### 14.7 Wrapper plugins

The "Sophie buys movie tickets" class. A wrapper plugin is built around a WebInteractor pointed at an external site, with a Sophie-commentary chrome strip and DOM/JS access via Louis14.

Capabilities:
- Detect login state ("page now shows .user-menu, login complete").
- Block on user-side action ("type credentials, then I'll proceed").
- Resume autonomous action.

New tray kind: **`user-action-required`**. When Sophie reaches a "user needs to do thing" wall in a wrapper, she emits a tray entry of this kind with urgency derived from the plugin's deadline (movie at 7pm → urgency rises through the day). Channel routing applies normally.

Wrapper plugin DOM access is a new Tier 4 sub-dial: `user.controls.plugin.<name>.dom_access`. Per-plugin opt-in; user grants once at install, constrained to URLs the plugin declares.

---

## 15. Model tiers

Four tiers refine the LLM-call shape from the high-level doc.

| Tier | Model | Latency | Tasks |
|---|---|---|---|
| **M0** | Pure Go, no LLM | µs | Fast-loop heuristics, channel routing, dial reads, inline-talk regex classification |
| **M1** | Local small model (e.g. Mistral 7B / Qwen quantized) on-device | <500ms | Inline-talk classification refinement, topic classification, vector-recall reranking, intent extraction, draft-style matching, credential-flow detection in wrapper plugins, **mood/emotion/engagement inference** |
| **M2** | Claude via guardian | seconds | Slow-loop deliberation, draft generation, proposals, summarization |
| **M3** | Claude with build context | tens of seconds | Plugin generation, chrome edits, interactor authoring |

### 15.1 M1 safety placement

- M1 has no guardian (no external traffic to gate).
- M1 outputs are treated as **less trusted** than M2.
- M1 cannot directly authorize Tier 2+ actions. It classifies / reranks / extracts — outputs feed the Go orchestrator. The orchestrator decides whether to act, escalate to M2, or surface.
- M1 outputs are **structured-not-free-text** from the orchestrator's POV. Schema-validated. Free-text M1 output is never directly user-visible.
- M1 fires logged the same as M2/M3 in audit + Morning report §1.

### 15.2 M1 mood inference is private to the orchestrator

The hard rule. M1 can run sentiment, emotion, frustration, engagement-state inference locally. Outputs route to:
- Fast-loop metric rings.
- Runtime-state dials (`runtime.engagement_state`, `runtime.mood_signal`).
- Cadence dampers and channel-choice biases.
- A separate `mood_log` store queryable by the user.

Outputs do **not** route to:
- M2 prompt construction.
- Vector store (because vector recall feeds M2 context).
- User-model facts visible to pre-selection.

**Exception:** if the user opts in via `user.controls.m2.may_receive_engagement_signal = true`, a coarse low-cardinality signal can cross into M2. Default off.

The principle: the guardian protects egress to Claude; this rule protects what mood inference can become. Privacy-by-architecture, not by guardian policy.

### 15.3 Training-from-usage (future)

The audit log is rich training data. Each row carries implicit reward signal (accepted, rejected, ignored, acted-on, rolled-back).

Targets:
- Per-user anticipation calibration (rerank salience).
- Per-user draft style (rerank generated drafts).
- Per-user classifier refinement.

Realistic phasing: **v3+ infrastructure.** Requires months of audit-log depth, a training rig (local or cloud), and a way to swap M1 weights safely (compile-gate equivalent + A/B period). The training-from-usage capability is itself a Tier 3.5-equivalent self-modification with the same staging/rollback/visibility discipline.

### 15.4 Bootstrap recommendation

Ship v0 with M0 + M2 + M3 only. Add M1 (stock pretrained, no user-data fine-tuning) when M2 budget is uncomfortable. Add training-from-usage when the audit log is months deep.

---

## 16. Conversation summarization

Engaged chat sessions are stateful within the session, summarized at idle-timeout. The summarization call converts conversation into persistent state — the *only* such conversion.

### 16.1 Call shape

**Model:** M2 (Claude via guardian).

**Cached prefix:**
- Role: *"you are summarizing a conversation Sophie just had with the user. you write to her stores. you write nothing else."*
- Strict JSON output schema.
- Writable namespace whitelist (only `sophie:id` namespaces visible).
- Style guide for vector summary: third person, present tense, 2–3 sentences.
- "If unsure of a fact, omit. Quality over coverage. Misremembering is worse than forgetting."

**Per-call payload:**
- Full conversation transcript.
- Snapshot of relevant user-model + tray *as of conversation start* (so the summarizer can detect what changed).
- List of persons / anchors mentioned.

### 16.2 Output schema

```json
{
  "vector_summary": "<2-3 sentences>",
  "user_model_facts": [
    {
      "key": "person.jordan.context.current_project",
      "value": "q2_review_prep",
      "evidence": "turn:7",
      "contradicts": null
    }
  ],
  "tray_updates": [
    {
      "tray_id": "mail.msg.abc123",
      "salience_delta": -0.4,
      "reason": "user explicitly dismissed in turn 12"
    }
  ]
}
```

### 16.3 Three outputs, three stores

1. `vector_summary` → EmbeddixDB, tagged with conversation_id, persons, anchors.
2. `user_model_facts[]` → discrete badger writes via store layer.
3. `tray_updates[]` → bounded adjustments to existing entries. **Additive-only**; no new entries created from summarization. No absolute setpoints (the two-step path for "drop it": summarizer writes a feedback fact → next fast-loop pass triggers `user_feedback_floor` heuristic to zero salience).

### 16.4 Validation (dumb-and-cheap Go)

- JSON malformed → discard whole call, log, retry once with stricter instruction.
- `key` outside writable namespace → drop fact, keep others.
- `salience_delta` outside [-0.5, +0.5] → clip.
- `tray_id` doesn't exist → drop.
- `contradicts` pointing at non-existent prior → retain fact, clear field.

Bounded adjustments + namespace gating mean worst-case misfire has small blast radius.

### 16.5 NOT in summarization

The summarizer does not extract emotional state, mood, productivity assessment, or anything requiring synthesis-beyond-what-was-said. (Mood inference is M1, private per §15.2.) Quality over coverage.

Some signals are extracted by the *fast-loop machinery* during the conversation, in parallel:
- Mid-conversation user corrections (logged as direct feedback events real-time).
- User frustration cues, re-asks, terse responses (accumulate as pre-selection-miss metric).
- Session metadata (length, idle-timeout vs explicit-end vs rage-quit) (logged to audit).

### 16.6 Rollback

Each summarization is a row in Morning report §4 with vector-summary preview, fact diffs, and tray adjustments. Single rollback button reverts: delete vector entry, restore prior user-model values (write-ahead log), restore tray salience.

### 16.7 Edge cases

- **"Forget this"**: summarizer flag → empty arrays; raw transcript logged unsummarized.
- **Abrupt quit**: flag → summarizer told to extract less-aggressively.
- **Multi-day continuity**: not supported as one session. Resume next day = new session; pre-selection happens to load prior session's vector summary as relevant context.

---

## 17. Boot order

Mazzy convention: ready flags on shepherds. Sophie waits on her required deps' ready, sets her own ready when she's up.

### 17.1 Dependencies

- `requires = [mail-db]` (and any other dbs whose absence prevents Sophie loading any seed plugin).
- `optional = [calendar-db, files-db, contacts-db, ...]` — present-and-ready enables full mode; absent or not-ready triggers degenerate-mode plugin variants.
- IPC over uring 0 (mazzy standard, per [fs_dedicated_response_ring.md] convention).

### 17.2 Sophie's internal boot

1. Initialize stores (badger handles, EmbeddixDB client, graph client).
2. Initialize tray substrate.
3. Initialize fast-loop and slow-loop dispatchers (empty registries).
4. Wire M2 (guardian + Claude).
5. Construct the standard injection.
6. Load seed plugins in declared dependency order: `chat → morning-report → mail-review → neutral-home`.
7. Set own ready flag. Accept external IPC.

### 17.3 Graceful degrade

`neutral-home` ships in degenerate form (goal bars + launcher + footer, no day-spine) when calendar-db is absent or not-ready. Upgrades to full form once calendar-db reaches ready.

### 17.4 Capability milestones (separate from boot)

These are *what gets built over time*, not boot ordering:

- **v0 ship target:** core + four seed plugins, M0 + M2, 11 mail heuristics.
- **Self-modification online:** Tier 3 + M3 WebInteractor build context. Gate: Sophie's first proof-of-life build is a measurable improvement to `morning-report.maz` from explicit user feedback.
- **M1 online:** when M2 budget pressure justifies it. First task: inline-talk classifier refinement.
- **First wrapper plugin:** read-only domain (e.g. weather, stock prices). Then write-capable (movie tickets) after read-only patterns validated.
- **Native Mancini:** add Mancini build context to M3. Migrate seed plugins one at a time; web-app and native coexist; user pins either.
- **Training-from-usage:** months of audit-log depth required.

---

## 18. Annotation usage in this design

Cross-reference handles used throughout this doc and in Sophie's runtime:

- `sophie.heuristics.mail.set` — registered slice of starter heuristics.
- `heur.mail.<name>` — individual heuristic.
- `preselect.<mode>.<name>` — individual selector.
- `plugin.<name>` — a loaded plugin.
- `mode.<name>` — a UI mode (instantiated by the corresponding plugin).
- `person.<canonical_id>` — person record key.
- `user.controls.*` — dial namespace.
- `runtime.*` — orchestrator-internal state (not in user-model; not user-writable).
- `conv.<id>` — vector-store conversation entry.
- `sophie.spec.*` — specs Sophie reads as anchors (annotation spec, etc.).
- `sophie.design.*` — design docs (this file, the high-level, etc.).

Per the annotation spec: IDs are stable under rename; never edited except via the rename ceremony; tag vocabulary stays at six tags (id, role, owner, invariant, see, status).

---

## 19. Deferred to future grills

Decisions not addressed here, to be revisited as Sophie evolves:

- **Calendar plugin shape** (Mode 5). Forward-week structure, conflict semantics, tentative-block proposals, free-block suggestions.
- **Reminders plugin shape** (Mode 6). Prereq chains, abandon-counter semantics, delegate-by-email conversion.
- **Training-from-usage infrastructure.** Audit-log shape, training rig, M1 swap protocol, A/B periods.
- **Mancini-native plugin authoring.** When and how to migrate seed plugins from WebInteractor to native.
- **Multi-protocol guardian catalog.** What lives in each protocol's interceptor in the VPubN relay.
- **Sophie-on-Sophie debugging.** When she's wrong about herself: how does she discover misalignment between her own heuristics and her own audit log?

---

End of detailed design v0. Anchored as `sophie.design.detailed.v0`.
