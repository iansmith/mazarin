// Run QEMU with cardinal/kmazarin
//
// Usage: go run scripts/run.go [-t TIMEOUT] [-c] [-s] [wait_seconds]
//
// Options:
//
//	-t TIMEOUT  Kill QEMU after TIMEOUT seconds
//	-c          Count mode: count occurrences of 111222 and 222111
//	-s          Squeeze mode: compress repeated 1's and 2's
//	wait_secs   Seconds to wait before showing output (default: 3)
//
// Environment:
//
//	QEMU    Path to qemu-system-aarch64 (default: from PATH)
//
// Requires QEMU 10.2 or later.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	requiredQEMUMajor = 10
	requiredQEMUMinor = 2
	logFile           = "/tmp/cardinal-serial.log"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse flags
	timeout := flag.Int("t", 0, "Kill QEMU after this many seconds")
	countMode := flag.Bool("c", false, "Count mode: count scheduling patterns")
	squeezeMode := flag.Bool("s", false, "Squeeze mode: compress repeated 1s and 2s")
	flag.Parse()

	// Get wait time from remaining args
	waitSecs := 3
	if flag.NArg() > 0 {
		if w, err := strconv.Atoi(flag.Arg(0)); err == nil {
			waitSecs = w
		}
	}

	// Find project root
	projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}

	// Find QEMU
	qemuPath, err := findQEMU()
	if err != nil {
		return err
	}

	// Check QEMU version
	if err := checkQEMUVersion(qemuPath); err != nil {
		return err
	}

	// Stop any existing QEMU
	stopQEMU()

	// Clear old log
	os.Remove(logFile)

	fmt.Printf("=== Starting QEMU (wait %ds) ===\n", waitSecs)

	// Start QEMU
	kernelPath := filepath.Join(projectRoot, "build", "cardinal.elf")
	cmd := exec.Command(qemuPath,
		"-M", "virt,virtualization=off",
		"-cpu", "cortex-a72",
		"-m", "8G",
		"-kernel", kernelPath,
		"-nodefaults",
		"-device", "bochs-display",
		"-object", "rng-random,id=rng0,filename=/dev/urandom",
		"-device", "virtio-rng-pci,rng=rng0,disable-legacy=on",
		"-display", "none",
		"-serial", "file:"+logFile,
		"-monitor", "tcp:127.0.0.1:4444,server,nowait",
		"-semihosting",
		"-no-reboot",
	)
	cmd.Dir = projectRoot

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting QEMU: %w", err)
	}

	fmt.Printf("QEMU PID: %d\n", cmd.Process.Pid)
	fmt.Printf("Log file: %s\n", logFile)
	fmt.Printf("Monitor: tcp:127.0.0.1:4444\n")

	// Setup timeout if requested
	if *timeout > 0 {
		fmt.Printf("Timeout: %ds\n", *timeout)
		go func() {
			time.Sleep(time.Duration(*timeout) * time.Second)
			cmd.Process.Kill()
		}()
	}

	// Wait for output
	time.Sleep(time.Duration(waitSecs) * time.Second)

	// Read and display output
	if err := displayOutput(*countMode, *squeezeMode); err != nil {
		return err
	}

	return nil
}

func findProjectRoot() (string, error) {
	// Try to find project root from executable location
	// Executable is at build/tools/run, so we need 3 Dir() calls
	exe, err := os.Executable()
	if err == nil && !strings.Contains(exe, "go-build") {
		// build/tools/run -> build/tools -> build -> project root
		return filepath.Dir(filepath.Dir(filepath.Dir(exe))), nil
	}

	// Fallback: use working directory
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	return wd, nil
}

