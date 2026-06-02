// runshepherd.go - SysRunShepherd syscall: create a new shepherd from ELF pages.
//
// The caller has pages containing an ELF binary (loaded via fsclient IPC).
// RunShepherd creates a new shepherd with its own address space, loads the ELF
// into it, and starts the shepherd's main thread.
// The raw ELF pages are implicitly unmapped from the caller.
package ksyscall

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// RunShepherdWorkRequest contains the parameters for a RunShepherd operation.
type RunShepherdWorkRequest struct {
	Name           string
	StartVA        uintptr
	NumPages       int
	TotalBytes     int
	Args           []string // additional command-line arguments (after filename and shepherd number)
	CallerShepherd *proc.Shepherd
	CallerL0PA     uintptr
}

// SyscallRunShepherd creates a new shepherd from ELF data in the caller's pages.
//
// arg0 = pointer to null-terminated name string (in caller's address space)
// arg1 = startVA (page-aligned, where raw ELF data is mapped)
// arg2 = numPages (number of 4KB pages)
// arg3 = totalBytes (actual file size)
// arg4 = pointer to packed args (null-separated strings in caller's address space, or 0 for no args)
// arg5 = total byte length of packed args
//
// Returns: 0 on success, ErrorCode on failure.
//
//go:noinline
func SyscallRunShepherd(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	namePtr := uintptr(arg0)
	startVA := uintptr(arg1)
	numPages := int(arg2)
	totalBytes := int(arg3)
	argsPtr := uintptr(arg4)
	argsLen := int(arg5)

	if namePtr == 0 || startVA == 0 {
		return int64(errNullPointer)
	}
	if startVA&0xFFF != 0 {
		return -22 // EINVAL
	}
	// RunShepherd uses copyPagesFromUser (byte-by-byte), not the fixed-size PA
	// array in TransferPages, so it can handle much larger transfers.
	const maxRunShepherdPages = 8192 // 32MB
	if numPages < 1 || numPages > maxRunShepherdPages {
		return -22 // EINVAL
	}
	if totalBytes < 64 || totalBytes > numPages*4096 {
		return int64(errInvalidELF)
	}
	// Validate args length (max 4KB of packed args).
	const maxArgsLen = 4096
	if argsLen < 0 || argsLen > maxArgsLen {
		return -22 // EINVAL
	}

	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return int64(errNullPointer)
	}

	// Read name string from caller's address space
	name := readNullTerminatedString(namePtr)
	if name == "" {
		return int64(errInvalidFilename)
	}

	// Read packed args from caller's address space (null-separated strings).
	var args []string
	if argsPtr != 0 && argsLen > 0 {
		args = readPackedArgs(argsPtr, argsLen)
	}

	ctxPtr := submitRunShepherd(RunShepherdWorkRequest{
		Name:           name,
		StartVA:        startVA,
		NumPages:       numPages,
		TotalBytes:     totalBytes,
		Args:           args,
		CallerShepherd: shepherd,
		CallerL0PA:     shepherd.PageTableL0PA,
	})
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
	} else {
		// Criticalf bypasses the soft-IRQ ring so this is visible even
		// when the worker thread is already busy with a previous request.
		klog.Criticalf("[RS]", "[RunShepherd] ERROR: busy or no thread for name=%s pages=%d\n",
			name, numPages)
		return int64(errNoSpace)
	}

	return 0
}

// readPackedArgs reads null-separated strings from userspace memory.
// Returns nil if the memory is not accessible.
func readPackedArgs(ptr uintptr, totalLen int) []string {
	var result []string
	var current []byte
	for i := 0; i < totalLen; i++ {
		b, ok := kmem.ReadUserByte(ptr + uintptr(i))
		if !ok {
			break
		}
		if b == 0 {
			if len(current) > 0 {
				result = append(result, string(current))
				current = current[:0]
			}
		} else {
			current = append(current, b)
		}
	}
	// Handle last arg if not null-terminated.
	if len(current) > 0 {
		result = append(result, string(current))
	}
	return result
}

