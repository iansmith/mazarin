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

	println("[runtime] RegisterMazModuledata: registered successfully")
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
