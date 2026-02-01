---
layout: default
title: Quick Start
---

# Quick Start

Cardinal bootloader + Kmazarin Go kernel for ARM64.

## Prerequisites

You need two tools:

* **Go** version 1.24 or later ([go.dev/dl](https://go.dev/dl/))
* **QEMU** version 10.2 or later (`qemu-system-aarch64`)

That's it. No Make, no Bash, no shell scripts. The entire build system runs
through `go tool`, so it works on macOS, Linux, and Windows without a POSIX
shell.

If you are running on a machine that is not ARM64, QEMU will emulate the
ARM64 architecture. This works but is slower than running on native hardware.

The first build will be slow as Go downloads dependencies.

## Why `go tool`?

mazarin has no dependency on a POSIX shell. Every build operation, every
utility, and the task runner itself are Go programs invoked via `go tool`.
This means:

- No `sh`, `bash`, `make`, `echo`, `cat`, `mkdir`, `rm`, or other shell commands
- No shell scripting of any kind in the build
- The same commands work identically on macOS, Linux, and Windows
- The only prerequisite is a Go compiler (and QEMU for running)

The task runner ([Task](https://taskfile.dev)) is installed as a Go tool
dependency. You invoke it as `$GO tool task`. All the small utilities that
would normally be shell commands (echo, sleep, rm, mkdir, etc.) are also
Go tools in the `cmd/` directory.

## Setup

Set three environment variables. Adjust the paths for your system:

```bash
export GOTOOLCHAIN=auto
export GO=/path/to/go
export QEMU=/path/to/qemu-system-aarch64
```

**macOS with Homebrew:**
```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

`GOTOOLCHAIN=auto` is required. It ensures the correct Go toolchain version
is selected.

## Build

Build everything (cardinal bootloader, kmazarin kernel, userspace programs,
and disk image):

```bash
$GO tool task
```

This runs the default task which builds all targets. Task tracks file
checksums and only rebuilds what changed.

## Run

Start QEMU with the built kernel:

```bash
# Build and run with 5 second timeout (default)
$GO tool task run

# Run with a longer timeout
$GO tool task run TIMEOUT=30

# Run with no timeout (interactive — you stop it manually)
$GO tool task run TIMEOUT=
```

The `run` task builds everything first (if needed), stops any existing QEMU
instance, then launches QEMU. After the timeout, it stops QEMU and displays
the last lines of serial output.

Serial output is written to `/tmp/cardinal-serial.log`.

## What You'll See

QEMU opens a graphical window. The kernel initializes the VirtIO GPU and
displays an image. Keyboard and mouse input is captured and delivered to a
userspace program (dapope) via the soft IRQ subsystem.

In the serial output you will see boot messages, device discovery, and
input events:

```
[dapope] Starting input event handler
[dapope] Found 2 input device(s)
[dapope] Registered keyboard on slot 0 (IRQ 112)
[dapope] Registered mouse on slot 1 (IRQ 113)
[dapope:kbd] A pressed
[dapope:kbd] A released
[dapope:mouse] X +5
[dapope:mouse] Y -3
[dapope:mouse] LEFT pressed
```

## Stop

```bash
$GO tool task stop
```

This sends a `quit` command to the QEMU monitor via TCP. It is safe to run
even when no QEMU instance is running.

## View Serial Output

```bash
$GO tool task show
```

This uses a safe reader that handles long lines and control characters.
Never read `/tmp/cardinal-serial.log` directly with `cat` or `less` --
the log can contain lines with millions of characters from kernel output
that will freeze your terminal.

## Clean and Rebuild

```bash
# Remove all build artifacts
$GO tool task clean

# Full rebuild
$GO tool task clean
$GO tool task run TIMEOUT=10
```

## Development Workflow

A typical session:

```bash
# Terminal 1: run QEMU with no timeout
$GO tool task run TIMEOUT=

# Terminal 2: view output, query state
$GO tool task show
$GO tool task qemu-console
$GO tool task qemu-console CMD='info registers'

# Terminal 2: stop when done
$GO tool task stop
```

## QEMU Monitor

QEMU runs with a TCP monitor on port 4444. Query it with:

```bash
# CPU registers (default)
$GO tool task qemu-console

# Disassemble memory
$GO tool task qemu-console CMD='x/10i 0x40100000'

# Memory map
$GO tool task qemu-console CMD='info mtree'

# Interrupt state
$GO tool task qemu-console CMD='info irq'
```

QEMU must be running when you issue these commands.

## Debug Runs

```bash
# Interrupt/exception tracing (lightweight)
$GO tool task run-debug-int

# Full instruction tracing (verbose)
$GO tool task run-debug-full

# View debug log
$GO tool task show-debug
$GO tool task show-debug N=500
```

Debug output is written to `/tmp/qemu-debug.log`.

## All Tasks

Run `$GO tool task --list` to see everything available. Key tasks:

| Command | Description |
|---------|-------------|
| `$GO tool task` | Build everything (default) |
| `$GO tool task run` | Build and run QEMU |
| `$GO tool task stop` | Stop QEMU |
| `$GO tool task show` | View serial output safely |
| `$GO tool task clean` | Remove build artifacts |
| `$GO tool task qemu-console` | Send command to QEMU monitor |
| `$GO tool task run-debug-int` | Run with interrupt tracing |
| `$GO tool task check-env` | Verify environment setup |

## Task Variables

| Variable | Default | Used by |
|----------|---------|---------|
| `TIMEOUT` | `5` | `run`, `run-debug-*` |
| `CMD` | `info registers` | `qemu-console` |
| `N` | `100` | `show-debug` |
| `DEBUG` | (unset) | Set to `1` for unoptimized builds |

## Homebrew QEMU Warning

The QEMU 10.2.0 recipe in Homebrew has a broken symlink for
`qemu-system-x86_64` in the default installation. This affects the
`diplomat` (x86_64 UEFI bootloader) build but does not affect the
ARM64 kernel. If you need `qemu-system-x86_64`, use `--build-from-source`
and immediately pin the package.

## Technical Reports

- [PriestSieve Fairness Analysis](priestsieve-fairness-analysis.html) - Analysis of goroutine scheduling fairness using a prime number sieve benchmark
