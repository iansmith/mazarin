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
// The child process inherits stdout and stderr from go-start, so output will
// still be visible unless redirected.
package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: go-start command [args...]\n")
		fmt.Fprintf(os.Stderr, "Start a command in the background.\n")
		os.Exit(1)
	}

	cmdName := os.Args[1]
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
