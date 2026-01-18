package kthread

import (
	"sync"

	"kmazarin/util"
)

// Global state
var (
	// All priests
	allPriests     = util.NewDLinkedList[*PCB]()
	allPriestsLock sync.Mutex

	// Scheduler
	runQueue     = util.NewDLinkedList[*KThread]()
	runQueueLock sync.Mutex

	// Per-CPU state (single CPU for now)
	cpuState [MaxCPUs]CPUSchedState

	// Linux PID allocation (for emulation)
	linuxPIDToPCB [128]*PCB
	linuxPIDLock  sync.RWMutex
	nextLinuxPID  uint8 = 1
)

// CPUSchedState holds per-CPU scheduler state.
type CPUSchedState struct {
	CurrentThread *KThread
	IdleThread    *KThread
}

// MaxCPUs is the maximum number of CPUs supported.
const MaxCPUs = 1 // Single CPU initially

// allocateLinuxPID assigns a Linux-style PID to a PCB (1-127).
func allocateLinuxPID(pcb *PCB) uint8 {
	linuxPIDLock.Lock()
	defer linuxPIDLock.Unlock()

	startID := nextLinuxPID
	for {
		if linuxPIDToPCB[nextLinuxPID] == nil {
			linuxPIDToPCB[nextLinuxPID] = pcb
			pcb.LinuxPID = nextLinuxPID
			nextLinuxPID++
			if nextLinuxPID >= 128 {
				nextLinuxPID = 1
			}
			return pcb.LinuxPID
		}
		nextLinuxPID++
		if nextLinuxPID >= 128 {
			nextLinuxPID = 1
		}
		if nextLinuxPID == startID {
			panic("no available Linux PIDs (127 priests active)")
		}
	}
}

// releaseLinuxPID frees a Linux PID for reuse.
func releaseLinuxPID(pcb *PCB) {
	linuxPIDLock.Lock()
	defer linuxPIDLock.Unlock()
	if pcb.LinuxPID > 0 && pcb.LinuxPID < 128 {
		linuxPIDToPCB[pcb.LinuxPID] = nil
	}
}

// LookupPCBByLinuxPID finds a PCB by its Linux emulation PID.
func LookupPCBByLinuxPID(pid uint8) *PCB {
	linuxPIDLock.RLock()
	defer linuxPIDLock.RUnlock()
	if pid == 0 || pid >= 128 {
		return nil
	}
	return linuxPIDToPCB[pid]
}

// GetCurrentKThread returns the thread running on the current CPU.
func GetCurrentKThread() *KThread {
	return cpuState[0].CurrentThread // Single CPU for now
}

// GetCurrentPCB returns the priest of the current thread.
func GetCurrentPCB() *PCB {
	kt := GetCurrentKThread()
	if kt == nil {
		return nil
	}
	return kt.Priest
}

// SetCurrentKThread sets the current thread for the CPU.
func SetCurrentKThread(kt *KThread) {
	cpuState[0].CurrentThread = kt
}
