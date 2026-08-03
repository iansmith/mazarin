// constraint_kernel.go — Kernel-published attributes.
//
// The kernel creates attributes under attr:///kernel/... and updates them from
// normal Go context (not nosplit). Shepherds discover these via the existing
// deref/WaitDirty infrastructure.
//
// KernelAttrCreate bypasses the syscall path (no ownership checks — Owner=0).
// KernelAttrWriteI64/KernelAttrWriteBool use seqlock writes with change-gated
// dirty propagation, same as SyscallAttrWrite but without user-buffer copies.

package ksyscall

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/ktime"
	"mazzy/mazarin/vm/flat"
	"sync/atomic"
)

// Slots for kernel-published attributes.
var (
	slotTimeSeconds uint16
	slotTimeNanos   uint16
	slotScreenW     uint16
	slotScreenH     uint16
	slotDarkMode    uint16
	slotCharWidth   uint16
	slotCharHeight  uint16
)

// Slots for system/config attributes (set by PublishSystemAttributes/PublishBootConfigAttributes).
var (
	slotTimezone       uint16
	slotRAMMB          uint16
	slotCPUCount       uint16
	slotKernelBudgetMB uint16
	slotGoMemLimitMB   uint16
	slotGCPercentage   uint16
)

// kernelAttrsPublished tracks whether PublishKernelAttributes has run.
var kernelAttrsPublished bool

// KernelAttrCreate creates an attribute in the kernel namespace (Owner=0).
// Bypasses the syscall path and ownership checks.
// Returns (slot, true) on success, or (0xFFFF, false) on failure.
func KernelAttrCreate(uri string, valueType uint8) (uint16, bool) {
	if !attrMgr.initialized {
		return 0xFFFF, false
	}

	// Check for duplicate URI.
	if _, exists := attrMgr.trieLookup(uri); exists {
		return 0xFFFF, false
	}

	// Allocate node.
	slot := attrMgr.allocNode()
	if slot == 0xFFFF {
		return 0xFFFF, false
	}

	// Initialize node — Owner=0 means kernel-owned.
	node := attrMgr.node(slot)
	node.Owner = 0
	node.Kind = flat.AttrKindValue
	node.ValueType = valueType

	// Allocate string slot for URI.
	nameOff, ok := attrMgr.allocString(uri)
	if !ok {
		attrMgr.freeNode(slot)
		return 0xFFFF, false
	}
	node.NameOffset = nameOff

	// Insert into namespace trie.
	rc := attrMgr.trieInsert(uri, slot)
	if rc < 0 {
		attrMgr.freeNode(slot)
		return 0xFFFF, false
	}

	// Check registered query patterns for matches.
	attrMgr.updateQueryResultsForURI(uri)

	return slot, true
}

// KernelAttrWriteI64 writes an int64 value to a kernel-owned attribute.
// Uses seqlock write + change-gated dirty propagation.
// Safe to call from normal Go context (not nosplit).
func KernelAttrWriteI64(slot uint16, val int64) {
	node := attrMgr.node(slot)
	newVal := flat.NewI64(val)

	// Change-gating: skip if value unchanged.
	if node.CachedValue == newVal {
		return
	}

	// Seqlock write.
	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	// Propagate dirty to dependents. The source node is marked dirty by
	// dirtyWalk itself; no need to clear it first.
	attrMgr.dirtyPropagate(slot)
	flushPendingDirtyWakes()
}

// KernelAttrWriteBool writes a bool value to a kernel-owned attribute.
// Uses seqlock write + change-gated dirty propagation.
func KernelAttrWriteBool(slot uint16, val bool) {
	node := attrMgr.node(slot)
	newVal := flat.NewBool(val)

	if node.CachedValue == newVal {
		return
	}

	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	attrMgr.dirtyPropagate(slot)
	flushPendingDirtyWakes()
}

// KernelAttrWriteStr writes a string value to a kernel-owned attribute.
// Allocates a string slot in the shared region. Not change-gated (intended
// for one-time writes like timezone).
func KernelAttrWriteStr(slot uint16, val string) {
	if len(val) > flat.StringMaxLen {
		return
	}
	node := attrMgr.node(slot)

	nameOff, ok := attrMgr.allocString(val)
	if !ok {
		klog.Errf("[attr] KernelAttrWriteStr: allocString failed\n")
		return
	}

	ref := flat.StrRef{
		RegionOffset: nameOff,
		Len:          uint16(len(val)),
	}
	newVal := flat.NewStr(ref)

	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	attrMgr.dirtyPropagate(slot)
	flushPendingDirtyWakes()
}

