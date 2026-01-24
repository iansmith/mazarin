// go-stopproc stops processes by name pattern.
//
// Usage:
//
//	go tool go-stopproc [-f] [-q] pattern
//
// Flags:
//
//	-f    Match against full command line, not just process name
//	-9    Send SIGKILL instead of SIGTERM
//	-q    Quiet mode - exit 0 even if no processes match
//
// Exit codes:
//
//	0 - Success (processes stopped or -q with no matches)
//	1 - No processes matched (unless -q)
//	2 - Error
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	// Simple argument parsing to avoid any flag package issues
	quiet := false
	fullMatch := false
	sigKill := false
	var pattern string

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch arg {
		case "-q":
			quiet = true
		case "-f":
			fullMatch = true
		case "-9":
			sigKill = true
		case "--help", "-h":
			printUsage()
			os.Exit(0)
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "go-stopproc: unknown flag: %s\n", arg)
				os.Exit(2)
			}
			pattern = arg
		}
	}

	if pattern == "" {
		printUsage()
		os.Exit(2)
	}

	// Get process list using ps
	psArgs := []string{"-eo", "pid,comm"}
	if fullMatch {
		psArgs = []string{"-eo", "pid,args"}
	}

	output, err := exec.Command("ps", psArgs...).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-stopproc: ps failed: %v\n", err)
		os.Exit(2)
	}

	// Find matching PIDs
	myPid := os.Getpid()
	var pidsToStop []int

	lines := strings.Split(string(output), "\n")
	for _, line := range lines[1:] { // Skip header
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == myPid {
			continue
		}

		cmdStr := strings.Join(fields[1:], " ")
		if strings.Contains(cmdStr, pattern) {
			pidsToStop = append(pidsToStop, pid)
		}
	}

	if len(pidsToStop) == 0 {
		if quiet {
			os.Exit(0)
		}
		os.Exit(1)
	}

	// Use kill command to stop processes (avoids direct syscall issues)
	sig := "TERM"
	if sigKill {
		sig = "KILL"
	}

	stopped := 0
	for _, pid := range pidsToStop {
		cmd := exec.Command("kill", "-s", sig, strconv.Itoa(pid))
		if err := cmd.Run(); err == nil {
			stopped++
		}
	}

	if stopped == 0 && !quiet {
		os.Exit(1)
	}
	os.Exit(0)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: go-stopproc [-f] [-9] [-q] pattern\n")
	fmt.Fprintf(os.Stderr, "Stop processes matching pattern.\n\n")
	fmt.Fprintf(os.Stderr, "  -f    Match against full command line\n")
	fmt.Fprintf(os.Stderr, "  -9    Send SIGKILL instead of SIGTERM\n")
	fmt.Fprintf(os.Stderr, "  -q    Exit 0 even if no processes match\n")
}