func findQEMU() (string, error) {
	// Check QEMU environment variable first
	if qemu := os.Getenv("QEMU"); qemu != "" {
		if _, err := os.Stat(qemu); err != nil {
			return "", fmt.Errorf("specified QEMU not found: %s", qemu)
		}
		return qemu, nil
	}

	// Look in PATH
	qemu, err := exec.LookPath("qemu-system-aarch64")
	if err != nil {
		return "", fmt.Errorf("qemu-system-aarch64 not found\n\nInstall QEMU %d.%d+ or set QEMU to point to it:\n  QEMU=/path/to/qemu-system-aarch64 go run scripts/run.go",
			requiredQEMUMajor, requiredQEMUMinor)
	}
	return qemu, nil
}

func checkQEMUVersion(qemuPath string) error {
	cmd := exec.Command(qemuPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("could not get QEMU version: %w", err)
	}

	// Extract version from output like "QEMU emulator version 10.2.0"
	re := regexp.MustCompile(`(\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 3 {
		return fmt.Errorf("could not parse QEMU version from: %s", output)
	}

	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])

	if major < requiredQEMUMajor || (major == requiredQEMUMajor && minor < requiredQEMUMinor) {
		return fmt.Errorf("QEMU version mismatch.\n\n  Required: %d.%d+\n  Found:    %d.%d (from: %s)\n\nInstall QEMU %d.%d+ or set QEMU to point to it:\n  QEMU=/path/to/qemu-system-aarch64 go run scripts/run.go",
			requiredQEMUMajor, requiredQEMUMinor, major, minor, qemuPath, requiredQEMUMajor, requiredQEMUMinor)
	}

	return nil
}

func stopQEMU() {
	// Kill any existing QEMU instances
	exec.Command("pkill", "-f", "qemu-system-aarch64").Run()
	time.Sleep(300 * time.Millisecond)
}

func displayOutput(countMode, squeezeMode bool) error {
	// Read log file
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("\n(no output yet)")
			return nil
		}
		return fmt.Errorf("reading log file: %w", err)
	}

	// Filter control characters
	filtered := filterControlChars(data)

	if countMode {
		fmt.Println("\n=== Pattern Analysis ===")
	} else {
		fmt.Println()
		if squeezeMode {
			fmt.Println("=== Serial Output (compressed, last 500 chars) ===")
			output := squeezeTail(filtered, 500)
			fmt.Println(output)
		} else {
			fmt.Println("=== Serial Output (last 500 chars) ===")
			if len(filtered) > 500 {
				filtered = filtered[len(filtered)-500:]
			}
			fmt.Println(string(filtered))
		}
		fmt.Println()
	}

	// Show log size
	info, _ := os.Stat(logFile)
	if info != nil {
		fmt.Printf("=== Log size: %d bytes ===\n", info.Size())
	}

	// Count and show scheduling patterns
	count111222 := bytes.Count(data, []byte("111222"))
	count222111 := bytes.Count(data, []byte("222111"))
	total := count111222 + count222111

	fmt.Println("\n=== Preemptive Scheduling Indicators ===")
	fmt.Printf("111222 occurrences: %d\n", count111222)
	fmt.Printf("222111 occurrences: %d\n", count222111)
	if total <= 2 {
		fmt.Printf("Total patterns: %d (LOW - possible scheduling issue!)\n", total)
	} else {
		fmt.Printf("Total patterns: %d\n", total)
	}

	return nil
}

func filterControlChars(data []byte) []byte {
	// Remove control characters (0x00-0x08, 0x0B-0x1F, 0x7F-0xFF)
	// Keep 0x09 (tab), 0x0A (newline)
	result := make([]byte, 0, len(data))
	for _, b := range data {
		if b == 0x09 || b == 0x0A || (b >= 0x20 && b < 0x7F) {
			result = append(result, b)
		}
	}
	return result
}

func squeezeTail(data []byte, n int) string {
	if len(data) > n {
		data = data[len(data)-n:]
	}

	// Compress runs of 1s and 2s
	result := make([]byte, 0, len(data))
	var lastChar byte
	for _, b := range data {
		if (b == '1' || b == '2') && b == lastChar {
			continue // Skip repeated 1s and 2s
		}
		result = append(result, b)
		lastChar = b
	}
	return string(result)
}
