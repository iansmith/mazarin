package ksyscall

// mmap_writeback.go — Write-back of MAP_SHARED writable mmap pages.
//
// When a MAP_SHARED + PROT_WRITE file-backed mapping is munmap'd (or the
// shepherd dies), dirty pages must be written back to the file. This uses
// the "Option B" batch approach:
//
//  1. Walk the VA range, copy mapped pages into contiguous kernel buffers
//  2. Build a descriptor page with MmapWritebackEntry array
//  3. Map everything into the linux shepherd's address space
//  4. Send one MmapPageWriteback uring message
//  5. Block the calling thread until the linux shepherd replies
//  6. Clean up all buffers on reply

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/ipc"
	"mazzy/shared/sysid"
	"sync/atomic"
	"unsafe"
)

// writebackBuddyBlock records a contiguous buddy allocation used for write-back.
// Stored in writebackCleanup so the reply path can free them.
type writebackBuddyBlock struct {
	PA        uintptr // Physical address of buddy block
	HandlerVA uint64  // VA in linux shepherd's address space
	Order     int     // Buddy order (2^order pages)
}

// writebackCleanupEntry holds all the resources allocated for one batch write-back.
// Indexed by caller TID (same as DelegateCallInfo).
type writebackCleanupEntry struct {
	InUse          bool
	DescriptorPA   uintptr // PA of descriptor page
	DescriptorVA   uint64  // VA in linux shepherd's address space
	HandlerSID     int16
	Blocks         [maxWritebackBlocks]writebackBuddyBlock
	NumBlocks      int
}

// maxWritebackBlocks limits how many buddy blocks per write-back.
// 16 blocks × 16 pages/block × 4KB = 1MB per batch. Multiple batches
// are used for larger mappings.
const maxWritebackBlocks = 16

// buddyOrder is the buddy order for write-back buffers (2^4 = 16 pages = 64KB).
const writebackBuddyOrder = 4
const writebackBlockSize = (1 << writebackBuddyOrder) * 4096 // 64KB

// writebackCleanups holds per-TID cleanup state for batch write-backs.
var writebackCleanups [MaxDelegateThreads]writebackCleanupEntry

// writeBackSharedPages writes back all mapped pages in a MAP_SHARED writable
// file-backed mapping to the file via the linux shepherd. Called from munmap
// and shepherd death cleanup.
//
// This function blocks the calling thread (via delegation) while the linux
// shepherd performs the writes. Other threads/shepherds continue running.
func writeBackSharedPages(fm *proc.FileMapping) {
	if !fm.Shared || !fm.Writable || !fm.InUse {
		return
	}

	// Find the linux shepherd (Write delegate handler)
	handlerSID := int16(atomic.LoadInt32(&syscallDelegates[sysid.Write].pid))
	if handlerSID < 0 {
		klog.Errf("[mmap-wb] no Write delegate handler, data lost\n")
		return
	}
	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))
	if handlerShepherd == nil {
		klog.Errf("[mmap-wb] handler shepherd gone, data lost\n")
		return
	}

	// Walk the VA range in batches of maxWritebackBlocks × writebackBlockSize.
	startVA := fm.StartVA
	endVA := fm.StartVA + fm.Length
	batchStart := startVA

	for batchStart < endVA {
		batchEnd := batchStart + uint64(maxWritebackBlocks)*uint64(writebackBlockSize)
		if batchEnd > endVA {
			batchEnd = endVA
		}
		writeBackBatch(fm, handlerSID, handlerShepherd, batchStart, batchEnd)
		batchStart = batchEnd
	}
}