// PublishKernelAttributes creates kernel-owned attributes and sets initial values.
// Must be called after InitKernelAttrManager().
func PublishKernelAttributes() {
	if kernelAttrsPublished {
		return
	}
	if !attrMgr.initialized {
		klog.Errf("[attr] PublishKernelAttributes: manager not initialized\n")
		return
	}

	var ok bool

	// Time attributes.
	slotTimeSeconds, ok = KernelAttrCreate("attr:///kernel/int64/time/utc_seconds", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/time/utc_seconds\n")
		return
	}
	slotTimeNanos, ok = KernelAttrCreate("attr:///kernel/int64/time/utc_nanos", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/time/utc_nanos\n")
		return
	}

	// Screen dimensions.
	slotScreenW, ok = KernelAttrCreate("attr:///kernel/int64/screen/width", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/screen/width\n")
		return
	}
	slotScreenH, ok = KernelAttrCreate("attr:///kernel/int64/screen/height", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/screen/height\n")
		return
	}

	// Dark mode toggle.
	slotDarkMode, ok = KernelAttrCreate("attr:///kernel/bool/darkMode", flat.TypeBool)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/bool/darkMode\n")
		return
	}

	// Write initial values.
	sec, nanos := ktime.GetTime()
	KernelAttrWriteI64(slotTimeSeconds, int64(sec))
	KernelAttrWriteI64(slotTimeNanos, int64(nanos))

	w := gpu.GetWidth()
	h := gpu.GetHeight()
	KernelAttrWriteI64(slotScreenW, int64(w))
	KernelAttrWriteI64(slotScreenH, int64(h))

	KernelAttrWriteBool(slotDarkMode, false)

	// Font metrics — AtkinsonHyperlegibleMono-Regular.otf at 16pt, 72 DPI, full hinting.
	// charWidth=10, charHeight=19 (matches linux shepherd's font init output).
	slotCharWidth, ok = KernelAttrCreate("attr:///kernel/int64/screen/charWidth", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/screen/charWidth\n")
		return
	}
	slotCharHeight, ok = KernelAttrCreate("attr:///kernel/int64/screen/charHeight", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/screen/charHeight\n")
		return
	}
	KernelAttrWriteI64(slotCharWidth, 10)
	KernelAttrWriteI64(slotCharHeight, 19)

	kernelAttrsPublished = true
}

// StartKernelAttrUpdaters initializes the tick-based time update state.
// Actual updates happen via TickTimeUpdate() called from KernelIdleLoop,
// and TopHalfTickTimeUpdate() called from ProcessDeadlinesTopHalf.
func StartKernelAttrUpdaters() {
	if !kernelAttrsPublished {
		return
	}
	timeUpdateLastTick = atomic.LoadUint64(&kirq.TimerIRQCount)
	// MAZ-172: the time-attribute cadence has its own policy knob
	// (time_update_hz) — it no longer inherits the preemption quantum.
	timeUpdateThreshold = kirq.TimeUpdateTicks

	// Set the counter-based interval for the top-half updates from the same
	// knob: one update every 1/TimeUpdateHz seconds of wall-clock time.
	if kirq.SystemTimerFrequency > 0 {
		topHalfUpdateInterval = kirq.SystemTimerFrequency / kirq.TimeUpdateHz
		topHalfLastCounter = kirq.ReadCounterValue()
	}
}

// timeUpdateLastTick / timeUpdateThreshold track when to write time attributes.
var timeUpdateLastTick uint64
var timeUpdateThreshold uint64

// TickTimeUpdate checks if enough timer ticks have elapsed and, if so, writes
// the time attributes. Called directly from KernelIdleLoop on
// every iteration — no goroutine needed.
//
// Returns true if an update was performed (dirty notifications sent).
func TickTimeUpdate() bool {
	if !kernelAttrsPublished {
		return false
	}
	currentTick := atomic.LoadUint64(&kirq.TimerIRQCount)
	if currentTick-timeUpdateLastTick < timeUpdateThreshold {
		return false
	}
	timeUpdateLastTick = currentTick

	// Update time attributes. Seconds is change-gated internally;
	// nanos always differs and propagates dirty notifications.
	sec, nanos := ktime.GetTime()
	KernelAttrWriteI64(slotTimeSeconds, int64(sec))
	KernelAttrWriteI64(slotTimeNanos, int64(nanos))

	return true
}

// topHalfLastCounter tracks the counter value at the last top-half time update.
var topHalfLastCounter uint64

