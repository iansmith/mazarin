//go:build ignore
// +build ignore

// Runtime patching tool for replacing Go runtime functions.
// Scans assembly files to find .global runtime.* symbols that need patching,
// then patches ELF binaries to redirect function calls from Go runtime's weak
// symbols to our strong global implementations.
//
// Usage: go run patch-runtime.go <elf_file> <asm_dir>
//
// The tool automatically discovers which runtime symbols need patching by
// scanning .s files for .global runtime.* declarations.

package main

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <elf_file> <asm_dir>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s build/mazboot/mazboot.elf src/mazboot/asm/aarch64\n", os.Args[0])
		os.Exit(1)
	}

	elfPath := os.Args[1]
	asmDir := os.Args[2]

	// Find runtime symbols that need patching by scanning .s files
	replacements := findRuntimeReplacements(asmDir)
	if len(replacements) == 0 {
		fmt.Println("No runtime symbols found to patch")
		os.Exit(0)
	}

	fmt.Printf("Found %d runtime symbol(s) to patch:\n", len(replacements))
	for old, new := range replacements {
		fmt.Printf("  %s -> %s\n", old, new)
	}

	// Patch the ELF file
	if err := patchRuntime(elfPath, replacements); err != nil {
		fmt.Fprintf(os.Stderr, "Error patching runtime: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nSuccessfully patched %d symbol(s)\n", len(replacements))
}

// findRuntimeReplacements scans assembly files for .global runtime.* symbols
// Returns a map of old_func -> new_func (both same name, redirecting to our implementation)
// Only includes symbols that need runtime patching (write barrier functions)
func findRuntimeReplacements(asmDir string) map[string]string {
	replacements := make(map[string]string)

	// Only patch write barrier symbols (gcWriteBarrier*)
	// Other runtime.* symbols like morestack don't need patching
	writeBarrierRe := regexp.MustCompile(`^\.global\s+(runtime\.gcWriteBarrier[234]|gcWriteBarrier)\b`)

	filepath.Walk(asmDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".s") {
			return nil
		}

		content, err := ioutil.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			matches := writeBarrierRe.FindStringSubmatch(line)
			if len(matches) > 1 {
				symbol := matches[1]
				// For runtime.* symbols, we redirect from Go's weak version to our strong version
				// Both old and new have the same name
				replacements[symbol] = symbol
			}
		}

		return nil
	})

	return replacements
}

// patchRuntime patches an ELF binary to redirect function calls
func patchRuntime(elfPath string, replacements map[string]string) error {
	// Open ELF file
	file, err := elf.Open(elfPath)
	if err != nil {
		return fmt.Errorf("failed to open ELF file: %w", err)
	}
	defer file.Close()

	// Read the entire file
	data, err := ioutil.ReadFile(elfPath)
	if err != nil {
		return fmt.Errorf("failed to read ELF file: %w", err)
	}

	// Find .text section
	var textSection *elf.Section
	for _, section := range file.Sections {
		if section.Name == ".text" {
			textSection = section
			break
		}
	}
	if textSection == nil {
		return fmt.Errorf("could not find .text section")
	}

	// Get bin directory for target-* tools
	binDir := findBinDir()
	env := os.Environ()
	env = append(env, "PATH="+binDir+":"+os.Getenv("PATH"))

	totalPatches := 0

	// Process each replacement
	for oldFunc, newFunc := range replacements {
		// Find symbol addresses using target-nm
		oldAddr, err := findSymbolAddress(elfPath, oldFunc, env, 't') // Prefer weak/local from Go runtime
		if err != nil {
			fmt.Printf("  Warning: Could not find old symbol %s: %v\n", oldFunc, err)
			continue
		}

		newAddr, err := findSymbolAddress(elfPath, newFunc, env, 'T') // Prefer strong/global from our code
		if err != nil {
			fmt.Printf("  Warning: Could not find new symbol %s: %v\n", newFunc, err)
			continue
		}

		// Find all call sites
		callSites, err := findCallSites(elfPath, oldAddr, env)
		if err != nil {
			fmt.Printf("  Warning: Could not find call sites for %s: %v\n", oldFunc, err)
			continue
		}

		if len(callSites) == 0 {
			// Skip gcWriteBarrier if no calls found (it's optional)
			if oldFunc == "gcWriteBarrier" {
				continue
			}
			fmt.Printf("  Warning: No call sites found for %s\n", oldFunc)
			continue
		}

		// Patch each call site
		for _, callAddr := range callSites {
			if patchCallSite(data, textSection, callAddr, oldAddr, newAddr) {
				totalPatches++
			}
		}
	}

	if totalPatches > 0 {
		// Write patched file
		if err := ioutil.WriteFile(elfPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write patched file: %w", err)
		}
		fmt.Printf("\nSuccessfully patched %d call site(s)\n", totalPatches)
		return nil
	}

	fmt.Println("\nNo patches applied")
	return nil
}

