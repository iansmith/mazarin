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
	overlayType := flag.String("type", "", "overlay type: kmazarin, kmazarin-amd64, kmazarin-riscv64, userspace, maz-exit, merge")
	patchesDir := flag.String("patches", "", "directory containing patch files")
	output := flag.String("o", "", "output JSON file")
	baseOverlay := flag.String("base", "", "base overlay JSON (for -type merge)")
	extraOverlay := flag.String("extra", "", "extra overlay JSON to merge on top (for -type merge)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gen-overlay -type TYPE -patches DIR -o OUTPUT\n")
		fmt.Fprintf(os.Stderr, "   or: gen-overlay -type merge -base BASE.json -extra EXTRA.json -o OUTPUT\n")
		fmt.Fprintf(os.Stderr, "Generate Go overlay JSON for patched runtime files.\n\n")
		fmt.Fprintf(os.Stderr, "Types:\n")
		fmt.Fprintf(os.Stderr, "  kmazarin         - Kernel runtime patches (ARM64)\n")
		fmt.Fprintf(os.Stderr, "  kmazarin-amd64   - Kernel runtime patches (x86_64)\n")
		fmt.Fprintf(os.Stderr, "  kmazarin-riscv64 - Kernel runtime patches (RISC-V 64)\n")
		fmt.Fprintf(os.Stderr, "  userspace        - Userspace runtime patches\n")
		fmt.Fprintf(os.Stderr, "  cardinal-linux   - Cardinal bootloader runtime patches (Linux ARM64→bare metal)\n")
		fmt.Fprintf(os.Stderr, "  diplomat         - UEFI bootloader runtime patches (Windows→UEFI, deprecated)\n")
		fmt.Fprintf(os.Stderr, "  diplomat-linux   - UEFI bootloader runtime patches (Linux→UEFI)\n")
		fmt.Fprintf(os.Stderr, "  maz-exit         - Maz-specific patches (os.Exit → panic)\n")
		fmt.Fprintf(os.Stderr, "  merge            - Merge two overlay JSONs (extra overwrites base for same keys)\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *overlayType == "" || *output == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Handle merge type specially — it doesn't need GOROOT or patches dir
	if *overlayType == "merge" {
		if *baseOverlay == "" || *extraOverlay == "" {
			fmt.Fprintf(os.Stderr, "gen-overlay: -type merge requires -base and -extra\n")
			os.Exit(1)
		}
		err := mergeOverlays(*baseOverlay, *extraOverlay, *output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen-overlay: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Generated %s\n", *output)
		return
	}

	if *patchesDir == "" {
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
	case "maz-exit":
		err = buildMazExitOverlay(&overlay, goroot, absPatchesDir)
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
		"runtime/fds_unix.go":        "fds_unix.go",
		"runtime/malloc.go":          "malloc.go",
		"runtime/mcache.go":          "mcache.go",
		"runtime/os_linux_arm64.go":  "os_linux_arm64.go",
		"runtime/preempt.go":         "preempt.go",
		"runtime/sys_linux_arm64.s":  "sys_linux_arm64.s",
		"runtime/tagptr_64bit.go":    "tagptr_64bit.go",
		"runtime/traceback.go":        "traceback.go",
		// "runtime/asm_arm64.s":       "asm_arm64.s",
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
		"runtime/fds_unix.go":                          "fds_unix.go",
		"runtime/malloc.go":                            "malloc.go",
		"runtime/mcache.go":                            "mcache.go",
		"runtime/os_linux_noauxv.go":                   "os_linux_noauxv.go",
		"runtime/preempt.go":                           "preempt.go",
		"runtime/sys_linux_amd64.s":                    "sys_linux_amd64.s",
		"runtime/tagptr_64bit.go":                      "tagptr_64bit.go",
		"runtime/traceback.go":                         "traceback.go",
		"runtime/panic.go":                             "panic.go",
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
		"runtime/sigaction.go":           "sigaction.go",
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
		"syscall/asm_linux_riscv64.s": "asm_linux_riscv64.s",
		"runtime/cgo_mmap.go":         "runtime/cgo_mmap.go",

		"runtime/maz_moduledata.go":     "runtime/maz_moduledata.go",

		"runtime/sys_linux_arm64.s":       "runtime/sys_linux_arm64.s",
		"runtime/netpoll_maz_init.go":    "runtime/netpoll_maz_init.go",
		"runtime/walltime_mazzy.go":      "runtime/walltime_mazzy.go",
		"runtime/timestub2.go":        "runtime/timestub2.go",
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

func buildMazExitOverlay(overlay *Overlay, goroot, patchesDir string) error {
	// Maz-specific patches: override os.Exit to panic instead of killing shepherd.
	patches := map[string]string{
		"os/proc.go": "os_proc.go",
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

// mergeOverlays reads two overlay JSON files, merges them (extra overwrites base
// for the same keys), and writes the result to output.
func mergeOverlays(basePath, extraPath, outputPath string) error {
	baseData, err := os.ReadFile(basePath)
	if err != nil {
		return fmt.Errorf("reading base overlay: %w", err)
	}
	extraData, err := os.ReadFile(extraPath)
	if err != nil {
		return fmt.Errorf("reading extra overlay: %w", err)
	}

	var base, extra Overlay
	if err := json.Unmarshal(baseData, &base); err != nil {
		return fmt.Errorf("parsing base overlay: %w", err)
	}
	if err := json.Unmarshal(extraData, &extra); err != nil {
		return fmt.Errorf("parsing extra overlay: %w", err)
	}

	// Start with base, overwrite with extra
	merged := Overlay{Replace: make(map[string]string)}
	for k, v := range base.Replace {
		merged.Replace[k] = v
	}
	for k, v := range extra.Replace {
		merged.Replace[k] = v
	}

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling merged overlay: %w", err)
	}

	fmt.Printf("Merged %d base + %d extra = %d total entries (%d overwritten)\n",
		len(base.Replace), len(extra.Replace), len(merged.Replace),
		len(base.Replace)+len(extra.Replace)-len(merged.Replace))

	return os.WriteFile(outputPath, data, 0644)
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
		"runtime/sys_linux_arm64.s":    "sys_linux_arm64.s",
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
