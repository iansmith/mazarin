// go-kill sends signals to processes, similar to kill.
//
// Usage:
//
//	go tool go-kill [-9] [-s signal] pid...
//
// Flags:
//
//	-9           Send SIGKILL
//	-s signal    Send specified signal (TERM, KILL, INT, HUP, etc.)
//
// Default signal is SIGTERM (15).
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

var signals = map[string]syscall.Signal{
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
	"QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL,
	"TERM": syscall.SIGTERM,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
	// Numeric aliases
	"1":  syscall.SIGHUP,
	"2":  syscall.SIGINT,
	"3":  syscall.SIGQUIT,
	"9":  syscall.SIGKILL,
	"15": syscall.SIGTERM,
}

func main() {
	sigKill := flag.Bool("9", false, "send SIGKILL")
	sigName := flag.String("s", "TERM", "signal to send")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-kill [-9] [-s signal] pid...\n")
		fmt.Fprintf(os.Stderr, "Send signal to processes.\n\n")
		fmt.Fprintf(os.Stderr, "Signals: HUP, INT, QUIT, KILL, TERM, USR1, USR2\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	signal := syscall.SIGTERM
	if *sigKill {
		signal = syscall.SIGKILL
	} else if *sigName != "TERM" {
		sig, ok := signals[strings.ToUpper(*sigName)]
		if !ok {
			fmt.Fprintf(os.Stderr, "go-kill: unknown signal: %s\n", *sigName)
			os.Exit(1)
		}
		signal = sig
	}

	exitCode := 0
	for _, arg := range flag.Args() {
		pid, err := strconv.Atoi(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-kill: invalid pid: %s\n", arg)
			exitCode = 1
			continue
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-kill: no such process: %d\n", pid)
			exitCode = 1
			continue
		}

		if err := proc.Signal(signal); err != nil {
			fmt.Fprintf(os.Stderr, "go-kill: %d: %v\n", pid, err)
			exitCode = 1
		}
	}

	os.Exit(exitCode)
}