// findBinDir finds the bin directory containing target-* tools
func findBinDir() string {
	// Assume we're running from project root or src/mazboot
	// Try to find bin/ directory

	// Check current directory
	if _, err := os.Stat("bin"); err == nil {
		abs, _ := filepath.Abs("bin")
		return abs
	}

	// Check parent directories
	for i := 1; i <= 3; i++ {
		path := filepath.Join(strings.Repeat("../", i), "bin")
		if _, err := os.Stat(path); err == nil {
			abs, _ := filepath.Abs(path)
			return abs
		}
	}

	// Default to relative path
	return "bin"
}

// findSymbolAddress finds the address of a symbol using target-nm
func findSymbolAddress(elfPath, symbolName string, env []string, preferType byte) (uint64, error) {
	cmd := exec.Command("target-nm", elfPath)
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("target-nm failed: %w", err)
	}

	var matches []struct {
		addr uint64
		typ  byte
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			addrStr := parts[0]
			symType := parts[1]
			name := strings.Join(parts[2:], " ")

			if name == symbolName && (symType[0] == 'T' || symType[0] == 't') {
				addr, err := parseHex(addrStr)
				if err != nil {
					continue
				}
				matches = append(matches, struct {
					addr uint64
					typ  byte
				}{addr, symType[0]})
			}
		}
	}

	if len(matches) == 0 {
		return 0, fmt.Errorf("symbol %s not found", symbolName)
	}

	// Prefer the specified type
	for _, m := range matches {
		if m.typ == preferType {
			return m.addr, nil
		}
	}

	// Return first match
	return matches[0].addr, nil
}

// findCallSites finds all call sites (bl instructions) that call the target address
func findCallSites(elfPath string, targetAddr uint64, env []string) ([]uint64, error) {
	cmd := exec.Command("target-objdump", "-d", elfPath)
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("target-objdump failed: %w", err)
	}

	var callSites []uint64
	blRe := regexp.MustCompile(`^\s*([0-9a-f]+):\s+[0-9a-f]{8}\s+bl\s+([0-9a-f]+)`)

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		matches := blRe.FindStringSubmatch(line)
		if len(matches) >= 3 {
			callAddr, err1 := parseHex(matches[1])
			target, err2 := parseHex(matches[2])
			if err1 == nil && err2 == nil && target == targetAddr {
				callSites = append(callSites, callAddr)
			}
		}
	}

	return callSites, nil
}

// patchCallSite patches a single call site to redirect from old_target to new_target
func patchCallSite(data []byte, textSection *elf.Section, callVAddr, oldTarget, newTarget uint64) bool {
	// Calculate file offset
	fileOffset := int64(textSection.Offset) + int64(callVAddr-textSection.Addr)

	if fileOffset < 0 || fileOffset >= int64(len(data)) {
		fmt.Printf("  Warning: Invalid file offset 0x%x for call at 0x%x\n", fileOffset, callVAddr)
		return false
	}

	// Read current instruction
	currentInsn := binary.LittleEndian.Uint32(data[fileOffset:])

	// Verify it's a bl instruction (opcode 0x94000000)
	if (currentInsn & 0xfc000000) != 0x94000000 {
		fmt.Printf("  Warning: Instruction at 0x%x is not a bl instruction: 0x%x\n", callVAddr, currentInsn)
		return false
	}

	// Decode current target to verify
	imm26 := currentInsn & 0x3ffffff
	// Sign extend 26-bit immediate
	if imm26&0x2000000 != 0 {
		imm26 |= 0xfc000000 // Sign extend
	}
	currentOffset := int64(imm26) << 2
	currentTarget := callVAddr + uint64(currentOffset)

	if currentTarget != oldTarget {
		// Allow some address differences (LOAD_ADDRESS, RAM_BASE offsets)
		diff := currentTarget
		if currentTarget > oldTarget {
			diff = currentTarget - oldTarget
		} else {
			diff = oldTarget - currentTarget
		}
		if diff != 0x200000 && diff != 0x40000000 && diff != 0x400000000 {
			fmt.Printf("  Warning: Call at 0x%x targets 0x%x, expected 0x%x\n", callVAddr, currentTarget, oldTarget)
		}
	}

	// Encode new instruction
	newOffset := int64(newTarget) - int64(callVAddr)
	if newOffset%4 != 0 {
		fmt.Printf("  Error: Target address must be 4-byte aligned (offset=%d)\n", newOffset)
		return false
	}
	if newOffset < -0x8000000 || newOffset > 0x7ffffff {
		fmt.Printf("  Error: Branch offset out of range: %d (max ±128MB)\n", newOffset)
		return false
	}

	imm26 = uint32((newOffset >> 2) & 0x3ffffff)
	newInsn := 0x94000000 | imm26

	// Write the new instruction
	binary.LittleEndian.PutUint32(data[fileOffset:], newInsn)
	return true
}

// parseHex parses a hexadecimal string
func parseHex(s string) (uint64, error) {
	var val uint64
	_, err := fmt.Sscanf(s, "%x", &val)
	return val, err
}



