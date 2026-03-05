// Mazzy overlay: Register .maz module pclntab with Go runtime for stack traces.

//go:build linux && (amd64 || arm64 || riscv64)

package runtime

import "unsafe"

// RegisterMazModuledata registers a .maz module's moduledata with the Go runtime.
// The .maz's firstmoduledata (built by the linker, PIE-relocated by LoadMaz) contains
// pclntab, ftab, findfuncbucket, and text range information. After registration,
// findfunc(pc) will resolve .maz PCs to function names, enabling stack traces.
//
// mdPtr is the address of the .maz's runtime.firstmoduledata in the priest's VA space.
//
//go:linkname RegisterMazModuledata RegisterMazModuledata
func RegisterMazModuledata(mdPtr uintptr) {
	if mdPtr == 0 {
		return
	}
	md := (*moduledata)(unsafe.Pointer(mdPtr))

	// Clear fields that could cause issues when registered with the priest's runtime.
	// We only want PC→function lookup (stack traces), not full plugin integration.
	md.hasmain = 0
	md.bad = false
	md.itablinks = nil
	md.typelinks = nil
	md.inittasks = nil
	md.ptab = nil
	md.pkghashes = nil
	md.modulehashes = nil
	md.pluginpath = ""
	md.modulename = ".maz"
	md.typemap = nil
	md.next = nil

	println("[runtime] RegisterMazModuledata: text=", hex(md.text), "-", hex(md.etext),
		"minpc=", hex(md.minpc), "maxpc=", hex(md.maxpc))

	if md.pcHeader != nil {
		println("[runtime] RegisterMazModuledata: pcHeader magic=", hex(md.pcHeader.magic),
			"nfunc=", md.pcHeader.nfunc, "textStart=", hex(md.pcHeader.textStart))
	}

	// Append to the moduledata linked list.
	// This is equivalent to what runtime.addmoduledata does in assembly.
	lastmoduledatap.next = md
	lastmoduledatap = md

	// Rebuild the activeModules snapshot so GC and findfunc see the new module.
	modulesinit()

	println("[runtime] RegisterMazModuledata: registered successfully")
}
