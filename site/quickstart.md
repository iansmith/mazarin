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

## Batteries Included: All Tools Ship with the Repo

Because the entire build runs through `go tool`, every program the build
needs is declared as a dependency in `go.mod` or lives in `cmd/` and is
built on demand by the Go toolchain. When you run `$GO tool task` for the
first time, Go downloads and compiles everything automatically. You never
need to install a separate build tool, package manager, or script
interpreter.

The tools fall into a few categories:

### Task Runner

| Tool | Description |
|------|-------------|
| `task` | [Task](https://taskfile.dev) — the build system. Reads `Taskfile.yml` and runs tasks. Invoked as `$GO tool task`. |

### Shell Replacements (`go-*`)

These exist solely so that `Taskfile.yml` never has to shell out to `sh`,
`bash`, or any POSIX utility. They behave like their Unix counterparts but
are pure Go and work on every platform.

| Tool | Unix equivalent | Description |
|------|-----------------|-------------|
| `go-cat` | `cat` | Concatenates and prints files. |
| `go-cp` | `cp` | Copies files. |
| `go-echo` | `echo` | Prints arguments to stdout. |
| `go-env` | — | Checks or displays environment variables. |
| `go-filesize` | `wc -c` | Prints a file's size in bytes or human-readable form. |
| `go-kill` | `kill` | Sends signals to processes by PID. |
| `go-ls` | `ls` | Lists directory contents. |
| `go-mkdir` | `mkdir -p` | Creates directories, including parents. |
| `go-mv` | `mv` | Moves or renames files. |
| `go-nc` | `nc` | Minimal netcat — opens a TCP connection and optionally sends a command. Used to talk to the QEMU monitor. |
| `go-rm` | `rm -rf` | Removes files and directories. |
| `go-sleep` | `sleep` | Pauses for a specified duration. |
| `go-start` | `... &` | Starts a command in the background and exits immediately. |
| `go-stopproc` | `pkill` | Stops processes by name pattern. |
| `go-tail` | `tail` | Outputs the last N lines or bytes of a file. |
| `go-test` | `test -d / test -f` | Evaluates conditions (file existence, string comparison). |
| `go-tr` | `tr` | Translates or deletes characters in a stream. |
| `go-wc` | `wc` | Counts lines, words, and bytes in files. |

### Disk Image Builders

| Tool | Description |
|------|-------------|
| `mkext2` | Creates ext2 filesystem images. Accepts loose files and `-dir /mountpoint=hostpath` mappings. Used to build the main data disk. |
| `mkfat32` | Creates FAT32 disk images with Long File Name support. Used for the fonts disk. |
| `mkesp` | Creates an EFI System Partition (ESP) image with the standard EFI directory layout. |

### Binary and ELF Utilities

| Tool | Description |
|------|-------------|
| `elf2bin` | Extracts `PT_LOAD` segments from an ELF file and writes raw binary. |
| `elf2pe` | Converts an ELF executable to PE format for use as a UEFI application. |
| `pe-fixup` | Patches a Go-produced PE binary to set the correct UEFI subsystem field. |
| `fix-go-elf` | Injects a bootstrap trampoline at the start of `.text` for RISC-V entry points. |
| `patch-entry` | Patches the ELF entry point to a specified symbol address. |
| `relocate-kmazarin` | Rewrites all addresses in the kmazarin ELF to a high-memory offset. |
| `print-kmazarin-addr` | Prints or verifies the kmazarin load address against the constant in source. |
| `incbin2goasm` | Converts binary files to Go assembly `DATA` directives for embedding. |
| `imageconvert` | Converts image files to raw ARGB8888 binary for kernel embedding. |

### Overlay and Stub Generators

The `.maz` program format requires thin stubs that get patched at load time.
These tools generate them.

| Tool | Description |
|------|-------------|
| `gen-overlay` | Generates or merges Go overlay JSON files used to swap out runtime source files at build time. |
| `gen-ast-stubs` | Parses Go standard library source and replaces function bodies with minimal `for{}` loops to create thin client stubs. |
| `gen-test-stubs` | Generates test stub files with forward declarations for assembly-implemented functions. |
| `maz-reloc` | Scans a `.maz` binary and emits a `.maz_imports` section listing references to thin stubs that need load-time patching by the kernel. |

### Kernel Build Helpers

| Tool | Description |
|------|-------------|
| `mktestkernel` | Generates a minimal test kernel ELF that writes to COM1 and halts — used for boot-path testing. |
| `compile-constraints` | Compiles `.vgo` constraint source files into `.vbc.go` files containing `*vm.Program` literals for the Mancini constraint VM. |

### Version and Environment Checks

| Tool | Description |
|------|-------------|
| `check-version` | Validates that `go` and `qemu` meet the minimum version requirements before the build starts. |

### Serial Log Safety

Reading the QEMU serial log directly with `cat` or a text editor is
dangerous — an infinite loop in the kernel can produce lines with millions
of characters that will freeze or crash most tools. This program makes it safe.

| Tool | Description |
|------|-------------|
| `safe-serial-read` | Reads a serial log file safely, detecting and truncating runaway long lines and stripping terminal control sequences. Always use this instead of `cat`. |

## Getting the Source

mazarin is split across three repositories that are designed to be cloned
as siblings inside a single parent directory. Create a directory called `maz`
and clone all three into it:

```bash
mkdir maz
cd maz
git clone git@github.com:iansmith/mazarin.git
git clone git@github.com:iansmith/gaston.git
git clone git@github.com:iansmith/louis14.git
```

Your directory should look like this:

```
maz/
  mazarin/   ← kernel, bootloader, shepherds, build system
  gaston/    ← C compiler and standard library (libgastonc)
  louis14/   ← fonts and supporting assets
```

The build system in `mazarin/` defaults to looking for the other two repos
as siblings (`../gaston` and `../louis14`). As long as you clone all three
into the same parent directory you do not need to set any extra environment
variables.

All subsequent commands should be run from inside the `mazarin/` directory.

## Environment Variables

The following environment variables control the build. The three marked
**required** must always be set. The rest have defaults that work when the
repos are cloned as siblings as described above.

### Required

| Variable | Purpose | Example (macOS/Homebrew) |
|----------|---------|--------------------------|
| `GOTOOLCHAIN` | Must be `auto`. Tells Go to select the right toolchain version. | `auto` |
| `GO` | Path to the Go binary (1.26.2). | `/opt/homebrew/Cellar/go/1.26.2/libexec/bin/go` |
| `QEMU` | Path to `qemu-system-aarch64` (>= 10.2). | `/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64` |

### Optional — sibling repo paths

These default to the sibling layout described above. Override them only
if your checkout is in a non-standard location.

| Variable | Default | Purpose |
|----------|---------|---------|
| `GASTON_DIR` | `../gaston` | Path to the gaston C compiler repo. |
| `LOUIS14_DIR` | `../louis14` | Path to the louis14 assets repo. |
| `FONTS_DIR` | `../louis14/fonts` | Path to the fonts directory. Derived from `LOUIS14_DIR` by default. |

### Optional — additional QEMU targets

| Variable | Default | Purpose |
|----------|---------|---------|
| `QEMU_X86_64` | `qemu-system-x86_64` | Path to `qemu-system-x86_64` for x86_64 runs. |
| `QEMU_RISCV64` | `qemu-system-riscv64` | Path to `qemu-system-riscv64` for RISC-V runs. |
| `OVMF` | *(auto-detected)* | Path to `edk2-x86_64-code.fd` UEFI firmware for x86_64. |
| `OVMF_ARM64` | *(auto-detected)* | Path to ARM64 UEFI firmware. |
| `QEMU_RAM` | `2G` | RAM given to the QEMU VM. |

### Optional — build tuning

| Variable | Default | Purpose |
|----------|---------|---------|
| `DEBUG` | *(unset)* | Set to `1` to disable compiler optimizations and inlining. |

## Setup

Set the required environment variables. Adjust the paths for your system:

```bash
export GOTOOLCHAIN=auto
export GO=/path/to/go
export QEMU=/path/to/qemu-system-aarch64
```

**Example: macOS with Homebrew:**
```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.26.2/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

If your repos are not in the standard sibling layout, also set:

```bash
export GASTON_DIR=/path/to/gaston
export LOUIS14_DIR=/path/to/louis14
```

## Build

Build everything (diplomat bootloader, kmazarin kernel, userspace programs,
C compiler, and disk image):

```bash
$GO tool task
```

This runs the default task which builds diplomat + kmazarin for ARM64 and
creates the disk image. Run `$GO tool task --list` to see all available tasks.

## Run

When you use task to run the system (which implies build if necessary):
```bash
$GO tool task run        # ARM64 (default)
$GO tool task run-x86_64
$GO tool task run-riscv64
```

Serial output is written to a file in `/tmp` but you shouldn't read the output
file directly because it frequently has very long lines (millions of characters).
Here is how to read the output from the ARM64 run safely:

```bash
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```

## What You'll See

If you are on AMD64 or ARM64 you'll see the UEFI boot up screen.  Once the
mazarin kernel gets running, the window will change to a window with a plain
gray background. Then a clock will appear in the upper right. Finally, the
stdio priest starts and opens an on-screen window and it shows the stdout
and stderr from different programs that are running. Most of the kernel output
is directed at the `/tmp/diplomat-XXX-serial.log`.

If you manipulate the mouse or keyboard you'll see that being reported by the
stdio program because something is writing information about the input to
stdout.

## Stop

```bash
$GO tool task stop   # stops all platforms
```

This sends a `quit` command to the QEMU monitor via TCP. It is safe to run
even when no QEMU instance is running.
