// forkexectest is the MAZ-114 boot-time acceptance smoke for the fork/exec
// core (the MAZ-62 umbrella DoD): a program running inside mazzy fork+exec's a
// trivial child binary and recovers its exit status via wait4, end to end
// through every layer (clone dispatch -> buffering window -> execve flush ->
// kernel clone_exec -> child startup stub -> kernel child-exit notification ->
// shepherd wait4 park/unpark).
//
// It is a FULL ELF (the net.elf / fs.elf shape), NOT a .maz plugin: os/exec
// needs the Go runtime's init to have run (os.Stdout/Stderr, the pidfd probe,
// the fork lock), which a .maz's MazarinMain entry skips. The child it spawns
// is /forkexecchild.elf (also a full ELF).
//
// PASS/FAIL lines go to the UART (fd 1) tagged [forkexectest]; the boot smoke
// greps the serial log for them. The four stages map to the four DoD items:
//
//	Stage 1 (DoD 1): single child exits 42; wait4 recovers pid + status 42.
//	Stage 2 (DoD 2): in-child intent — redirected stdout + changed cwd visible
//	                 in the child.
//	Stage 3 (DoD 3): concurrent children — N at once, each reaped exactly once.
//	Stage 4 (DoD 4): clean failure — wait4 with no child returns ECHILD, no hang.
//
// STATUS: this stage is DISABLED at boot (config/startup.arm64.toml) and BLOCKED
// on MAZ-127. The merged fork/exec chain parks the process-flavor clone SVC and
// never returns it, but Go's os/exec issues clone via syscall.rawVforkSyscall,
// whose single SVC must return 0 (the vfork "child" return) before the runtime
// can run the in-child setup + execve. So stages 1-3 currently hang at the first
// clone (`delegate stuck: sysid=32`). Stage 4 does not depend on clone and PASSes
// today. This harness is the acceptance vehicle MAZ-127 flips on once the kernel
// honors the vfork double-return.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	tag = "[forkexectest] "
	// childPath is the absolute path of the child ELF on the mazzy filesystem;
	// the linux shepherd's execve resolves and loads it via h.fs.ReadFile.
	childPath = "/forkexecchild.elf"
	// singleExit is the known exit status the single-child stage asserts.
	singleExit = 42
	// concurrentN is the number of simultaneous children stage 3 launches —
	// the `go build -p N` shape (DoD item 3).
	concurrentN = 8
)

func say(s string) { os.Stdout.WriteString(tag + s + "\n") }

func main() {
	say("start")

	ok := true
	ok = stageSingleChild() && ok
	ok = stageInChildIntent() && ok
	ok = stageConcurrent() && ok
	ok = stageCleanFailure() && ok

	if ok {
		say("all checks done")
	} else {
		say("checks failed; staying alive")
	}
	// Stay alive: a test shepherd that exits while the kernel is mid-cleanup of
	// its reaped children risks yanking shared mappings (cf. xfertest's
	// keep-alive rationale). The boot smoke has its PASS/FAIL lines by now.
	// A bare select{} would trip the runtime's "all goroutines asleep" deadlock
	// detector (this is goroutine 1 with no others), so sleep in a loop instead.
	for {
		time.Sleep(time.Hour)
	}
}

// wireStdio gives a child explicit stdin/stdout/stderr so os/exec never opens
// os.DevNull ("/dev/null") for a nil stream — there is no /dev/null on the
// mazzy filesystem. The child inherits the parent's real FDs (the os/exec
// child still dup3's them into 0/1/2 between clone and execve, exercising the
// FD-plumbing path). Callers that need to CAPTURE stdout (the intent stage)
// leave c.Stdout nil and let cmd.Output() install its capture pipe.
func wireStdio(c *exec.Cmd) {
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
}

// stageSingleChild is DoD item 1, the literal MAZ-62 acceptance: fork+exec a
// child that exits 42 and recover that status via wait4. cmd.Run() does the
// clone+execve and Process.Wait()'s PID-path wait4 under the hood.
func stageSingleChild() bool {
	cmd := exec.Command(childPath, fmt.Sprintf("%d", singleExit))
	wireStdio(cmd)
	err := cmd.Run()
	code, pid, ok := exitInfo(cmd, err)
	if !ok {
		say(fmt.Sprintf("FAIL single child: unexpected err=%v", err))
		return false
	}
	if code != singleExit {
		say(fmt.Sprintf("FAIL single child pid=%d status=%d want=%d", pid, code, singleExit))
		return false
	}
	say(fmt.Sprintf("PASS single child pid=%d status=%d", pid, code))
	return true
}

