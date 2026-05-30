
// processenv.go - Reusable builder for process environment (envp + auxv) and stack layout.
package ksyscall

import "unsafe"

type envEntry struct{ key, value string }
type auxvEntry struct{ key, value uint64 }

// ProcessEnv collects environment variables and auxiliary vector entries.
// Both use replace-on-duplicate-key semantics.
type ProcessEnv struct {
	env  []envEntry
	auxv []auxvEntry
}

// NewProcessEnv returns an empty ProcessEnv.
func NewProcessEnv() *ProcessEnv {
	return &ProcessEnv{}
}

// SetEnv adds or replaces an environment variable (stored as "key=value").
func (p *ProcessEnv) SetEnv(key, value string) {
	for i := range p.env {
		if p.env[i].key == key {
			p.env[i].value = value
			return
		}
	}
	p.env = append(p.env, envEntry{key, value})
}

// SetAuxv adds or replaces an auxiliary vector entry.
func (p *ProcessEnv) SetAuxv(key, value uint64) {
	for i := range p.auxv {
		if p.auxv[i].key == key {
			p.auxv[i].value = value
			return
		}
	}
	p.auxv = append(p.auxv, auxvEntry{key, value})
}

// StackWriter encapsulates parameters for writing to a userspace stack via
// kernel scratch mapping.
//
// The stack spans multiple pages; each user stack page is mapped to a kernel
// scratch VA (MapPAToKernelScratch is a fixed linear PA→VA offset, so physical
// frames that are non-contiguous land at non-contiguous scratch VAs). PageVAs /
// ScratchVAs hold one entry per mapped stack page, lowest VA first, so writeByte
// can reach any byte in the stack — not just the top page. They are sized and
// populated by setupUserStack; a faithful execve argv+envp that overflows the
// single top page is the whole reason this is page-aware (MAZ-120).
//
// KernelVA is retained as the top-page scratch mapping for callers that only
// touch the top page (the legacy single-page path); when PageVAs is populated
// it is unused.
type StackWriter struct {
	StackBase, StackTop uint64
	KernelVA            uintptr
	PageVAs             []uint64  // user VA of each mapped stack page (page-aligned, ascending)
	ScratchVAs          []uintptr // kernel scratch VA for the page at the same index
}

// writeByte writes a single byte to the stack through the kernel scratch
// mapping, resolving which stack page addr lands in.
func (sw *StackWriter) writeByte(addr uint64, val byte) {
	if addr < sw.StackBase || addr >= sw.StackTop {
		return // Out of bounds
	}
	pageSize := uint64(4096)
	pageVA := addr &^ (pageSize - 1)
	offset := addr - pageVA

	// Page-aware path: find the scratch mapping for addr's page.
	for i, pv := range sw.PageVAs {
		if pv == pageVA {
			*(*byte)(unsafe.Pointer(sw.ScratchVAs[i] + uintptr(offset))) = val
			return
		}
	}

	// Legacy single-page fallback: only the top page is mapped via KernelVA.
	topPageVA := (sw.StackTop - 1) &^ (pageSize - 1)
	if sw.KernelVA != 0 && pageVA == topPageVA {
		*(*byte)(unsafe.Pointer(sw.KernelVA + uintptr(offset))) = val
	}
}

// writeU64 writes a uint64 in little-endian order.
func (sw *StackWriter) writeU64(addr, val uint64) {
	for i := uint64(0); i < 8; i++ {
		sw.writeByte(addr+i, byte(val>>(i*8)))
	}
}

// envString returns "key=value" for an envEntry.
func (e *envEntry) envString() string {
	return e.key + "=" + e.value
}

// EnvBytes returns the collected mandatory environment as a list of
// "key=value" byte slices, in insertion order. The clone_exec faithful-env
// path merges this (mandatory) with the caller's envp via proc.MergeExecEnv
// before laying out the child stack (MAZ-120).
func (p *ProcessEnv) EnvBytes() [][]byte {
	out := make([][]byte, len(p.env))
	for i := range p.env {
		out[i] = []byte(p.env[i].envString())
	}
	return out
}

