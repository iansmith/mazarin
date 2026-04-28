package ksyscall

import (
	"sync/atomic"

	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// vaCollisionProbeEnabled toggles the SharePages target-VA collision check.
// Default off; flip true for a focused diagnostic run.
//
// Design: only Criticalf-syncs to UART when a smoking-gun event happens
// (target VA falls outside the IPC bump region — this is the failure mode
// we want to catch). Routine in-region maps just increment a counter that
// the [status] line surfaces, so probe-on doesn't synchronously block the
// kernel under heavy SharePages bursts (the reason the original probe
// regressed render-time).
var vaCollisionProbeEnabled = true

// VA-collision probe counters (atomic). Surfaced on the [status] line and
// reset never (cumulative since boot).
var (
	probeShareInIPC  uint64 // VA in [0x500000000000, 0xC000000000) — expected
	probeShareOutIPC uint64 // VA outside expected IPC region — smoking gun
	probeMinVA       uint64 // lowest VA seen this boot, 0 = unset
	probeMaxVA       uint64 // highest VA seen this boot
)

// IPC bump pointer range. Kernel-allocated shepherd-side mappings live at
// or above 0x500000000000 (IPC region, 88 TiB). Anything below — including
// the shepherd's own Go heap arena base (0xC000000000) — is suspect: a VA
// below this means the kernel picked from the wrong allocator.
const (
	probeIPCStart  uint64 = 0x500000000000
	probeHeapStart uint64 = 0xC000000000 // mail-app Go heap base, for log clarity
)

// ProbeShareCounts returns cumulative probe stats. Used by the [status] line
// so the data appears alongside other kernel telemetry without needing
// per-call sync UART traffic.
func ProbeShareCounts() (inIPC, outIPC, minVA, maxVA uint64) {
	return atomic.LoadUint64(&probeShareInIPC),
		atomic.LoadUint64(&probeShareOutIPC),
		atomic.LoadUint64(&probeMinVA),
		atomic.LoadUint64(&probeMaxVA)
}

// SyscallSharePages maps a page from the caller's address space into
// a target shepherd's address space and caches the VA↔VA translation.
// arg0 = targetSID (shepherd to map page into)
// arg1 = callerVA  (VA of the page/ring in caller's space — need not be page-aligned)
// Returns: target VA (with offset preserved) on success, or negative errno.
//
//go:noinline
func SyscallSharePages(arg0, arg1, _, _, _, _ uint64) int64 {
	targetSID := int16(arg0)
	callerVA := uintptr(arg1)

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	callerSID := int16(callerShepherd.PID)

	if callerSID == targetSID {
		return -22 // EINVAL — can't map into self
	}

	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetSID))
	if targetShepherd == nil || targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH
	}

	pageOffset := callerVA & (kmem.PageSize - 1)
	callerPageVA := callerVA &^ (kmem.PageSize - 1)

	// Check if already cached
	if targetPageVA, ok := lookupVACache(callerSID, targetSID, callerPageVA); ok {
		return int64(targetPageVA + pageOffset)
	}

	// Resolve PA from caller's page table
	pa := kmem.WalkUserPageTableWithL0(callerPageVA, callerShepherd.PageTableL0PA)
	if pa == 0 {
		pa = kmem.DemandMapUserPage(callerPageVA, callerShepherd.PageTableL0PA)
		if pa == 0 {
			klog.Errf("[SharePages] page not mapped in caller\n")
			return -14 // EFAULT
		}
	}
	pa = pa &^ (kmem.PageSize - 1)

	// Verify ownership
	desc := kmem.GetPageDescriptor(pa)
	if desc == nil || desc.Owner != callerSID {
		klog.Errf("[SharePages] page not owned by caller\n")
		return -1 // EPERM
	}

	// Increment refcount, mark shared
	desc.RefCount++
	desc.Flags |= kmem.PD_SHARED

	// Allocate VA in target
	targetPageVAu64 := bumpAllocForShepherd(targetShepherd, uint64(kmem.PageSize))
	if targetPageVAu64 == 0 {
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}
	targetPageVA := uintptr(targetPageVAu64)

	targetShepherd.Spans.Add(targetPageVAu64, uint64(kmem.PageSize))

	if !kmem.MapPageInProcess(targetSID, targetPageVA, pa, 0) {
		targetShepherd.Spans.Remove(targetPageVAu64, uint64(kmem.PageSize))
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}

	// VA-collision probe: only sync-UART log if the target VA falls outside
	// the expected IPC bump region — that would be the smoking gun for the
	// "kernel maps font-cache page into mail-app's Go heap" hypothesis.
	// Routine in-region maps just bump counters; counts + observed range
	// show up on the [status] line. Synchronous Criticalf is reserved for
	// the actual failure mode, keeping the hot path non-blocking.
	if vaCollisionProbeEnabled {
		va := uint64(targetPageVA)
		if va >= probeIPCStart {
			atomic.AddUint64(&probeShareInIPC, 1)
		} else {
			atomic.AddUint64(&probeShareOutIPC, 1)
			klog.Criticalf("[fV]", "[fontslot:VA OUT-OF-RANGE] caller=%d target=%d va=%x type=%s\n",
				callerSID, targetSID, va, desc.Type.String())
		}
		// Track lowest/highest VA seen — surfaces drift in the bump pointer
		// across boots without per-call sync UART.
		for {
			cur := atomic.LoadUint64(&probeMinVA)
			if cur != 0 && va >= cur {
				break
			}
			if atomic.CompareAndSwapUint64(&probeMinVA, cur, va) {
				break
			}
		}
		for {
			cur := atomic.LoadUint64(&probeMaxVA)
			if va <= cur {
				break
			}
			if atomic.CompareAndSwapUint64(&probeMaxVA, cur, va) {
				break
			}
		}
	}

	// Cache the translation
	addVACacheEntry(callerSID, targetSID, callerPageVA, targetPageVA)

	return int64(targetPageVA + pageOffset)
}