// DoRunShepherdWork performs the heavy shepherd creation from the caller's pages.
// Called by the kernel worker goroutine on a normal growable stack.
func DoRunShepherdWork(req *RunShepherdWorkRequest) int64 {
	klog.Criticalf("[RS]", "[RunShepherd] start name=%s pages=%d bytes=%d\n",
		req.Name, req.NumPages, req.TotalBytes)

	// Copy ELF data from the caller's pages into a contiguous kernel buffer.
	elfData := copyPagesFromUser(req.StartVA, req.TotalBytes, req.CallerL0PA)
	if elfData == nil {
		klog.Errf("[RunShepherd] ERROR: copyPagesFromUser failed name=%s\n", req.Name)
		return int64(errNullPointer)
	}

	// Unmap the raw ELF pages from the caller (implicit cleanup).
	unmapUserPages(req.StartVA, req.NumPages, req.CallerL0PA, req.CallerShepherd.PID)

	// Validate ELF header
	if len(elfData) < 64 {
		klog.Errf("[RunShepherd] ERROR: ELF too small name=%s len=%d\n", req.Name, len(elfData))
		return int64(errInvalidELF)
	}
	hdr := parseELFHeader(elfData)
	if hdr.Magic != ELF_MAGIC {
		klog.Errf("[RunShepherd] ERROR: bad ELF magic name=%s\n", req.Name)
		return int64(errInvalidELF)
	}
	if hdr.Class != ELF_CLASS64 || hdr.Machine != elfExpectedMachine {
		klog.Errf("[RunShepherd] ERROR: wrong arch name=%s class=%d machine=%d\n",
			req.Name, hdr.Class, hdr.Machine)
		return int64(errWrongArch)
	}

	// Create a fresh page table for the new shepherd
	processL0PA := kmem.CreateProcessPageTable()
	if processL0PA == 0 {
		klog.Errf("[RunShepherd] ERROR: CreateProcessPageTable failed name=%s\n", req.Name)
		return int64(errNoSpace)
	}

	// Map framebuffer into new shepherd's address space using explicit L0PA.
	// We do NOT switch TTBR0 here — this goroutine is preemptible and other
	// goroutines on the same M would see the wrong address space.
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebufferWithL0(fbPA, fbSize, processL0PA) {
		kmem.FreeProcessPageTable(processL0PA) // reclaim the page table (MAZ-108)
		klog.Errf("[RunShepherd] ERROR: MapUserFramebufferWithL0 failed name=%s\n", req.Name)
		return int64(errNoSpace)
	}

	// Map constraint shared pages read-only into shepherd address space.
	if !kmem.MapUserConstraintPagesWithL0(processL0PA) {
		kmem.FreeProcessPageTable(processL0PA) // reclaim page table + framebuffer mapping (MAZ-108)
		klog.Errf("[RunShepherd] ERROR: MapUserConstraintPagesWithL0 failed name=%s\n", req.Name)
		return int64(errNoSpace)
	}

	// Initialize kernel attribute manager (once, on first shepherd launch).
	InitKernelAttrManager()

	// Build symbol table and find highest VA from the raw ELF
	shepherdSymTable := buildSymbolTable(elfData, &hdr)
	shepherdHighestVA := findHighestVA(elfData, &hdr)

	// Load ELF into the new shepherd's page table
	loadedProc, err := loadELF(elfData, "/"+req.Name+".elf", processL0PA, 0, req.Args, nil)
	if err != nil {
		// Reclaim the page table + any segment/stack frames loadELF mapped
		// before failing — one FreeProcessPageTable walk covers it (MAZ-108).
		kmem.FreeProcessPageTable(processL0PA)
		klog.Errf("[RunShepherd] ERROR: loadELF failed name=%s err=%v\n", req.Name, err)
		return int64(errInvalidELF)
	}

	// Final I-cache invalidation
	kmem.InvalidateAllICache()
	SetUserspaceActive()
	kmem.FinalUserspaceSync()

	// Create a new thread for this shepherd. The returned TID isn't needed here
	// (the shepherd is located below by its L0 page-table address).
	CreateUserspaceThread(loadedProc.EntryPoint, loadedProc.StackTop, processL0PA)

	// Cache symbol table, highest VA, filename, allocate IPC uring ring, and
	// register all kernel-allocated VA ranges in Spans so CleanupShepherdPages
	// Phase 1 correctly reclaims ELF, stack, framebuffer, and constraint pages.
	proc.Shepherds.ForEach(func(p *proc.Shepherd) bool {
		if p.PageTableL0PA != processL0PA {
			return true // keep iterating
		}
		p.SymbolTable = shepherdSymTable
		p.HighestVA = shepherdHighestVA
		p.Filename = "/" + req.Name + ".elf"

		// Allocate IPC uring ring for the new shepherd
		uringID := allocateUringID()
		p.UringID = uringID
		allocateUringIPCRing(p, 0)
		registerUringID(uringID, int16(p.PID))

		// Register kernel-allocated VA ranges that mmap syscall never sees.
		for j := 0; j < loadedProc.SegmentCount; j++ {
			p.Spans.Add(loadedProc.SegmentSpans[j].VA, loadedProc.SegmentSpans[j].Size)
		}
		p.Spans.Add(loadedProc.StackBase, 64*1024)
		// UserFramebufferVA and UserConstraintPagesVA are intentionally NOT added
		// to Spans. Both are system-lifetime shared resources mapped into EVERY
		// shepherd from a single global PA (the framebuffer via device-page maps,
		// the constraint block read-only). CleanupShepherdPages Phase 1 must never
		// releasePageByPA them: the first shepherd to die would free the shared
		// pages (dropping refcount to 0 and clearing the descriptor), and every
		// later shepherd death would underflow on the already-freed pages. Phase 2
		// still unmaps both ranges' PTEs from the dying shepherd's page table. The
		// constraint block is additionally protected by PD_PINNED; the framebuffer
		// is device memory whose descriptors are not pinned, so exclusion here is
		// the sole guard. (MAZ-127: forkexectest was the first thing to make a
		// shepherd die, exposing this latent framebuffer double-free.)

		return false // stop iteration — found our shepherd
	})

	return 0
}
