# Continuation: ARM64 TCG — Current Status and Next Steps

## Context

This is a continuation prompt for the Mazzy OS project. The system is a
microkernel (kmazarin) written in Go with a UEFI bootloader (diplomat). It
runs on ARM64, RISC-V, and x86_64 under QEMU.

## What's Working on ARM64 TCG Right Now

The ARM64 TCG boot chain is functional through the delegate receive loop:

1. **Diplomat UEFI** boots, loads kmazarin ELF, jumps to kernel
2. **Kmazarin kernel** initializes: page tables, exception vectors, GIC, timer
3. **VirtIO devices** all discovered and initialized:
   - GPU: 1920x1080 framebuffer at PA 0xB7000000
   - Block: MSI-X via GICv2m (edge-triggered), interrupt-driven I/O
   - Keyboard/Mouse: soft IRQ event delivery
   - RNG: entropy source
4. **TOML config parsed** by kernel from FAT32 data disk (`/kmazarin.toml`)
5. **Disk priest** launched (the only `[[bootstrap_priest]]`)
6. **fs.maz** loaded by disk priest via SysLoadMaz, MazarinPriest injection works
7. **FAT32 mounted** by fs.maz using injected BlockDevice
8. **LoadFile delegate** registered (SysID 40), `SetReady(true)` called
9. **Delegate receive loop** entered — fs.maz is ready to serve requests

Serial output confirms clean boot (last ARM64 run):
```
[boot] config: 1 bootstrap, 2 priests, tz=America/New_York
[Launch] /disk.elf
[boot] disk launched
[boot] 2 application priests deferred to fs.maz
...
[fs] FAT32 mounted successfully
[fs] no /kmazarin.toml found       ← THE PROBLEM
[DLG] P28 handles SysID 40
[fs] registered as LoadFile delegate
[SetReady] P28 ready=1
[fs] entering delegate receive loop
```

## What's Broken: fs.maz Can't Find /kmazarin.toml

The kernel successfully reads `/kmazarin.toml` from the FAT32 data disk
(output: `[boot] config: 1 bootstrap, 2 priests`). But when fs.maz tries
to read the same file from the same disk, it fails with "no /kmazarin.toml
found".

**Impact:** Without the TOML config, fs.maz never queues priest launch work
items. The 2 application priests (dapope, stdio) declared in `[[priest]]`
sections are never launched. The system sits in the delegate receive loop
with no clients to serve.

**Both the kernel and fs.maz use the same code path:**
- Same FAT32 library: `shared/fs/fat32`
- Same block device: VirtIO PCI block (via SyscallBlockRead for fs.maz)
- Same file: `/kmazarin.toml` (confirmed present in mkfat32 output)

**Key difference:** The kernel reads via direct block device driver calls
(kernel space). fs.maz reads via the injected BlockDevice interface, which
calls SyscallBlockRead, which goes through the kernel's block driver.
The mount succeeds (BPB, FAT table, root directory all read correctly),
but the file search for "kmazarin.toml" fails. This suggests either:
- A VFAT long filename (LFN) entry issue — "kmazarin.toml" is 14 chars,
  too long for 8.3 format, requires LFN support
- A directory traversal issue — file exists but is past where the search stops
- A read-after-read issue — kernel's prior FAT32 reads leave some state that
  affects subsequent reads through SyscallBlockRead

**Same issue exists on RISC-V** — the RISC-V serial output also shows fs.maz
entering the delegate loop without finding kmazarin.toml or launching priests.
This is NOT an ARM64-specific bug.

## The Two-Phase Boot Architecture

The boot has two phases:

### Phase 1: Kernel-Driven (works)
```
kernel readBootConfig()
  → device.GetBlockDevice() (VirtIO PCI block)
  → fat32.Mount(blk)
  → fs.Open("/kmazarin.toml")  ← succeeds
  → toml.Parse(data)
  → launch only [[bootstrap_priest]] entries (just "disk")
  → print "[boot] 2 application priests deferred to fs.maz"
```

### Phase 2: fs.maz-Driven (broken at TOML read)
```
fs.maz MazarinMain()
  → fat32.Mount(injectedBlockDev)  ← succeeds
  → readBootConfig(fs)
    → fs.Open("/kmazarin.toml")  ← FAILS
  → (if cfg != nil) queue priest launches for dapope, stdio
  → register as LoadFile delegate
  → SetReady(true)
  → enter delegate receive loop
```

When readBootConfig returns nil, the priest launch queue is empty.
No dapope, no stdio, no helloworld.maz loading.

## Key Files

**Boot sequence:**
- `kmazarin/kmazarin/main.go:904-921` — readBootConfig + launchPriestsFromConfig
- `kmazarin/kmazarin/main.go:972-1033` — readBootConfig, launchPriestsFromConfig, launchPriest

