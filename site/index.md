---
layout: default
title: Quick Start
---

# Mazzy Quick Start

Cardinal bootloader + Kmazarin Go kernel for ARM64.

[![Windows (No POSIX Shell)](https://github.com/iansmith/mazarin/actions/workflows/windows-test.yml/badge.svg)](https://github.com/iansmith/mazarin/actions/workflows/windows-test.yml)

## Build

Set environment variables and build with Task:

```bash
# Set environment (adjust paths for your system)
export GOTOOLCHAIN=auto
export GO=/path/to/go1.25.5
export QEMU=/path/to/qemu-system-aarch64

# Build everything
go tool task all
```

> **Note:** Requires Go 1.25.5+ and QEMU 10.2+

## Run

Start QEMU with the built kernel:

```bash
# Run with 5 second timeout (shows output then stops)
go tool task run TIMEOUT=5

# Run indefinitely (use 'task stop' to stop)
go tool task run TIMEOUT=0

# Stop QEMU
go tool task stop

# View serial output
go tool task show
```

Output is written to `/tmp/cardinal-serial.log`.

## What You'll See

When you run Mazzy, QEMU opens a graphical window. The kernel initializes the VirtIO GPU and displays an image in the center of the screen. This demonstrates the graphics subsystem working with the Go kernel running on bare metal ARM64.
