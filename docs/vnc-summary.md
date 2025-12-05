# VNC Framebuffer - Summary

Complete summary of VNC framebuffer setup for the Mazarin kernel running in QEMU.

## What We Accomplished

✅ **Framebuffer Writing** - Kernel writes to PCI bochs-display device framebuffer
✅ **VNC Connection** - QEMU VNC server streams framebuffer to host
✅ **Documentation** - Comprehensive guides and troubleshooting
✅ **Apple VNC Issues** - Explained why Apple Screen Sharing doesn't work (with sources)

## Three Key Components

### 1. Writing to the Framebuffer

**Location:** Kernel writes to PCI device BAR0 (Base Address Register 0)

**How it works:**
```
Kernel starts
    ↓
framebufferInit() called (src/go/mazarin/framebuffer_qemu.go)
    ↓
findBochsDisplay() scans PCI bus (src/go/mazarin/pci_qemu.go)
    ↓
Finds bochs-display device (vendor 0x1234, device 0x1111)
    ↓
Reads BAR0 from PCI config space (typically 0x40000000+)
    ↓
Maps framebuffer to BAR0 address
    ↓
Writes pixels: RGB, 24-bit, 640x480
    ↓
QEMU bochs-display device receives writes
    ↓
Rendered to virtual display
```

**Key files:**
- `src/go/mazarin/framebuffer_qemu.go` - QEMU-specific framebuffer init
- `src/go/mazarin/pci_qemu.go` - PCI enumeration to find device
- `src/go/mazarin/framebuffer_common.go` - Shared framebuffer structures

**Memory addresses:**
- **PCI ECAM base:** 0x30000000 (PCI config space)
- **Framebuffer:** 0x40000000+ (read from BAR0, varies)
- **Resolution:** 640x480
- **Pixel format:** RGB, 3 bytes per pixel (24-bit)

### 2. VNC Connection to Container

**Port:** 5900 (VNC display :0)

**How it works:**
```
Docker container runs QEMU
    ↓
QEMU starts with: -device bochs-display -vnc ":0"
    ↓
bochs-display device provides framebuffer
    ↓
QEMU VNC server encodes framebuffer as RFB protocol
    ↓
Listens on container port 5900
    ↓
Docker maps port: -p 5900:5900
    ↓
Host machine can connect to localhost:5900
    ↓
VNC client decodes RFB protocol
    ↓
Displays framebuffer on screen
```

**Key scripts:**
- `docker/runqemu-virt-vga` - Runs QEMU with bochs-display and VNC
- `docker/runqemu-fb` - Same, now includes bochs-display
- `docker/test-vnc-setup` - Tests that setup is correct

**QEMU command:**
```bash
qemu-system-aarch64 \
    -M virt \                    # Generic AArch64 virtual machine
    -cpu cortex-a72 \            # Same CPU as Raspberry Pi 4
    -m 512M \                    # 512MB RAM
    -kernel kernel.elf \         # Your kernel
    -device bochs-display \      # Add display device
    -vnc ":0" \                  # VNC server on display 0 (port 5900)
    -serial stdio \              # UART to console
    -semihosting                 # Clean exit
```

### 3. Apple VNC Viewer Doesn't Work

**Problem:** Apple's built-in Screen Sharing has protocol incompatibilities with QEMU VNC

**Technical reasons:**

| Issue | Apple Screen Sharing | QEMU VNC Server | Result |
|-------|---------------------|-----------------|--------|
| **Protocol Version** | Expects RFB 3.889 or Apple extensions | Implements RFB 3.8 (standard) | Handshake failure |
| **Authentication** | Prefers Apple Remote Desktop (ARD) | Supports None, VNC Auth, SASL, TLS | Auth negotiation fails |
| **Encodings** | Optimized for ZRLE + Apple extensions | Uses Raw, Hextile, Tight | Encoding mismatch |
| **Pixel Format** | May expect BGR or Apple formats | Standard RGB formats | Blank/corrupted display |

