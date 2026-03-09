# Continuation Prompt: LoadFile and Page Transfer Implementation

## Context

You are implementing a major restructuring of the Mazzy OS boot sequence and
file loading mechanism. The full plan is at:

    design/LOADFILE-AND-PAGE-TRANSFER.md

Read that file first. It describes 7 implementation phases, new syscalls
(TransferAndUnmap, LoadFile, RunMaz, RunPriest, SetReady), the fs.maz rewrite,
and the kernel boot change.

## Current Branch

    git checkout fix/pr-17-comments

This branch is based on `feature/shared-memory-ipc`. Work already completed
on this branch:

- **IPC queue removed from kernel** — SyscallIPCCall/Recv/Reply and all queue
  infrastructure deleted from kernel. Userspace wrappers in mazarin/sys/ipc.go
  and flock/cmd/fat32/main.go still exist (will be rewritten).
- **Delegate queue concurrency fixed** — IRQ disable around queue push/pop,
  CAS for handler registration in delegate.go.

## What To Implement

Follow the 7-phase implementation order in the plan document. For each phase:

1. Add the new syscall number to `mazarin/sys/syscall.go`
2. Add sysid entry if needed in `shared/sysid/sysid.go`
3. Write the kernel implementation in `kmazarin/ksyscall/`
4. Write the userspace wrapper in `mazarin/sys/`
5. Add dispatch entry in `kmazarin/ksyscall/mazzy.go`
6. Add bridge functions in `kmazarin/kmazarin/` if needed
7. Add test stubs in `kmazarin/ksyscall/forward_decl_stubs.go` if needed

## Key Files To Read Before Starting

- `design/LOADFILE-AND-PAGE-TRANSFER.md` — the full plan
- `kmazarin/ksyscall/loadmaz.go` — existing DoLoadMazWork (ELF loading logic to refactor for RunMaz)
- `kmazarin/ksyscall/delegate.go` — delegate mechanism (LoadFile will be delegated)
- `kmazarin/ksyscall/mazzy.go` — syscall dispatch table
- `kmazarin/kmazarin/io_bridge.go` — BlockForBlockIO/WakeBlockIOThread
- `kmazarin/kmazarin/main.go` — launchPriestsFromConfig (to modify for boot change)
- `kmazarin/proc/proc.go` — Priest struct (add Ready field)
- `flock/cmd/fat32/main.go` — fs.maz (to rewrite)
- `flock/cmd/disk/main.go` — disk priest (loads fs.maz via bootstrap path)
- `mazarin/sys/syscall.go` — syscall numbers
- `shared/sysid/sysid.go` — sysid entries for delegation
- `shared/constants/boot_config.go` — BootPriest struct
- `config/kmazarin-arm64.toml` — boot config format

## Critical Rules

- NEVER disable async preemption or the GC (see CLAUDE.md)
- ALWAYS set GODEBUG=gctrace=1 for kernel and priests
- Keep architecture similar across RISC-V, ARM64, AMD64
- No polling or timeouts without discussion (user directive)
- Use IRQ disable (not mutexes) for kernel critical sections on GOMAXPROCS=1
- RunMaz and RunPriest implicitly unmap the raw ELF pages from the caller
- fs.maz channel buffer cap = 64
- LoadFile is fail-fast when delegate not ready (ErrNotReady), caller retries
- fs.maz uses a real TOML library (pelletier/go-toml/v2 or BurntSushi/toml)
- Serial log safety: use `$GO tool safe-serial-read`, never cat/read raw logs

## Build and Test

```bash
export GOTOOLCHAIN=auto
export GO=/Users/iansmith/sdk/go1.25.5/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64

# Build all architectures
$GO tool task

# Run and check serial output
$GO tool task run TIMEOUT=15
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```
