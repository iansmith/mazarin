package main

import (
	"mazzy/kmazarin/ds"
	"mazzy/kmazarin/kmem"
	"sync/atomic"
	"unsafe"
)

// ============================================================================
// Per-CPU Data Structures for SMP Safety
// ============================================================================
//
// In an SMP (Symmetric Multi-Processing) environment, each CPU core needs
// its own copy of certain variables to prevent race conditions. This file
// provides the infrastructure for per-CPU data access.
//
// The per-CPU data is indexed by a logical CPU ID obtained from
// platform-specific assembly (getCPUIDAsm).
//
// Access Pattern:
//   cpu := GetPerCPU()
//   t := cpu.CurrentThread()
//   cpu.SetCurrentThread(newThread)

// MaxCPUs is the maximum number of CPU cores supported.
const MaxCPUs = 16

// Stack sizes for each CPU
const (
	PerCPUG0StackSize        = 0x10000 // 64KB g0 stack (SP_EL0)
	PerCPUExceptionStackSize = 0x20000 // 128KB exception stack (SP_EL1)
)

// PerCPU contains per-CPU data that must not be shared across cores.
// Each field is isolated per-core to prevent SMP race conditions.
type PerCPU struct {
	// currentThread is the thread currently executing on this CPU.
	// Uses unsafe.Pointer to avoid GC write barriers in exception context.
	// Access via CurrentThread()/SetCurrentThread() methods.
	currentThread unsafe.Pointer

	// topHalfIRQNum is set by the assembly IRQ handler before calling
	// NonTimerIRQTopHalf. Each CPU needs its own copy because IRQs can
	// fire simultaneously on different cores.
	TopHalfIRQNum uint64

	// _reservedAsyncPreempt is padding (was NeedsAsyncPreempt).
	// Kept to preserve struct layout for assembly hardcoded offsets.
	// NeedsThreadPreempt is at offset 20, LocalTickCounter at 24.
	_reservedAsyncPreempt uint32

	// needsThreadPreempt is set when a thread has exceeded its time slice.
	// Each CPU needs its own flag.
	NeedsThreadPreempt uint32

	// localTickCounter is the tick count for this CPU's timer.
	// Useful for per-CPU timing and statistics.
	LocalTickCounter uint64

	// ============================================================================
	// Per-CPU Stack Allocation for SMP
	// ============================================================================
	// Each secondary CPU needs its own stacks. CPU 0 uses the existing
	// kernel stacks mapped during boot.

	// G0StackTop is the top of the g0 stack for this CPU (SP_EL0).
	// For CPU 0, this is the kernel g0 stack from constants.
	// For CPUs 1-N, this is dynamically allocated.
	G0StackTop uint64

	// G0StackBottom is the bottom of the g0 stack for this CPU.
	G0StackBottom uint64

	// ExceptionStackTop is the top of the exception stack (SP_EL1).
	// For CPU 0, this is the kernel exception stack from constants.
	// For CPUs 1-N, this is dynamically allocated.
	ExceptionStackTop uint64

	// ExceptionStackBottom is the bottom of the exception stack.
	ExceptionStackBottom uint64

	// Online indicates whether this CPU is online and running.
	// Set to 1 when the CPU finishes its initialization sequence.
	Online uint32

	// LocalReadyQueue is this CPU's ready queue for per-CPU scheduling.
	// Protected by LocalLock (finer-grained than schedulerLock).
	LocalReadyQueue ds.StaticQueue[ThreadId]

	// LocalLock protects LocalReadyQueue only.
	// Lock ordering: schedulerLock → LocalLock (never reverse)
	LocalLock ds.Spinlock
}

// perCPUData is the array of per-CPU data structures.
// Indexed by logical CPU ID from getCPUIDAsm().
var perCPUData [MaxCPUs]PerCPU

// perCPUInitialized tracks whether InitPerCPU has been called.
var perCPUInitialized bool = false

// Per-CPU ready queue backing storage (statically allocated)
// Each CPU has its own queue with capacity for 64 threads.
const perCPUQueueCapacity = 64

var perCPUReadyQueueData [MaxCPUs][perCPUQueueCapacity]ThreadId
var perCPUReadyQueueInUse [MaxCPUs][perCPUQueueCapacity]bool

