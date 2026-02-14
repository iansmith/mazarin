// gen-overlay generates Go overlay JSON files for patched runtime files.
//
// Usage:
//
//	go tool gen-overlay -type kmazarin -patches runtime-patches -o build/overlay.json
//	go tool gen-overlay -type userspace -patches mazarin/overlay/userspace -o build/overlay.json
//
// The tool calls $GO env GOROOT to find the Go installation and generates
// a JSON overlay file mapping standard library files to patched versions.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Overlay struct {
	Replace map[string]string `json:"Replace"`
}

func main() {
	overlayType := flag.String("type", "", "overlay type: kmazarin, kmazarin-amd64, kmazarin-riscv64, userspace")
	patchesDir := flag.String("patches", "", "directory containing patch files")
	output := flag.String("o", "", "output JSON file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gen-overlay -type TYPE -patches DIR -o OUTPUT\n")
		fmt.Fprintf(os.Stderr, "Generate Go overlay JSON for patched runtime files.\n\n")
		fmt.Fprintf(os.Stderr, "Types:\n")
		fmt.Fprintf(os.Stderr, "  kmazarin         - Kernel runtime patches (ARM64)\n")
		fmt.Fprintf(os.Stderr, "  kmazarin-amd64   - Kernel runtime patches (x86_64)\n")
		fmt.Fprintf(os.Stderr, "  kmazarin-riscv64 - Kernel runtime patches (RISC-V 64)\n")
		fmt.Fprintf(os.Stderr, "  userspace        - Userspace runtime patches\n")
		fmt.Fprintf(os.Stderr, "  cardinal-linux   - Cardinal bootloader runtime patches (Linux ARM64→bare metal)\n")
		fmt.Fprintf(os.Stderr, "  diplomat         - UEFI bootloader runtime patches (Windows→UEFI, deprecated)\n")
		fmt.Fprintf(os.Stderr, "  diplomat-linux   - UEFI bootloader runtime patches (Linux→UEFI)\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *overlayType == "" || *patchesDir == "" || *output == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Get GOROOT by calling $GO env GOROOT
	goroot, err := getGOROOT()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-overlay: %v\n", err)
		os.Exit(1)
	}

	// Get absolute path to patches directory
	absPatchesDir, err := filepath.Abs(*patchesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-overlay: %v\n", err)
		os.Exit(1)
	}

	// Build the overlay based on type
	var overlay Overlay
	overlay.Replace = make(map[string]string)

	switch *overlayType {
	case "kmazarin":
		err = buildKmazarinOverlay(&overlay, goroot, absPatchesDir)
	case "kmazarin-amd64":
		err = buildKmazarinAMD64Overlay(&overlay, goroot, absPatchesDir)
	case "kmazarin-riscv64":
		err = buildKmazarinRISCV64Overlay(&overlay, goroot, absPatchesDir)
	case "userspace":
		err = buildUserspaceOverlay(&overlay, goroot, absPatchesDir)
	case "diplomat":
		err = buildDiplomatOverlay(&overlay, goroot, absPatchesDir)
	case "cardinal-linux":
		err = buildCardinalLinuxOverlay(&overlay, goroot, absPatchesDir)
	case "diplomat-linux":
		err = buildDiplomatLinuxOverlay(&overlay, goroot, absPatchesDir)
	default:
		fmt.Fprintf(os.Stderr, "gen-overlay: unknown type: %s\n", *overlayType)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-overlay: %v\n", err)
		os.Exit(1)
	}

	// Write output
	data, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen-overlay: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*output, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "gen-overlay: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s\n", *output)
}

