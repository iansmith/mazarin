package proc

// [MAZ-179 probe — NOT FOR MERGE] handleFlushReply tripwire counters.
// Incremented from ksyscall/mmap_writeback.go, drained by the thread-0
// status printer ([FLUSHREL] line). They live here because the status
// printer (package main) cannot import ksyscall, but both import proc.
// The per-event klog lines are console-ring-only after
// EnableSoftIRQConsole; these counters ride the serial-visible [status]
// Criticalf channel.
var (
	FlushRelRounds   uint32 // flush-reply rounds processed
	FlushRelOK       uint32 // pages legitimately released
	FlushRelUnmapped uint32 // handler VA had no PTE (garbage VA)
	FlushRelNoDesc   uint32 // released PA had no descriptor (free refused)
	FlushRelNotFB    uint32 // page was NOT file-backed (free REFUSED)
	FlushRelBadRef   uint32 // RefCount != 1 at release (visibility only)
)

// [MAZ-179 probe — NOT FOR MERGE] Double-booking tripwire counters: a FRESH
// VA grant (tryReserveHint / userBumpAlloc / bumpAllocForShepherd) whose
// range already contains mapped pages is a live-range collision — the
// span-exhaustion landmine being stepped on. Grants that trip are REFUSED.
var (
	DoubleBookTrips    uint32 // grants refused for already-mapped pages
	DoubleBookPages    uint32 // total already-mapped pages seen across trips
	DoubleBookFirstSID int32  // shepherd of the first trip
	DoubleBookFirstVA  uint64 // first already-mapped page VA of the first trip
)

// [MAZ-179 probe — NOT FOR MERGE] Delegate reply copy-back liveness gate
// (tier 6): copies skipped because the caller was dead or its page table
// was recycled between request and reply.
var (
	DelegReplyCopies    uint32 // total copy-backs attempted (exercised proof)
	DelegReplyDeadSkips uint32 // copies REFUSED: dead/recycled caller
)

// [MAZ-179 probe — NOT FOR MERGE] MmapPageFill reply SID-recycle detector
// (tier 10 / stage 1): the fill reply (delegate.go) maps a file-backed frame
// into the caller resolved by SID, with NO L0-match check — unlike the
// copy-back gate at delegate.go:1232. If the caller died and its SID was
// recycled to a different shepherd between fault and reply, this injects the
// file frame into the WRONG live process. Detection-only (still maps): measure
// how often the recycle window is actually hit during corrupt boots, to prove
// or refute this path as the concurrent writer named by the tier-9 dumps.
var (
	MmapFillTotal    uint32 // fill replies mapped (exercised proof)
	MmapFillDead     uint32 // caller gone (nil) at reply — page freed, not mapped
	MmapFillRecycle  uint32 // caller SID resolves to a DIFFERENT L0 (recycled)
	MmapFillFirstSID int32  // caller SID of the first recycle trip
	MmapFillFirstPA  uint64 // data-page PA of the first recycle trip
)

// [MAZ-179 probe — NOT FOR MERGE] TID-recycle staleness detector (tier 11).
// Unlike SIDs (monotonic since MAZ-150, cannot recycle in a boot — tier-10
// proved recycle=0), TIDs still use ds.StaticAllocator, a LIFO stack that
// reissues a freed TID as the very next allocation. The in-flight mmap-fault /
// delegate state lives in the TID-indexed delegateCallInfos[callerTID]. If a
// blocked caller thread dies and its TID is immediately reissued to a different
// thread, a new call claims the SAME slot while the prior call is still in
// flight. prepareDelegateSlotForReuse only retires the prior page when the
// owner is flagged CallerDead; a slot claimed InUse-but-not-CallerDead is a
// SILENT recycle collision — the suspected concurrent writer. Detection-only.
var (
	TidReuseClaims      uint32 // delegate slot claimed while prior call still InUse
	TidReuseSilent      uint32 // ...and prior owner NOT flagged CallerDead (silent)
	TidReuseCrossHome   uint32 // ...and new caller SID or L0 differs from the prior
	TidReuseFirstTID    int32  // TID of the first silent collision
	TidReuseFirstOldSID int32  // prior owner SID of the first silent collision
	TidReuseFirstNewSID int32  // claiming caller SID of the first silent collision
	TidReuseFirstPA     uint64 // prior in-flight DataPagePA at the first collision
)

// [MAZ-179 probe — NOT FOR MERGE] Block-completion staleness tripwires
// (tier 7): the IRQ top-half invalidates dcache over a saved KVA and
// derefs a saved *DMAClump. A freed/reused data page makes the invalidate
// DESTRUCTIVE (discards the new owner's dirty lines — corruption with no
// wrong-value write anywhere); a swap-compacted clump slot makes the
// InFlight/free ops hit the wrong clump.
var (
	BlkComplTotal   uint32 // completions processed (exercised proof)
	BlkStaleInval   uint32 // cache-invalidates SKIPPED: page freed/retyped
	BlkClumpAlias   uint32 // clump ops SKIPPED: dataKVA outside clump range
	BlkStaleFirstPA uint64 // PA of the first stale-invalidate trip
)

