// runpriest.go - SysRunPriest syscall: create a new priest from ELF pages.
//
// The caller has pages containing an ELF binary (loaded via LoadFile).
// RunPriest creates a new priest with its own address space, loads the ELF
// into it, and starts the priest's main thread.
// The raw ELF pages are implicitly unmapped from the caller.
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"sync/atomic"
)

// RunPriestWorkRequest contains the parameters for a RunPriest operation.
type RunPriestWorkRequest struct {
	Name       string
	StartVA    uintptr
	NumPages   int
	TotalBytes int
	CallerPriest *proc.Priest
	CallerL0PA   uintptr
	BlockedTID   int32 // Set by BlockForRunPriest in main package
}

// RunPriestReq is the global request struct shared between the SVC handler
// and the kernel worker goroutine. One request at a time.
// RunPriestBusy guards against concurrent access (latent SMP hazard).
var RunPriestReq RunPriestWorkRequest
var RunPriestBusy int32

// SyscallRunPriest creates a new priest from ELF data in the caller's pages.
//
// arg0 = pointer to null-terminated name string (in caller's address space)
// arg1 = startVA (page-aligned, where raw ELF data is mapped)
// arg2 = numPages (number of 4KB pages)
// arg3 = totalBytes (actual file size)
//
// Returns: 0 on success, ErrorCode on failure.
//
//go:noinline
func SyscallRunPriest(arg0, arg1, arg2, arg3, _, _ uint64) int64 {
	namePtr := uintptr(arg0)
	startVA := uintptr(arg1)
	numPages := int(arg2)
	totalBytes := int(arg3)

	if namePtr == 0 || startVA == 0 {
		return int64(errNullPointer)
	}
	if startVA&0xFFF != 0 {
		return -22 // EINVAL
	}
	// RunPriest uses copyPagesFromUser (byte-by-byte), not the fixed-size PA
	// array in TransferPages, so it can handle much larger transfers.
	const maxRunPriestPages = 4096 // 16MB
	if numPages < 1 || numPages > maxRunPriestPages {
		return -22 // EINVAL
	}
	if totalBytes < 64 || totalBytes > numPages*4096 {
		return int64(errInvalidELF)
	}

	priest := proc.CurrentPriest()
	if priest == nil {
		return int64(errNullPointer)
	}

	// Read name string from caller's address space
	name := readNullTerminatedString(namePtr)
	if name == "" {
		return int64(errInvalidFilename)
	}

	// Guard against concurrent access (latent SMP hazard).
	if !atomic.CompareAndSwapInt32(&RunPriestBusy, 0, 1) {
		console.KWriteString("[RunPriest] ERROR: concurrent request\r\n")
		return -16 // EBUSY
	}

	// Store request for the worker goroutine
	RunPriestReq = RunPriestWorkRequest{
		Name:         name,
		StartVA:      startVA,
		NumPages:     numPages,
		TotalBytes:   totalBytes,
		CallerPriest: priest,
		CallerL0PA:   priest.PageTableL0PA,
	}

	// Block and dispatch to worker goroutine (needs growable stack)
	ctxPtr := blockForRunPriest()
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
	} else {
		console.KWriteString("[RunPriest] ERROR: no thread to switch to\r\n")
		return int64(errNoSpace)
	}

	return 0
}

// DoRunPriestWork performs the heavy priest creation from the caller's pages.
// Called by the kernel worker goroutine on a normal growable stack.
func DoRunPriestWork(req *RunPriestWorkRequest) int64 {
	// Copy ELF data from the caller's pages into a contiguous kernel buffer.
	elfData := copyPagesFromUser(req.StartVA, req.TotalBytes, req.CallerL0PA)
	if elfData == nil {
		console.KWriteString("[RunPriest] ERROR: failed to copy pages from user\r\n")
		return int64(errNullPointer)
	}

	// Unmap the raw ELF pages from the caller (implicit cleanup).
	unmapUserPages(req.StartVA, req.NumPages, req.CallerL0PA, int16(req.CallerPriest.PID))

	// Validate ELF header
	if len(elfData) < 64 {
		return int64(errInvalidELF)
	}
	hdr := parseELFHeader(elfData)
	if hdr.Magic != ELF_MAGIC {
		return int64(errInvalidELF)
	}
	if hdr.Class != ELF_CLASS64 || hdr.Machine != elfExpectedMachine {
		return int64(errWrongArch)
	}

	// Create a fresh page table for the new priest
	processL0PA := kmem.CreateProcessPageTable()
	if processL0PA == 0 {
		return int64(errNoSpace)
	}

	// Switch TTBR0 to new process page table for IC IVAU correctness
	kmem.SwitchTTBR0WithASID(processL0PA, 0)

	// Map framebuffer into new priest's address space
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebuffer(fbPA, fbSize) {
		return int64(errNoSpace)
	}
	addSpan(UserFramebufferVA, UserFramebufferSize)

	// Build symbol table and find highest VA from the raw ELF
	priestSymTable := buildSymbolTable(elfData, &hdr)
	priestHighestVA := findHighestVA(elfData, &hdr)

	// Load ELF into the new priest's page table
	loadedProc, err := loadELF(elfData, "/"+req.Name+".elf", processL0PA, 0)
	if err != nil {
		console.KWriteString("[RunPriest] loadELF failed\r\n")
		return int64(errInvalidELF)
	}

	// Final I-cache invalidation
	kmem.InvalidateAllICache()
	SetUserspaceActive()
	kmem.FinalUserspaceSync()

	// Create a new thread for this priest
	tid := CreateUserspaceThread(loadedProc.EntryPoint, loadedProc.StackTop, processL0PA)

	// Cache symbol table, highest VA, and filename on the priest struct
	for i := 0; i < proc.MaxPriests; i++ {
		if proc.PriestListInUse[i] && proc.PriestListData[i].PageTableL0PA == processL0PA {
			proc.PriestListData[i].SymbolTable = priestSymTable
			proc.PriestListData[i].HighestVA = priestHighestVA
			proc.PriestListData[i].Filename = "/" + req.Name + ".elf"
			console.KPrintf("[RunPriest] %s launched (TID=%d, PID=%d)\n",
				req.Name, tid, proc.PriestListData[i].PID)
			break
		}
	}

	return 0
}
