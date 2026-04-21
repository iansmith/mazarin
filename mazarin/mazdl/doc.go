// Package mazdl is the userspace loader for .maz plugins produced by
// mazlink's Phase-2 plugin mode. It is the Mazarin analogue of glibc's
// dlopen, specialized for the one case Go plugins actually need:
//
//  1. Read the plugin ELF from disk.
//  2. Map each PT_LOAD into the host's address space at the requested vaddr.
//  3. Apply R_*_RELATIVE relocations against the load base.
//  4. Walk .rela.dyn / .rela.plt UNDEF entries, look each symbol name up in
//     the host's exported dynsym table (populated by RegisterHost() at
//     startup), write the resolved address into the corresponding GOT/PLT
//     slot.
//  5. Walk DT_INIT_ARRAY. The first entry is always the linker-generated
//     addmoduledata wrapper that registers the plugin's firstmoduledata
//     with the host runtime; subsequent entries are user init funcs.
//  6. Return a Handle whose Sym(name) returns the absolute address of any
//     symbol the plugin DEFINED in its own dynsym.
//
// There is no cross-module lookup: Handle.Sym only returns this module's
// own defined symbols. The global table is used only during Open's UNDEF
// resolution pass, never read by user code.
//
// Phase 4 MVP limits:
//   - arm64 and amd64 (R_AARCH64_{RELATIVE,GLOB_DAT,JUMP_SLOT} /
//     R_X86_64_{RELATIVE,GLOB_DAT,JMP_SLOT,64}).
//   - Linux only (plain mmap via syscall; no SysMapELFSegment kernel
//     syscall yet — that lands when we integrate with the real shepherd).
//   - Plugin DT_NEEDED must be exactly "mazarin-host".
//   - Duplicate symbol export across modules is an error (no last-wins).
//
// See design/MAZARIN-DLOPEN.md §6 for the full design.
package mazdl
