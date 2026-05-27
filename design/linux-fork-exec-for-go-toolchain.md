# Fork/exec needed to run the Go toolchain

**Date:** 2026-05-27
**Status:** Scoping document, follow-up to [linux-fork-exec-survey.md](linux-fork-exec-survey.md)
**Goal:** Identify the minimum subset of the fork/exec surface needed to run `go build` (the "big prize") on mazzy.
**Source material:** Go 1.26.2 at `/opt/homebrew/Cellar/go/1.26.2/libexec/src/cmd/`

---

## TL;DR

The Go toolchain uses **a single, narrow spawn pattern** and **nothing exotic** from the `SysProcAttr` knob menu. To run pure-Go `go build` (which is what we need for the modified Go compiler), we need essentially the **Tier-1 list from the prior survey** and nothing else:

```
clone (process flavor)  execve  wait4  pipe2  dup3  close  chdir  fcntl(F_SETFD)
```

Plus the already-supported `read`/`write`/`exit`. That's it. ~8–10 new syscall handlers.

Specifically:
- `cmd/go` only uses `os/exec.Cmd` — never `os.StartProcess`, never `syscall.ForkExec` directly.
- `cmd/go` never sets `SysProcAttr` at all.
- `cmd/compile` and `cmd/asm` spawn NOTHING themselves.
- `cmd/link` defaults to `LinkInternal` for pure-Go programs and produces the final ELF in-process — zero spawns.

**Cgo is explicitly out of scope** for this work and is not discussed below. The modified Go compiler we want to run is pure Go; that's the entire target.

---

## 1. How `cmd/go` spawns subprocesses

### The one and only spawn path

