# Taskfile Reference

This project uses [Task](https://taskfile.dev) for build automation. Task is installed as a Go tool and must be run via `$GO tool task`.

## Cross-Platform Design

The Taskfile is designed to work on macOS, Linux, and Windows without requiring a POSIX shell. This is achieved by:

- **No `echo`, `tail`, `cat`, `mkdir`, `rm`, or other shell commands.** Every utility operation uses a Go tool (e.g., `go-echo`, `go-tail`, `go-rm`, `go-mkdir`). These are compiled Go programs that work identically on all platforms.
- **No `sh:` variable captures.** Values that would traditionally require shell command substitution (like `go env GOROOT` or reading a constant from source) are handled by Go tools with appropriate flags (e.g., `gen-ast-stubs -go=... -runtime-from-go`, `print-kmazarin-addr -check ...`).
- **Inline `KEY=VALUE cmd` syntax** for cross-compilation environment variables (e.g., `CGO_ENABLED=0 GOARCH=arm64 GOOS=linux`). This is handled by Task's built-in shell interpreter (`mvdan.cc/sh`), not a system shell.
- **Platform-adaptive display.** QEMU display backend is selected automatically: `-display cocoa` on macOS, `-display sdl` on Linux/Windows, using Task's `{{OS}}` template function.

### Go Tools Replacing Shell Commands

These tools live in `cmd/` and are compiled and cached automatically by `go tool`:

| Tool | Replaces | Purpose |
|------|----------|---------|
| `go-echo` | `echo` | Print messages (supports empty args for blank lines) |
| `go-tail` | `tail` | Show last N lines of a file (`-n N`) |
| `go-cat` | `cat`/`cp` | Copy file contents (`-o output input`) |
| `go-mkdir` | `mkdir -p` | Create directories |
| `go-rm` | `rm -rf` | Remove files and directories (`-r` for recursive) |
| `go-mv` | `mv` | Move/rename files |
| `go-sleep` | `sleep` | Sleep for a duration (supports seconds and fractions) |
| `go-start` | `cmd &` | Start a process in the background |
| `go-nc` | `nc`/`netcat` | Send a command to a TCP socket (QEMU monitor) |
| `go-test` | `test -d` | Test file/directory existence |
| `go-env` | `printenv` | Check environment variables |
| `go-kill` | `kill`/`pkill` | Kill processes |
| `safe-serial-read` | `cat`/`less` | Safely read serial logs (handles long lines, control chars) |
| `check-version` | version scripts | Verify Go and QEMU versions |
| `print-kmazarin-addr` | `sh:` capture | Print or validate kmazarin load address |
| `gen-ast-stubs` | `sh:` + manual GOROOT | Generate thin overlay stubs (discovers GOROOT internally) |

## Prerequisites

Set environment variables before using task:

```bash
export GOTOOLCHAIN=auto
export GO=/path/to/go          # Go >= 1.24
export QEMU=/path/to/qemu-system-aarch64   # QEMU >= 10.2
```

If `GO` or `QEMU` are not set, the build will attempt to find them via `which`. If found, a warning is printed. If not found, the build aborts.

**Requirements:**
- `GOTOOLCHAIN=auto` - Required
- `GO` - Go >= 1.24 (1.25 recommended)
- `QEMU` - QEMU >= 10.2

## Running Task

Task is a Go tool, not a standalone binary. Always run it via:

```bash
$GO tool task [task-name] [VARIABLE=value ...]
```

## Quick Reference

| Command | Description |
|---------|-------------|
| `$GO tool task` | Build cardinal (default) |
| `$GO tool task run` | Build and run QEMU (5s timeout) |
| `$GO tool task show` | View serial output with pager |
| `$GO tool task stop` | Stop all QEMU instances |
| `$GO tool task clean` | Remove build artifacts |
| `$GO tool task --list` | List all available tasks |

---

## Build Tasks

### Build Everything (Default)

```bash
$GO tool task
```

Builds cardinal bootloader and kmazarin kernel. Kmazarin is loaded from disk at runtime.

### Build Specific Targets

```bash
$GO tool task cardinal       # Build cardinal bootloader
$GO tool task kmazarin       # Build kmazarin kernel
$GO tool task priestsieve    # Build priestsieve prime sieve test
$GO tool task priest2        # Build priest2 scheduling test
$GO tool task priest3        # Build priest3 scheduling test
$GO tool task helloworld     # Build helloworld test program
$GO tool task helloworld-maz # Build helloworld.maz (direct SVC, no overlay)
$GO tool task disk           # Create FAT32 disk image with flock binaries
$GO tool task diplomat       # Build diplomat UEFI bootloader (x86_64)
```

### Clean Build

```bash
$GO tool task clean        # Remove build/ directory
$GO tool task clean && $GO tool task   # Full rebuild
```

### Debug Build

Disable optimizations for better GDB debugging:

```bash
DEBUG=1 $GO tool task cardinal
DEBUG=1 $GO tool task kmazarin
```

---

## Run Tasks

### Run QEMU

The `run` task builds everything (cardinal + disk image) then starts QEMU.

```bash
# Default: 5 second timeout
$GO tool task run

# Custom timeout (30 seconds)
$GO tool task run TIMEOUT=30

# No timeout (runs until stopped manually)
$GO tool task run TIMEOUT=

# Very short run for quick tests
$GO tool task run TIMEOUT=3
```

**What `run` does:**
1. Stops any existing QEMU instances
2. Builds disk image (which builds all flock userspace programs)
3. Builds cardinal (which builds kmazarin)
4. Starts QEMU with the built kernel
5. Waits for timeout
6. Stops QEMU and displays serial output

**QEMU devices:** The run tasks launch QEMU with `virtio-gpu-pci`, `virtio-keyboard-pci`, `virtio-mouse-pci`, `virtio-rng-device`, and `virtio-blk-device` (for the disk image).

**Serial output location:** `/tmp/cardinal-serial.log`

### Stop QEMU

```bash
$GO tool task stop
```

Sends `quit` to the QEMU monitor via TCP. Safe to run even if no QEMU is running.

### View Serial Output

```bash
# View with pager
$GO tool task show

# Use a specific pager
PAGER=less $GO tool task show
```

Uses `safe-serial-read` which handles:
- Lines with millions of characters (truncates)
- Terminal control sequences (filters)
- Infinite loop detection

---

## Debug Tasks

### Debug Run Variants

```bash
# Full instruction/CPU tracing
$GO tool task run-debug

# Interrupt/exception tracing only (lightweight)
$GO tool task run-debug-int

# MMU/paging tracing
$GO tool task run-debug-mmu

# Full tracing (very verbose)
$GO tool task run-debug-full

# Custom debug options
$GO tool task run-debug DEBUG_OPTS='exec,int'
```

Debug output is written to `/tmp/qemu-debug.log`. View it with:

```bash
$GO tool task show-debug          # Last 100 lines
$GO tool task show-debug N=500    # Last 500 lines
```

### QEMU Console

Send commands to the QEMU monitor (port 4444):

```bash
# Show CPU registers (default)
$GO tool task qemu-console

# Disassemble memory
$GO tool task qemu-console CMD='x/10i 0x40100000'

# Show memory regions
$GO tool task qemu-console CMD='info mtree'

# Show interrupts
$GO tool task qemu-console CMD='info irq'

# Show CPU state
$GO tool task qemu-console CMD='info cpus'
```

**Note:** QEMU must be running. Use `$GO tool task run TIMEOUT=` to start without timeout, then use `$GO tool task qemu-console` in another terminal.

---

## Diplomat (x86_64 UEFI)

```bash
$GO tool task diplomat         # Build diplomat EFI binary
$GO tool task esp              # Create ESP disk image
$GO tool task run-diplomat     # Run diplomat in QEMU x86_64
$GO tool task stop-diplomat    # Stop diplomat QEMU instance
```

Requires additional environment variables:
- `QEMU_AMD64` - Path to `qemu-system-x86_64`
- `OVMF` - Path to OVMF firmware (`edk2-x86_64-code.fd`)

---

## Typical Workflows

### Quick Build and Test

```bash
$GO tool task run
```

Builds everything and runs for 5 seconds. Good for quick smoke tests.

### Development Cycle

```bash
# Terminal 1: Run QEMU without timeout
$GO tool task run TIMEOUT=

# Terminal 2: Query state, view output
$GO tool task show
$GO tool task qemu-console
$GO tool task qemu-console CMD='info registers'

# Terminal 2: Stop when done
$GO tool task stop
```

### Full Rebuild

```bash
$GO tool task clean
$GO tool task run TIMEOUT=10
```

### Check Environment

```bash
$GO tool task check-env
```

Verifies GO, QEMU, GOTOOLCHAIN are set correctly, versions meet requirements, and the hardcoded `KMAZARIN_LOAD_ADDR` matches the constant in source.

---

## Task Variables

Variables can be passed on the command line:

```bash
$GO tool task run TIMEOUT=30
$GO tool task qemu-console CMD='info mtree'
$GO tool task show-debug N=500
$GO tool task run-debug DEBUG_OPTS='exec,int'
DEBUG=1 $GO tool task cardinal
```

| Variable | Default | Description |
|----------|---------|-------------|
| `TIMEOUT` | `5` | Seconds before stopping QEMU in `run` tasks |
| `CMD` | `info registers` | Command to send in `qemu-console` task |
| `N` | `100` | Number of lines in `show-debug` task |
| `DEBUG_OPTS` | `exec,cpu,int,nochain` | QEMU `-d` flags for `run-debug` task |
| `DEBUG` | (unset) | Set to `1` for debug builds (-N -l flags) |

---

## All Available Tasks

Run `$GO tool task --list` to see all tasks. Key tasks:

| Task | Description |
|------|-------------|
| `all` | Build cardinal and kmazarin (default) |
| `cardinal` | Build cardinal bootloader |
| `kmazarin` | Build kmazarin kernel |
| `disk` | Create FAT32 disk image with flock binaries and kmazarin |
| `check-env` | Verify GO, QEMU, GOTOOLCHAIN, and tool versions |
| `clean` | Remove build artifacts |
| `run` | Run QEMU with cardinal/kmazarin |
| `run-debug` | Run QEMU with instruction/CPU tracing |
| `run-debug-int` | Run QEMU with interrupt tracing only |
| `run-debug-mmu` | Run QEMU with MMU tracing |
| `run-debug-full` | Run QEMU with full tracing |
| `show` | Show serial output with pager |
| `show-debug` | Show last N lines of QEMU debug log |
| `stop` | Stop all running QEMU instances |
| `qemu-console` | Send command to QEMU monitor |
| `test` | Run Go tests |
| `diplomat` | Build diplomat UEFI bootloader (x86_64) |
| `esp` | Create EFI System Partition image |
| `run-diplomat` | Run diplomat in QEMU x86_64 |
| `priestsieve` | Build priestsieve prime sieve test |
| `priest2` | Build priest2 scheduling test |
| `priest3` | Build priest3 scheduling test |
| `sievetest` | Build sievetest |
| `sieve3`..`sieve9` | Build individual sieve workers |
| `helloworld` | Build helloworld (with userspace overlay) |
| `helloworld-thin` | Build helloworld with thin client overlay |
| `helloworld-maz` | Build helloworld.maz (direct SVC, no overlay) |

---

## Troubleshooting

### "task: command not found"

Task is a Go tool, not in PATH. Use:
```bash
$GO tool task
```

### "GOTOOLCHAIN must be 'auto'"

```bash
export GOTOOLCHAIN=auto
```

### "Go >= 1.24 required"

Install Go 1.24+ or set GO to point to it:
```bash
export GO=/path/to/go1.25/bin/go
```

### "QEMU >= 10.2 required"

Install QEMU 10.2+ or set QEMU to point to it:
```bash
export QEMU=/path/to/qemu-system-aarch64
```

### "KMAZARIN_LOAD_ADDR mismatch"

The hardcoded address in `Taskfile.yml` no longer matches `cardinal/constants.KmazarinLoadAddr`. Update the `KMAZARIN_LOAD_ADDR` var in Taskfile.yml to the value shown in the error message.

### Serial log crashes tools

Never read `/tmp/cardinal-serial.log` directly. Use:
```bash
$GO tool task show
# or
$GO tool safe-serial-read
```

### QEMU won't start

Check if another instance is running:
```bash
$GO tool task stop
```

### Build seems stale

Task tracks file checksums. Force rebuild:
```bash
$GO tool task clean
$GO tool task
```
