package kthread

import "kmazarin/util"

// CreatePCB allocates a new PCB.
func CreatePCB() *PCB {
	pcb := &PCB{
		State:           PriestReady,
		Thins:           util.NewDLinkedList[*ThinCB](),
		Threads:         util.NewDLinkedList[*KThread](),
		NextLinuxThinID: 1,
		Symbols:         make(map[string]uint64),
	}

	allocateLinuxPID(pcb)

	allPriestsLock.Lock()
	pcb.listNode = allPriests.PushBack(pcb)
	allPriestsLock.Unlock()

	return pcb
}

// ReleaseResourcesPCB cleans up priest resources.
// All threads and thins must be cleaned up first.
func ReleaseResourcesPCB(pcb *PCB) {
	// Ensure all threads cleaned up first
	if !pcb.Threads.IsEmpty() {
		panic("ReleaseResourcesPCB: threads still exist")
	}

	// Ensure all thins cleaned up
	if !pcb.Thins.IsEmpty() {
		panic("ReleaseResourcesPCB: thins still exist")
	}

	// TODO: Unmap all pages, free page tables
	// for _, pm := range pcb.MappedPages {
	//     kmem.Unmap(pcb.PageTableRoot, pm.VirtAddr)
	//     kmem.FreeFrame(pm.PhysAddr)
	// }

	// Release Linux PID
	releaseLinuxPID(pcb)

	// Remove from global list
	allPriestsLock.Lock()
	allPriests.Remove(pcb.listNode)
	allPriestsLock.Unlock()
	pcb.listNode = nil
}

// CreateKThread creates a new kernel thread in a priest.
func CreateKThread(priest *PCB, entryPoint uint64, stackSize uint64) *KThread {
	kt := &KThread{
		Priest:       priest,
		State:        KThreadReady,
		CurrentCPU:   -1,
		AffinityMask: ^uint64(0), // All CPUs allowed
		Priority:     0,
		TimeSlice:    ThreadTimeSliceTicks,
	}

	// TODO: Allocate stack pages
	// physAddr := kmem.AllocFrames(stackSize / 4096)
	// virtAddr := findFreeVirtAddr(priest, stackSize)
	// kmem.MapPages(priest.PageTableRoot, virtAddr, physAddr, stackSize, ...)
	// kt.StackBase = virtAddr
	// kt.StackTop = virtAddr + stackSize
	_ = stackSize

	// Set up initial context
	kt.Context.ELR = entryPoint
	kt.Context.SP = kt.StackTop
	// SPSR = 0x344 = EL1t mode (M=0100) with D=A=F masked, I=0 (IRQs enabled)
	kt.Context.SPSR = 0x344

	// Add to priest's thread list
	priest.Lock.Lock()
	kt.priestNode = priest.Threads.PushBack(kt)
	priest.Lock.Unlock()

	// Add to run queue
	AddToRunQueue(kt)

	return kt
}

// ReleaseResourcesKThread cleans up thread resources.
func ReleaseResourcesKThread(kt *KThread) {
	priest := kt.Priest

	RemoveFromRunQueue(kt)

	// TODO: Free stack pages
	// kmem.Unmap(priest.PageTableRoot, kt.StackBase)
	// kmem.FreeFrames(kt.StackBase, (kt.StackTop - kt.StackBase) / 4096)

	// Remove from priest's thread list
	priest.Lock.Lock()
	priest.Threads.Remove(kt.priestNode)
	priest.Lock.Unlock()
	kt.priestNode = nil

	kt.Priest = nil
}

// allocateLinuxThinID assigns a Linux-style thin ID within a priest.
func allocateLinuxThinID(priest *PCB) uint8 {
	startID := priest.NextLinuxThinID
	for {
		inUse := false
		for node := priest.Thins.Front(); node != nil; node = node.Next() {
			if node.Value.LinuxThinID == priest.NextLinuxThinID {
				inUse = true
				break
			}
		}
		if !inUse {
			id := priest.NextLinuxThinID
			priest.NextLinuxThinID++
			if priest.NextLinuxThinID == 0 {
				priest.NextLinuxThinID = 1
			}
			return id
		}
		priest.NextLinuxThinID++
		if priest.NextLinuxThinID == 0 {
			priest.NextLinuxThinID = 1
		}
		if priest.NextLinuxThinID == startID {
			panic("no available Linux thin IDs")
		}
	}
}

// CreateThinCB creates a new thin control block in a priest.
func CreateThinCB(priest *PCB) *ThinCB {
	priest.Lock.Lock()
	defer priest.Lock.Unlock()

	thin := &ThinCB{
		LinuxThinID: allocateLinuxThinID(priest),
		Priest:      priest,
		State:       ThinReady,
	}

	thin.listNode = priest.Thins.PushBack(thin)
	return thin
}

// ReleaseResourcesThinCB cleans up thin resources.
func ReleaseResourcesThinCB(thin *ThinCB) {
	priest := thin.Priest

	// TODO: Unmap thin's pages
	// for _, pm := range thin.MappedPages {
	//     kmem.Unmap(priest.PageTableRoot, pm.VirtAddr)
	//     kmem.FreeFrame(pm.PhysAddr)
	// }

	priest.Lock.Lock()
	priest.Thins.Remove(thin.listNode)
	priest.Lock.Unlock()
	thin.listNode = nil

	thin.Priest = nil
}

// GetAllPriests returns a snapshot of all priests (for debugging/inspection).
func GetAllPriests() []*PCB {
	allPriestsLock.Lock()
	defer allPriestsLock.Unlock()

	result := make([]*PCB, 0, allPriests.Size())
	for node := allPriests.Front(); node != nil; node = node.Next() {
		result = append(result, node.Value)
	}
	return result
}

// GetPriestThreadCount returns the number of threads in a priest.
func GetPriestThreadCount(pcb *PCB) int {
	pcb.Lock.Lock()
	defer pcb.Lock.Unlock()
	return pcb.Threads.Size()
}

// GetPriestThinCount returns the number of thins in a priest.
func GetPriestThinCount(pcb *PCB) int {
	pcb.Lock.Lock()
	defer pcb.Lock.Unlock()
	return pcb.Thins.Size()
}