// writeBackBatch handles one batch of write-back (up to maxWritebackBlocks × 64KB).
func writeBackBatch(fm *proc.FileMapping, handlerSID int16, handlerShepherd *proc.Shepherd, batchStart, batchEnd uint64) {
	_, callerTID := getCurrentThreadSIDAndTID()
	if int(callerTID) >= MaxDelegateThreads {
		klog.Errf("[mmap-wb] TID %d out of range\n", callerTID)
		return
	}

	cleanup := &writebackCleanups[callerTID]
	cleanup.HandlerSID = handlerSID
	cleanup.NumBlocks = 0

	// Allocate descriptor page
	descriptorPA := kmem.AllocPage(kmem.PageSharedIPC, handlerSID)
	if descriptorPA == 0 {
		klog.Errf("[mmap-wb] ENOMEM descriptor page\n")
		return
	}
	cleanup.DescriptorPA = descriptorPA

	// Map descriptor page to kernel scratch for writing entries
	descriptorScratchVA := kmem.MapPAToKernelScratch(descriptorPA)
	if descriptorScratchVA == 0 {
		kmem.ReleasePageByPA(descriptorPA)
		return
	}
	zeroPage(descriptorScratchVA)

	entries := (*[ipc.MmapWritebackEntriesPerPage]ipc.MmapWritebackEntry)(unsafe.Pointer(descriptorScratchVA))
	numEntries := 0

	// Walk VA range, collect mapped pages into contiguous buffers
	va := batchStart
	for va < batchEnd && numEntries < ipc.MmapWritebackEntriesPerPage && cleanup.NumBlocks < maxWritebackBlocks {
		// Find the first mapped page in this potential block
		blockStartVA := va
		pagesInBlock := 0

		// Allocate a contiguous buffer for this block
		blockPA := kmem.BuddyAllocTyped(writebackBuddyOrder, kmem.PageSharedIPC, handlerSID)
		if blockPA == 0 {
			klog.Errf("[mmap-wb] ENOMEM buddy block\n")
			break
		}

		// Copy mapped pages into the contiguous buffer
		for va < batchEnd && pagesInBlock < (1<<writebackBuddyOrder) {
			srcPA := kmem.WalkUserPageTable(uintptr(va))
			if srcPA != 0 {
				if pagesInBlock == 0 {
					blockStartVA = va
				}
				// Copy page content via kernel scratch
				srcScratch := kmem.MapPAToKernelScratch(srcPA)
				dstOffset := uintptr(pagesInBlock) * 4096
				dstScratch := kmem.MapPAToKernelScratch(blockPA + dstOffset)
				if srcScratch != 0 && dstScratch != 0 {
					copyPage(dstScratch, srcScratch)
				}
				pagesInBlock++
			} else if pagesInBlock > 0 {
				// Gap in mapped pages — flush current run as an entry
				break
			}
			va += 4096
		}

		if pagesInBlock == 0 {
			// No mapped pages found in this block range — free and continue
			kmem.BuddyFreeTyped(blockPA, writebackBuddyOrder, kmem.PageSharedIPC)
			continue
		}

		// Map the contiguous buffer into the linux shepherd's address space
		handlerBlockVA := bumpAllocForShepherd(handlerShepherd, uint64(1<<writebackBuddyOrder)*4096)
		if handlerBlockVA == 0 {
			kmem.BuddyFreeTyped(blockPA, writebackBuddyOrder, kmem.PageSharedIPC)
			klog.Errf("[mmap-wb] ENOMEM handler VA\n")
			break
		}
		if !kmem.MapContiguousUserPages(handlerSID, handlerShepherd.PageTableL0PA,
			uintptr(handlerBlockVA), blockPA, 1<<writebackBuddyOrder) {
			kmem.BuddyFreeTyped(blockPA, writebackBuddyOrder, kmem.PageSharedIPC)
			klog.Errf("[mmap-wb] MapContiguousUserPages failed\n")
			break
		}
		handlerShepherd.Spans.Add(handlerBlockVA, uint64(1<<writebackBuddyOrder)*4096)

		// Record the block for cleanup
		cleanup.Blocks[cleanup.NumBlocks] = writebackBuddyBlock{
			PA:        blockPA,
			HandlerVA: handlerBlockVA,
			Order:     writebackBuddyOrder,
		}
		cleanup.NumBlocks++

		// Add descriptor entry
		fileOffset := fm.FileOffset + (blockStartVA - fm.StartVA)
		entries[numEntries] = ipc.MmapWritebackEntry{
			FileOffset: fileOffset,
			DataVA:     handlerBlockVA,
			Length:     uint64(pagesInBlock) * 4096,
		}
		numEntries++
	}

	if numEntries == 0 {
		// No pages to write back — clean up descriptor and return
		kmem.ReleasePageByPA(descriptorPA)
		return
	}

	// Map descriptor page into linux shepherd's address space
	handlerDescVA := bumpAllocForShepherd(handlerShepherd, 4096)
	if handlerDescVA == 0 {
		cleanupWritebackResources(cleanup, handlerShepherd)
		kmem.ReleasePageByPA(descriptorPA)
		klog.Errf("[mmap-wb] ENOMEM descriptor VA\n")
		return
	}
	kmem.MapPageInProcess(handlerSID, uintptr(handlerDescVA), descriptorPA, 0) // RW
	handlerShepherd.Spans.Add(handlerDescVA, 4096)
	cleanup.DescriptorVA = handlerDescVA

	// Set up DelegateCallInfo so the reply path can route back to us
	info := &delegateCallInfos[callerTID]
	info.DataPagePA = 0 // No single data page — cleanup uses writebackCleanups
	info.DataPageVA = 0
	info.HandlerSID = handlerSID
	info.CallerSID = int16(proc.CurrentShepherd().PID)
	info.CallerBufVA = 0
	info.CallerBufLen = 0
	info.CallerL0PA = uintptr(kmem.ReadCurrentL0PA())
	info.SysID = sysid.MmapPageWriteback
	info.WritableMapping = false
	info.InUse = true
	cleanup.InUse = true

	// Send the batch write-back request
	// Args: [0]=fd, [1]=descriptorVA, [2]=numEntries
	reqPayload := ipc.FSDelegateReqPayload{
		SysID:     uint16(sysid.MmapPageWriteback),
		CallerSID: int16(proc.CurrentShepherd().PID),
		CallerTID: callerTID,
		Args:      [6]uint64{uint64(fm.FD), handlerDescVA, uint64(numEntries), 0, 0, 0},
		DataVA:    handlerDescVA,
		DataLen:   uint32(numEntries) * 24, // each entry is 24 bytes
	}
	msg := ipc.EncodeFSDelegateReq(&reqPayload)
	result, _ := uringSendKernel(-1, handlerSID, uintptr(unsafe.Pointer(&msg)))
	if result < 0 {
		info.InUse = false
		cleanup.InUse = false
		cleanupWritebackResources(cleanup, handlerShepherd)
		kmem.ReleasePageByPA(descriptorPA)
		klog.Errf("[mmap-wb] uring send failed\n")
		return
	}

	// Block the calling thread until the linux shepherd finishes writing
	ctx := blockForDelegatedSyscall()
	if ctx == 0 {
		klog.Errf("[mmap-wb] NO NEXT THREAD\n")
		return
	}
	SetSyscallSwitchTarget(ctx)
}

