package ksyscall

import (
	"unsafe"

	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// copyPagesFromUser copies totalBytes of user memory starting at startVA
// (in the process owning l0PA) into a fresh kernel-side byte slice, walking
// through each page and mapping its PA into the kernel scratch window as
// needed. Returns nil if any page is not demand-mappable.
func copyPagesFromUser(startVA uintptr, totalBytes int, l0PA uintptr) []byte {
	buf := make([]byte, totalBytes)
	offset := 0
	for offset < totalBytes {
		va := startVA + uintptr(offset)
		pageRem := 4096 - int(va&0xFFF)
		chunk := pageRem
		if chunk > totalBytes-offset {
			chunk = totalBytes - offset
		}
		pa := kmem.DemandMapUserPage(va, l0PA)
		if pa == 0 {
			return nil
		}
		kernelVA := kmem.MapPAToKernelScratch(pa)
		if kernelVA == 0 {
			return nil
		}
		src := unsafe.Slice((*byte)(unsafe.Pointer(kernelVA)), chunk)
		copy(buf[offset:offset+chunk], src)
		offset += chunk
	}
	return buf
}

// unmapUserPages unmaps numPages contiguous pages starting at startVA from
// the process owning l0PA, releases each page back to the buddy allocator,
// and removes the span from the owning shepherd's span list.
//
// Bug B family instrumentation: same `[munmap:FREED]` log as SyscallMunmap
// when an IPC-region (>= 0x500000000000) shared page returns to the buddy.
// SyscallFreePages calls this for fontsvc's `mem.FreePages` on cache
// pages, so without this branch the fontsvc-side releases would be invisible
// to the cross-shepherd correlation.
func unmapUserPages(startVA uintptr, numPages int, l0PA uintptr, ownerSID int16) {
	for i := 0; i < numPages; i++ {
		va := startVA + uintptr(i)*4096
		pa := kmem.UnmapUserPageWithL0(va, l0PA)
		if pa != 0 {
			paAligned := pa &^ 0xFFF
			var preRefCount int16
			var preOwner int16
			var wasShared bool
			ipc := va >= 0x500000000000
			if ipc {
				if desc := kmem.GetPageDescriptor(paAligned); desc != nil {
					preRefCount = desc.RefCount
					preOwner = desc.Owner
					wasShared = desc.Flags&kmem.PD_SHARED != 0
				}
			}
			freed := kmem.ReleasePageByPA(paAligned)
			if ipc && freed && wasShared {
				klog.Logf("[munmap:FREED] sid=%d va=%x pa=%x preRefCount=%d origOwner=%d\n",
					ownerSID, uint64(va), uint64(paAligned), preRefCount, preOwner)
			}
		}
	}
	callerShepherd := proc.FindShepherdBySID(proc.ShepherdId(ownerSID))
	if callerShepherd != nil {
		callerShepherd.Spans.Remove(uint64(startVA), uint64(numPages)*4096)
	}
}
