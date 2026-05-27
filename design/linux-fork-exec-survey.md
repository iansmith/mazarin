# Linux fork/exec Surface Survey

**Date:** 2026-05-27
**Status:** Scoping document — pre-implementation
**Audience:** Anyone planning fork/exec support in the linux shepherd (`maz/linux`)
**Source material:** Go 1.26.2 stdlib at `/opt/homebrew/Cellar/go/1.26.2/libexec/src/`

---

## Executive summary

There are two distinct concerns here, and they are very different in size:

1. **The Go runtime itself** issues NO `fork`, `vfork`, `execve`, `execveat`, `wait4`, or `waitid` syscalls. The only "process creation" primitive the runtime uses is `clone()` with `CLONE_THREAD|CLONE_VM|...`, which is **thread creation, not process creation**. We almost certainly already handle this (since goroutines work). The runtime *does* call `tgkill`/`kill`/`getpid`/`gettid`/`exit`/`exit_group` for signal delivery and thread lifecycle, but none of those create or replace a process.

2. **The Go standard library** (`os/exec`, `os`, `syscall`) is the entire surface that exposes fork+exec to user code. This is large and has many knobs (chroot, namespaces, ambient caps, ptrace, cgroups, pidfd, ...). The good news is that the *required* core is small (`clone+execve+wait4+kill+pipe2+dup3+chdir` plus FD plumbing); the rest are optional `SysProcAttr` knobs we can stub or refuse one at a time.

The implication: we can ship a useful `os/exec.Command(...).Run()` with maybe 8–10 new syscalls in the shepherd. Everything else is an opt-in upgrade.

---

## Part 1 — The Go runtime's process-related syscalls

This is the *runtime's own* usage, separate from anything user code does via `os/exec`. The verdict, in one line: **the runtime never forks or execs.**

### Summary table

| Syscall    | amd64 # | arm64 # | Runtime caller                          | Purpose                              | Reachable now? |
|------------|---------|---------|-----------------------------------------|--------------------------------------|----------------|
| clone      | 56      | 220     | `runtime.clone` (asm) ← `newosproc`     | Create OS thread (NOT process)       | Constantly     |
| exit_group | 231     | 94      | `runtime.exit` (asm)                    | Whole-process exit                   | On shutdown    |
| exit       | 60      | 93      | `runtime.exitThread` (asm)              | Single-thread exit                   | Worker teardown|
| gettid     | 186     | 178     | inside clone child / `runtime.gettid`   | Stash kernel TID in `m.procid`       | Per new thread |
| getpid     | 39      | 172     | inside `raise` / `raiseproc`            | PID arg for tgkill/kill              | Signals/profile|
| tgkill     | 234     | 131     | `runtime.raise` (asm)                   | Async preemption, GC signals, panic  | Common         |
| kill       | 62      | 129     | `runtime.raiseproc` (asm)               | Process-wide signal                  | Rare           |

### The one important detail: the `clone` flag mask

`runtime/os_linux.go:156-161` defines:

```go
cloneFlags = _CLONE_VM | _CLONE_FS | _CLONE_FILES | _CLONE_SIGHAND |
             _CLONE_SYSVSEM | _CLONE_THREAD
```

On amd64, the assembly at `runtime/sys_linux_amd64.s:574-620` additionally ORs `0x00080000` (CLONE_SETTLS) when m/g pointers are present. arm64 equivalent is at `runtime/sys_linux_arm64.s:670-750`.

This combination is unambiguously "make a new thread in the same address space and same FD table" — the same semantic as `pthread_create`. There is **no path in the runtime** that calls `clone` without `CLONE_THREAD|CLONE_VM`. So if our shepherd's `clone` handler recognizes this flag combo and routes it to our existing OS-thread-creation primitive, the runtime is happy.

### What the runtime does NOT call (worth saying out loud)

- `fork`, `vfork` — never used; the runtime always uses `clone` directly.
- `execve`, `execveat` — the runtime never re-execs itself, never spawns helpers, never does a "fork-exec dance" of its own.
- `wait4`, `waitid` — the runtime has no parent/child reaping to do.
- `clone3`, `clone2` — not used; the classic `clone` is sufficient for thread creation.

There is no escape hatch: even crash handling (`GOTRACEBACK=crash`, `runtime/debug.SetCrashOutput`, panic, signal dumps) does not exec gdb or any helper. The runtime only ever sends signals to itself via tgkill/kill.

