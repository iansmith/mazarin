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

// [MAZ-179 probe — NOT FOR MERGE, tier 13c] MAP_FIXED resident-page
// destruction witness. MAP_FIXED is exempted by every other overlap
// tripwire (removeSpan runs first; scanForStalePTEs is file-backed-only),
// yet unmapFixedRange destroys resident pages — the next fault serves
// fresh zeros, which IS the dump signature (all-zero gcmarkBits, bolt
// page self-id 0, "plausible-value" refills after the zeroing). Go heap
// arena sysMaps live in [0x2040_0000_0000, 0x2080_0000_0000); a resident-
// destroying MAP_FIXED OUTSIDE that band ([MFIX_OFFBAND] serial marker)
// is replacing another subsystem's live memory (persistentalloc / gcbits
// band 0x2000xx) — the sharpened form of the arena-misgrant hypothesis.
var (
	MFixResidentTrips   uint32 // MAP_FIXED grants that destroyed resident pages
	MFixResidentPages   uint32 // total resident pages destroyed across those
	MFixOffBandResident uint32 // ...where the range lies outside the heap-arena band
	MFixFirstVA         uint64 // VA of the first resident-destroying MAP_FIXED
	MFixFirstLen        uint64 // length of that grant
	MFixFirstResident   uint32 // resident pages it destroyed
)

// [MAZ-179 probe — NOT FOR MERGE, tier 16] Kernel futex contract witness.
// runtime.lock/unlock is the last synchronization layer shepherds delegate
// to the kernel (MAZ-146/147/169 precedent); a broken wait/wake contract
// lets two goroutines inside one critical section — silent in-place
// scribbles (the tier-15/15b verdict: gcmarkBits dirtied in place in the
// handout→sweep window). Contract checked here:
//
//	lost wake — FUTEX_WAKE returns 0 while a thread is still blocked on
//	  exactly that (uaddr, shepherd): FtxLostWake (scan under schedulerLock).
//	spurious wake — accounting drift: FtxBlocked (waits that context-
//	  switched away) must equal FtxWokenBlocked + FtxDeadlineWakes at
//	  quiescence; a persistent gap = threads resumed without a waker.
//	timeout sanity — FtxDeadlineWakes counts deadline-fired futex wakes
//	  (vs the CNTVCT timestub); compare against DbgFutexExplicitTimeout adds.
var (
	FtxWokenTotal    uint64 // threads transitioned Blocked→Ready by wake calls
	FtxWakeZero      uint64 // wake calls that woke nothing (census; mostly benign)
	FtxLostWake      uint64 // wake-0 with a matching blocked waiter still present
	FtxLostWakeAddr  uint64 // uaddr of the first lost wake
	FtxLostWakeTID   uint64 // TID of the stranded waiter at the first lost wake
	FtxDeadlineWakes uint64 // futex waiters woken by the deadline queue (timeout path)
)

// [MAZ-179 probe — NOT FOR MERGE, tier 17] Mislabeled-continuation witness.
// Tier-16 proved (E:00 SPSR/SVD/SP1/CT fields, 4/4 fatal boots) that a
// ThreadContext pairing an EL1t kernel-style SPSR with a PC inside the
// exception-handler blob gets resumed via ctx→frame→ERET. Handler code only
// executes at EL1h, so ELR-in-blob + M[3:0]!=EL1h is poisoned by definition.
// Save side counts captures of such a pair (sites 1-2); resume side (site 3,
// badResumeRIP) counts — and badResumeHalt HALTS — before the poisoned ERET.
var (
	CtxBadSaves     uint64 // poisoned pairs captured at a save site
	CtxBadResumes   uint64 // poisoned pairs caught at a resume-guard site
	CtxBadFirstSite uint32 // 1=SaveContextFromFrame 2=SaveCurrentThreadContext 3=badResumeRIP
	CtxBadFirstELR  uint64 // ELR of the first trip
	CtxBadFirstSPSR uint64 // SPSR of the first trip
	CtxBadFirstTID  uint64 // TID of the first trip
)