// Layout writes the complete Linux process startup stack using the ProcessEnv's
// own collected envp (the RunShepherd / embedded-launch path). argv is the
// shepherd-launch argument vector. See LayoutFaithful for the execve path that
// supplies a pre-merged caller envp.
func (p *ProcessEnv) Layout(argv []string, sw *StackWriter) (uint64, error) {
	argvBytes := make([][]byte, len(argv))
	for i, s := range argv {
		argvBytes[i] = []byte(s)
	}
	return p.LayoutFaithful(argvBytes, p.EnvBytes(), sw)
}

// LayoutFaithful writes the complete Linux process startup stack (argc, argv,
// envp, auxv, string data) into the stack area described by sw, using the
// supplied argv and envp verbatim. Returns the aligned stack pointer to set for
// the process. The auxv comes from the ProcessEnv; argv/envp are caller-owned
// (the execve faithful-argv path passes the merged caller env here).
//
// Stack layout from SP upward:
//
//	argc                                (uint64)
//	argv[0] ... argv[argc-1]            (pointers)
//	NULL                                (end of argv)
//	envp[0] ... envp[N-1]               (pointers)
//	NULL                                (end of envp)
//	auxv[0].key, auxv[0].val, ...       (key-value pairs)
//	AT_NULL (0), 0                      (auxv terminator)
//	<string data area>                  (null-terminated C strings)
func (p *ProcessEnv) LayoutFaithful(argv, envp [][]byte, sw *StackWriter) (uint64, error) {
	// Count pointer-area slots:
	//   1 (argc) + len(argv) + 1 (NULL) + len(envp) + 1 (NULL)
	//   + (len(auxv)+1)*2 (pairs including AT_NULL terminator)
	numSlots := 1 + len(argv) + 1 + len(envp) + 1 + (len(p.auxv)+1)*2
	pointerAreaSize := uint64(numSlots) * 8

	// String data: all argv and envp strings, each with null terminator
	stringDataSize := uint64(0)
	for _, s := range argv {
		stringDataSize += uint64(len(s)) + 1
	}
	for _, s := range envp {
		stringDataSize += uint64(len(s)) + 1
	}

	totalSize := (pointerAreaSize + stringDataSize + 15) &^ 15
	// Reject a layout that does not fit the mapped stack — writeByte silently
	// no-ops out-of-range bytes, so an unchecked overflow would corrupt the
	// child's argv/envp instead of failing loudly. The caller surfaces this as
	// E2BIG (MAZ-120).
	if totalSize > sw.StackTop-sw.StackBase {
		return 0, &elfError{"process stack layout exceeds mapped stack"}
	}
	sp := (sw.StackTop - totalSize) &^ 15
	stringBase := sp + pointerAreaSize

	// Phase 1: Write string data, record userspace VAs
	stringOff := uint64(0)

	argvAddrs := make([]uint64, len(argv))
	for i, s := range argv {
		addr := stringBase + stringOff
		argvAddrs[i] = addr
		for j := 0; j < len(s); j++ {
			sw.writeByte(addr+uint64(j), s[j])
		}
		sw.writeByte(addr+uint64(len(s)), 0)
		stringOff += uint64(len(s)) + 1
	}

	envpAddrs := make([]uint64, len(envp))
	for i, s := range envp {
		addr := stringBase + stringOff
		envpAddrs[i] = addr
		for j := 0; j < len(s); j++ {
			sw.writeByte(addr+uint64(j), s[j])
		}
		sw.writeByte(addr+uint64(len(s)), 0)
		stringOff += uint64(len(s)) + 1
	}

	// Phase 2: Write pointer area
	pos := sp

	// argc
	sw.writeU64(pos, uint64(len(argv)))
	pos += 8

	// argv pointers + NULL
	for _, addr := range argvAddrs {
		sw.writeU64(pos, addr)
		pos += 8
	}
	sw.writeU64(pos, 0)
	pos += 8

	// envp pointers + NULL
	for _, addr := range envpAddrs {
		sw.writeU64(pos, addr)
		pos += 8
	}
	sw.writeU64(pos, 0)
	pos += 8

	// auxv pairs + AT_NULL terminator
	for _, av := range p.auxv {
		sw.writeU64(pos, av.key)
		pos += 8
		sw.writeU64(pos, av.value)
		pos += 8
	}
	sw.writeU64(pos, 0) // AT_NULL
	pos += 8
	sw.writeU64(pos, 0)

	return sp, nil
}