**fs.maz:**
- `flock/cmd/fat32/main.go` — MazarinMain (mount, readBootConfig, register delegate, receive loop)
- `flock/cmd/fat32/main.go:260-279` — readBootConfig (the failing path)
- `flock/cmd/fat32/main.go:156-164` — fileWorker (processes LoadFile + priest launches)
- `flock/cmd/fat32/main.go:196-226` — handleLaunchPriest (reads ELF, calls RunPriest)

**Block I/O:**
- `kmazarin/ksyscall/blockio.go` — SyscallBlockRead, blockReadInterrupt (WFI loop)
- `kmazarin/device/virtio/block/block.go` — VirtIO block driver, DoBlockIOSubmit/Complete

**FAT32 library:**
- `shared/fs/fat32/` — FAT32 implementation used by both kernel and fs.maz

**Delegation infrastructure:**
- `kmazarin/ksyscall/delegate.go` — kernel-side delegation (register, queue, forward, reply)
- `mazarin/sys/delegate.go` — userspace client (HandleSyscalls, SyscallRequest, Reply)
- `kmazarin/kmazarin/ipc_bridge.go` — BlockForDelegatedSyscall/Recv, WakeDelegateThread

**Disk priest:**
- `flock/cmd/disk/main.go` — registers block device IRQ, loads fs.maz, injects BlockDevice

**Application priests:**
- `flock/cmd/dapope/main.go` — keyboard/mouse handler, cursor, loads helloworld.maz
- `flock/cmd/stdio/main.go` — UART soft IRQ, intercepts Write() syscalls, GPU framebuffer console

**Config:**
- `config/kmazarin-arm64.toml` — 1 bootstrap (disk), 2 priests (dapope, stdio)

## Block I/O Architecture (Recently Fixed)

Block I/O uses interrupt-driven synchronous WFI within the SVC handler:

```go
// kmazarin/ksyscall/blockio.go
func blockReadInterrupt(dev *block.VirtIOBlockDevice, lba uint64, buf []byte) error {
    reqDescIdx, err := dev.DoBlockIOSubmit(block.VIRTIO_BLK_T_IN, lba, buf)
    if err != nil { return err }
    for atomic.LoadUint32(&dev.IOComplete) == 0 {
        enableIRQsAndWait()  // DAIFClr + WFI + DAIFSet
    }
    return dev.DoBlockIOComplete(block.VIRTIO_BLK_T_IN, reqDescIdx, buf)
}
```

- `enableIRQsAndWait` enables IRQs, halts CPU (WFI), re-disables IRQs
- `svcDepth=1` prevents timer preemption during SVC handler
- IRQ handler (NonTimerIRQTopHalf) sets `IOComplete=1`, WFI wakes
- NOT polling — CPU is truly idle until interrupt
- Verified working on ARM64 TCG (MSI-X) and RISC-V (INTx/PLIC)

Dead code from the old broken approach (BlockForBlockIO, WakeBlockIOThread,
PollBlockIOCompletion, blockIOBlockedTID) was cleaned up in this session.

## What "Fully Working" Means

RISC-V was previously described as "fully working" but that predates the
TOML-driven boot. The current state on ALL architectures is:

- Kernel boots ✓
- Disk priest launches ✓
- fs.maz loads and mounts FAT32 ✓
- Delegate receive loop entered ✓
- Application priests (dapope, stdio) launch ✗ (blocked by TOML read failure)

"Fully working" requires:
1. fs.maz finds and parses `/kmazarin.toml`
2. fs.maz launches dapope and stdio via RunPriest syscall
3. dapope enters its keyboard/mouse event loop
4. stdio enters its Write() delegation + framebuffer rendering loop
5. helloworld.maz loads and runs (dapope calls LoadFile → fs.maz serves it)

## User Rules (Non-Negotiable)

- **No polling or timeouts** — these are architectural changes requiring discussion
- **No architectural changes without discussion** — talk through design first
- **Keep architecture similar across ARM64, RISC-V, x86_64**
- **NEVER disable async preemption or GC** — always GODEBUG=gctrace=1
- **Go 1.25.5 required** — runtime patches NOT compatible with 1.26
- **Serial log safety** — ALWAYS use `$GO tool safe-serial-read`, never read raw

## Suggested Next Step

Debug why `fs.Open("/kmazarin.toml")` fails in fs.maz when the kernel's
identical call succeeds. This is likely a shared/fs/fat32 issue with VFAT
long filename entries, or a subtle block read issue when going through the
SyscallBlockRead path vs direct kernel block device access. Adding directory
listing output after the FAT32 mount in fs.maz would quickly narrow it down.