func getGOROOT() (string, error) {
	goBin := os.Getenv("GO")
	if goBin == "" {
		goBin = "go"
	}

	cmd := exec.Command(goBin, "env", "GOROOT")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get GOROOT: %v", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func buildKmazarinOverlay(overlay *Overlay, goroot, patchesDir string) error {
	// Kmazarin patches for runtime
	runtimePatches := map[string]string{
		"runtime/cgo_mmap.go":        "cgo_mmap.go",
		"runtime/malloc.go":          "malloc.go",
		"runtime/mcache.go":          "mcache.go",
		"runtime/os_linux_arm64.go":  "os_linux_arm64.go",
		"runtime/preempt.go":         "preempt.go",
		"runtime/sys_linux_arm64.s":  "sys_linux_arm64.s",
		"runtime/tagptr_64bit.go":    "tagptr_64bit.go",
		// "runtime/traceback.go":      "traceback.go",
		// "runtime/panic.go":          "panic.go",
	}

	for goFile, patchFile := range runtimePatches {
		src := filepath.Join(goroot, "src", goFile)
		dst := filepath.Join(patchesDir, patchFile)
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("patch file not found: %s", dst)
		}
		overlay.Replace[src] = dst
	}

	// Syscall patch
	syscallSrc := filepath.Join(goroot, "src", "syscall/syscall_linux.go")
	syscallDst := filepath.Join(patchesDir, "syscall/syscall_linux.go")
	if _, err := os.Stat(syscallDst); err != nil {
		return fmt.Errorf("patch file not found: %s", syscallDst)
	}
	overlay.Replace[syscallSrc] = syscallDst

	return nil
}

func buildKmazarinAMD64Overlay(overlay *Overlay, goroot, patchesDir string) error {
	// Kmazarin patches for x86_64 runtime — same arch-independent patches as ARM64,
	// but with amd64-specific os_linux and sys_linux files.
	runtimePatches := map[string]string{
		"runtime/cgo_mmap.go":                          "cgo_mmap.go",
		"runtime/malloc.go":                            "malloc.go",
		"runtime/mcache.go":                            "mcache.go",
		"runtime/os_linux_noauxv.go":                   "os_linux_noauxv.go",
		"runtime/preempt.go":                           "preempt.go",
		"runtime/sys_linux_amd64.s":                    "sys_linux_amd64.s",
		"runtime/tagptr_64bit.go":                      "tagptr_64bit.go",
		"internal/runtime/syscall/asm_linux_amd64.s":   "asm_linux_amd64.s",
	}

	for goFile, patchFile := range runtimePatches {
		src := filepath.Join(goroot, "src", goFile)
		dst := filepath.Join(patchesDir, patchFile)
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("patch file not found: %s", dst)
		}
		overlay.Replace[src] = dst
	}

	// Syscall patch
	syscallSrc := filepath.Join(goroot, "src", "syscall/syscall_linux.go")
	syscallDst := filepath.Join(patchesDir, "syscall/syscall_linux.go")
	if _, err := os.Stat(syscallDst); err != nil {
		return fmt.Errorf("patch file not found: %s", syscallDst)
	}
	overlay.Replace[syscallSrc] = syscallDst

	return nil
}

func buildKmazarinRISCV64Overlay(overlay *Overlay, goroot, patchesDir string) error {
	// Kmazarin patches for RISC-V 64-bit runtime — same arch-independent patches as ARM64/AMD64,
	// but with riscv64-specific os_linux and sys_linux files.
	runtimePatches := map[string]string{
		"runtime/cgo_mmap.go":            "cgo_mmap.go",
		"runtime/mmap.go":                "mmap.go",
		"runtime/malloc.go":              "malloc.go",
		"runtime/mcache.go":              "mcache.go",
		"runtime/os_linux_noauxv.go":     "os_linux_noauxv.go",
		"runtime/preempt.go":             "preempt.go",
		"runtime/sys_linux_riscv64.s":    "sys_linux_riscv64.s",
		"runtime/rt0_linux_riscv64.s":    "rt0_linux_riscv64.s",
		"runtime/tagptr_64bit.go":        "tagptr_64bit.go",
		"runtime/fds_unix.go":            "fds_unix.go",
	}

	for goFile, patchFile := range runtimePatches {
		src := filepath.Join(goroot, "src", goFile)
		dst := filepath.Join(patchesDir, patchFile)
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("patch file not found: %s", dst)
		}
		overlay.Replace[src] = dst
	}

	// Syscall patch
	syscallSrc := filepath.Join(goroot, "src", "syscall/syscall_linux.go")
	syscallDst := filepath.Join(patchesDir, "syscall/syscall_linux.go")
	if _, err := os.Stat(syscallDst); err != nil {
		return fmt.Errorf("patch file not found: %s", syscallDst)
	}
	overlay.Replace[syscallSrc] = syscallDst

	return nil
}

