// go-pkill kills processes by name pattern, similar to pkill.
//
// Usage:
//
//	go tool go-pkill [-f] pattern
//
// Flags:
//
//	-f    Match against full command line, not just process name
//	-9    Send SIGKILL instead of SIGTERM
//
// Exit codes:
//
//	0 - One or more processes matched
//	1 - No processes matched
//	2 - Error
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	fullMatch := flag.Bool("f", false, "match against full command line")
	sigKill := flag.Bool("9", false, "send SIGKILL instead of SIGTERM")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-pkill [-f] [-9] pattern\n")
		fmt.Fprintf(os.Stderr, "Kill processes matching pattern.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	pattern := flag.Arg(0)
	re, err := regexp.Compile(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-pkill: invalid pattern: %v\n", err)
		os.Exit(2)
	}

	signal := syscall.SIGTERM
	if *sigKill {
		signal = syscall.SIGKILL
	}

	pids, err := findProcesses(re, *fullMatch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-pkill: %v\n", err)
		os.Exit(2)
	}

	if len(pids) == 0 {
		os.Exit(1) // No processes matched
	}

	myPid := os.Getpid()
	killed := 0
	for _, pid := range pids {
		if pid == myPid {
			continue // Don't kill ourselves
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Signal(signal); err == nil {
			killed++
		}
	}

	if killed == 0 {
		os.Exit(1)
	}
	os.Exit(0)
}

func findProcesses(pattern *regexp.Regexp, fullMatch bool) ([]int, error) {
	switch runtime.GOOS {
	case "darwin", "linux":
		return findProcessesUnix(pattern, fullMatch)
	case "windows":
		return findProcessesWindows(pattern, fullMatch)
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func findProcessesUnix(pattern *regexp.Regexp, fullMatch bool) ([]int, error) {
	// Use ps to get process list
	var cmd *exec.Cmd
	if fullMatch {
		cmd = exec.Command("ps", "-eo", "pid,args")
	} else {
		cmd = exec.Command("ps", "-eo", "pid,comm")
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var pids []int
	lines := strings.Split(string(output), "\n")
	for _, line := range lines[1:] { // Skip header
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cmdStr := strings.Join(fields[1:], " ")
		if pattern.MatchString(cmdStr) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func findProcessesWindows(pattern *regexp.Regexp, fullMatch bool) ([]int, error) {
	// Use tasklist on Windows
	cmd := exec.Command("tasklist", "/FO", "CSV", "/NH")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var pids []int
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		// CSV format: "name.exe","PID","Session Name","Session#","Mem Usage"
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		name := strings.Trim(fields[0], "\"")
		pidStr := strings.Trim(fields[1], "\"")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if pattern.MatchString(name) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