**Sources:**
1. [QEMU Bug Report #925405](https://bugs.launchpad.net/bugs/925405) - Protocol version issues
2. [LogCG - macOS VNC Connection Issues](https://www.logcg.com/en/archives/3807.html) - Authentication problems
3. [Apple StackExchange](https://apple.stackexchange.com/questions/310625/screen-sharing-app-can-t-connect-to-vnc) - User reports

**Symptoms:**
- ❌ "Connection refused" or timeout
- ❌ Connection succeeds but blank/black screen
- ❌ "Software appears incompatible" error
- ❌ Immediate disconnect after connecting
- ❌ Corrupted/wrong colors

**Solution:** Use compatible VNC clients:

```bash
# RealVNC Viewer (recommended)
brew install --cask vnc-viewer
# Then: Open app, connect to localhost:5900

# TigerVNC (open source)
brew install tiger-vnc
vncviewer localhost:5900

# TightVNC (alternative)
brew install tightvnc
vncviewer localhost:5900
```

## Quick Start Guide

### Complete Workflow

```bash
# 1. Setup environment
cd /Users/iansmith/mazzy
source enable-mazzy

# 2. Build kernel for QEMU
cd src
make kernel-qemu.elf

# 3. Copy to Docker
make push-qemu

# 4. Test setup (optional but recommended)
test-vnc-setup

# 5. Start QEMU with VNC
runqemu-virt-vga

# 6. In another terminal/app, connect with VNC client
# Option A: TigerVNC
vncviewer localhost:5900

# Option B: RealVNC Viewer
open -a "VNC Viewer"  # Enter: localhost:5900

# 7. You should see:
#    - Terminal: UART output with PCI enumeration messages
#    - VNC window: 640x480 display with framebuffer contents
```

### Expected Output

**Terminal (UART):**
```
Hello, Mazarin!
framebufferInit (QEMU): Initializing framebuffer
findBochsDisplay: Scanning PCI bus...
findBochsDisplay: Found bochs-display device
  Bus: 0, Slot: 1, Func: 0
  BAR0: 0x10000000
  Framebuffer address: 0x0000000010000000
framebufferInit (QEMU): Framebuffer mapped successfully
  Address: 0x0000000010000000
  Size: 921600 bytes
  Dimensions: 640x480
  Note: Writing directly to QEMU's framebuffer memory
```

**VNC Window:**
- 640x480 pixel window
- Black background (framebuffer cleared to 0)
- Any graphics/text your kernel draws

## Troubleshooting

### Quick Checks

```bash
# Check all setup requirements
test-vnc-setup

# Check if QEMU is running
docker ps | grep alpine-qemu

# Check port mapping
docker ps --format "table {{.Names}}\t{{.Ports}}" | grep mazarin
# Should show: 0.0.0.0:5900->5900/tcp

# Check if port is listening
lsof -i :5900
# Should show: Docker process

# Check for UART output
# Look for PCI enumeration and framebuffer init messages
```

### Common Issues

**1. "Connection refused"**
- QEMU container not running → Check `docker ps`
- Port not mapped → Check docker run command has `-p 5900:5900`
- Firewall blocking → Try `VNC_PORT=5901 runqemu-virt-vga`

**2. "Blank screen"**
- Using Apple Screen Sharing → Use RealVNC/TigerVNC instead
- Framebuffer not initialized → Check UART output
- No bochs-display device → Verify `-device bochs-display` in QEMU command

**3. "Port already in use"**
- Old container running → `docker kill $(docker ps -q --filter ancestor=alpine-qemu:3.22)`
- Another service on 5900 → `lsof -i :5900`, then `VNC_PORT=5901 runqemu-virt-vga`

**4. "PCI enumeration fails"**
- UART shows: "findBochsDisplay: bochs-display device not found"
- Falls back to 0x40000000 (usually works)
- If display still blank → Check bochs-display is added to QEMU

## Architecture Overview

### QEMU virt Machine Memory Map

```
0x00000000 ┌─────────────────────────────────────┐
           │ ROM (128MB)                         │
           │  - Read-only                        │
0x00200000 │  ← Kernel loaded here               │
           │                                     │
0x08000000 ├─────────────────────────────────────┤
0x09000000 │ UART (PL011)                        │
           ├─────────────────────────────────────┤
           │ (gaps)                              │
0x30000000 ├─────────────────────────────────────┤
           │ PCI ECAM (config space)             │
           │  - PCI enumeration reads from here  │
           ├─────────────────────────────────────┤
0x40000000 │ PCI MMIO (256MB)                    │
           │  - Display device framebuffer       │
           │  - Other PCI device memory          │
           ├─────────────────────────────────────┤
0x40000000 │ RAM (writable)                      │
           │  - BSS section                      │
0x40400000 │  ← Stack                            │
0x40500000 │  ← Heap                             │
           │  ... (rest of RAM)                  │
           └─────────────────────────────────────┘
```

**Key points:**
- ROM and RAM overlap at 0x40000000 (ROM is read-only, RAM is writable)
- PCI MMIO includes display framebuffer (address varies, read from BAR0)
- Kernel BSS/stack/heap in RAM region (0x40000000+)
- PCI config space at 0x30000000 for device enumeration

### PCI Configuration Space

```
ECAM Base: 0x30000000

Address calculation:
config_addr = base + (bus << 20) + (slot << 15) + (func << 12) + offset

For bochs-display (typically bus 0, slot 1, func 0):
config_addr = 0x30000000 + (0 << 20) + (1 << 15) + (0 << 12) + offset
            = 0x30008000 + offset

Offsets:
  0x00: Vendor ID (0x1234 for bochs-display)
  0x02: Device ID (0x1111 for bochs-display)
  0x10: BAR0 (framebuffer base address)
```

## Documentation Files

Comprehensive guides created:

1. **`docs/qemu-vnc-framebuffer-setup.md`** - Complete guide
   - How VNC works with QEMU (architecture diagrams)
   - Framebuffer memory location (with memory map)
   - VNC setup (scripts, commands, workflow)
   - Apple VNC compatibility issues (with sources and technical details)
   - Testing and troubleshooting (step-by-step)
   - Advanced topics (multiple instances, passwords, etc.)

2. **`VNC-QUICKSTART.md`** - Quick reference
   - TL;DR commands
   - Why Apple Screen Sharing doesn't work
   - Framebuffer location summary
   - Common troubleshooting

3. **`docs/vnc-summary.md`** - This file
   - Overview of all three components
   - Quick start workflow
   - Architecture overview

4. **`.cursorrules`** - Updated with VNC info
   - Quick reference for AI assistant
   - Points to full documentation

5. **`docker/test-vnc-setup`** - Setup verification script
   - Checks environment, Docker, kernel, port, VNC client
   - Provides fix suggestions for each issue

## Scripts Updated

1. **`docker/runqemu-fb`**
   - Added `-device bochs-display`
   - Updated comments to warn about Apple Screen Sharing
   - Points to documentation

2. **`docker/runqemu-virt-vga`**
   - Already had bochs-display
   - No changes needed (was already correct)

3. **`docker/test-vnc-setup`** (new)
   - Automated setup verification
   - Checks all prerequisites
   - Provides actionable fix instructions

## Testing

To verify everything works:

```bash
# 1. Run verification script
source enable-mazzy
test-vnc-setup

# Expected: All checks pass ✓

# 2. Build and run
cd src
make kernel-qemu.elf && make push-qemu
runqemu-virt-vga

# Expected: UART output shows PCI enumeration and framebuffer init

# 3. Connect with VNC
vncviewer localhost:5900

# Expected: 640x480 window appears with framebuffer
```

## Next Steps

Now that VNC framebuffer is working, you can:

1. **Implement graphics primitives**
   - Lines, rectangles, circles
   - Color support (RGB values)
   - Pixel-level drawing

2. **Add font rendering**
   - Load bitmap fonts
   - Draw characters to framebuffer
   - Text console on display

3. **Build a terminal emulator**
   - Combine UART input with framebuffer output
   - Full-screen text editing
   - Color text support

4. **Port to Raspberry Pi 4**
   - Different display device (VC4)
   - Mailbox interface instead of PCI
   - Real hardware testing

## Key Takeaways

✅ **Framebuffer location:** Found via PCI enumeration (BAR0 of bochs-display device)

✅ **VNC connection:** Port 5900, use RealVNC Viewer or TigerVNC

❌ **Apple Screen Sharing:** Known incompatibilities with QEMU VNC - don't use it

📚 **Documentation:** Complete guides in docs/ with sources and troubleshooting

🔧 **Scripts:** `runqemu-virt-vga` and `runqemu-fb` both support VNC with bochs-display

✅ **Testing:** `test-vnc-setup` verifies all prerequisites

## Resources

**Internal Documentation:**
- `docs/qemu-vnc-framebuffer-setup.md` - Complete guide
- `VNC-QUICKSTART.md` - Quick reference
- `.cursorrules` - Project rules (includes VNC info)

**External Sources:**
- [QEMU VNC Server Documentation](https://www.qemu.org/docs/master/system/vnc-security.html)
- [RFB Protocol Specification](https://github.com/rfbproto/rfbproto/blob/master/rfbproto.rst)
- [QEMU Bug #925405](https://bugs.launchpad.net/bugs/925405) - macOS compatibility
- [PCI Configuration Space](https://wiki.osdev.org/PCI) - OS Dev Wiki

**Tools:**
- [RealVNC Viewer](https://www.realvnc.com/download/viewer/) - Recommended VNC client
- [TigerVNC](https://tigervnc.org/) - Open source VNC client
- [QEMU](https://www.qemu.org/) - Machine emulator





