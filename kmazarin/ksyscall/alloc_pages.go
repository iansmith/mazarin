package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/constants"
)

// MaxAllocPages is the maximum number of pages a single SysAllocPages call can allocate.
// 32768 pages = 128MB — large enough for ramdisk backing store.
const MaxAllocPages = 32768

// SyscallAllocPages allocates kernel-tracked pages and maps them contiguously
// into the calling shepherd's address space. Pages are zeroed before return.
//
// Unlike mmap (which demand-faults from the Go heap as PageUserHeap), these
// pages are born with a proper PageType and are immediately backed by physical
// frames. This is the correct allocation path for shared-purpose memory:
// IPC ring buffers, font caches, and other pages intended for cross-shepherd sharing.
//
// Args:
//
//	arg0 = number of pages to allocate (1..MaxAllocPages)
//	arg1 = userspace page type code (constants.UserPageIPC, UserPageFontCache, etc.)
//
// Returns: base VA of the contiguous mapping on success, or negative errno.
//
//go:noinline
func SyscallAllocPages(arg0, arg1, _, _, _, _ uint64) int64 {
	numPages := int(arg0)
	userType := int(arg1)

	// Validate count.
	if numPages < 1 || numPages > MaxAllocPages {
		klog.Errf("[AllocPages] EINVAL: count=%d (must be 1..%d)\n", numPages, MaxAllocPages)
		return -22 // EINVAL
	}

	// Map userspace type code to internal PageType.
	pageType := mapUserPageType(userType)
	if pageType == kmem.PageTypeCount {
		klog.Errf("[AllocPages] EINVAL: unknown userType=%d\n", userType)
		return -22 // EINVAL — unknown type
	}

	// Get caller shepherd.
	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM — kernel context
	}
	callerSID := int16(callerShepherd.PID)

	// Reserve contiguous VA range in the caller's address space.
	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	baseVA := bumpAllocForShepherd(callerShepherd, totalSize)
	if baseVA == 0 {
		klog.Errf("[AllocPages] ENOMEM: VA space exhausted for SID=%d, wanted %d bytes\n", callerSID, totalSize)
		return -12 // ENOMEM — VA space exhausted
	}

	// Track span so demand-fault validation accepts these VAs.
	callerShepherd.Spans.Add(baseVA, totalSize)

	// Allocate, zero, and map each page.
	for i := 0; i < numPages; i++ {
		pa := kmem.AllocPage(pageType, callerSID)
		if pa == 0 {
			klog.Errf("[AllocPages] OOM at page %x/%x\n", uint64(i), uint64(numPages))
			return -12 // ENOMEM
		}

		va := uintptr(baseVA) + uintptr(i)*kmem.PageSize

		// Map into caller's address space.
		if !kmem.MapPageInProcess(callerSID, va, pa, 0) {
			klog.Errf("[AllocPages] MapPageInProcess failed\n")
			return -12 // ENOMEM
		}

		// Zero the page via kernel scratch mapping (same approach as DemandMapUserPage).
		scratchVA := kmem.MapPAToKernelScratch(pa)
		if scratchVA != 0 {
			kmem.Bzero4K(scratchVA)
			kmem.CleanPageCache(scratchVA)
		}
	}

	// TLB invalidate after all pages are mapped.
	kmem.TlbiVMALLE1()
	kmem.DsbISH()
	kmem.IsbSY()

	return int64(baseVA)
}

// mapUserPageType converts a userspace page type code to the kernel's internal PageType.
func mapUserPageType(userType int) kmem.PageType {
	switch userType {
	case constants.UserPageIPC:
		return kmem.PageIPCBuffer
	case constants.UserPageFontCache:
		return kmem.PageFontCache
	case constants.UserPageShared:
		return kmem.PageSharedIPC
	case constants.UserPageRamdisk:
		return kmem.PageRamdisk
	default:
		return kmem.PageTypeCount // sentinel = invalid
	}
}
