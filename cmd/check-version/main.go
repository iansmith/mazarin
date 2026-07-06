// check-version validates tool versions meet minimum requirements.
//
// Usage:
//
//	go tool check-version -go $GO -min 1.24
//	go tool check-version -go $GO -min 1.26.4
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
	minVersion := flag.String("min", "", "minimum version required (e.g., 1.24, 1.26.4, or 10.2)")
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

	min, err := parseVersion(*minVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-version: invalid min version: %v\n", err)
		os.Exit(1)
	}

	var toolName string
	var actualVersion string
	var actual version

	if *goBin != "" {
		toolName = "Go"
		actualVersion, actual, err = getGoVersion(*goBin)
	} else if *qemuBin != "" {
		toolName = "QEMU"
		actualVersion, actual, err = getQEMUVersion(*qemuBin)
	} else {
		fmt.Fprintf(os.Stderr, "check-version: specify -go or -qemu\n")
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "check-version: %v\n", err)
		os.Exit(1)
	}

	if actual.less(min) {
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

type version struct {
	major, minor, patch int
}

func (v version) less(w version) bool {
	if v.major != w.major {
		return v.major < w.major
	}
	if v.minor != w.minor {
		return v.minor < w.minor
	}
	return v.patch < w.patch
}

// parseVersion accepts MAJOR.MINOR or MAJOR.MINOR.PATCH; a missing patch
// component is zero.
func parseVersion(s string) (version, error) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return version{}, fmt.Errorf("expected MAJOR.MINOR or MAJOR.MINOR.PATCH format")
	}
	var v version
	var err error
	v.major, err = strconv.Atoi(parts[0])
	if err != nil {
		return version{}, err
	}
	v.minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return version{}, err
	}
	if len(parts) == 3 {
		v.patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return version{}, err
		}
	}
	return v, nil
}

var (
	goVersionRE      = regexp.MustCompile(`go(\d+)\.(\d+)(?:\.(\d+))?`)
	genericVersionRE = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)
)

func extractVersion(output string, re *regexp.Regexp) (string, version, error) {
	matches := re.FindStringSubmatch(output)
	if len(matches) < 3 {
		return "", version{}, fmt.Errorf("cannot parse version from: %s", output)
	}
	var v version
	v.major, _ = strconv.Atoi(matches[1])
	v.minor, _ = strconv.Atoi(matches[2])
	rendered := fmt.Sprintf("%d.%d", v.major, v.minor)
	if matches[3] != "" {
		v.patch, _ = strconv.Atoi(matches[3])
		rendered = fmt.Sprintf("%s.%d", rendered, v.patch)
	}
	return rendered, v, nil
}

func getGoVersion(goBin string) (string, version, error) {
	// Parse "go version go1.26.4 darwin/arm64"
	output, err := exec.Command(goBin, "version").Output()
	if err != nil {
		return "", version{}, fmt.Errorf("failed to run go version: %v", err)
	}
	return extractVersion(string(output), goVersionRE)
}

func getQEMUVersion(qemuBin string) (string, version, error) {
	// Parse "QEMU emulator version 10.2.0"
	output, err := exec.Command(qemuBin, "--version").Output()
	if err != nil {
		return "", version{}, fmt.Errorf("failed to run qemu --version: %v", err)
	}
	return extractVersion(string(output), genericVersionRE)
}