func buildUserspaceOverlay(overlay *Overlay, goroot, patchesDir string) error {
	// Userspace patches
	patches := map[string]string{
		"syscall/syscall_linux.go":    "syscall_linux.go",
		"syscall/asm_linux_arm64.s":   "asm_linux_arm64.s",
		"syscall/asm_linux_amd64.s":   "asm_linux_amd64.s",
		"runtime/cgo_mmap.go":         "runtime/cgo_mmap.go",
		"runtime/lock_spinbit.go":     "runtime/lock_spinbit.go",
	}

	for goFile, patchFile := range patches {
		src := filepath.Join(goroot, "src", goFile)
		dst := filepath.Join(patchesDir, patchFile)
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("patch file not found: %s", dst)
		}
		overlay.Replace[src] = dst
	}

	return nil
}

func buildCardinalLinuxOverlay(overlay *Overlay, goroot, patchesDir string) error {
	// Cardinal patches for Linux ARM64 runtime to make it bare-metal compatible.
	// We're building with GOOS=linux GOARCH=arm64, so we patch the Linux syscall/runtime.
	patches := map[string]string{
		"syscall/syscall_linux.go":    "syscall_linux.go",
		"runtime/sys_linux_arm64.s":   "sys_linux_arm64.s",
		"runtime/mbitmap.go":          "mbitmap.go",
	}

	for goFile, patchFile := range patches {
		src := filepath.Join(goroot, "src", goFile)
		dst := filepath.Join(patchesDir, patchFile)
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("patch file not found: %s", dst)
		}
		overlay.Replace[src] = dst
	}

	return nil
}

func buildDiplomatOverlay(overlay *Overlay, goroot, patchesDir string) error {
	// Diplomat patches for Windows runtime to make it UEFI-compatible
	// These replace Windows-specific runtime files with UEFI stubs
	patches := map[string]string{
		"runtime/os_windows.go":         "os_windows.go",
		"runtime/syscall_windows.go":    "syscall_windows.go",
		"runtime/sys_windows_amd64.s":   "sys_windows_amd64.s",
		"runtime/mem_windows.go":        "mem_windows.go",
	}

	for goFile, patchFile := range patches {
		src := filepath.Join(goroot, "src", goFile)
		dst := filepath.Join(patchesDir, patchFile)
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("patch file not found: %s", dst)
		}
		overlay.Replace[src] = dst
	}

	return nil
}

func buildDiplomatLinuxOverlay(overlay *Overlay, goroot, patchesDir string) error {
	// Diplomat patches for Linux runtime to make it UEFI-compatible.
	// We're building with GOOS=linux GOARCH={amd64,riscv64}, so we patch the Linux syscall/runtime.
	//
	// Critical patches:
	// - syscall_linux.go: Centralize syscall routing
	// - sys_linux_amd64.s: Stub out all syscall instructions (write1, exit, futex, etc.) for x86_64
	// - rt0_linux_riscv64.s: Trampoline entry point for RISC-V OpenSBI boot
	patches := map[string]string{
		"syscall/syscall_linux.go":     "syscall_linux.go",
		"runtime/sys_linux_amd64.s":    "sys_linux_amd64.s",
		"runtime/rt0_linux_riscv64.s":  "trampoline_riscv64.s",
		"debug/elf/file.go":            "elf_file.go",
		"debug/elf/reader.go":          "elf_reader.go",
	}

	for goFile, patchFile := range patches {
		src := filepath.Join(goroot, "src", goFile)
		dst := filepath.Join(patchesDir, patchFile)
		if _, err := os.Stat(dst); err != nil {
			// Skip missing files (e.g., arch-specific files for other archs)
			continue
		}
		overlay.Replace[src] = dst
	}

	return nil
}
