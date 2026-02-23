---
layout: default
title: Quick Start
---

# Quick Start

This is to get you rockin' on the kmazarin kernel.  This kernel boots using
the open UEFI firmware for X86_64 and ARM64.  For RISC-V we have our own
bootloader, but all three are unified under the name "diplomat".  We use
qemu as our hypervisor and run on the "virt" board for ARM64 and RISC-V.
On X86_64 we use the q35 machine.  In all cases the devices are paravirtualized
hardware from the virtio project.

## Prerequisites

You need two tools:

* **Go** version 1.24 or later ([go.dev/dl](https://go.dev/dl/))
* **QEMU** version 10.2 or later (`qemu-system-aarch64`)

That's it. No Make, no Bash, no shell scripts. The entire build system runs
through `go tool`, so it works on macOS, Linux, and Windows without a POSIX
shell.

QEMU will emulate the architecture you want, although we only test on ARM64
right now (can you help?). Obviously, emulating a different chip is much slower
than running on that platform.

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
export QEMU=/path/to/qemu-system-aarch64 # you may want to change this for your platform
```

**Example: macOS with Homebrew:**
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

This runs the default task which prints out information about how to build the
and run the version of the code for different platforms. It explains the 
environment variables as well.

## Run

When you use task to run the system (which implies build if necessary):
```
task run  # implies ARM64
```

Serial output is written to a file in `/tmp` but you shouldn't read the output
file directly because it frequently has very long lines (millions of characters).
Here is how to read the output from the ARM64 

```
$GO$ tool safe-serial-read /tmp/diplomat-arm64-serial.log
```

## What You'll See

If you are on AMD64 or ARM64 you'll see the UEFI boot up screen.  Once the
mazarin kernel gets running, the window will change to a window with a plain
gray background. Then a clock will appear in the upper right. Finally, the
stdio priest starts and opens an on-screen window and it shows the stdout
and stderr from different programs that are running. Most of the kernel output
is directed at the /tmp/diplomat-XXX-serial.log.  

If you manipulate the mouse or keyboard you'll see that being reported by the
stdio program because something is writing information about the input to
stdout.  



## Stop

```bash
$GO tool task stop #stops all platforms
```

This sends a `quit` command to the QEMU monitor via TCP. It is safe to run
even when no QEMU instance is running.