// copyPage copies 4096 bytes from src to dst kernel VA.
//
//go:nosplit
func copyPage(dst, src uintptr) {
	s := unsafe.Slice((*byte)(unsafe.Pointer(src)), 4096)
	d := unsafe.Slice((*byte)(unsafe.Pointer(dst)), 4096)
	copy(d, s)
}

// cleanupWritebackResources frees buddy blocks and unmaps from the handler.
// Called on error paths and from the reply handler.
func cleanupWritebackResources(cleanup *writebackCleanupEntry, handlerShepherd *proc.Shepherd) {
	for i := 0; i < cleanup.NumBlocks; i++ {
		b := &cleanup.Blocks[i]
		if handlerShepherd != nil {
			// Unmap all pages in the block from the handler
			for j := 0; j < (1 << uint(b.Order)); j++ {
				va := uintptr(b.HandlerVA) + uintptr(j)*4096
				kmem.UnmapUserPageWithL0(va, handlerShepherd.PageTableL0PA)
			}
			handlerShepherd.Spans.Remove(b.HandlerVA, uint64(1<<uint(b.Order))*4096)
		}
		kmem.BuddyFreeTyped(b.PA, b.Order, kmem.PageSharedIPC)
	}
	cleanup.NumBlocks = 0
}

// HandleMmapWritebackReply is called from SyscallReply when the linux shepherd
// replies to an MmapPageWriteback request. Cleans up all batch resources.
func HandleMmapWritebackReply(callerTID int16, handlerSID int16) {
	if int(callerTID) >= MaxDelegateThreads {
		return
	}
	cleanup := &writebackCleanups[callerTID]
	if !cleanup.InUse {
		return
	}

	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))

	// Clean up data blocks
	cleanupWritebackResources(cleanup, handlerShepherd)

	// Clean up descriptor page
	if handlerShepherd != nil && cleanup.DescriptorVA != 0 {
		kmem.UnmapUserPageWithL0(uintptr(cleanup.DescriptorVA), handlerShepherd.PageTableL0PA)
		handlerShepherd.Spans.Remove(cleanup.DescriptorVA, 4096)
	}
	if cleanup.DescriptorPA != 0 {
		kmem.ReleasePageByPA(cleanup.DescriptorPA)
	}

	cleanup.InUse = false
}

// WriteBackSharedMmapOnDeath writes back all MAP_SHARED writable mmap pages
// for a dying shepherd. Called from TerminateShepherd Phase 1 (pre-lock).
func WriteBackSharedMmapOnDeath(pid int16) {
	// Find the shepherd
	for i := int16(0); i < int16(len(proc.ShepherdListData)); i++ {
		if !proc.ShepherdListInUse[i] {
			continue
		}
		if int16(proc.ShepherdListData[i].PID) != pid {
			continue
		}
		p := &proc.ShepherdListData[i]
		for j := 0; j < proc.MaxFileMappings; j++ {
			fm := &p.FileMappings[j]
			if fm.InUse && fm.Shared && fm.Writable {
				writeBackSharedPages(fm)
			}
		}
		return
	}
}
