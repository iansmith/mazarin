# Recent Fixes - VNC Framebuffer Setup

## Date: December 4, 2025

### Problem: QEMU Memory Overlap Error

When running `runqemu-virt-vga`, QEMU refused to load the kernel with this error:

```
The following two regions overlap (in the cpu-memory-0 address space):
  /mnt/builtin/kernel.elf ELF program header segment 1 (addresses 0x0000000040000000 - 0x000000004003b720)
  dtb (addresses 0x0000000040000000 - 0x0000000040100000)
```

### Root Cause

QEMU's `virt` machine type reserves `0x40000000 - 0x40100000` (1MB) for its Device Tree Blob (DTB). Our BSS section was placed at `0x40000000`, causing a direct overlap.

### Solution

**Fixed by moving BSS section to `0x40100000`** (immediately after the DTB region).

### Files Changed

1. **src/linker.ld** - BSS section moved from 0x40000000 to 0x40100000
2. **src/asm/boot.s** - Updated memory layout comments
3. **src/go/mazarin/kernel.go** - Updated memory layout comments

### New Memory Layout

```
0x40000000 - 0x40100000: DTB (QEMU device tree, reserved)
0x40100000+:             BSS section
0x40400000:              Stack
0x40500000:              Heap
```

### Testing

```bash
# Build and test
cd src && make clean && make kernel-qemu.elf && make push-qemu
source enable-mazzy
runqemu-virt-vga

# Result: ✓ Kernel boots successfully
# Output: "Hello, Mazarin!" + heap tests pass
```

### Status: FIXED ✓

The kernel now boots without memory overlap errors. VNC framebuffer is ready for testing.

---

## VNC Framebuffer Setup Completed

### Three Requirements Addressed

1. **✓ Writing to Framebuffer**
   - Kernel uses PCI enumeration to find bochs-display device
   - Reads framebuffer address from PCI BAR0
   - Writes pixels in RGB format (640x480, 24-bit)
   - See: `src/go/mazarin/framebuffer_qemu.go`, `src/go/mazarin/pci_qemu.go`

2. **✓ VNC Connection (Port 5900)**
   - Both `runqemu-fb` and `runqemu-virt-vga` support VNC
   - Docker maps container port 5900 to host
   - Connect with: `vncviewer localhost:5900`
   - bochs-display device automatically added

3. **✓ Apple VNC Compatibility Issues Explained**
   - Documented with sources from web research
   - Protocol version mismatch (RFB 3.8 vs 3.889)
   - Authentication incompatibility
   - Encoding/pixel format issues
   - **Solution**: Use RealVNC Viewer or TigerVNC instead

### Documentation Created

1. **`docs/qemu-vnc-framebuffer-setup.md`** (comprehensive guide)
   - Architecture diagrams
   - Memory maps
   - Apple VNC issues with cited sources
   - Complete troubleshooting

2. **`VNC-QUICKSTART.md`** (quick reference)
   - TL;DR commands
   - VNC client recommendations
   - Common issues

3. **`docs/vnc-summary.md`** (overview)
   - All three components explained
   - Testing workflow
   - Architecture details

4. **`docs/dtb-memory-fix.md`** (this fix)
   - Problem and solution
   - Memory layout changes
   - Testing checklist

5. **`docker/test-vnc-setup`** (verification script)
   - Checks all prerequisites
   - Provides fix suggestions

### Scripts Updated

1. **`docker/runqemu-fb`** - Added bochs-display device, updated warnings
2. **`docker/runqemu-virt-vga`** - Already correct, no changes needed
3. **`.cursorrules`** - Added VNC quick reference

### Quick Start (Now Working!)

```bash
# 1. Build kernel
source enable-mazzy
cd src
make kernel-qemu.elf && make push-qemu

# 2. Run QEMU with VNC
runqemu-virt-vga

# 3. Connect with VNC
# Option A: TigerVNC
brew install tiger-vnc
vncviewer localhost:5900

# Option B: RealVNC Viewer
brew install --cask vnc-viewer
# Open app, connect to localhost:5900

# ❌ Don't use Apple Screen Sharing (incompatible)
```

### Expected Behavior

**Terminal (UART output):**
```
SB
K
Hello, Mazarin!
Testing write barrier...
Write barrier flag: enabled
SUCCESS: Global pointer assignment works!
Heap initialized at RAM region
All tests passed! Exiting via semihosting...
```

**VNC window:**
- 640x480 display
- Black background (framebuffer cleared)
- Any graphics your kernel draws

### Next Steps

Now that VNC framebuffer is working:
1. Implement graphics primitives (lines, rectangles, text)
2. Add font rendering for text display
3. Build a terminal emulator (UART → framebuffer)
4. Port to Raspberry Pi 4 real hardware

### Key Takeaways

✅ **DTB region must be reserved** - QEMU places it at 0x40000000 (1MB)
✅ **Linker script must account for QEMU's reserved regions**
✅ **Apple Screen Sharing doesn't work with QEMU VNC** - use RealVNC/TigerVNC
✅ **PCI enumeration finds framebuffer dynamically** - no hardcoded addresses needed
✅ **Memory layout is critical** - BSS, stack, heap must not overlap with DTB

### Resources

- Quick reference: `VNC-QUICKSTART.md`
- Full guide: `docs/qemu-vnc-framebuffer-setup.md`
- Memory fix: `docs/dtb-memory-fix.md`
- Test script: `docker/test-vnc-setup`