// [MAZ-179 probe — NOT FOR MERGE, tier 20] Nested-resume census + IRQ-leak
// witness. Tier-19 halted 3 boots at irq_return ERETing back into the EL0
// SVC handler (post-SyscallDispatch) with SPSR EL1h + I=0 — a mechanically
// legit nested resume whose precondition (IRQs enabled inside the handler)
// is itself a leak: SVC entry masks I, so some syscall body re-enables and
// returns unmasked. Tier-20 lets the legit shape proceed (counted here) so
// a later guard catches the downstream EL1t fabrication, and records which
// syscall number leaks I=0 at dispatch return.
var (
	EretNestedResumes   uint64 // blob-ELR + EL1h ERETs allowed through (legit nested)
	EretNestedFirstELR  uint64 // ELR of the first nested resume
	EretNestedFirstSite uint32 // site char of the first ('S','E','I','C')
	DispatchIrqLeaks    uint64 // SyscallDispatch returns with DAIF.I=0 (unmasked)
	DispatchLeakFirstNr uint64 // frame X8 (syscall nr) of the first leak
	DispatchLeakLastNr  uint64 // frame X8 of the most recent leak
)

// [MAZ-179 probe — NOT FOR MERGE, tier 21] Handler-context yield witness —
// the red-hand confirmation for the tier-20 root-mechanism diagnosis.
//
// YieldToReadyThread saves a continuation with SPSR hardcoded to 0x4 ("EL1t,
// all exceptions unmasked") and SP taken from RSP under the comment "current
// stack pointer = SP_EL0 since we're in EL1t". Both statements are only true
// when the caller runs at EL1t. Called from handler context (SPSel=1, EL1h)
// the save captures the EXCEPTION stack as the thread SP and mislabels the
// mode; the later ERET then resumes that continuation at EL1t with
// SP_EL0 = an exception-stack position — matching every fatal boot's
// SPSR=0x80000004 / SP0=0xFFFFFFFF44127EC0. YieldFromHandler > 0 in a fatal
// boot is the proof; YieldHandlerFirstLR names the caller that did it.
var (
	YieldCalls            uint64 // yields reaching the context-save block (denominator)
	YieldFromHandler      uint64 // ...of those, taken with SPSel=1 (handler context)
	YieldHandlerFirstLR   uint64 // caller LR (= saved continuation ELR) of the first
	YieldHandlerFirstSP   uint64 // RSP captured as the thread SP at the first
	YieldHandlerFirstDAIF uint64 // real DAIF at the first (what SPSR should have carried)
)

// [MAZ-179 tier-22] Suspended-handler-chain enforcement + structural detector.
//
// YieldToReadyThread abandons a handler mid-flight: it ERETs away WITHOUT
// running the matching `ADD $EXC_FRAME_SIZE, RSP`, so the suspended frame
// stays live on the single global SP_EL1 exception stack (128 KB, set once at
// boot, never reset). That is only sound if handler frames retire in strict
// LIFO order — i.e. if SP_EL1 at resume is bit-identical to SP_EL1 at
// suspension. Nothing enforced it. amd64 needed explicit global IST/RSP0
// rotation cursors for exactly this (MAZ-136, "retiring the abandoning
// handler's level"); ARM64 has no equivalent.
//
// ChainDepth counts handler-context yields currently suspended. Depth >= 2 is
// the state the structural fix (refuse to park from handler context; drop via
// softIRQDroppedBytes instead) would rule out: with two suspended chains a
// resume can land on the WRONG frame and return to userspace with another
// thread's ELR/SPSR/SP_EL0. Today only accident prevents it — the thread-0-only
// yield and pushStringFull's single-blocker policy — so ChainConcurrent > 0
// would mean the structural risk is live, not theoretical.
var (
	YieldSPEL1Expected   uint64 // SP_EL1 recorded at the most recent handler-context yield
	YieldSPEL1Armed      uint64 // 1 = a handler-context yield is outstanding (check armed)
	YieldSPEL1Mismatch   uint64 // resumes whose SP_EL1 != the recorded value (trample!)
	YieldChainDepth      uint64 // handler-context yields currently suspended
	YieldChainMax        uint64 // high-water mark of the above
	YieldChainConcurrent uint64 // times a handler-context yield began at depth >= 1
)

// [MAZ-179 tier-23 / MAZ-183 detection — NOT FOR MERGE] Span-table silent
// tracking loss witness.
//
// LockedSpanGroup has 1024 fixed slots and THREE paths that lose a live range
// with no signal to anyone (span.go): Add's final "no free slot" branch returns
// false (callers largely ignore it, and an untracked left neighbour then breaks
// coalescing for every subsequent adjacent add — the cascade); Remove's
// middle-split scans for a free slot to hold the tail remnant and, finding
// none, simply drops it; TryReserve returns 0. A live-but-untracked range is
// invisible to inAllocatedUserRegion (wrong demand-fault behaviour) and to
// shepherd-death cleanup (pages leak, then get reused under a live mapping) —
// which is the "memory reappears as fresh zeros" shape that fits boot 7's
// bleve nil-pointer corruption with every other tripwire silent.
//
// SpanOccHigh is sampled inside Add's existing coalesce scan, so measuring
// occupancy costs one increment on a loop that already runs.
var (
	SpanAddFull      uint64 // Add found no free slot — range live but UNTRACKED
	SpanSplitDrop    uint64 // Remove middle-split dropped its tail remnant
	SpanReserveFull  uint64 // TryReserve found no free slot
	SpanOccHigh      uint64 // high-water occupancy seen in any group (max 1024)
	SpanLossFirstVA  uint64 // start VA of the first loss of any kind
	SpanLossFirstLen uint64 // length of that range
)

