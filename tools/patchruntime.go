//go:build !qemuvirt

// patchruntime patches a userspace ELF binary to redirect all runtime functions
// to priest's runtime via trampolines.
//
// Usage: patchruntime <priest.elf> <userspace.elf> <output.elf>
//
// Each runtime function's prologue is replaced with:
//   MOVD $priest_addr, R16
//   JMP  (R16)
//
// This makes userspace programs share priest's runtime - scheduler, GC, etc.
package main

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	// Minimum function size required for the 12-byte trampoline
	minTrampolineSize = 12
)

// RuntimeFunc holds information about a runtime function to patch
type RuntimeFunc struct {
	Name         string
	PriestAddr   uint64
	UserAddr     uint64
	UserSize     uint64
	UserFileOff  uint64 // File offset in userspace binary
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s <priest.elf> <userspace.elf> <output.elf>\n", os.Args[0])
		os.Exit(1)
	}

	priestPath := os.Args[1]
	userspacePath := os.Args[2]
	outputPath := os.Args[3]

	// Read priest's runtime functions
	priestFuncs, err := readRuntimeFunctions(priestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading priest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found %d runtime functions in priest\n", len(priestFuncs))

	// Read userspace's runtime functions with file offsets
	userFuncs, err := readRuntimeFunctionsWithOffsets(userspacePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading userspace: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found %d runtime functions in userspace\n", len(userFuncs))

	// Match functions
	toMatch := matchFunctions(priestFuncs, userFuncs)
	fmt.Printf("Matched %d functions to patch\n", len(toMatch))

	// Check for functions too small
	var tooSmall []string
	for _, fn := range toMatch {
		if fn.UserSize < minTrampolineSize {
			tooSmall = append(tooSmall, fmt.Sprintf("%s (size=%d)", fn.Name, fn.UserSize))
		}
	}
	if len(tooSmall) > 0 {
		fmt.Fprintf(os.Stderr, "\nWARNING: %d functions too small for %d-byte trampoline:\n",
			len(tooSmall), minTrampolineSize)
		for _, name := range tooSmall[:min(10, len(tooSmall))] {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		if len(tooSmall) > 10 {
			fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(tooSmall)-10)
		}
		fmt.Fprintf(os.Stderr, "These functions will NOT be patched.\n\n")
	}

	// Filter to only patchable functions
	var patchable []RuntimeFunc
	for _, fn := range toMatch {
		if fn.UserSize >= minTrampolineSize {
			patchable = append(patchable, fn)
		}
	}
	fmt.Printf("Will patch %d functions\n", len(patchable))

	// Copy input to output
	if err := copyFile(userspacePath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error copying file: %v\n", err)
		os.Exit(1)
	}

	// Patch the output file
	patched, err := patchFunctions(outputPath, patchable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error patching: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully patched %d runtime functions in %s\n", patched, outputPath)
}

// isRuntimeSymbol returns true if this is a runtime function to redirect
func isRuntimeSymbol(name string) bool {
	if !strings.HasPrefix(name, "runtime.") {
		return false
	}

	// Exclude functions that must remain local
	excludePatterns := []string{
		".abi0",              // ABI0 wrappers - keep for cross-package calls
		"systemstack_switch", // Context switch internals
		"mcall",              // Context switch
		"gogo",               // Context switch
		"gosave",             // Context switch
		"mstart",             // Thread startup
		"rt0_go",             // Entry point
		"_rt0_",              // Entry points
		"asminit",            // Low-level init
		"abort",              // Must be local
		"breakpoint",         // Debug
		"morestack",          // Stack growth - ABI sensitive
		"gcWriteBarrier",     // GC write barrier - ABI sensitive
		"cgoSigtramp",        // CGO signal handling
		"sigtramp",           // Signal trampoline
		"asmcgocall",         // CGO
		"cgocall",            // CGO
	}

	for _, pattern := range excludePatterns {
		if strings.Contains(name, pattern) {
			return false
		}
	}

	return true
}

// readRuntimeFunctions reads runtime function addresses from an ELF
func readRuntimeFunctions(path string) (map[string]uint64, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	symbols, err := f.Symbols()
	if err != nil {
		return nil, err
	}

	funcs := make(map[string]uint64)
	for _, sym := range symbols {
		if elf.ST_TYPE(sym.Info) != elf.STT_FUNC || sym.Value == 0 {
			continue
		}
		if isRuntimeSymbol(sym.Name) {
			funcs[sym.Name] = sym.Value
		}
	}

	return funcs, nil
}

// readRuntimeFunctionsWithOffsets reads runtime functions with their file offsets
func readRuntimeFunctionsWithOffsets(path string) (map[string]RuntimeFunc, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	symbols, err := f.Symbols()
	if err != nil {
		return nil, err
	}

	funcs := make(map[string]RuntimeFunc)
	for _, sym := range symbols {
		if elf.ST_TYPE(sym.Info) != elf.STT_FUNC || sym.Value == 0 {
			continue
		}
		if !isRuntimeSymbol(sym.Name) {
			continue
		}

		// Calculate file offset
		fileOff, err := vaddrToFileOffset(f, sym.Value)
		if err != nil {
			// Skip functions not in loadable segments
			continue
		}

		funcs[sym.Name] = RuntimeFunc{
			Name:        sym.Name,
			UserAddr:    sym.Value,
			UserSize:    sym.Size,
			UserFileOff: fileOff,
		}
	}

	return funcs, nil
}

// vaddrToFileOffset converts virtual address to file offset
func vaddrToFileOffset(f *elf.File, vaddr uint64) (uint64, error) {
	for _, prog := range f.Progs {
		if prog.Type != elf.PT_LOAD {
			continue
		}
		if vaddr >= prog.Vaddr && vaddr < prog.Vaddr+prog.Filesz {
			return prog.Off + (vaddr - prog.Vaddr), nil
		}
	}
	return 0, fmt.Errorf("vaddr 0x%x not in loadable segment", vaddr)
}

// matchFunctions creates the list of functions to patch
func matchFunctions(priestFuncs map[string]uint64, userFuncs map[string]RuntimeFunc) []RuntimeFunc {
	var matched []RuntimeFunc

	// Get sorted names for consistent output
	var names []string
	for name := range userFuncs {
		if _, ok := priestFuncs[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		fn := userFuncs[name]
		fn.PriestAddr = priestFuncs[name]
		matched = append(matched, fn)
	}

	return matched
}

// copyFile copies a file
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// patchFunctions patches each function's prologue with a trampoline
func patchFunctions(path string, functions []RuntimeFunc) (int, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	patched := 0
	for _, fn := range functions {
		// Generate 12-byte trampoline
		trampoline := generateTrampoline(fn.PriestAddr)

		// Write at function's file offset
		if _, err := f.Seek(int64(fn.UserFileOff), 0); err != nil {
			return patched, fmt.Errorf("seek for %s: %w", fn.Name, err)
		}
		if _, err := f.Write(trampoline); err != nil {
			return patched, fmt.Errorf("write for %s: %w", fn.Name, err)
		}
		patched++
	}

	return patched, nil
}

// generateTrampoline creates a 12-byte ARM64 trampoline to jump to the target address
//
// Instructions (little-endian ARM64):
//   MOVZ X16, #(addr & 0xFFFF)
//   MOVK X16, #((addr >> 16) & 0xFFFF), LSL #16
//   BR   X16
//
// For addresses > 32 bits, we'd need more MOVK instructions, but priest addresses
// are typically small enough to fit in 32 bits.
func generateTrampoline(targetAddr uint64) []byte {
	trampoline := make([]byte, 12)

	// MOVZ X16, #imm16 (zeros other bits)
	// Encoding: sf=1, opc=10, hw=00, imm16, Rd
	// 1 10 100101 00 imm16 Rd
	// = 0xD2800010 | (imm16 << 5)
	imm16_0 := uint32(targetAddr & 0xFFFF)
	movz := uint32(0xD2800010) | (imm16_0 << 5)
	binary.LittleEndian.PutUint32(trampoline[0:4], movz)

	// MOVK X16, #imm16, LSL #16 (keeps other bits)
	// Encoding: sf=1, opc=11, hw=01, imm16, Rd
	// 1 11 100101 01 imm16 Rd
	// = 0xF2A00010 | (imm16 << 5)
	imm16_1 := uint32((targetAddr >> 16) & 0xFFFF)
	movk := uint32(0xF2A00010) | (imm16_1 << 5)
	binary.LittleEndian.PutUint32(trampoline[4:8], movk)

	// BR X16
	// Encoding: 1101011 0000 11111 000000 Rn 00000
	// = 0xD61F0200
	br := uint32(0xD61F0200)
	binary.LittleEndian.PutUint32(trampoline[8:12], br)

	return trampoline
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
