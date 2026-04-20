// check-version validates tool versions meet minimum requirements.
//
// Usage:
//
//	go tool check-version -go $GO -min 1.24
//	go tool check-version -qemu $QEMU -min 10.2
//
// Exit codes:
//
//	0 - Version meets requirement
//	1 - Version too old or error
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	goBin := flag.String("go", "", "path to go binary")
	qemuBin := flag.String("qemu", "", "path to qemu-system-aarch64 binary")
	minVersion := flag.String("min", "", "minimum version required (e.g., 1.24 or 10.2)")
	quiet := flag.Bool("q", false, "quiet mode - no output on success")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: check-version [-go PATH | -qemu PATH] -min VERSION\n")
		fmt.Fprintf(os.Stderr, "Validate tool versions meet minimum requirements.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *minVersion == "" {
		fmt.Fprintf(os.Stderr, "check-version: -min is required\n")
		os.Exit(1)
	}

	minMajor, minMinor, err := parseVersion(*minVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-version: invalid min version: %v\n", err)
		os.Exit(1)
	}

	var toolName string
	var actualVersion string
	var actualMajor, actualMinor int

	if *goBin != "" {
		toolName = "Go"
		actualVersion, actualMajor, actualMinor, err = getGoVersion(*goBin)
	} else if *qemuBin != "" {
		toolName = "QEMU"
		actualVersion, actualMajor, actualMinor, err = getQEMUVersion(*qemuBin)
	} else {
		fmt.Fprintf(os.Stderr, "check-version: specify -go or -qemu\n")
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "check-version: %v\n", err)
		os.Exit(1)
	}

	// Compare versions
	if actualMajor < minMajor || (actualMajor == minMajor && actualMinor < minMinor) {
		fmt.Fprintf(os.Stderr, "ERROR: %s >= %s required, found %s\n", toolName, *minVersion, actualVersion)
		os.Exit(1)
	}

	if !*quiet {
		fmt.Printf("%s: %s (%s)\n", toolName, actualVersion, getBinPath(*goBin, *qemuBin))
	}
	os.Exit(0)
}

func getBinPath(goBin, qemuBin string) string {
	if goBin != "" {
		return goBin
	}
	return qemuBin
}

func parseVersion(s string) (major, minor int, err error) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("expected MAJOR.MINOR format")
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return major, minor, nil
}

func getGoVersion(goBin string) (version string, major, minor int, err error) {
	cmd := exec.Command(goBin, "version")
	output, err := cmd.Output()
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to run go version: %v", err)
	}

	// Parse "go version go1.26.2 darwin/arm64"
	re := regexp.MustCompile(`go(\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 3 {
		return "", 0, 0, fmt.Errorf("cannot parse go version from: %s", output)
	}

	major, _ = strconv.Atoi(matches[1])
	minor, _ = strconv.Atoi(matches[2])
	version = fmt.Sprintf("%d.%d", major, minor)
	return version, major, minor, nil
}

func getQEMUVersion(qemuBin string) (version string, major, minor int, err error) {
	cmd := exec.Command(qemuBin, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to run qemu --version: %v", err)
	}

	// Parse "QEMU emulator version 10.2.0"
	re := regexp.MustCompile(`(\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 3 {
		return "", 0, 0, fmt.Errorf("cannot parse qemu version from: %s", output)
	}

	major, _ = strconv.Atoi(matches[1])
	minor, _ = strconv.Atoi(matches[2])
	version = fmt.Sprintf("%d.%d", major, minor)
	return version, major, minor, nil
}
