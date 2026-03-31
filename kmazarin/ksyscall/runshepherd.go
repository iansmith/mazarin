// runshepherd.go - SysRunShepherd syscall: create a new shepherd from ELF pages.
//
// The caller has pages containing an ELF binary (loaded via LoadFile).
// RunShepherd creates a new shepherd with its own address space, loads the ELF
// into it, and starts the shepherd's main thread.
// The raw ELF pages are implicitly unmapped from the caller.
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"sync/atomic"
)

// RunShepherdWorkRequest contains the parameters for a RunShepherd operation.
type RunShepherdWorkRequest struct {
	Name       string
	StartVA    uintptr
	NumPages   int
	TotalBytes int
	CallerShepherd *proc.Shepherd
	CallerL0PA   uintptr
	BlockedTID   int32 // Set by BlockForRunShepherd in main package
}

// RunShepherdReq is the global request struct shared between the SVC handler
// and the kernel worker goroutine. One request at a time.
// RunShepherdBusy guards against concurrent access (latent SMP hazard).
var RunShepherdReq RunShepherdWorkRequest
var RunShepherdBusy int32

// SyscallRunShepherd creates a new shepherd from ELF data in the caller's pages.
//
// arg0 = pointer to null-terminated name string (in caller's address space)
// arg1 = startVA (page-aligned, where raw ELF data is mapped)
// arg2 = numPages (number of 4KB pages)
// arg3 = totalBytes (actual file size)
//
// Returns: 0 on success, ErrorCode on failure.
//
//go:noinline
func SyscallRunShepherd(arg0, arg1, arg2, arg3, _, _ uint64) int64 {
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
	// RunShepherd uses copyPagesFromUser (byte-by-byte), not the fixed-size PA
	// array in TransferPages, so it can handle much larger transfers.
	const maxRunShepherdPages = 4096 // 16MB
	if numPages < 1 || numPages > maxRunShepherdPages {
		return -22 // EINVAL
	}
	if totalBytes < 64 || totalBytes > numPages*4096 {
		return int64(errInvalidELF)
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

	// Guard against concurrent access (latent SMP hazard).
	if !atomic.CompareAndSwapInt32(&RunShepherdBusy, 0, 1) {
		console.KWriteString("[RunShepherd] ERROR: concurrent request\r\n")
		return -16 // EBUSY
	}

	console.KWriteString("[RunShepherd] CAS ok, name=")
	console.KWriteString(name)
	console.KWriteString("\r\n")

	// Store request for the worker goroutine
	RunShepherdReq = RunShepherdWorkRequest{
		Name:         name,
		StartVA:      startVA,
		NumPages:     numPages,
		TotalBytes:   totalBytes,
		CallerShepherd: shepherd,
		CallerL0PA:   shepherd.PageTableL0PA,
	}

	// Block and dispatch to worker goroutine (needs growable stack)
	ctxPtr := blockForRunShepherd()
	if ctxPtr != 0 {
		console.KWriteString("[RunShepherd] blocked, switching\r\n")
		SetSyscallSwitchTarget(ctxPtr)
	} else {
		console.KWriteString("[RunShepherd] ERROR: no thread to switch to\r\n")
		return int64(errNoSpace)
	}

	return 0
}

// DoRunShepherdWork performs the heavy shepherd creation from the caller's pages.
// Called by the kernel worker goroutine on a normal growable stack.
func DoRunShepherdWork(req *RunShepherdWorkRequest) int64 {
	// Copy ELF data from the caller's pages into a contiguous kernel buffer.
	elfData := copyPagesFromUser(req.StartVA, req.TotalBytes, req.CallerL0PA)
	if elfData == nil {
		console.KWriteString("[RunShepherd] ERROR: failed to copy pages from user\r\n")
		return int64(errNullPointer)
	}

	// Unmap the raw ELF pages from the caller (implicit cleanup).
	unmapUserPages(req.StartVA, req.NumPages, req.CallerL0PA, int16(req.CallerShepherd.PID))

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

	// Create a fresh page table for the new shepherd
	processL0PA := kmem.CreateProcessPageTable()
	if processL0PA == 0 {
		return int64(errNoSpace)
	}

	// Switch TTBR0 to new process page table for IC IVAU correctness
	kmem.SwitchTTBR0WithASID(processL0PA, 0)

	// Map framebuffer into new shepherd's address space
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebuffer(fbPA, fbSize) {
		return int64(errNoSpace)
	}
	addSpan(UserFramebufferVA, UserFramebufferSize)

	// Map constraint shared pages read-only into shepherd address space.
	if !kmem.MapUserConstraintPages() {
		return int64(errNoSpace)
	}
	addSpan(UserConstraintPagesVA, UserConstraintPagesSize)

	// Initialize kernel attribute manager (once, on first shepherd launch).
	InitKernelAttrManager()

	// Build symbol table and find highest VA from the raw ELF
	shepherdSymTable := buildSymbolTable(elfData, &hdr)
	shepherdHighestVA := findHighestVA(elfData, &hdr)

	// Load ELF into the new shepherd's page table
	loadedProc, err := loadELF(elfData, "/"+req.Name+".elf", processL0PA, 0)
	if err != nil {
		console.KWriteString("[RunShepherd] loadELF failed\r\n")
		return int64(errInvalidELF)
	}

	// Final I-cache invalidation
	kmem.InvalidateAllICache()
	SetUserspaceActive()
	kmem.FinalUserspaceSync()

	// Create a new thread for this shepherd
	tid := CreateUserspaceThread(loadedProc.EntryPoint, loadedProc.StackTop, processL0PA)

	// Cache symbol table, highest VA, and filename on the shepherd struct
	for i := 0; i < proc.MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] && proc.ShepherdListData[i].PageTableL0PA == processL0PA {
			proc.ShepherdListData[i].SymbolTable = shepherdSymTable
			proc.ShepherdListData[i].HighestVA = shepherdHighestVA
			proc.ShepherdListData[i].Filename = "/" + req.Name + ".elf"
			console.KPrintf("[RunShepherd] %s launched (TID=%d, PID=%d)\n",
				req.Name, tid, proc.ShepherdListData[i].PID)
			break
		}
	}

	return 0
}
