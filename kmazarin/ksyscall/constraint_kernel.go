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
	"mazzy/kmazarin/ktime"
	"mazzy/kmazarin/serial"
	"mazzy/mazarin/vm/flat"
	"mazzy/shared/hid"
	"runtime"
	"sync/atomic"
)

// Slots for kernel-published attributes.
var (
	slotTimeSeconds uint16
	slotTimeNanos   uint16
	slotScreenW     uint16
	slotScreenH     uint16
	slotDarkMode    uint16
	slotModifiers   uint16
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

// Modifier key bitmask — updated atomically from nosplit top-half IRQ handler.
var modifierState uint64
var modifierDirty uint32

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
}

// KernelAttrWriteStr writes a string value to a kernel-owned attribute.
// Allocates a string slot in the shared region. Not change-gated (intended
// for one-time writes like timezone).
func KernelAttrWriteStr(slot uint16, val string) {
	if len(val) > flat.FlatStringMaxLen {
		return
	}
	node := attrMgr.node(slot)

	nameOff, ok := attrMgr.allocString(val)
	if !ok {
		serial.RawUARTPuts("[attr] KernelAttrWriteStr: allocString failed\r\n")
		return
	}

	ref := flat.FlatStrRef{
		RegionOffset: nameOff,
		Len:          uint16(len(val)),
	}
	newVal := flat.NewStr(ref)

	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	attrMgr.dirtyPropagate(slot)
}

// TopHalfUpdateModifiers updates the modifier bitmask from nosplit IRQ context.
// Called from NonTimerIRQTopHalf for keyboard EV_KEY events.
//
//go:nosplit
func TopHalfUpdateModifiers(code uint16, value uint32) {
	var bit uint64
	switch code {
	case hid.KeyLShift:
		bit = hid.ModLShift
	case hid.KeyRShift:
		bit = hid.ModRShift
	case hid.KeyLCtrl:
		bit = hid.ModLCtrl
	case hid.KeyRCtrl:
		bit = hid.ModRCtrl
	case hid.KeyLAlt:
		bit = hid.ModLAlt
	case hid.KeyRAlt:
		bit = hid.ModRAlt
	case hid.KeyLMeta:
		bit = hid.ModLMeta
	case hid.KeyRMeta:
		bit = hid.ModRMeta
	default:
		return
	}

	if value == 0 {
		// Release: clear bit.
		for {
			old := atomic.LoadUint64(&modifierState)
			nw := old &^ bit
			if atomic.CompareAndSwapUint64(&modifierState, old, nw) {
				break
			}
		}
	} else {
		// Press or autorepeat: set bit.
		for {
			old := atomic.LoadUint64(&modifierState)
			nw := old | bit
			if atomic.CompareAndSwapUint64(&modifierState, old, nw) {
				break
			}
		}
	}
	atomic.StoreUint32(&modifierDirty, 1)
}

// PublishKernelAttributes creates kernel-owned attributes and sets initial values.
// Must be called after InitKernelAttrManager().
func PublishKernelAttributes() {
	if kernelAttrsPublished {
		return
	}
	if !attrMgr.initialized {
		serial.RawUARTPuts("[attr] PublishKernelAttributes: manager not initialized\r\n")
		return
	}

	var ok bool

	// Time attributes.
	slotTimeSeconds, ok = KernelAttrCreate("attr:///kernel/int64/time/utc_seconds", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/time/utc_seconds\r\n")
		return
	}
	slotTimeNanos, ok = KernelAttrCreate("attr:///kernel/int64/time/utc_nanos", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/time/utc_nanos\r\n")
		return
	}

	// Screen dimensions.
	slotScreenW, ok = KernelAttrCreate("attr:///kernel/int64/screen/width", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/screen/width\r\n")
		return
	}
	slotScreenH, ok = KernelAttrCreate("attr:///kernel/int64/screen/height", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/screen/height\r\n")
		return
	}

	// Dark mode toggle.
	slotDarkMode, ok = KernelAttrCreate("attr:///kernel/bool/darkMode", flat.TypeBool)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/bool/darkMode\r\n")
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

	// Input modifier bitmask (keyboard shift/ctrl/alt/meta state).
	slotModifiers, ok = KernelAttrCreate("attr:///kernel/int64/input/modifiers", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/input/modifiers\r\n")
		return
	}
	KernelAttrWriteI64(slotModifiers, 0)

	// Font metrics — AtkinsonHyperlegibleMono-Regular.otf at 16pt, 72 DPI, full hinting.
	// charWidth=10, charHeight=19 (matches stdio's font init output).
	slotCharWidth, ok = KernelAttrCreate("attr:///kernel/int64/screen/charWidth", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/screen/charWidth\r\n")
		return
	}
	slotCharHeight, ok = KernelAttrCreate("attr:///kernel/int64/screen/charHeight", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/screen/charHeight\r\n")
		return
	}
	KernelAttrWriteI64(slotCharWidth, 10)
	KernelAttrWriteI64(slotCharHeight, 19)

	kernelAttrsPublished = true
	serial.RawUARTPuts("[attr] kernel attributes published (time, screen, darkMode, modifiers, charMetrics)\r\n")
}

