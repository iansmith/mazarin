// forkexecchild is the trivial child binary for the MAZ-114 fork+exec+wait4
// acceptance smoke. It is fork+exec'd by /forkexectest.elf (the parent) and
// exercised in four scenarios; its only job is to behave like a real Linux
// child so the parent can observe the round trip via wait4.
//
// Built as a FULL ELF (the net.elf / fs.elf shape), NOT a .maz plugin: the
// kernel's clone_exec loadELF jumps to the ELF entry point and boots the Go
// runtime, which runs THIS main() to completion — a .maz's MazarinMain entry
// is never reached on that path (a .maz needs the shepherd.elf host to call
// it). A self-contained ELF whose main() runs and os.Exit()s is exactly what
// a fork/exec'd Linux child is.
//
// Behavior is selected by argv:
//
//	forkexecchild <exitcode>
//	    exit immediately with the given status (single-child + concurrent
//	    stages; argv[0] is the program, argv[1] is the status).
//	forkexecchild --intent <exitcode>
//	    first print one line to stdout (fd 1): "child cwd=<getcwd>"; then exit
//	    with <exitcode>. The parent redirected fd 1 to a pipe and changed the
//	    child's cwd between clone and execve, so the printed cwd + the fact the
//	    parent reads it back over the pipe proves the in-child intent took
//	    effect (DoD item 2).
//
// argv parsing is deliberately hand-rolled (no flag package) to keep the child
// minimal and its syscall surface tiny — every syscall it makes is one the
// linux shepherd must service while a fork/exec is in flight.
package main

import (
	"os"
	"strconv"
)

func main() {
	args := os.Args
	// Defensive: a child with no args is a launch bug; exit non-zero so the
	// parent's wait4 sees a distinct, non-42 status rather than a hang.
	if len(args) < 2 {
		os.Exit(64)
	}

	if args[1] == "--intent" {
		// In-child intent stage: prove the redirected stdout + changed cwd.
		// getcwd reflects the chdir the parent buffered between clone and
		// execve; writing to fd 1 reaches the parent's pipe (the dup3'd FD).
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(65)
		}
		os.Stdout.WriteString("child cwd=" + cwd + "\n")
		code := 0
		if len(args) >= 3 {
			code, _ = strconv.Atoi(args[2])
		}
		os.Exit(code)
	}

	// Default stage: exit with the requested status.
	code, err := strconv.Atoi(args[1])
	if err != nil {
		os.Exit(66)
	}
	os.Exit(code)
}