Every subprocess `cmd/go` launches goes through one function: `(*Shell).runOut()` at [cmd/go/internal/work/shell.go:600-683](https://github.com/golang/go/blob/master/src/cmd/go/internal/work/shell.go). The relevant lines:

```go
// shell.go:635
path, err := pathcache.LookPath(cmdline[0])
if err != nil { return nil, err }
// shell.go:639
cmd := exec.Command(path, cmdline[1:]...)
// ...
cmd.Stdout = &buf
cmd.Stderr = &buf
if dir != "." { cmd.Dir = dir }
cmd.Env = append(cmd.Environ(), env...)
err = cmd.Run()
```

That's the entire spawn API surface of `cmd/go` for our purposes. Pure `os/exec.Cmd`, synchronous `Run()`, captured stdout/stderr to a `bytes.Buffer`. No `os.StartProcess`. No `syscall.ForkExec`. No `Start`/`Wait` split, no goroutines around the child.

### What fields `cmd/go` sets on the child

| Field          | Set?                                           | Notes                                                  |
|----------------|------------------------------------------------|--------------------------------------------------------|
| `Path`         | Always                                         | Absolute path from `base.Tool(name)` or `LookPath`     |
| `Args`         | Always                                         | argv as you'd expect                                   |
| `Dir`          | Sometimes (when package dir ≠ `.`)             | Triggers `chdir()` in child                            |
| `Env`          | Always                                         | Parent env + `TOOLEXEC_IMPORTPATH`, `GOROOT`, etc.     |
| `Stdin`        | Never                                          | Inherits parent's stdin                                |
| `Stdout`       | Always (to `bytes.Buffer`)                     | → `os/exec` creates a pipe + copy goroutine            |
| `Stderr`       | Always (same `bytes.Buffer` as Stdout)         | Combined output                                        |
| `ExtraFiles`   | **Never**                                      | Only stdin/stdout/stderr inherited                     |
| `SysProcAttr`  | **Never**                                      | Vanilla child process                                  |
| `Cancel`       | **Never**                                      | No context-based cancellation                          |
| `WaitDelay`    | **Never**                                      | No timeout machinery                                   |

This is significant. Every single one of the optional `SysProcAttr` features we catalogued in the prior survey — chroot, setuid, setsid, setpgid, Setctty, Noctty, Foreground, Pdeathsig, Cloneflags, Unshareflags, UidMappings, GidMappings, AmbientCaps, UseCgroupFD, PidFD, Ptrace — is **untouched by the Go toolchain**. Every TTY ioctl, every namespace operation, every capability dance — all skippable.

### Tool path resolution

`cmd/go` resolves toolchain binaries via `base.Tool(name)` in [cmd/go/internal/base/tool.go:32-42](https://github.com/golang/go/blob/master/src/cmd/go/internal/base/tool.go), which returns:

```
$GOROOT/pkg/tool/$GOOS_$GOARCH/<name>
```

This is an absolute path with a `/` in it, so `exec.Command` skips `LookPath` entirely. No `stat` or `access` lookups in the parent for builtin tools — the child just gets a path and `execve` either succeeds or returns ENOENT/EACCES. Easy.

`pathcache.LookPath` is only used for *external* tools (which we don't need to support).

---

## 2. What the spawned tools do themselves

| Tool          | Spawns subprocesses?                         | When                                                              |
|---------------|----------------------------------------------|-------------------------------------------------------------------|
| `cmd/compile` | **No** (only test files import `os/exec`)    | Never in production                                               |
| `cmd/asm`     | **No**                                       | Never                                                             |
| `cmd/link`    | **No** for pure-Go (`LinkInternal` default)  | Only `LinkExternal` mode spawns, and we don't enter that mode     |

### Compiler: zero spawns

Grep for `os/exec` under `cmd/compile/`: hits are all `_test.go` files (`gcimporter_test.go`, `versions_test.go`, `debug_test.go`) and one non-production helper in `cmd/compile/internal/ssa/html.go` for debugging SSA dumps. The actual compilation pipeline — parse → typecheck → SSA → codegen → object emit — is entirely in-process.

### Assembler: zero spawns

Grep under `cmd/asm/`: no `exec.Command` calls outside tests. Assembly is in-process work.

### Linker: zero spawns in the pure-Go path

All external-linker logic is gated by `hostlinksetup()` at [cmd/link/internal/ld/lib.go:1254](https://github.com/golang/go/blob/master/src/cmd/link/internal/ld/lib.go):

```go
func hostlinksetup(ctxt *Link) {
    if ctxt.LinkMode != LinkExternal {
        return
    }
```

`LinkMode` is decided in `determineLinkMode()` at [cmd/link/internal/ld/config.go:187-227](https://github.com/golang/go/blob/master/src/cmd/link/internal/ld/config.go). For pure-Go programs without c-shared/c-archive/shared/plugin/`-linkmode=external`, it defaults to `LinkInternal` — the linker emits the final binary itself with zero spawns.

---

## 3. End-to-end spawn sequence for a real `go build`

### `go build hello.go` (single file, no imports beyond `runtime`/`internal`)

```
cmd/go
  ├─ exec.Command("$GOROOT/pkg/tool/.../compile", "-o", "obj.a", ..., "hello.go").Run()
  └─ exec.Command("$GOROOT/pkg/tool/.../link",    "-o", "hello", ..., "main=obj.a").Run()
```

Two child processes total. Each one runs `cmd.Run()` synchronously. Each one captures stdout/stderr to a `bytes.Buffer`. Each one has the parent's env + a few overrides. Done.

### `go build` with one imported package (e.g. `fmt`)

```
cmd/go (action graph runs in parallel up to GOMAXPROCS)
  ├─ compile fmt        (one child)
  ├─ compile main       (one child, after fmt finishes)
  └─ link main          (one child, after all compiles finish)
```

The `cmd/go` action graph at [cmd/go/internal/work/exec.go](https://github.com/golang/go/blob/master/src/cmd/go/internal/work/exec.go) (`Do` at lines 73-258) walks the DAG and dispatches actions; `-p` controls parallelism (default = # CPUs). So we may have multiple `compile` children running concurrently. **This means our shepherd needs to support multiple live child processes simultaneously**, not just one at a time.

### Building the Go compiler itself

Building `cmd/compile` is just a `go build cmd/compile` (or `go install cmd/compile`) — same shape as above, scaled up. Roughly hundreds of `compile` invocations across the dependency graph, then one `link`. All pure Go.

---

## 4. Mapping back to the syscall survey

Cross-referencing what `cmd/go` actually exercises against the [linux-fork-exec-survey.md](linux-fork-exec-survey.md) tiers:

### Tier 1 (must have) — every item is exercised:
- **`clone`** — for each child. Process-creation flavor (not the runtime's thread-creation flavor).
- **`execve`** — for each child.
- **`wait4`** — `cmd.Run()` reaps via `Process.Wait()` → PID path → `wait4`. (We don't need pidfd; the PID path is the default fallback.)
- **`pipe2`** — `os/exec` creates pipes internally when `cmd.Stdout`/`Stderr` are set to a non-`*os.File`. Specifically `os.Pipe()` → `pipe2(O_CLOEXEC)`.
- **`dup3`** — child wires the pipe FDs into positions 1 and 2.
- **`close`** — child drops unwanted FDs.
- **`chdir`** — when `Cmd.Dir` is set (sometimes).
- **`fcntl(F_SETFD)`** — to clear CLOEXEC on inherited stdio.
- **`read`/`write`** — already implemented. Used for the error-reporting pipe between parent and child, and for the stdout/stderr copy goroutine on the parent side.
- **`exit`** — child exit on execve failure.

### Tier 2 (would-be-nice) — NOT exercised by `cmd/go`:
- **`pidfd_open` / `pidfd_send_signal` / `waitid(P_PIDFD)`** — `os/exec` would *prefer* these if available, but if `pidfd_open` fails on our startup probe, Go silently falls back to the PID path. We can return ENOSYS for the pidfd syscalls and `cmd/go` won't notice.
- **`setsid` / `setpgid`** — never set by `cmd/go` (no `SysProcAttr.Setsid` or `.Setpgid`).
- **`prctl(PR_SET_PDEATHSIG)`** — never set.
- **`prlimit64`** — Go restores RLIMIT_NOFILE if a previous syscall raised it; if our `getrlimit` returns "unchanged from default" the restore path is a no-op. We may need a stub here, but it won't be invoked for real work.

### Tier 3 (legal to ENOSYS) — definitely never exercised:
- **`chroot`, `setuid`, `setgid`, `setgroups`** — no `SysProcAttr.Chroot`/`.Credential` use.
- **`unshare`, `mount`, `/proc/PID/{uid,gid}_map`** — no namespaces.
- **`capget` / `capset` / `prctl(PR_CAP_AMBIENT)`** — no ambient caps.
- **`ioctl(TIOCSCTTY/TIOCNOTTY/TIOCSPGRP)`** — no TTY control.
- **`ptrace`** — no.
- **`clone3(CLONE_INTO_CGROUP)`** — no cgroup placement.

### Signal-related: also not needed for `cmd/go`
`cmd/go` doesn't use `Cmd.Cancel` or context cancellation on its children. So we don't need **`kill`** or **`pidfd_send_signal`** on the parent side to make the Go toolchain run. (The runtime's existing `tgkill`/`kill` paths for in-process signal delivery are unrelated; those already exist.)

---

## 5. So what's the actual implementation list?

To run `go build` for pure-Go programs (including the Go compiler itself), implement the following in the linux shepherd:

### New syscall handlers
1. `clone` — detect the **process-creation** flavor (typically `SIGCHLD` only, or `CLONE_VFORK|CLONE_VM` for the optimized path; not `CLONE_THREAD|CLONE_VM`). Route this to "spawn a child process". The existing thread-flavor path is untouched.
2. `execve` — load and run a binary from our filesystem in the child.
3. `wait4` — block until child exits, return status + rusage.
4. `pipe2` — create a pipe pair with the requested flags (`O_CLOEXEC`, possibly `O_NONBLOCK`).
5. `dup3` — duplicate FD with a target FD index and flags.
6. `close` — already implemented presumably, but verify our shepherd handles arbitrary FDs.
7. `chdir` — change working directory of the calling "process".
8. `fcntl(F_SETFD)` — clear `FD_CLOEXEC` on an FD.

### Things to make real (currently stubbed)
9. **`O_CLOEXEC`** — currently a silent no-op per the TODO at [maz/linux/syscalls.go:24-31](maz/linux/syscalls.go:24). Make it actually close FDs across execve.

### Things to stub with ENOSYS / EPERM
- `pidfd_open`, `pidfd_send_signal`, `waitid` — return ENOSYS; Go falls back gracefully.
- `chroot`, `setuid`/`setgid`/`setgroups`, `setsid`, `setpgid`, `prctl`, `unshare`, `capget`/`capset`, `ptrace`, all TTY ioctls — return EPERM or ENOSYS. None of these are reachable from pure-Go `go build`.
- `clone3` — return ENOSYS; Go falls back to classic `clone`.

### Architectural decisions we need to make
The harder questions are unchanged from the prior survey, and they're not syscalls — they're model questions:

1. **Process model**: a forked child needs to be a real entity in our system. Does it become a new shepherd? A sub-shepherd inside the parent? Something else?
2. **Execve semantics for `.elf` binaries**: the Go tools are real ELF executables on Linux. Can our shepherd's execve load a stock Go-compiled ELF from disk, or do we need to constrain to `.maz` plugins? If the latter, we'd be running modified Go tools packaged as `.maz`, which is fine but means the build pipeline needs to produce `.maz` artifacts.
3. **Filesystem layout**: $GOROOT needs to exist and contain `pkg/tool/$GOOS_$GOARCH/{compile,link,asm}` reachable from the running process.
4. **PID space**: `Cmd.Process.Pid` needs to be *something*. Pick a numbering scheme (sequential? shepherd-ID-derived?) and make sure `wait4` matches it.
5. **Concurrent children**: `go build -p N` will have up to N children alive at once. Our model needs to handle this.

---

## 6. What this means for sequencing the work

This survey suggests a clear minimal milestone:

> **Milestone "hello-world build"**: from inside mazzy, run `go build hello.go` where `hello.go` is a pure-Go program importing only stdlib, and produce a working executable.

If we can hit that, building the Go compiler itself is the same shape — just more invocations of the same syscalls.

External linkers, `vet`, `generate`, and `test` are not on the critical path to "compile Go from inside mazzy."