// [MAZ-179 tier-24 — NOT FOR MERGE] Delegate send-failure witness.
//
// `klog.Errf("[DLG] uring send failed sysid=%d handler=%d ring=%d")`
// (ksyscall/delegate.go) fires when uringSendKernel cannot deliver a
// delegated syscall — ring full or target gone — and the call returns
// -EAGAIN, so the caller retries and the failure storms. Observed live on
// screen as a wall of `sysid=10 handler=4 ring=3`: Write, to the linux
// shepherd, on the dedicated stdio override ring. That is console
// backpressure at its source, and console backpressure is what drives
// pushStringFull to park through YieldToReadyThread.
//
// It has been INVISIBLE to every soak graded here: klog.Errf routes to the
// soft-IRQ console ring once linux is ready and never to serial, so
// grepping serial logs for it can only ever return zero (it returned zero
// for 20 boots while the storm was happening). Only Criticalf does an
// unconditional rawWrite. These counters ride the Criticalf heartbeat, which
// is what makes the condition gradeable at all.
var (
	DlgSendFail      uint64 // delegate sends that failed (ring full / target gone)
	DlgFailFirstSys  uint64 // sysid of the first failure (10 = Write)
	DlgFailFirstSID  uint64 // handler shepherd SID of the first
	DlgFailFirstRing uint64 // ring index of the first
)

// [MAZ-179 tier-25 / MAZ-186 detection — NOT FOR MERGE] isKernelAddr bypass census.
//
// isKernelAddr(va) == (va & 0xFFFF000000000000 != 0) short-circuits all 17
// WriteUser*/ReadUser*/CopyToUser*/ZeroUserMemory* sites in kmem/paging.go into
// a RAW unchecked load/store at that VA. Legitimate kernel callers exist
// (kernel threads passing kernel-stack pointers), so the question is not
// whether the bypass fires but WHERE it lands.
//
// Bands, given KernelVAOffset = 0xFFFFFFFF00000000:
//
//	Kern  — 0xFFFFFFFF________ : kernel image, stacks, and the sub-4GB linear
//	        map. Expected and legitimate.
//	Other — any other top-16-set prefix (e.g. the 0xFFFF0001________ high-PA
//	        mapping seen in the dcache and !YSP data). A "write user memory"
//	        primitive handed a pointer in this band writes straight through a
//	        physical alias, which is how a kernel-range VA could reach a
//	        SHEPHERD heap page — the one route by which MAZ-186 could produce
//	        MAZ-179's corruption, since shepherd heap VAs (0x2000/0x2040) have
//	        the top bits CLEAR and so never trip the bypass directly.
//
// LIMITATION: counted inside isKernelAddr, so reads and writes are pooled.
// That is deliberate — one edit covers every site, and a total of zero (or an
// all-Kern distribution) refutes MAZ-186 for this workload outright. Splitting
// reads from writes is only worth doing if Other is nonzero.
var (
	KwBypassTotal      uint64 // every bypass taken (reads + writes)
	KwBypassKern       uint64 // ...landing in the 0xFFFFFFFF band (expected)
	KwBypassOther      uint64 // ...landing anywhere else (suspicious)
	KwBypassFirstOther uint64 // VA of the first Other-band bypass
)

// [MAZ-179 tier-25] Delegate send failure: cause or consequence?
//
// Tier-24 found DLG send failures in 6/6 corrupting boots and 1/4 clean ones —
// the sharpest correlate in this investigation — but could not say which way
// the arrow points. This splits the failures by whether the handler shepherd
// still EXISTS at the moment of failure:
//
//	ShepGone — the target died; the failures are downstream wreckage.
//	RingFull — the target is alive and simply not draining its ring. That is a
//	           wedged consumer, and console backpressure is what drives
//	           pushStringFull into the handler-context yield (tier-21).
var (
	DlgFailShepGone uint64 // failures where the handler shepherd was not found
	DlgFailRingFull uint64 // failures where it was alive (ring not draining)
)
