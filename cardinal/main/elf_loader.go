// cardinal/main/elf_loader.go
// ELF64 loader for loading kmazarin kernel from FAT32 filesystem.
// Uses shared bootloader ELF parser + cardinal's memory management.
package main

import (
	"mazzy/cardinal/asm"
	"mazzy/cardinal/constants"
	"mazzy/shared/bootloader"
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// Global buffer for reading file data
var elfReadBuf [4096]byte

// cardinalBootError is a pre-allocated error type for boot failures.
type cardinalBootError struct {
	Msg string
}

func (e *cardinalBootError) Error() string {
	return e.Msg
}

// SimpleFile represents a file for reading
type SimpleFile struct {
	Cluster uint32
	Size    uint32
}

// cardinalLoadKernel loads the kmazarin ELF from disk.
func cardinalLoadKernel(fsys *fat32.FileSystem, path string) (*LoadedKernel, error) {
	file, err := findFile(fsys, path)
	if err != nil {
		return nil, err
	}

	// Read entire ELF into memory using physical pages
	elfSize := uint64(file.Size)
	numPages := (elfSize + 0xFFF) >> 12
	elfPhys, err := plat.AllocPhysPages(numPages)
	if err != nil {
		return nil, err
	}

	// Identity-map the allocated pages so we can access them
	for p := uint64(0); p < numPages; p++ {
		pa := elfPhys + p*0x1000
		mapPage(uintptr(pa), uintptr(pa), PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}
	asm.Dsb()
	asm.Isb()

	// Create a byte slice over the allocated physical memory
	var elfData []byte
	sliceHeader := (*struct {
		Data uintptr
		Len  int
		Cap  int
	})(unsafe.Pointer(&elfData))
	sliceHeader.Data = uintptr(elfPhys)
	sliceHeader.Len = int(elfSize)
	sliceHeader.Cap = int(numPages << 12)

	// Read file into memory cluster by cluster
	bytesPerClus := uint64(fsys.BytesPerCluster())
	cluster := file.Cluster
	readOffset := 0
	remaining := int(elfSize)

	for remaining > 0 && cluster >= 2 {
		if err := readClusterDirect(fsys, cluster, dirClusterBuf[:bytesPerClus]); err != nil {
			return nil, err
		}
		toCopy := int(bytesPerClus)
		if toCopy > remaining {
			toCopy = remaining
		}
		// Copy from cluster buffer to ELF memory
		dst := unsafe.Pointer(uintptr(elfPhys) + uintptr(readOffset))
		for i := 0; i < toCopy; i++ {
			*(*byte)(unsafe.Pointer(uintptr(dst) + uintptr(i))) = dirClusterBuf[i]
		}
		readOffset += toCopy
		remaining -= toCopy

		next, err := readFATEntryDirect(fsys, cluster)
		if err != nil {
			return nil, err
		}
		if next >= 0x0FFFFFF8 {
			break
		}
		cluster = next
	}

	// Parse ELF headers using shared parser
	var segBuf [bootloader.MaxLoadSegments]bootloader.LoadSegment
	kernel, nsegs, err := bootloader.ParseELFHeaders(elfData, &segBuf)
	if err != nil {
		return nil, err
	}

	// Load each segment: allocate physical frames per-page, map into TTBR1, copy data.
	// This matches the old loadAndRunKmazarin pattern — no need for contiguous physical region.
	for i := 0; i < nsegs; i++ {
		seg := &segBuf[i]
		if seg.Memsz == 0 {
			continue
		}

		// Map pages for this segment in TTBR1 (high-memory)
		segStart := seg.Vaddr &^ 0xFFF
		segEnd := (seg.Vaddr + seg.Memsz + 0xFFF) &^ 0xFFF
		segPages := (segEnd - segStart) >> 12

		// Determine execute permission
		execPerm := uint64(PTE_EXEC_NEVER)
		if seg.Flags&bootloader.PF_X != 0 {
			execPerm = PTE_EXEC_ALLOW
		}

		// Allocate and map each page individually
		for p := uint64(0); p < segPages; p++ {
			virt := segStart + p*0x1000

			if kmazarinAllocatedBytes+0x1000 > uintptr(constants.KmazarinTotalLimit) {
				return nil, &cardinalBootError{"kmazarin size limit exceeded"}
			}

			physFrame := allocKFrame()
			if physFrame == 0 {
				return nil, &cardinalBootError{"out of memory mapping segment"}
			}

			// Map as RW initially (need to write data), remap executable segments to RO later
			mapKernelPage(uintptr(virt), physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, execPerm)
			kmazarinAllocatedBytes += 0x1000
		}

		// Barrier after mapping
		asm.Dsb()
		asm.Isb()

		// Copy file data (write to kernel VAs, which are now mapped)
		if seg.Filesz > 0 {
			var srcOffset uint64
			var dstAddr uintptr
			var copySize uint64

			if bootloader.IsNegativeOffset(seg.Offset) {
				// Go's linker negative offset quirk: 64KB header region before .text
				headerSize := uint64(0x10000)
				bzero4K(unsafe.Pointer(uintptr(segStart)), uint32(headerSize))
				srcOffset = 0x1000
				dstAddr = uintptr(segStart) + uintptr(headerSize)
				copySize = seg.Filesz - headerSize
			} else {
				srcOffset = seg.Offset
				dstAddr = uintptr(segStart)
				copySize = seg.Filesz
			}

			if copySize > 0 {
				src := unsafe.Pointer(uintptr(elfPhys) + uintptr(srcOffset))
				dst := unsafe.Pointer(dstAddr)
				asm.MemmoveBytes(dst, src, uint32(copySize))
			}
		}

		// Zero BSS
		if seg.Memsz > seg.Filesz {
			bssStart := unsafe.Pointer(uintptr(seg.Vaddr) + uintptr(seg.Filesz))
			bssSize := seg.Memsz - seg.Filesz
			bzeroSimple(bssStart, uint32(bssSize))
		}
	}

	// Cache maintenance: clean D-cache, remap executable segments to RO, invalidate I-cache
	minVA := uintptr(kernel.LowestVirt)
	maxVA := uintptr(kernel.HighestVirt)

	// Step 1: Clean D-cache
	for va := minVA; va < maxVA; va += 64 {
		asm.DcCvau(va)
	}
	asm.Dsb()

	// Step 2: Remap executable segments to RO
	for i := 0; i < nsegs; i++ {
		seg := &segBuf[i]
		if seg.Memsz == 0 || seg.Flags&bootloader.PF_X == 0 {
			continue
		}
		segStart := seg.Vaddr &^ 0xFFF
		segEnd := (seg.Vaddr + seg.Memsz + 0xFFF) &^ 0xFFF
		segPages := (segEnd - segStart) >> 12

		asm.DsbIsh()
		for p := uint64(0); p < segPages; p++ {
			kernelVA := uintptr(segStart + p*0x1000)
			// Walk page tables to find and modify PTE
			ttbr1 := asm.ReadTtbr1El1()
			l0Table := uintptr(ttbr1 & ^uint64(0xFFF))
			l0Index := (kernelVA >> 39) & 0x1FF
			l0Entry := (*uint64)(unsafe.Pointer(l0Table + l0Index*8))
			l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)
			l1Index := (kernelVA >> 30) & 0x1FF
			l1Entry := (*uint64)(unsafe.Pointer(l1Table + l1Index*8))
			l2Table := uintptr(*l1Entry & PTE_ADDR_MASK)
			l2Index := (kernelVA >> 21) & 0x1FF
			l2Entry := (*uint64)(unsafe.Pointer(l2Table + l2Index*8))
			l3Table := uintptr(*l2Entry & PTE_ADDR_MASK)
			l3Index := (kernelVA >> 12) & 0x1FF
			l3Entry := (*uint64)(unsafe.Pointer(l3Table + l3Index*8))
			oldPTE := *l3Entry
			newPTE := (oldPTE & ^uint64(0xC0)) | PTE_AP_RO_EL1
			*l3Entry = newPTE
		}
		asm.DsbIsh()
		asm.InvalidateTlbAll()
	}

	// Step 3: Invalidate I-cache
	for va := minVA; va < maxVA; va += 64 {
		asm.IcIvau(va)
	}
	asm.Dsb()
	asm.Isb()

	// Register kmazarin memory span
	if !registerMmapSpan(minVA, maxVA) {
		return nil, &cardinalBootError{"failed to register kmazarin span"}
	}

	// Update LinkerKmazarinSize for downstream consumers
	LinkerKmazarinSize = uint64(maxVA - minVA)

	result := cNew[LoadedKernel]()
	if result == nil {
		return nil, &cardinalBootError{"allocation failed"}
	}
	*result = kernel
	result.PhysBase = 0 // per-segment allocation, no single physBase

	return result, nil
}

// findFile finds a file by path in the root directory.
// Only supports root-level files (e.g., "/KMAZARIN.ELF").
func findFile(fsys *fat32.FileSystem, path string) (*SimpleFile, error) {
	// Strip leading /
	name := path
	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}

	cluster := fsys.RootCluster()
	var found *SimpleFile

	err := WalkDir(fsys, cluster, func(e *SimpleDirEntry) bool {
		if matchName(e, name) {
			found = cNew[SimpleFile]()
			if found != nil {
				found.Cluster = e.Cluster
				found.Size = e.Size
			}
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, &cardinalBootError{"file not found"}
	}
	return found, nil
}
