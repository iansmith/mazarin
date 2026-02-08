// go-start starts a command in the background and exits immediately.
//
// Usage:
//
//	go tool go-start command [args...]
//
// The command is started as a detached process and go-start exits immediately.
// This provides cross-platform background process launching (like & on Unix or
// start /B on Windows).
//
// If the command is not found in PATH, go-start also checks common system
// locations (/opt/homebrew/bin, /usr/local/bin) so that tools like QEMU
// can be found even when run from a restricted Go tool environment.
//
// The child process inherits stdout and stderr from go-start, so output will
// still be visible unless redirected.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fallbackDirs are common installation directories to search when the
// command is not found in PATH.  Checked in order.
var fallbackDirs = []string{
	"/opt/homebrew/bin",
	"/usr/local/bin",
	"/usr/bin",
}

// resolveCommand attempts to find the executable for cmdName.
// 1. If cmdName contains a path separator, return it as-is.
// 2. Try exec.LookPath (searches PATH).
// 3. Fall back to well-known directories.
func resolveCommand(cmdName string) string {
	if strings.Contains(cmdName, string(filepath.Separator)) {
		return cmdName
	}

	// Try normal PATH lookup first
	if path, err := exec.LookPath(cmdName); err == nil {
		return path
	}

	// Fall back to common system directories
	for _, dir := range fallbackDirs {
		candidate := filepath.Join(dir, cmdName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}

	// Return the original name — exec.Command will produce
	// the standard "not found" error.
	return cmdName
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: go-start command [args...]\n")
		fmt.Fprintf(os.Stderr, "Start a command in the background.\n")
		os.Exit(1)
	}

	cmdName := resolveCommand(os.Args[1])
	cmdArgs := os.Args[2:]

	cmd := exec.Command(cmdName, cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "go-start: %v\n", err)
		os.Exit(1)
	}

	// Exit immediately - the child process continues running
	os.Exit(0)
}
