// Mazzy overlay: Register .maz module pclntab with Go runtime for stack traces
// and cross-module type deduplication.

//go:build linux && (amd64 || arm64 || riscv64)

package runtime

import (
	"internal/abi"
	"unsafe"
)

// RegisterMazModuledata registers a .maz module's moduledata with the Go runtime.
// The .maz's firstmoduledata (built by the linker, PIE-relocated by LoadMaz) contains
// pclntab, ftab, findfuncbucket, and text range information. After registration,
// findfunc(pc) will resolve .maz PCs to function names, enabling stack traces.
//
// Additionally, this function runs typelinksinit() and itabsinit() to build the
// typemap for cross-module type deduplication. Without this, type assertions like
// shepherd.(blockdev.BlockDevice) fail across module boundaries because the .maz
// has its own copy of type descriptors.
//
// mdPtr is the address of the .maz's runtime.firstmoduledata in the shepherd's VA space.
//
//go:linkname RegisterMazModuledata RegisterMazModuledata
func RegisterMazModuledata(mdPtr uintptr) {
	if mdPtr == 0 {
		return
	}
	md := (*moduledata)(unsafe.Pointer(mdPtr))

	// Save init tasks before clearing — we'll selectively run safe ones later.
	mazInitTasks := md.inittasks

	// Clear fields that could cause issues when registered with the shepherd's runtime.
	// Keep typelinks and itablinks intact — they are needed for cross-module type
	// deduplication (typelinksinit) and interface dispatch (itabsinit). Without them,
	// type assertions across module boundaries fail.
	md.hasmain = 0
	md.bad = false
	md.inittasks = nil
	md.ptab = nil
	md.pkghashes = nil
	md.modulehashes = nil
	md.pluginpath = ""
	md.modulename = ".maz"
	md.typemap = nil
	md.next = nil

	println("[runtime] RegisterMazModuledata: text=", hex(md.text), "-", hex(md.etext),
		"nfunc=", md.pcHeader.nfunc,
		"typelinks=", len(md.typelinks), "itablinks=", len(md.itablinks))
	// Append to the moduledata linked list.
	lastmoduledatap.next = md
	lastmoduledatap = md

	// Rebuild the activeModules snapshot so GC and findfunc see the new module.
	modulesinit()

	// Build typemap for the new module so that types shared between the host
	// shepherd and the .maz resolve to the same *_type pointers. This is the
	// same sequence used by Go's plugin system (see runtime/plugin.go).
	typelinksinit()

	// Extend the typemap to cover interface method types that aren't in typelinks.
	// typelinksinit only maps types listed in typelinks. Interface method signature
	// types (e.g. func(uint64, []byte) error) may not be in typelinks, causing
	// itabInit's method type comparison to fail. This function finds matching
	// interfaces across modules and maps their method types directly.
	buildCompleteTypemap(md)

	// Register the .maz's pre-built itabs in the global itab table.
	lock(&itabLock)
	for _, i := range md.itablinks {
		itabAdd(i)
	}
	unlock(&itabLock)

	// Selectively run .maz init tasks. Skip any task whose functions belong
	// to the "runtime" package — those init functions read/write the .maz's
	// own copy of runtime globals (ncpu, allp, sched, etc.) which are zero
	// and cause panics (e.g., divide-by-zero). Non-runtime init tasks are
	// safe because their runtime calls are trampolined to the host shepherd.
	if len(mazInitTasks) > 0 {
		ran, skipped := runSafeMazInitTasks(mazInitTasks, md)
		println("[runtime] RegisterMazModuledata: ran", ran, "init tasks, skipped", skipped, "(runtime)")
	}

	println("[runtime] RegisterMazModuledata: registered successfully")
}

// runSafeMazInitTasks runs .maz init tasks that don't belong to the runtime
// package. Returns (ran, skipped) counts.
func runSafeMazInitTasks(tasks []*initTask, md *moduledata) (int, int) {
	ran, skipped := 0, 0
	for _, t := range tasks {
		if t.state == 2 {
			continue // already done
		}
		if t.nfns == 0 {
			continue
		}
		// Check the first function pointer — all functions in an initTask
		// belong to the same package (the linker groups them this way).
		firstFunc := add(unsafe.Pointer(t), 8)
		pc := *(*uintptr)(firstFunc)

		name := funcname(findfunc(pc))
		if stringHasPrefix(name, "runtime.") ||
			stringHasPrefix(name, "internal/runtime") ||
			stringHasPrefix(name, "internal/abi") {
			skipped++
			continue
		}
		doInit1(t)
		ran++
	}
	return ran, skipped
}