// stageInChildIntent is DoD item 2: the parent redirects the child's stdout to
// a pipe and changes its working directory; the child prints its cwd, and the
// parent reads it back. Both the redirected FD (the dup3 the os/exec child does
// between clone and execve) and the chdir must have taken effect.
func stageInChildIntent() bool {
	const wantDir = "/data" // a directory that exists on the mazzy disk image
	cmd := exec.Command(childPath, "--intent", "0")
	cmd.Dir = wantDir
	// Wire stdin + stderr explicitly (no /dev/null); leave Stdout nil so
	// cmd.Output() installs its capture pipe — that pipe FD is what the child
	// dup3's onto fd 1 between clone and execve, the redirect we assert.
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if _, _, ok := exitInfo(cmd, err); !ok {
		say(fmt.Sprintf("FAIL in-child intent: unexpected err=%v", err))
		return false
	}
	got := strings.TrimSpace(string(out))
	const prefix = "child cwd="
	if !strings.HasPrefix(got, prefix) {
		say(fmt.Sprintf("FAIL in-child intent: stdout=%q (pipe redirect not honored)", got))
		return false
	}
	childCwd := strings.TrimPrefix(got, prefix)
	if childCwd != wantDir {
		say(fmt.Sprintf("FAIL in-child intent: cwd=%q want=%q", childCwd, wantDir))
		return false
	}
	say(fmt.Sprintf("PASS in-child intent: cwd=%s fd=redirected", childCwd))
	return true
}

// stageConcurrent is DoD item 3: launch concurrentN children at once and assert
// every distinct PID is reaped exactly once (no child lost, none double-reaped).
func stageConcurrent() bool {
	cmds := make([]*exec.Cmd, concurrentN)
	for i := range cmds {
		// Each child exits with a distinct status (i) so a misattributed reap
		// would show up as a wrong status, not just a wrong count.
		cmds[i] = exec.Command(childPath, fmt.Sprintf("%d", i))
		wireStdio(cmds[i])
		if err := cmds[i].Start(); err != nil {
			say(fmt.Sprintf("FAIL concurrent: child %d Start: %v", i, err))
			return false
		}
	}
	seen := map[int]bool{}
	for i, cmd := range cmds {
		err := cmd.Wait()
		code, pid, ok := exitInfo(cmd, err)
		if !ok {
			say(fmt.Sprintf("FAIL concurrent: child %d wait err=%v", i, err))
			return false
		}
		if seen[pid] {
			say(fmt.Sprintf("FAIL concurrent: pid %d reaped twice", pid))
			return false
		}
		seen[pid] = true
		if code != i {
			say(fmt.Sprintf("FAIL concurrent: pid %d status=%d want=%d (misattributed reap)", pid, code, i))
			return false
		}
	}
	if len(seen) != concurrentN {
		say(fmt.Sprintf("FAIL concurrent: reaped %d/%d distinct", len(seen), concurrentN))
		return false
	}
	say(fmt.Sprintf("PASS concurrent: reaped %d/%d distinct", len(seen), concurrentN))
	return true
}

// stageCleanFailure is DoD item 4: wait4 with no matching child must return a
// clean ECHILD, not hang or lie. We call wait4(-1) directly (no live child)
// rather than going through os/exec, because os/exec only ever wait4's a child
// it actually spawned. A raw wait4 with no children is the clone-without-a-
// matching-execve / no-child shape the DoD names.
func stageCleanFailure() bool {
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(-1, &ws, 0, nil)
	if err == syscall.ECHILD {
		say("PASS clean failure: ECHILD")
		return true
	}
	say(fmt.Sprintf("FAIL clean failure: wait4(-1) wpid=%d err=%v (want ECHILD)", wpid, err))
	return false
}

// exitInfo extracts (exitCode, pid, ok) from a finished exec.Cmd. ok is false
// only when the command did not run-and-exit cleanly (a non-ExitError failure
// such as a failed clone/execve). A non-zero EXIT status is a normal exit and
// yields ok=true with that code — the caller decides whether the code is the
// one it wanted.
func exitInfo(cmd *exec.Cmd, err error) (code, pid int, ok bool) {
	if cmd.ProcessState == nil {
		return 0, 0, false
	}
	pid = cmd.ProcessState.Pid()
	if err == nil {
		return 0, pid, true
	}
	// cmd.Run()/cmd.Wait() return *exec.ExitError directly (unwrapped) for a
	// child that ran and exited non-zero; anything else (a failed clone/execve)
	// is a real launch failure.
	if ee, isExit := err.(*exec.ExitError); isExit {
		return ee.ProcessState.ExitCode(), pid, true
	}
	return 0, pid, false
}