// topHalfUpdateInterval is the counter tick interval for 10Hz updates.
// Set by StartKernelAttrUpdaters once SystemTimerFrequency is known.
var topHalfUpdateInterval uint64

// TopHalfFireCount tracks how many times TopHalfTickTimeUpdate actually updated.
var TopHalfFireCount uint64

// TopHalfTickTimeUpdate writes time attributes and propagates
// dirty notifications. Called from ProcessDeadlinesTopHalf under the
// scheduler lock with IRQs disabled.
//
// Uses counter-based threshold (not TimerIRQCount) for precise wall-clock
// cadence independent of timer interrupt delivery rate.
//
// After this returns, the caller must read PendingDirtyWakeCount/TIDs and
// wake those threads under the scheduler lock.
//
//go:nosplit
func TopHalfTickTimeUpdate() {
	if !kernelAttrsPublished {
		return
	}
	if topHalfUpdateInterval == 0 {
		return
	}

	currentCounter := kirq.ReadCounterValue()
	if currentCounter-topHalfLastCounter < topHalfUpdateInterval {
		return
	}
	topHalfLastCounter = currentCounter
	TopHalfFireCount++

	// Clear pending wakes from any previous propagation.
	PendingDirtyWakeCount = 0

	// --- Time seconds: seqlock write + propagate ---
	sec, nanos := ktime.GetTime()

	secNode := attrMgr.node(slotTimeSeconds)
	secVal := flat.NewI64(int64(sec))
	if secNode.CachedValue != secVal {
		secNode.SeqCounter++
		secNode.CachedValue = secVal
		secNode.SeqCounter++
		attrMgr.dirtyPropagate(slotTimeSeconds)
	}

	// --- Time nanos: seqlock write + propagate ---
	nanosNode := attrMgr.node(slotTimeNanos)
	nanosVal := flat.NewI64(int64(nanos))
	if nanosNode.CachedValue != nanosVal {
		nanosNode.SeqCounter++
		nanosNode.CachedValue = nanosVal
		nanosNode.SeqCounter++
		attrMgr.dirtyPropagate(slotTimeNanos)
	}

	// PendingDirtyWakeTIDs/Count now populated — caller wakes under sched lock.
}

// PublishSystemAttributes creates kernel-owned attributes for system info.
// Called from main with values from runtime config (avoids ksyscall→main import).
func PublishSystemAttributes(ramMB, cpuCount, kernelBudgetMB uint64) {
	if !kernelAttrsPublished {
		return
	}

	var ok bool

	slotRAMMB, ok = KernelAttrCreate("attr:///kernel/int64/system/ram_mb", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/system/ram_mb\n")
		return
	}
	KernelAttrWriteI64(slotRAMMB, int64(ramMB))

	slotCPUCount, ok = KernelAttrCreate("attr:///kernel/int64/system/cpu_count", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/system/cpu_count\n")
		return
	}
	KernelAttrWriteI64(slotCPUCount, int64(cpuCount))

	slotKernelBudgetMB, ok = KernelAttrCreate("attr:///kernel/int64/system/kernel_budget_mb", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/system/kernel_budget_mb\n")
		return
	}
	KernelAttrWriteI64(slotKernelBudgetMB, int64(kernelBudgetMB))

}

// PublishBootConfigAttributes creates kernel-owned attributes from TOML boot config.
// Called from main after readBootConfig().
func PublishBootConfigAttributes(tz string, goMemLimitMB, gcPercentage int) {
	if !kernelAttrsPublished {
		return
	}

	var ok bool

	if tz != "" {
		slotTimezone, ok = KernelAttrCreate("attr:///kernel/str/timezone", flat.TypeStr)
		if !ok {
			klog.Errf("[attr] FAIL: kernel/str/timezone\n")
		} else {
			KernelAttrWriteStr(slotTimezone, tz)
		}
	}

	slotGoMemLimitMB, ok = KernelAttrCreate("attr:///kernel/int64/system/go_mem_limit_mb", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/system/go_mem_limit_mb\n")
	} else {
		KernelAttrWriteI64(slotGoMemLimitMB, int64(goMemLimitMB))
	}

	slotGCPercentage, ok = KernelAttrCreate("attr:///kernel/int64/system/gc_percentage", flat.TypeI64)
	if !ok {
		klog.Errf("[attr] FAIL: kernel/int64/system/gc_percentage\n")
	} else {
		KernelAttrWriteI64(slotGCPercentage, int64(gcPercentage))
	}

}
