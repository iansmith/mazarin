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
	overlayType := flag.String("type", "", "overlay type: kmazarin, userspace")
	patchesDir := flag.String("patches", "", "directory containing patch files")
	output := flag.String("o", "", "output JSON file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gen-overlay -type TYPE -patches DIR -o OUTPUT\n")
		fmt.Fprintf(os.Stderr, "Generate Go overlay JSON for patched runtime files.\n\n")
		fmt.Fprintf(os.Stderr, "Types:\n")
		fmt.Fprintf(os.Stderr, "  kmazarin   - Kernel runtime patches\n")
		fmt.Fprintf(os.Stderr, "  userspace  - Userspace runtime patches\n")
		fmt.Fprintf(os.Stderr, "  diplomat        - UEFI bootloader runtime patches (Windows→UEFI, deprecated)\n")
		fmt.Fprintf(os.Stderr, "  diplomat-linux  - UEFI bootloader runtime patches (Linux→UEFI)\n\n")
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
	case "userspace":
		err = buildUserspaceOverlay(&overlay, goroot, absPatchesDir)
	case "diplomat":
		err = buildDiplomatOverlay(&overlay, goroot, absPatchesDir)
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
		"runtime/cgo_mmap.go":       "cgo_mmap.go",
		"runtime/malloc.go":         "malloc.go",
		"runtime/mcache.go":         "mcache.go",
		"runtime/os_linux_arm64.go": "os_linux_arm64.go",
		"runtime/preempt.go":        "preempt.go",
		"runtime/tagptr_64bit.go":   "tagptr_64bit.go",
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
	// We're building with GOOS=linux GOARCH=amd64, so we patch the Linux syscall/runtime.
	//
	// Critical patches:
	// - syscall_linux.go: Centralize syscall routing
	// - sys_linux_amd64.s: Stub out all syscall instructions (write1, exit, futex, etc.)
	patches := map[string]string{
		"syscall/syscall_linux.go":     "syscall_linux.go",
		"runtime/sys_linux_amd64.s":    "sys_linux_amd64.s",
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