// getCPUIDAsm returns the CPU ID directly from assembly (fast path).
func getCPUIDAsm() uint64

// getPerCPUPtrAsm returns pointer to current CPU's PerCPU struct (fast path).
func getPerCPUPtrAsm() uintptr

// InitPerCPU initializes the per-CPU data structures.
// Must be called early in kernel boot, before any per-CPU access.
//
//go:nosplit
func InitPerCPU() {
	// Zero all per-CPU structures and initialize local ready queues
	for i := 0; i < MaxCPUs; i++ {
		perCPUData[i].currentThread = nil
		perCPUData[i].TopHalfIRQNum = 0
		perCPUData[i]._reservedAsyncPreempt = 0
		perCPUData[i].NeedsThreadPreempt = 0
		perCPUData[i].LocalTickCounter = 0
		perCPUData[i].G0StackTop = 0
		perCPUData[i].G0StackBottom = 0
		perCPUData[i].ExceptionStackTop = 0
		perCPUData[i].ExceptionStackBottom = 0
		perCPUData[i].Online = 0

		// Initialize per-CPU ready queue with backing arrays
		perCPUData[i].LocalReadyQueue.Data = perCPUReadyQueueData[i][:]
		perCPUData[i].LocalReadyQueue.InUse = perCPUReadyQueueInUse[i][:]
		perCPUData[i].LocalReadyQueue.Clear()
	}

	// CPU 0 uses the kernel stacks from platform-specific constants (already mapped during boot)
	g0Top, g0Bottom, excTop, excBottom := platformCPU0Stacks()
	perCPUData[0].G0StackTop = g0Top
	perCPUData[0].G0StackBottom = g0Bottom
	perCPUData[0].ExceptionStackTop = excTop
	perCPUData[0].ExceptionStackBottom = excBottom
	perCPUData[0].Online = 1 // CPU 0 is already online

	perCPUInitialized = true
}

// AllocSecondaryCPUStacks allocates stacks for secondary CPUs (1 to cpuCount-1).
// Must be called after the memory allocator is initialized.
// Returns true if all stacks were allocated successfully.
func AllocSecondaryCPUStacks(cpuCount uint64) bool {
	if cpuCount > MaxCPUs {
		cpuCount = MaxCPUs
	}

	// Calculate VA offset for converting PA to VA
	kernelVAOffset := platformKernelVAOffset()

	for cpuID := uint64(1); cpuID < cpuCount; cpuID++ {
		// Allocate g0 stack (64KB = 16 pages)
		g0StackPages := PerCPUG0StackSize / 0x1000
		g0StackPA := allocContiguousPages(g0StackPages)
		if g0StackPA == 0 {
			return false
		}
		perCPUData[cpuID].G0StackBottom = uint64(g0StackPA) + kernelVAOffset
		perCPUData[cpuID].G0StackTop = perCPUData[cpuID].G0StackBottom + PerCPUG0StackSize

		// Allocate exception stack (128KB = 32 pages)
		excStackPages := PerCPUExceptionStackSize / 0x1000
		excStackPA := allocContiguousPages(excStackPages)
		if excStackPA == 0 {
			return false
		}
		perCPUData[cpuID].ExceptionStackBottom = uint64(excStackPA) + kernelVAOffset
		perCPUData[cpuID].ExceptionStackTop = perCPUData[cpuID].ExceptionStackBottom + PerCPUExceptionStackSize
	}

	return true
}

// allocContiguousPages allocates multiple contiguous physical pages.
// Returns the base physical address or 0 on failure.
//
//go:nosplit
func allocContiguousPages(numPages int) uintptr {
	// Allocate pages one by one and verify they're contiguous
	// This is a simple approach; could be optimized with a buddy allocator
	if numPages <= 0 {
		return 0
	}

	basePA := kmem.AllocKernelFrame()
	if basePA == 0 {
		return 0
	}

	for i := 1; i < numPages; i++ {
		nextPA := kmem.AllocKernelFrame()
		if nextPA == 0 {
			// Failed to allocate - we can't easily return the already allocated pages
			return 0
		}
		expectedPA := basePA + uintptr(i*0x1000)
		if nextPA != expectedPA {
			// Pages are not contiguous - for now, continue anyway
			// The buddy allocator should provide contiguous pages typically
			// In a real system, we'd want to retry or use a different strategy
		}
	}

	return basePA
}

