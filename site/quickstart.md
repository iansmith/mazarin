---
layout: default
title: Quick Start
---

# Quick Start

A short admission first: in practice, mazarin is tested almost
exclusively on **ARM64 macOS with QEMU and the HVF accelerator turned
on**. The kernel and the build system support x86_64 too (boot, build,
and run all work), but the ARM64-on-Apple-Silicon-with-HVF path is the
one that gets exercised every day. If you are on that platform, you
should have no trouble. If you are not, you should still be able to
follow along, but expect a few rough edges that we have not seen.

## Prerequisites

Two tools:

- **Go 1.26.2** ([go.dev/dl](https://go.dev/dl/))
- **QEMU 10.2 or later**, with `qemu-system-aarch64`. On macOS:
  `brew install qemu`.

That is it. No make, no bash, no shell scripts -- the entire build
runs through `go tool`. Every utility the build needs (echo, mkdir,
rm, mkext2, the task runner itself) is a Go program declared as a tool
dependency in `go.mod`. You never have to install a separate package
manager or scripting language.

## Get the source

mazarin is split across two repositories that live as siblings inside
a parent directory called `mazos`:

```bash
mkdir mazos
cd mazos
git clone git@github.com:iansmith/mazarin.git mazzy
git clone git@github.com:iansmith/louis14.git
```

You should now have:

```
mazos/
  mazzy/     ← kernel, bootloader, shepherds, build system
  louis14/   ← fonts and supporting assets
```

The build looks for `louis14` as a sibling of the `mazzy` checkout
(`../louis14`), so as long as both repos are inside `mazos/` you do
not need to set anything else.

All commands below should be run from inside `mazos/mazzy`.

## Environment

Three required variables:

| Variable | Purpose |
|----------|---------|
| `GOTOOLCHAIN` | Must be `auto`. Tells Go to select the right toolchain version. |
| `GO` | Path to the Go 1.26.2 binary. |
| `QEMU` | Path to `qemu-system-aarch64` (>= 10.2). |

On macOS with Homebrew, the typical values are:

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.26.2/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

## Build and run

Build everything -- bootloader, kernel, all shepherds, and the disk
image:

```bash
$GO tool task
```

Run mazarin under QEMU with HVF acceleration (ARM64 macOS, the
recommended path):

```bash
$GO tool task run-arm64-hvf
```

A QEMU window opens, shows the UEFI splash, then mazarin's kernel
boots and rachel (the window manager) starts compositing application
windows. The mail app, fti (full-text search), maildb, and a console
window all come up within a few seconds.

If you are not on Apple Silicon, drop `-hvf` and use one of:

```bash
$GO tool task run-arm64        # software-emulated ARM64 (slow)
$GO tool task run-x86_64       # x86_64 with KVM if available
```

Run `$GO tool task --list` to see every available task.

## Reading the serial log

Serial output goes to a file in `/tmp`. Do **not** read it with `cat`,
`tail`, or a text editor -- a runaway loop in the kernel can produce
lines with millions of characters that will hang or crash most tools.
Use the shipped safe reader:

```bash
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```

## Stop

```bash
$GO tool task stop
```

Sends a `quit` to the QEMU monitor over TCP. Safe to run even when
nothing is running.

## Where to next

- Read the [news](news.md) for what is in the system right now.
- Read the [Mancini API reference](mancini/index.md) for the UI
  toolkit.
- Browse the source on [GitHub](https://github.com/iansmith/mazarin).