// stringHasPrefix is a runtime-safe prefix check (can't import strings).
func stringHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// buildCompleteTypemap extends the .maz module's typemap beyond what typelinks covers.
// For each interface type in the .maz, it searches the host's typelinks and itablinks
// for a matching interface, then maps the .maz's method type offsets to the host's
// resolved method types. This ensures itabInit's method type pointer comparison works
// across modules.
func buildCompleteTypemap(md *moduledata) {
	if md.typemap == nil {
		return
	}

	host := &firstmoduledata
	added := 0

	// Collect all interface types in the .maz from typelinks AND itablinks.
	mazIfaces := make(map[uintptr]*interfacetype)
	for _, tl := range md.typelinks {
		t := (*_type)(unsafe.Pointer(md.types + uintptr(tl)))
		if t.Kind_&abi.KindMask == abi.Interface {
			iface := (*interfacetype)(unsafe.Pointer(t))
			mazIfaces[uintptr(unsafe.Pointer(iface))] = iface
		}
	}
	for _, itab := range md.itablinks {
		mazIfaces[uintptr(unsafe.Pointer(itab.Inter))] = itab.Inter
	}

	// For each .maz interface, find the matching host interface.
	for _, mazIface := range mazIfaces {
		mazIfaceType := &mazIface.Type

		hostIface := findHostInterface(host, mazIfaceType)
		if hostIface == nil || len(hostIface.Methods) != len(mazIface.Methods) {
			continue
		}

		// Map each .maz method type offset to the host's resolved type.
		// Methods are sorted identically in both modules.
		for i, mazMethod := range mazIface.Methods {
			hostMethod := hostIface.Methods[i]
			mazOff := mazMethod.Typ

			// Skip if already properly mapped to a host type
			if existing := md.typemap[mazOff]; existing != nil {
				mazLocal := (*_type)(unsafe.Pointer(md.types + uintptr(mazOff)))
				if existing != mazLocal {
					continue
				}
			}

			// Map .maz method type offset → host's method type
			hostMethodType := (*_type)(unsafe.Pointer(host.types + uintptr(hostMethod.Typ)))
			md.typemap[mazOff] = hostMethodType
			added++
		}
	}

	if added > 0 {
		println("[runtime] buildCompleteTypemap: added", added, "method type mappings")
	}
}

// === .maz writeBarrier sync ===
//
// .maz PIE modules include their own runtime.writeBarrier BSS variable.
// The host GC toggles writeBarrier.enabled via setGCPhase during STW, but
// the .maz's copy is never updated. Compiler-generated code in .maz checks
// its own copy, so the write barrier is permanently disabled for .maz code.
// This causes the GC to miss pointer stores to .maz globals, leading to
// "checkmark found unmarked object" crashes.
//
// Solution: RegisterMazWriteBarrier records each .maz's writeBarrier address.
// syncMazWriteBarriers (called from startTheWorldWithSema during STW exit)
// copies the host's writeBarrier.enabled to all registered .maz copies.

// mazWriteBarriers holds the addresses of .maz writeBarrier variables.
var mazWriteBarriers [16]uintptr
var mazWriteBarrierCount int32

// RegisterMazWriteBarrier registers a .maz module's runtime.writeBarrier
// address for GC phase sync. Also performs an initial sync so the .maz
// sees the correct state if loaded during an active GC cycle.
//
//go:linkname RegisterMazWriteBarrier RegisterMazWriteBarrier
func RegisterMazWriteBarrier(addr uintptr) {
	if addr == 0 {
		return
	}
	n := mazWriteBarrierCount
	if n >= int32(len(mazWriteBarriers)) {
		println("[runtime] RegisterMazWriteBarrier: too many .maz modules (max", len(mazWriteBarriers), ")")
		return
	}
	mazWriteBarriers[n] = addr
	mazWriteBarrierCount = n + 1

	// Initial sync: copy current host writeBarrier state to the .maz.
	// The host may already be in a GC mark phase.
	mazWB := (*struct {
		enabled bool
		pad     [3]byte
		alignme uint64
	})(unsafe.Pointer(addr))
	mazWB.enabled = writeBarrier.enabled

	println("[runtime] RegisterMazWriteBarrier: registered .maz writeBarrier at", hex(addr),
		"enabled=", writeBarrier.enabled)
}

// syncMazWriteBarriers copies the host's writeBarrier.enabled to all
// registered .maz writeBarrier addresses. Called from startTheWorldWithSema
// after setGCPhase has toggled the host's writeBarrier.enabled but before
// goroutines resume.
//
//go:nosplit
func syncMazWriteBarriers() {
	n := mazWriteBarrierCount
	if n == 0 {
		return
	}
	val := writeBarrier.enabled
	for i := int32(0); i < n; i++ {
		*(*bool)(unsafe.Pointer(mazWriteBarriers[i])) = val
	}
}

// findHostInterface searches the host module's typelinks and itablinks for an
// interface type that matches the given type (by hash and deep equality).
func findHostInterface(host *moduledata, target *_type) *interfacetype {
	// Search typelinks first
	for _, tl := range host.typelinks {
		ht := (*_type)(unsafe.Pointer(host.types + uintptr(tl)))
		if ht.Hash == target.Hash && ht.Kind_&abi.KindMask == abi.Interface {
			seen := map[_typePair]struct{}{}
			if typesEqual(target, ht, seen) {
				return (*interfacetype)(unsafe.Pointer(ht))
			}
		}
	}
	// Search itablinks
	for _, hitab := range host.itablinks {
		hinter := hitab.Inter
		if hinter.Type.Hash == target.Hash {
			seen := map[_typePair]struct{}{}
			if typesEqual(&hinter.Type, target, seen) {
				return hinter
			}
		}
	}
	return nil
}