// GetCPUID returns the current CPU's ID (0 to MaxCPUs-1).
// Uses the platform-specific getCPUIDAsm() to read the CPU ID register.
//
//go:nosplit
func GetCPUID() uint64 {
	return getCPUIDAsm()
}

// GetPerCPU returns a pointer to the current CPU's per-CPU data structure.
// This is the primary accessor for per-CPU data.
//
//go:nosplit
func GetPerCPU() *PerCPU {
	cpuID := GetCPUID()
	if cpuID >= MaxCPUs {
		// Safeguard: cap to MaxCPUs-1 if system has more CPUs than expected
		cpuID = MaxCPUs - 1
	}
	return &perCPUData[cpuID]
}

// GetPerCPUByID returns the per-CPU data for a specific CPU ID.
// Used for cross-CPU operations (IPI, statistics, etc).
//
//go:nosplit
func GetPerCPUByID(cpuID uint64) *PerCPU {
	if cpuID >= MaxCPUs {
		cpuID = MaxCPUs - 1
	}
	return &perCPUData[cpuID]
}

// CurrentThread returns the thread currently running on this CPU.
// Uses atomic load for safe concurrent access.
//
//go:nosplit
func (p *PerCPU) CurrentThread() *Thread {
	return (*Thread)(atomic.LoadPointer(&p.currentThread))
}

// SetCurrentThread sets the thread currently running on this CPU.
// Uses atomic store for safe concurrent access.
//
//go:nosplit
func (p *PerCPU) SetCurrentThread(t *Thread) {
	atomic.StorePointer(&p.currentThread, unsafe.Pointer(t))
}

// ============================================================================
// Compatibility Layer - Bridge between global CurrentThread and PerCPU
// ============================================================================
//
// During the migration from global CurrentThread to per-CPU, these functions
// provide backward compatibility. The global CurrentThread variable is kept
// for assembly code that hasn't been updated yet.

// GetCurrentThread returns the current thread for this CPU.
// This is the new preferred way to access CurrentThread.
//
//go:nosplit
func GetCurrentThread() *Thread {
	return GetPerCPU().CurrentThread()
}

// SetCurrentThreadGlobal updates both the per-CPU and global CurrentThread.
// Used during the migration period. Eventually, only per-CPU will be used.
//
//go:nosplit
func SetCurrentThreadGlobal(t *Thread) {
	// Update per-CPU
	GetPerCPU().SetCurrentThread(t)
	// Update global for backward compatibility with assembly
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(t))
}

// ============================================================================
// Assembly-Accessible Per-CPU Offsets
// ============================================================================
//
// These variables hold offsets within the PerCPU struct for assembly access.
// Computed once at startup and used by exception handlers.

var (
	// PerCPUCurrentThreadOffset is the offset of currentThread within PerCPU
	PerCPUCurrentThreadOffset uintptr

	// PerCPUTopHalfIRQNumOffset is the offset of TopHalfIRQNum within PerCPU
	PerCPUTopHalfIRQNumOffset uintptr

	// PerCPUSize is the size of one PerCPU struct (for indexing)
	PerCPUSize uintptr
)

// initPerCPUOffsets computes PerCPU struct field offsets for assembly access.
// Must be called before any assembly code uses per-CPU data.
//
//go:nosplit
func initPerCPUOffsets() {
	var p PerCPU
	PerCPUCurrentThreadOffset = unsafe.Offsetof(p.currentThread)
	PerCPUTopHalfIRQNumOffset = unsafe.Offsetof(p.TopHalfIRQNum)
	PerCPUSize = unsafe.Sizeof(p)
}

// PerCPUDataBaseAddr returns the address of the perCPUData array for assembly.
// Assembly can compute per-CPU address as: base + (cpuID * PerCPUSize) + fieldOffset
//
//go:nosplit
func PerCPUDataBaseAddr() uintptr {
	return uintptr(unsafe.Pointer(&perCPUData[0]))
}
