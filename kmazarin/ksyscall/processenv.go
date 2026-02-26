
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
type StackWriter struct {
	StackBase, StackTop uint64
	KernelVA            uintptr
}

// writeByte writes a single byte to the stack through the kernel scratch mapping.
func (sw *StackWriter) writeByte(addr uint64, val byte) {
	if addr < sw.StackBase || addr >= sw.StackTop {
		return // Out of bounds
	}
	pageSize := uint64(4096)
	topPageVA := (sw.StackTop - 1) &^ (pageSize - 1)
	if addr >= topPageVA && addr < topPageVA+pageSize {
		offset := addr - topPageVA
		*(*byte)(unsafe.Pointer(sw.KernelVA + uintptr(offset))) = val
	}
	// TODO: Handle multiple pages if needed
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

// Layout writes the complete Linux process startup stack (argc, argv, envp,
// auxv, string data) into the stack area described by sw. Returns the aligned
// stack pointer to set for the process.
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
func (p *ProcessEnv) Layout(argv []string, sw *StackWriter) (uint64, error) {
	envpStrings := make([]string, len(p.env))
	for i := range p.env {
		envpStrings[i] = p.env[i].envString()
	}

	// Count pointer-area slots:
	//   1 (argc) + len(argv) + 1 (NULL) + len(envp) + 1 (NULL)
	//   + (len(auxv)+1)*2 (pairs including AT_NULL terminator)
	numSlots := 1 + len(argv) + 1 + len(envpStrings) + 1 + (len(p.auxv)+1)*2
	pointerAreaSize := uint64(numSlots) * 8

	// String data: all argv and envp strings, each with null terminator
	stringDataSize := uint64(0)
	for _, s := range argv {
		stringDataSize += uint64(len(s)) + 1
	}
	for _, s := range envpStrings {
		stringDataSize += uint64(len(s)) + 1
	}

	totalSize := (pointerAreaSize + stringDataSize + 15) &^ 15
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

	envpAddrs := make([]uint64, len(envpStrings))
	for i, s := range envpStrings {
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