// [MAZ-179 probe — NOT FOR MERGE, tier 12] DMA-descriptor liveness at clump
// free. Every existing tripwire (MAP_LEAK/MAP_STALE/poison) watches PTEs —
// but virtio descriptors are not PTEs. munmapClump/CleanupShepherdDMAClumps
// unmap the PTEs FIRST (satisfying every PTE check) and then BuddyFree the
// frames; a descriptor still posted in a device ring keeps DMA-writing the
// frame after buddy reissues it. These counters witness that exact moment:
// at each clump free, the block in-flight slot table and the net RX/TX
// virtqueue descriptor tables are scanned for PAs inside the freed range.
var (
	DmaClumpFrees     uint32 // clump frees observed (exercised proof)
	DmaClumpFreePages uint32 // total pages across those frees
	DmaFreeBlkHits    uint32 // freed-range PAs found in live block in-flight slots
	DmaFreeNetHits    uint32 // freed-range PAs found in posted net VQ descriptors
	NetPoolFrees      uint32 // frees of net-pool-sized clumps (>=64 pages)
	DmaFreeFirstPA    uint64 // clump StartPA of the first hit (blk or net)
	DmaFreeFirstSID   int32  // owning shepherd of the first hit
	ShepherdDeaths    uint32 // TerminateShepherd entries (death-event census)

	// BlkSubmitWindow is a gauge, not a counter: raised for the whole
	// unlock→LookupPA→submit→Notify span of SyscallBlockSubmit. A clump
	// free observing InFlight==0 while the gauge is up proves the
	// free-vs-submit TOCTOU window (block_submit.go:100-117's own comment)
	// is genuinely reachable — the witness for ranked suspect #1.
	BlkSubmitWindow int32  // gauge: submits currently between unlock and Notify
	BlkSubmitTotal  uint32 // async submits attempted (exercised proof)
	BlkFreeRace     uint32 // clump frees that saw the gauge up with InFlight==0
)

// [MAZ-179 probe — NOT FOR MERGE, tier 13a] g↔m thread-identity coherence.
// The tier-12 negative (no external writer across 12 tripwire families)
// plus dump content (mspan mutated µs-scale with PLAUSIBLE values) points
// at the shepherd's own runtime racing itself — the ARM64 analogue of
// MAZ-135's x86 dual-home g lossiness. Invariant checked at every EL0
// context save: the g in the saved R28 must have g.m == the thread's own
// recorded M (a g running on an m always has g.m set to that m before
// gogo). A mismatch means the kernel is saving/restoring a thread whose
// goroutine identity belongs to a DIFFERENT thread — two threads acting
// as one goroutine → two sweepers on one span → exactly the observed
// impossible sweep counts.
var (
	GIdentChecks     uint32 // EL0 saves checked (exercised proof)
	GIdentUnreadable uint32 // g.m read failed (page not resident — no verdict)
	GIdentMismatch   uint32 // g.m != thread.MPtr — the smoking gun
	GIdentFirstTID   uint64 // TID of the first mismatch
	GIdentFirstG     uint64 // R28 (g) of the first mismatch
	GIdentFirstM     uint64 // g.m read at the first mismatch
	GIdentFirstWantM uint64 // thread.MPtr expected at the first mismatch
)

// [MAZ-179 probe — NOT FOR MERGE, tier 13b] VA-grant overlap witness (Ian's
// arena hypothesis: under peak boot pressure the kernel mis-grants arena
// VAs to a shepherd's several runtime subsystems). The tier-5 doubleBook
// probe checked grants against already-MAPPED pages — two overlapping
// DEMAND-PAGED grants (neither faulted yet) pass it, then both subsystems
// later fault the same VA and legitimately share frames: plausible-value
// mutual scribble, invisible to every PTE/refcount tripwire. This witness
// checks each new grant against the shepherd's SPAN LEDGER instead,
// before Spans.Add coalesces the evidence away.
var (
	GrantOverlapTrips    uint32 // non-FIXED grants overlapping an existing span
	GrantOverlapFirstSID int32  // shepherd of the first trip
	GrantOverlapFirstVA  uint64 // granted VA of the first trip
	GrantOverlapFirstLen uint64 // granted length of the first trip
	GrantOverlapFirstEnd uint64 // FindOverlapEnd result at the first trip
	GrantFixedMaps       uint32 // MAP_FIXED user grants (overlap intended; census)
	GrantHintFallbacks   uint32 // arena hints refused → bump fallback (pressure gauge)
)