**Conclusion: implementing user-code fork/exec does not touch the runtime's existing clone path.** They are independent.

---

## Part 2 — The Go standard library process-execution surface

Three packages are involved. They form a strict layering:

```
os/exec.Cmd          (high-level, ergonomic)
    ↓
os.StartProcess      (mid-level, returns *os.Process)
    ↓
syscall.ForkExec     (low-level, the actual clone+execve dance)
    ↓
syscall.forkAndExecInChild1   ← THIS is where most of the syscall work lives
```

### 2.1 `os/exec.Cmd` — what users actually touch

Fields on `exec.Cmd` that the user can set:

- `Path string`, `Args []string` — program + args
- `Dir string` — working directory for child
- `Env []string` — child environment (defaults to parent's)
- `Stdin io.Reader`, `Stdout`, `Stderr io.Writer` — I/O wiring
- `ExtraFiles []*os.File` — extra FDs inherited at indices 3+
- `SysProcAttr *syscall.SysProcAttr` — the kitchen sink (see 2.3)
- `Cancel func() error` — what to do on context cancel (defaults to Kill)
- `WaitDelay time.Duration` — grace period before forcing teardown

Methods that matter:

- `Start()` — fork+exec, returns immediately
- `Run()` — `Start` + `Wait`
- `Output()`, `CombinedOutput()` — `Run` + capture
- `StdinPipe()`, `StdoutPipe()`, `StderrPipe()` — returns `os.Pipe()` ends
- `Wait()` — reaps the child, returns `*ProcessState`

Path resolution (`exec.LookPath`, in `os/exec/lp_unix.go:44-78`): when `Path` doesn't contain `/`, walks `$PATH`, `stat`s each candidate, `faccessat` (via `unix.Eaccess`) for `X_OK`. Falls back to a 0111 mode-bit check if the access syscall isn't there. **No fork/exec required for `LookPath` itself.**

### 2.2 `os.StartProcess` and `os.Process`

`os.StartProcess(name, argv, *ProcAttr)` is a thin shim over `syscall.ForkExec`. `ProcAttr` has `Dir`, `Env`, `Files []*os.File`, and `Sys *syscall.SysProcAttr`.

`os.Process` is the handle. Key methods:

- `Wait() (*ProcessState, error)` — blocks until the child exits and reaps it
- `Signal(sig os.Signal) error` — sends a signal
- `Kill() error` — SIGKILL convenience
- `Release() error` — give up the handle without reaping (zombie risk)
- `FindProcess(pid) (*Process, error)` — get a handle for an arbitrary PID

On Linux 5.3+, `os.Process` internally holds a **pidfd** (a file descriptor that refers to a process) rather than a raw PID. This avoids the classic PID-reuse race in Wait/Signal coordination. There is a startup probe (`pidfd_linux.go:154-197`) that calls `pidfd_open(getpid(), 0)`, then `waitid(P_PIDFD, ...)` and `pidfd_send_signal(pidfd, 0)`; if any of those three fails, Go silently falls back to PID-based bookkeeping.

### 2.3 `syscall.SysProcAttr` — the full knob list (linux/amd64, linux/arm64)

Every field, and what syscall(s) each one implies inside the forked child:

| Field                         | Type             | Syscall(s) it triggers                                   |
|-------------------------------|------------------|----------------------------------------------------------|
| `Chroot`                      | string           | `chroot()`                                               |
| `Credential`                  | `*Credential`    | `setgroups()`, `setgid()`, `setuid()`                    |
| `Ptrace`                      | bool             | `ptrace(PTRACE_TRACEME)`                                 |
| `Setsid`                      | bool             | `setsid()`                                               |
| `Setpgid`                     | bool             | `setpgid()`                                              |
| `Setctty`                     | bool             | `ioctl(TIOCSCTTY)`                                       |
| `Noctty`                      | bool             | `ioctl(TIOCNOTTY)`                                       |
| `Ctty`                        | int              | (FD index used by Setctty)                               |
| `Foreground`                  | bool             | `ioctl(TIOCSPGRP)`                                       |
| `Pgid`                        | int              | (param to setpgid)                                       |
| `Pdeathsig`                   | Signal           | `prctl(PR_SET_PDEATHSIG)` + `getppid` + maybe `kill`     |
| `Cloneflags`                  | uintptr          | (ORed into the `clone()` call)                           |
| `Unshareflags`                | uintptr          | `unshare()`                                              |
| `UidMappings`, `GidMappings`  | `[]SysProcIDMap` | `openat`+`write`+`close` to `/proc/PID/{uid,gid}_map`    |
| `GidMappingsEnableSetgroups`  | bool             | `openat`+`write` to `/proc/PID/setgroups`                |
| `AmbientCaps`                 | `[]uintptr`      | `capget`, `capset`, `prctl(PR_CAP_AMBIENT_RAISE)`        |
| `UseCgroupFD` + `CgroupFD`    | bool, int        | `clone3(CLONE_INTO_CGROUP)` — requires clone3            |
| `PidFD`                       | `*int`           | `clone(CLONE_PIDFD)` or `clone3()`                       |

### 2.4 The in-child sequence (`syscall/exec_linux.go: forkAndExecInChild1`)

This is the function we'd most need to support, because the **entire sequence between clone and execve runs in async-signal-safe context using raw syscalls** — every one of these is a syscall we'd be servicing on the shepherd side. From `exec_linux.go:138-679`:

1. (if `AmbientCaps`) `prctl(PR_SET_KEEPCAPS, 1)`
2. (if `UidMappings`/`GidMappings`) `read()` the map-pipe, wait for parent's `/proc/PID/{uid,gid}_map` write
3. (if `Setsid`) `setsid()`
4. (if `Setpgid` or `Foreground`) `setpgid()` and possibly `ioctl(TIOCSPGRP)`
5. `runtime_AfterForkInChild()` — restore signal mask
6. (if `Unshareflags`) `unshare(...)`, then write `/proc/self/setgroups`, `uid_map`, `gid_map`, and maybe `mount(MS_REC|MS_PRIVATE, "/")`
7. (if `Chroot`) `chroot()`
8. (if `Credential`) `setgroups()`, `setgid()`, `setuid()`
9. (if `AmbientCaps`) `capget` / `capset` / `prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE)` per cap
10. (if `Dir`) `chdir()`
11. (if `Pdeathsig`) `prctl(PR_SET_PDEATHSIG)`, `getppid()`, maybe `kill(self, sig)`
12. **FD plumbing** — multi-pass `dup3` to move user-specified FDs into positions 0..N, clearing CLOEXEC; `close()` for stdio that wasn't supplied
13. (if `Noctty`) `ioctl(TIOCNOTTY)` on fd 0
14. (if `Setctty`) `ioctl(TIOCSCTTY, 1)` on `Ctty`
15. (if cached) `prlimit64(RLIMIT_NOFILE, ...)` to restore the parent's pre-fork limit
16. (if `Ptrace`) `ptrace(PTRACE_TRACEME)`
17. **`execve(argv0, argv[], envp[])`** — point of no return
18. On execve failure: `write(errpipe, &errno, ...)`, `exit(253)`

The parent meanwhile reads from `errpipe`; an EOF (because `execve` closed it via CLOEXEC) means success, and any errno bytes mean the child failed before exec.

### 2.5 Wait, Signal, Kill

**Wait** has two implementations:

- **pidfd path** (`pidfd_linux.go:83-123`): `waitid(P_PIDFD, pidfd, &info, WEXITED, &rusage)` — single syscall, no PID reuse race.
- **PID path** (`exec_unix.go:32-75`): first `waitid(P_PID, pid, &info, WEXITED|WNOWAIT, NULL)` to peek non-destructively, then `wait4(pid, &status, 0, &rusage)` to actually reap.

**Signal/Kill** also has two implementations:

- **pidfd path**: `pidfd_send_signal(pidfd, sig)`.
- **PID path**: `kill(pid, sig)` under an `sigMu.RLock` to coordinate with concurrent Wait.

### 2.6 Pipes

`os.Pipe()` calls `pipe2(p, O_CLOEXEC)`. `cmd.StdoutPipe()`/`StdinPipe()`/`StderrPipe()` are thin wrappers — they create a pipe pair, pass one end into the child's FD table, and keep the other for the parent. Teardown happens after `Wait()` (or `WaitDelay`).

### 2.7 Things Go does **not** do on Linux

- **`posix_spawn`** — not used at all on Linux. Constants exist only for NetBSD/FreeBSD.
- **`vfork`** — not called directly. Instead Go uses `clone(CLONE_VFORK|CLONE_VM)` when safe, which gets vfork-like "child blocks parent until exec or exit" semantics without the libc vfork() trap. (Skipped when creating a user namespace.)
- **`close_range`** / **`/proc/self/fd` enumeration** — never enumerates all open FDs. It only manipulates the exact FDs in `ProcAttr.Files`, relying on `O_CLOEXEC` for everything else.

### 2.8 Full Linux syscall surface — what `os/exec` + `os.StartProcess` can hit

| Capability                         | User-facing trigger                          | Syscall(s)                                       | Kernel min   |
|------------------------------------|----------------------------------------------|--------------------------------------------------|--------------|
| Spawn a child                      | implicit                                     | `clone`, `clone3`                                | 2.6 / 5.3    |
| Replace child's image              | implicit                                     | `execve`                                         | 1.0          |
| Reap a child                       | `Process.Wait()`                             | `wait4`, `waitid`                                | 2.4 / 2.6    |
| Wait with pidfd                    | `Process.Wait()` when pidfd is up            | `waitid(P_PIDFD)`                                | 5.4          |
| Signal / kill                      | `Process.Signal()`, `Process.Kill()`         | `kill`, `pidfd_send_signal`                      | 1.0 / 5.1    |
| pidfd lifecycle                    | automatic                                    | `pidfd_open`                                     | 5.3          |
| Pipes for stdio                    | `Cmd.StdoutPipe()` etc.                      | `pipe2`                                          | 2.6.27       |
| FD reshuffling in child            | `ExtraFiles`, stdio redirection              | `dup3`, `close`, `fcntl(F_SETFD)`                | 2.6.27       |
| Working directory                  | `Cmd.Dir`                                    | `chdir`                                          | 1.0          |
| PATH lookup                        | implicit                                     | `stat`/`statx`, `faccessat`                      | 3.3          |
| Set session                        | `SysProcAttr.Setsid`                         | `setsid`                                         | 1.0          |
| Set process group                  | `SysProcAttr.Setpgid`                        | `setpgid`                                        | 1.0          |
| Set foreground pgrp                | `SysProcAttr.Foreground`                     | `ioctl(TIOCSPGRP)`                               | 1.0          |
| Controlling TTY                    | `SysProcAttr.Setctty` / `Noctty`             | `ioctl(TIOCSCTTY)` / `ioctl(TIOCNOTTY)`          | 1.0          |
| chroot                             | `SysProcAttr.Chroot`                         | `chroot`                                         | 1.0          |
| Credentials                        | `SysProcAttr.Credential`                     | `setgroups`, `setgid`, `setuid`                  | 1.0          |
| Parent-death signal                | `SysProcAttr.Pdeathsig`                      | `prctl(PR_SET_PDEATHSIG)`, `getppid`             | 2.6.2        |
| Namespace unshare                  | `SysProcAttr.Unshareflags`                   | `unshare`                                        | 2.6.16       |
| User-ns id mapping                 | `Uid/GidMappings`                            | `openat`, `write`, `close` on /proc maps         | 3.5          |
| Private root after CLONE_NEWNS     | implicit when unsharing mount ns             | `mount(MS_REC\|MS_PRIVATE)`                      | 2.4.19       |
| Ambient caps                       | `SysProcAttr.AmbientCaps`                    | `capget`, `capset`, `prctl(PR_CAP_AMBIENT, ...)` | 4.3          |
| Restore RLIMIT_NOFILE              | automatic                                    | `prlimit64`                                      | 2.6.36       |
| Cgroup-into-cgroup spawn           | `UseCgroupFD`/`CgroupFD`                     | `clone3(CLONE_INTO_CGROUP)`                      | 5.7          |
| ptrace                             | `SysProcAttr.Ptrace`                         | `ptrace(PTRACE_TRACEME)`                         | 1.0          |
| Error reporting child→parent       | implicit                                     | `read`, `write` on internal pipe                 | 1.0          |
| Worst-case child exit              | exec failure                                 | `exit`                                           | 1.0          |

---

## Part 3 — Implementation scope and proposed phasing

### Tier 1: minimum viable `os/exec.Command(...).Run()`

The smallest set that makes a no-options `Command("ls").Run()` work end-to-end:

- `clone` — recognize "fork-a-process" flag combos (the runtime-thread case is already handled; this is the new flavor: `CLONE_VFORK|CLONE_VM` or plain `SIGCHLD`)
- `execve` — load+run a new binary from our filesystem
- `wait4` — reap a child and surface exit status
- `kill` — send signals to a child
- `pipe2(O_CLOEXEC)` — pipes for stdio wiring (we may already have a partial here for `os.Pipe`)
- `dup3` — FD reshuffling in the child
- `close` — drop FDs in the child
- `chdir` — `Cmd.Dir`
- `fcntl(F_SETFD)` — clear CLOEXEC on inherited FDs
- `read`/`write` — already implemented, but worth flagging that the child→parent error pipe relies on it
- `exit` — child cleanup on execve failure

That's ~10 new syscall handlers. The hard part is not the syscall count — it is implementing the **semantics of an actual child process** inside our shepherd model. Today, each shepherd is one process running one program. Fork+exec means a single shepherd would need to host **two distinct programs** (a forked clone of itself momentarily, then the new binary post-exec), or we model the child as a new shepherd entirely.

### Tier 2: useful niceties

Bumps us from "ls works" to "shell scripts work":

- `pidfd_open`, `pidfd_send_signal`, `waitid(P_PIDFD)` — robust process management free of PID-reuse races
- `setsid`, `setpgid` — required for job control
- `getppid`, `prctl(PR_SET_PDEATHSIG)` — clean child cleanup on parent death
- `prlimit64` for RLIMIT_NOFILE — Go restores it automatically on fork
- `faccessat`/`statx` — PATH lookup (we may have these already for filesystem)

### Tier 3: stub-and-EPERM territory

Real Linux features that almost no `os/exec` user touches; legal to return ENOSYS or EPERM:

- `chroot`, `setuid`/`setgid`/`setgroups`, ambient caps (`capget`/`capset`/`PR_CAP_AMBIENT`)
- `ioctl(TIOCSCTTY/TIOCNOTTY/TIOCSPGRP)` — TTY control
- `ptrace(PTRACE_TRACEME)` — debuggers
- `unshare` + namespace setup, `mount(MS_PRIVATE)`, `/proc/PID/{uid,gid}_map` writes — containers
- `clone3(CLONE_INTO_CGROUP)` — modern cgroup placement

If we stub these, callers that set the corresponding `SysProcAttr` field will get an error, but plain `exec.Command` use keeps working.

### Architectural questions to answer before coding

1. **Does a fork spawn a new shepherd, or split the existing one?** Splitting is closer to Linux semantics but is hostile to our single-program-per-shepherd model. Spawning a new shepherd is simpler but means the brief between-clone-and-execve window (where the child runs Go code to set up its own state) needs a host process to run on.
2. **How do we model `execve` of a non-`.maz` binary?** Do we only support executing other `.maz` plugins, or do we want real ELF loading inside the shepherd?
3. **Is the child's FD table a real Linux FD table, or do we just keep a shadow `fdtable` that mirrors stdio + ExtraFiles?** Tier 1 only needs the latter.
4. **What does `Pid` mean to us?** A real Linux PID assigned by our kernel, or our existing shepherd ID, or something new?
5. **`O_CLOEXEC` is currently a silent no-op** (`maz/linux/syscalls.go:24-31`); once exec lands, every FD opened with O_CLOEXEC must actually close on exec. This is the seam called out in the existing TODO.

---

## Appendix: file citations

Runtime:
- `runtime/os_linux.go:156-217` — clone flags, `newosproc`, `newosproc0`
- `runtime/sys_linux_amd64.s:51-163, 574-620` — exit, signals, clone (amd64)
- `runtime/sys_linux_arm64.s:53-167, 670-750` — exit, signals, clone (arm64)

Standard library:
- `os/exec/exec.go` — `Cmd`, `Start`, `Wait`, pipes, context cancel
- `os/exec/lp_unix.go:44-78` — `LookPath`
- `os/exec.go` (in `os`) — `StartProcess`, `Process`, `processHandle`
- `os/pidfd_linux.go:83-197` — pidfd Wait/Signal + startup probe
- `os/exec_unix.go:32-119, 143-249` — PID-path Wait/Signal, `ForkExec` driver
- `syscall/exec_linux.go:67-113, 138-679` — `SysProcAttr` definition, `forkAndExecInChild1`
- `syscall/exec_unix.go` — `forkExecPipe`, fork lock, error pipe protocol
