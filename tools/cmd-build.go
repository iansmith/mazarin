// Build cardinal and kmazarin
//
// Usage: go run scripts/build.go [clean]
//
// Environment:
//
//	GO    Path to Go 1.25.5 compiler (default: 'go' from PATH)
//
// Requires Go 1.25.5 exactly.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const requiredGoVersion = "1.25.5"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Find project root from executable location
	// Executable is at build/tools/build, so we need 3 Dir() calls
	exe, err := os.Executable()
	var projectRoot string
	if err == nil && !strings.Contains(exe, "go-build") {
		// build/tools/build -> build/tools -> build -> project root
		projectRoot = filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	} else {
		// Fallback: use working directory
		projectRoot, _ = os.Getwd()
	}

	// Change to project root
	if err := os.Chdir(projectRoot); err != nil {
		return fmt.Errorf("changing to project root: %w", err)
	}

	// Get Go path from environment or use default
	goPath := os.Getenv("GO")
	if goPath == "" {
		goPath = "go"
	}

	// Check if "clean" argument was passed
	clean := len(os.Args) > 1 && os.Args[1] == "clean"

	if clean {
		fmt.Println("=== Clean build ===")
		cmd := exec.Command("make", "clean")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("make clean failed: %w", err)
		}
	}

	fmt.Println("=== Building ===")

	// Build make command with GO override if specified
	args := []string{"cardinal"}
	if os.Getenv("GO") != "" {
		args = []string{"GO=" + goPath, "cardinal"}
	}

	cmd := exec.Command("make", args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Println("=== Build FAILED ===")
		return err
	}

	fmt.Println("=== Build successful ===")
	return nil
}

// checkGoVersion verifies Go version is exactly requiredGoVersion
func checkGoVersion(goPath string) error {
	cmd := exec.Command(goPath, "version")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Go compiler not found at %s", goPath)
	}

	// Extract version from output like "go version go1.25.5 darwin/arm64"
	re := regexp.MustCompile(`go(\d+\.\d+\.\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return fmt.Errorf("could not parse Go version from: %s", output)
	}

	version := matches[1]
	if version != requiredGoVersion {
		return fmt.Errorf("Go version mismatch.\n\n  Required: go%s\n  Found:    go%s (from: %s)\n\nSet GO to point to Go %s:\n  GO=/path/to/go%s go run scripts/build.go",
			requiredGoVersion, version, goPath, requiredGoVersion, requiredGoVersion)
	}

	return nil
}