// timeUpdateHertz is the configured time update frequency (Hz).
// 0 or 1 means once per second (default). Set from boot config.
var timeUpdateHertz int

// StartKernelAttrUpdaters spawns goroutines that keep kernel attributes current.
// hertz is the time update frequency from boot config (0 = default 1Hz).
func StartKernelAttrUpdaters(hertz int) {
	if !kernelAttrsPublished {
		return
	}
	timeUpdateHertz = hertz
	go timeUpdateLoop()
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
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/system/ram_mb\r\n")
		return
	}
	KernelAttrWriteI64(slotRAMMB, int64(ramMB))

	slotCPUCount, ok = KernelAttrCreate("attr:///kernel/int64/system/cpu_count", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/system/cpu_count\r\n")
		return
	}
	KernelAttrWriteI64(slotCPUCount, int64(cpuCount))

	slotKernelBudgetMB, ok = KernelAttrCreate("attr:///kernel/int64/system/kernel_budget_mb", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/system/kernel_budget_mb\r\n")
		return
	}
	KernelAttrWriteI64(slotKernelBudgetMB, int64(kernelBudgetMB))

	serial.RawUARTPuts("[attr] system attributes published (ram, cpu, budget)\r\n")
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
			serial.RawUARTPuts("[attr] FAIL: kernel/str/timezone\r\n")
		} else {
			KernelAttrWriteStr(slotTimezone, tz)
		}
	}

	slotGoMemLimitMB, ok = KernelAttrCreate("attr:///kernel/int64/system/go_mem_limit_mb", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/system/go_mem_limit_mb\r\n")
	} else {
		KernelAttrWriteI64(slotGoMemLimitMB, int64(goMemLimitMB))
	}

	slotGCPercentage, ok = KernelAttrCreate("attr:///kernel/int64/system/gc_percentage", flat.TypeI64)
	if !ok {
		serial.RawUARTPuts("[attr] FAIL: kernel/int64/system/gc_percentage\r\n")
	} else {
		KernelAttrWriteI64(slotGCPercentage, int64(gcPercentage))
	}

	serial.RawUARTPuts("[attr] boot config attributes published\r\n")
}

// timeUpdateLoop updates the time attributes at the configured frequency and
// flushes modifier state changes from the nosplit top-half.
// Runs as a regular goroutine on the kernel's M/P, yielding cooperatively.
//
// At 1Hz (default): only writes when the second changes (change-gated).
// At >1Hz: writes nanos on every tick interval (nanos always changes),
// giving shepherds more frequent dirty notifications for smoother displays.
func timeUpdateLoop() {
	hertz := timeUpdateHertz
	if hertz <= 1 {
		hertz = 1
	}

	// Compute tick interval for the configured Hz using the timer frequency.
	timerFreq := uint64(ktime.GetFrequency())
	tickInterval := timerFreq / uint64(hertz)
	lastUpdateTick := ktime.ReadCounter()

	for {
		// Flush modifier bitmask if changed by top-half IRQ handler.
		if atomic.CompareAndSwapUint32(&modifierDirty, 1, 0) {
			mods := atomic.LoadUint64(&modifierState)
			KernelAttrWriteI64(slotModifiers, int64(mods))
		}

		now := ktime.ReadCounter()
		if now-lastUpdateTick < tickInterval {
			runtime.Gosched()
			continue
		}
		lastUpdateTick = now

		sec, nanos := ktime.GetTime()
		// Always write seconds (change-gated internally) and nanos
		// (always different → always propagates dirty notifications).
		KernelAttrWriteI64(slotTimeSeconds, int64(sec))
		KernelAttrWriteI64(slotTimeNanos, int64(nanos))

		runtime.Gosched()
	}
}

