// runshepherd.go - SysRunShepherd syscall: create a new shepherd from ELF pages.
//
// The caller has pages containing an ELF binary (loaded via LoadFile).
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
		klog.Errf("[RunShepherd] ERROR: busy or no thread to switch to\n")
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
	// Copy ELF data from the caller's pages into a contiguous kernel buffer.
	elfData := copyPagesFromUser(req.StartVA, req.TotalBytes, req.CallerL0PA)
	if elfData == nil {
		klog.Errf("[RunShepherd] ERROR: failed to copy pages from user\n")
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

	// Map framebuffer into new shepherd's address space using explicit L0PA.
	// We do NOT switch TTBR0 here — this goroutine is preemptible and other
	// goroutines on the same M would see the wrong address space.
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebufferWithL0(fbPA, fbSize, processL0PA) {
		return int64(errNoSpace)
	}
	addSpan(UserFramebufferVA, UserFramebufferSize)

	// Map constraint shared pages read-only into shepherd address space.
	if !kmem.MapUserConstraintPagesWithL0(processL0PA) {
		return int64(errNoSpace)
	}
	addSpan(UserConstraintPagesVA, UserConstraintPagesSize)

	// Initialize kernel attribute manager (once, on first shepherd launch).
	InitKernelAttrManager()

	// Build symbol table and find highest VA from the raw ELF
	shepherdSymTable := buildSymbolTable(elfData, &hdr)
	shepherdHighestVA := findHighestVA(elfData, &hdr)

	// Load ELF into the new shepherd's page table
	loadedProc, err := loadELF(elfData, "/"+req.Name+".elf", processL0PA, 0, req.Args)
	if err != nil {
		klog.Errf("[RunShepherd] loadELF failed\n")
		return int64(errInvalidELF)
	}

	// Final I-cache invalidation
	kmem.InvalidateAllICache()
	SetUserspaceActive()
	kmem.FinalUserspaceSync()

	// Create a new thread for this shepherd
	_ = CreateUserspaceThread(loadedProc.EntryPoint, loadedProc.StackTop, processL0PA)

	// Cache symbol table, highest VA, filename, and allocate IPC uring ring
	for i := 0; i < proc.MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] && proc.ShepherdListData[i].PageTableL0PA == processL0PA {
			proc.ShepherdListData[i].SymbolTable = shepherdSymTable
			proc.ShepherdListData[i].HighestVA = shepherdHighestVA
			proc.ShepherdListData[i].Filename = "/" + req.Name + ".elf"

			// Allocate IPC uring ring for the new shepherd
			uringID := allocateUringID()
			proc.ShepherdListData[i].UringID = uringID
			allocateUringIPCRing(&proc.ShepherdListData[i], 0)
			registerUringID(uringID, int16(proc.ShepherdListData[i].PID))

			break
		}
	}

	return 0
}
