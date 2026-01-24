# Taskfile Reference

This project uses [Task](https://taskfile.dev) for build automation. Task is installed as a Go tool and must be run via `$GO tool task`.

## Prerequisites

Set environment variables before using task:

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

**Requirements:**
- `GOTOOLCHAIN=auto` - Required
- `GO` - Go >= 1.24 (falls back to `which go` with warning)
- `QEMU` - QEMU >= 10.2 (falls back to `which qemu-system-aarch64` with warning)

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

Builds cardinal, which includes kmazarin embedded inside it.

### Build Specific Targets

```bash
$GO tool task cardinal     # Build cardinal bootloader (includes kmazarin)
$GO tool task kmazarin     # Build kmazarin kernel only
$GO tool task priest       # Build priest syscall router
$GO tool task priest2      # Build priest2 scheduling test
$GO tool task helloworld   # Build helloworld test program
$GO tool task disk         # Create FAT32 disk image with flock binaries
```

### Clean Build

```bash
$GO tool task clean        # Remove build/ directory and generated files
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
2. Builds disk image (which builds priest, priest2, helloworld)
3. Builds cardinal (which builds kmazarin)
4. Starts QEMU with the built kernel
5. Waits for timeout
6. Kills QEMU and displays last 50 lines of serial output

**Serial output location:** `/tmp/cardinal-serial.log`

### Stop QEMU

```bash
$GO tool task stop
```

Kills all `qemu-system-aarch64` processes. Safe to run even if no QEMU is running.

### View Serial Output

```bash
# View with pager ($PAGER or 'more')
$GO tool task show

# Use a specific pager
PAGER=less $GO tool task show
PAGER='less -R' $GO tool task show
```

Uses `safe-serial-read` which handles:
- Lines with millions of characters (truncates)
- Terminal control sequences (filters)
- Infinite loop detection

---

## Debug Tasks

### QEMU Console

Send commands to the QEMU monitor (port 4444):

```bash
# Show CPU registers (default)
$GO tool task qemu-console

# Disassemble memory
$GO tool task qemu-console CMD='x/10i 0x40100000'
$GO tool task qemu-console CMD='x/20i 0x43800000'

# Show memory regions
$GO tool task qemu-console CMD='info mtree'

# Show interrupts
$GO tool task qemu-console CMD='info irq'

# Show CPU state
$GO tool task qemu-console CMD='info cpus'

# Show loaded device tree
$GO tool task qemu-console CMD='info fdt'
```

**Note:** QEMU must be running. Use `task run TIMEOUT=` to start without timeout, then use `task qemu-console` in another terminal.

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

### Build Only (No Run)

```bash
$GO tool task cardinal     # Build kernel
$GO tool task disk         # Build disk image
```

### Check Environment

```bash
$GO tool task check-env
```

Verifies GO, QEMU, GOTOOLCHAIN are set correctly and versions meet requirements.

---

## All Available Tasks

Run `$GO tool task --list` to see all tasks:

| Task | Description |
|------|-------------|
| `all` | Build cardinal and kmazarin (default) |
| `cardinal` | Build cardinal bootloader |
| `check-env` | Verify GO, QEMU, GOTOOLCHAIN, and tool versions |
| `clean` | Remove build artifacts |
| `disk` | Create FAT32 disk image with flock binaries |
| `embed-boot-image` | Generate assembly with embedded boot image |
| `embed-kmazarin` | Generate assembly with embedded kmazarin binary |
| `helloworld` | Build helloworld test program |
| `helloworld-maz` | Build helloworld.maz (direct SVC, no overlay) |
| `helloworld-thin` | Build helloworld with thin client overlay |
| `kmazarin` | Build kmazarin kernel |
| `kmazarin-overlay` | Generate runtime overlay JSON for kmazarin |
| `priest` | Build priest syscall router |
| `priest2` | Build priest2 scheduling test program |
| `qemu-console` | Send command to QEMU monitor |
| `run` | Run QEMU with cardinal/kmazarin |
| `show` | Show serial output using safe-serial-read |
| `stop` | Stop all running QEMU instances |
| `test` | Run Go tests |
| `thin-overlay` | Generate thin client overlay via AST stub generator |
| `userspace-overlay` | Generate runtime overlay JSON for userspace programs |

---

## Task Variables

Variables can be passed on the command line:

```bash
$GO tool task run TIMEOUT=30
$GO tool task qemu-console CMD='info mtree'
PAGER=less $GO tool task show
DEBUG=1 $GO tool task cardinal
```

| Variable | Default | Description |
|----------|---------|-------------|
| `TIMEOUT` | `5` | Seconds before killing QEMU in `run` task |
| `CMD` | `info registers` | Command to send in `qemu-console` task |
| `DEBUG` | `0` | Set to `1` for debug builds (-N -l flags) |
| `PAGER` | `more` | Pager for `show` task |

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
pgrep -f qemu-system-aarch64
```

### Build seems stale

Task tracks file checksums. Force rebuild:
```bash
$GO tool task clean
$GO tool task
```
