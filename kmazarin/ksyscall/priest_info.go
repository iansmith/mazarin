package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/hid"
	"unsafe"
)

// SyscallPriestInfo returns information about all running priests.
//
// arg0: pointer to userspace buffer (array of hid.PriestInfoEntry)
// arg1: max number of entries the buffer can hold
//
// Returns the number of priests written, or negative errno on error.
func SyscallPriestInfo(bufPtr, maxEntries, _, _, _, _ uint64) int64 {
	if bufPtr == 0 || maxEntries == 0 {
		return -22 // EINVAL
	}

	entrySize := int(unsafe.Sizeof(hid.PriestInfoEntry{}))
	written := 0

	for i := 0; i < proc.MaxPriests && written < int(maxEntries); i++ {
		if !proc.PriestListInUse[i] {
			continue
		}
		p := &proc.PriestListData[i]

		var entry hid.PriestInfoEntry
		entry.PID = int16(p.PID)
		entry.ThreadCount = int16(p.ThreadCount)

		// Copy filename from Go string into fixed-size buffer
		n := len(p.Filename)
		if n > len(entry.Filename) {
			n = len(entry.Filename)
		}
		copy(entry.Filename[:n], p.Filename)
		entry.FilenameLen = uint8(n)

		// Gather thread IDs belonging to this priest
		for j := range entry.ThreadIDs {
			entry.ThreadIDs[j] = -1 // sentinel for unused slots
		}
		ForEachThread(int16(p.PID), &entry)

		// Count mapped pages by walking the priest's page table
		if p.PageTableL0PA != 0 {
			entry.PageCount = uint32(kmem.CountMappedPages(p.PageTableL0PA))
		}

		// Copy entry to userspace buffer
		destVA := uintptr(bufPtr) + uintptr(written)*uintptr(entrySize)
		src := unsafe.Slice((*byte)(unsafe.Pointer(&entry)), entrySize)
		kmem.CopyToUser(destVA, src)

		written++
	}

	return int64(written)
}

// ForEachThread iterates over threads belonging to priestPID and fills
// entry.ThreadIDs. Implemented via go:linkname in kmazarin/kmazarin/linkname_impl.go.
func ForEachThread(priestPID int16, entry *hid.PriestInfoEntry)
