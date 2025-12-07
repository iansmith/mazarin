# QEMU VNC Framebuffer Setup Guide

This guide explains how to set up and use VNC to view the framebuffer output from QEMU running in a Docker container.

## Table of Contents

1. [Overview](#overview)
2. [How It Works](#how-it-works)
3. [Framebuffer Memory Location](#framebuffer-memory-location)
4. [VNC Setup](#vnc-setup)
5. [Apple VNC Viewer Compatibility Issues](#apple-vnc-viewer-compatibility-issues)
6. [Testing the Setup](#testing-the-setup)
7. [Troubleshooting](#troubleshooting)

## Overview

When QEMU runs inside a Docker container, it cannot directly access your display. We solve this by:

1. **QEMU VNC Server**: QEMU runs a built-in VNC server that streams the framebuffer
2. **Docker Port Mapping**: Docker maps the VNC port from container to host
3. **VNC Client**: You connect to `localhost:5900` with a VNC client to see the display

## How It Works

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Docker Container (alpine-qemu:3.22)                         │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ QEMU System (qemu-system-aarch64)                  │    │
│  │                                                     │    │
│  │  ┌──────────────────────────────────────────────┐ │    │
│  │  │ Your Kernel                                   │ │    │
│  │  │                                               │ │    │
│  │  │  Writes to framebuffer → PCI device BAR0     │ │    │
│  │  │                           (e.g. 0x40000000)   │ │    │
│  │  └──────────────────────────────────────────────┘ │    │
│  │                          ↓                          │    │
│  │  ┌──────────────────────────────────────────────┐ │    │
│  │  │ Display Device (bochs-display)               │ │    │
│  │  │  - Receives framebuffer writes               │ │    │
│  │  │  - Renders to virtual display                │ │    │
│  │  └──────────────────────────────────────────────┘ │    │
│  │                          ↓                          │    │
│  │  ┌──────────────────────────────────────────────┐ │    │
│  │  │ VNC Server (built into QEMU)                 │ │    │
│  │  │  - Encodes display as VNC protocol           │ │    │
│  │  │  - Listens on port 5900                      │ │    │
│  │  └──────────────────────────────────────────────┘ │    │
│  └────────────────────────────────────────────────────┘    │
│                          ↓                                  │
└──────────────────────────│───────────────────────────────────┘
                           │ Docker port mapping (-p 5900:5900)
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ Host Machine (macOS)                                         │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ VNC Client (RealVNC, TigerVNC, etc.)               │    │
│  │  - Connects to localhost:5900                      │    │
│  │  - Decodes VNC protocol                            │    │
│  │  - Displays framebuffer on screen                  │    │
│  └────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

### Key Components

1. **bochs-display Device**: A simple PCI display device that QEMU emulates
   - Vendor ID: 0x1234
   - Device ID: 0x1111
   - Provides a framebuffer via BAR0 (Base Address Register 0)
   - Simpler than full VGA emulation

2. **PCI Enumeration**: The kernel scans the PCI bus to find the display device
   - Reads PCI configuration space at 0x30000000 (ECAM base on virt machine)
   - Finds bochs-display by matching vendor/device IDs
   - Reads BAR0 to get framebuffer address

3. **Framebuffer Writing**: The kernel writes pixels to the framebuffer
   - Each pixel is 3 bytes (RGB, 24-bit color)
   - Resolution: 640x480 (default)
   - Linear framebuffer (no hardware acceleration needed)

4. **VNC Server**: QEMU's built-in VNC server (enabled with `-vnc` flag)
   - Captures the display device output
   - Encodes as VNC protocol (RFB - Remote Framebuffer Protocol)
   - Streams to connected clients

## Framebuffer Memory Location

### QEMU virt Machine Memory Map

```
0x00000000 - 0x08000000   ROM (128MB)
  └─ 0x00200000           Kernel loaded here
0x09000000                UART (PL011)
0x30000000                PCI ECAM (config space)
0x40000000 - 0x50000000   PCI MMIO (256MB)
  └─ 0x40000000+          Display device framebuffer (BAR0)
0x40000000 - end          RAM
  └─ 0x40400000           Stack
  └─ 0x40500000           Heap
```

### Finding the Framebuffer Address

The kernel uses PCI enumeration to find the bochs-display device:

```go
// src/go/mazarin/pci_qemu.go
func findBochsDisplay() uintptr {
    // Scan PCI bus 0, slots 0-31
    for bus := 0; bus < 1; bus++ {
        for slot := 0; slot < 32; slot++ {
            // Read vendor/device ID from PCI config space
            vendorID := pciConfigRead32(bus, slot, 0, PCI_VENDOR_ID)
            deviceID := pciConfigRead32(bus, slot, 0, PCI_DEVICE_ID)
            
            // Match bochs-display (vendor=0x1234, device=0x1111)
            if vendorID == 0x1234 && deviceID == 0x1111 {
                // Read BAR0 to get framebuffer address
                bar0 := pciConfigRead32(bus, slot, 0, PCI_BAR0)
                return uintptr(bar0 & 0xFFFFFFF0) // Mask out flag bits
            }
        }
    }
    return 0 // Not found - use fallback address
}
```

**Why PCI enumeration?**
- The framebuffer address is not fixed - it's assigned by QEMU
- Different devices or QEMU versions may use different addresses
- PCI enumeration is the standard way to find device resources
- Falls back to 0x40000000 if enumeration fails

## VNC Setup

### Using the runqemu-virt-vga Script (Recommended)

This script is specifically designed for VNC framebuffer display with proper device configuration:

```bash
# 1. Build the kernel for QEMU
cd src
make kernel-qemu.elf

# 2. Copy to Docker builtin directory
make push-qemu

# 3. Source the environment (adds scripts to PATH)
source /Users/iansmith/mazzy/enable-mazzy

# 4. Run QEMU with VNC (default port 5900)
runqemu-virt-vga

# 5. Connect with VNC client
# See "VNC Clients" section below
```

### Custom VNC Port

```bash
# Use a different port (e.g., 5901 for display :1)
VNC_PORT=5901 runqemu-virt-vga

# Connect to vnc://localhost:5901
```

### What the Script Does

```bash
# Key QEMU arguments:
qemu-system-aarch64 \
    -M virt \                      # Generic AArch64 virtual machine
    -cpu cortex-a72 \              # Same CPU as Raspberry Pi 4
    -m 512M \                      # 512MB RAM
    -device bochs-display \        # Add display device
    -vnc ":0" \                    # VNC server on display :0 (port 5900)
    -serial mon:stdio \            # UART output to console
    -semihosting \                 # Enable semihosting for clean exit
    -no-reboot                     # Exit instead of reboot
```

**Key points:**
- `-device bochs-display`: Adds the display device that the kernel writes to
- `-vnc ":0"`: Starts VNC server on display 0 (port 5900)
- `-p 5900:5900`: Docker maps container port 5900 to host port 5900

## Apple VNC Viewer Compatibility Issues

### Why Apple's Built-in Screen Sharing Doesn't Work

Apple's built-in VNC viewer (Screen Sharing.app, also accessible via Finder's "Connect to Server") has **known compatibility issues** with QEMU's VNC server. This is a documented problem, not a bug in your setup.

#### Technical Reasons

1. **VNC Protocol Version Mismatch**
   - QEMU's VNC server implements RFB protocol version 3.8
   - Apple's Screen Sharing expects protocol 3.889 or Apple's custom extensions
   - The handshake may fail or result in incorrect negotiation
   - Source: [QEMU Bug Report #925405](https://bugs.launchpad.net/bugs/925405)

2. **Authentication Method Incompatibility**
   - QEMU's VNC server supports: None, VNC Authentication, SASL, TLS
   - Apple's Screen Sharing prefers Apple Remote Desktop (ARD) authentication
   - When QEMU is configured with `password=off`, Screen Sharing may not connect
   - Source: [LogCG - macOS VNC Connection Issues](https://www.logcg.com/en/archives/3807.html)

3. **Encoding Mismatch**
   - QEMU primarily uses Raw, Hextile, and Tight encodings
   - Apple's Screen Sharing optimizes for ZRLE and Tight with Apple extensions
   - Encoding negotiation may fail, resulting in blank screen
   - Source: [Apple StackExchange - Screen Sharing Can't Connect](https://apple.stackexchange.com/questions/310625/screen-sharing-app-can-t-connect-to-vnc)

4. **Pixel Format Issues**
   - QEMU uses standard RGB pixel formats
   - Apple's Screen Sharing may expect BGR or other formats
   - Color rendering may be incorrect or fail entirely

#### Observed Symptoms

- ❌ Connection refused or timeout
- ❌ Connection succeeds but displays blank/black screen
- ❌ Connection drops immediately after establishing
- ❌ Colors appear wrong or corrupted
- ❌ Screen Sharing shows "The software on the remote computer appears to be incompatible"

### Recommended VNC Clients for macOS

Instead of Apple's Screen Sharing, use these compatible VNC clients:

#### 1. **RealVNC Viewer** (Best for QEMU)

```bash
# Install:
brew install --cask vnc-viewer

# Connect:
# Open VNC Viewer app
# Enter: localhost:5900
# Or command line: open vnc://localhost:5900 (will try Screen Sharing first)
```

**Pros:**
- ✅ Fully compatible with QEMU VNC
- ✅ Supports all standard VNC encodings
- ✅ Free for personal use
- ✅ Works on macOS, Linux, Windows

**Cons:**
- Requires separate download
- Account creation prompted (but not required for basic use)

#### 2. **TigerVNC Viewer** (Open Source)

```bash
# Install:
brew install tiger-vnc

# Connect:
vncviewer localhost:5900
```

**Pros:**
- ✅ Fully compatible with QEMU VNC
- ✅ Open source
- ✅ Lightweight
- ✅ No account required

**Cons:**
- Less polished UI than RealVNC
- Requires Homebrew installation

#### 3. **TightVNC Viewer** (Cross-Platform)

```bash
# Install:
brew install tightvnc

# Connect:
vncviewer localhost:5900
```

**Pros:**
- ✅ Works with QEMU
- ✅ Open source
- ✅ Cross-platform

**Cons:**
- UI is dated
- Less actively maintained

### Workarounds for Apple Screen Sharing (Not Recommended)

If you absolutely must use Apple's Screen Sharing:

1. **Enable VNC password authentication** (may help with auth negotiation):

```bash
# Modify runqemu-virt-vga script:
# Change: -vnc ":0,password=off"
# To:     -vnc ":0,password=on"

# Then set password in QEMU monitor:
# (Press Ctrl+A then C to access QEMU monitor)
# (qemu) change vnc password
# (qemu) <enter password>
```

2. **Try different VNC settings**:

```bash
# Use -vnc with explicit lossy encoding:
-vnc ":0,lossy,password=off"

# Or force specific encoding:
-vnc ":0,compress=9,password=off"
```

**Note:** These workarounds are unreliable and not recommended. Using a proper VNC client is the better solution.

## Testing the Setup

### Complete Test Workflow

```bash
# 1. Source environment
cd /Users/iansmith/mazzy
source enable-mazzy

# 2. Build kernel for QEMU
cd src
make kernel-qemu.elf

# 3. Copy to Docker
make push-qemu

# 4. Start QEMU with VNC (in one terminal)
runqemu-virt-vga

# Expected output:
# "Starting QEMU with 'virt' machine type and VGA device"
# "VNC server will listen on port 5900"
# ... kernel boots ...
# "findBochsDisplay: Scanning PCI bus..."
# "findBochsDisplay: Found bochs-display device"
# "  BAR0: 0x..."
# "framebufferInit (QEMU): Framebuffer mapped successfully"

# 5. Connect with VNC (in another terminal or GUI)
# Using RealVNC Viewer:
open -a "VNC Viewer"  # Then enter localhost:5900

# Using TigerVNC:
vncviewer localhost:5900

# Using command line (tries Screen Sharing first - may not work):
open vnc://localhost:5900
```

### What You Should See

1. **UART Output** (in the terminal running runqemu-virt-vga):
   - "Hello, Mazarin!" (from kernel initialization)
   - PCI enumeration messages
   - Framebuffer initialization messages
   - Memory allocation info

2. **VNC Display** (in the VNC client window):
   - A 640x480 window appears
   - Black background (framebuffer cleared to black)
   - Any text or graphics that your kernel draws

### Simple Test: Draw to Framebuffer

Add this to your kernel to test framebuffer:

```go
// src/go/mazarin/kernel.go
func testFramebuffer() {
    // Draw a white rectangle (100x100 at position 100,100)
    for y := uint32(100); y < 200; y++ {
        for x := uint32(100); x < 200; x++ {
            offset := (y * fbinfo.Pitch) + (x * BYTES_PER_PIXEL)
            pixels := (*[1 << 30]byte)(fbinfo.Buf)
            pixels[offset+0] = 0xFF // Blue
            pixels[offset+1] = 0xFF // Green
            pixels[offset+2] = 0xFF // Red
        }
    }
}

// Call from kmain:
func kmain() {
    // ... existing initialization ...
    
    framebufferInit()
    testFramebuffer()  // Draw test rectangle
    
    // ... rest of kernel ...
}
```

You should see a white 100x100 rectangle in the VNC window.

## Troubleshooting

### VNC Connection Refused

**Symptom:** VNC client shows "Connection refused" or "Failed to connect"

**Causes:**
1. QEMU container not running
2. Port not mapped correctly
3. Firewall blocking port 5900

**Solutions:**

```bash
# 1. Check if container is running
docker ps | grep alpine-qemu

# 2. Check if port is mapped
docker ps -a --format "table {{.Names}}\t{{.Ports}}" | grep mazarin

# Expected output: 0.0.0.0:5900->5900/tcp

# 3. Check if port is listening
lsof -i :5900

# Expected output: Docker process listening on port 5900

# 4. Try different port
VNC_PORT=5901 runqemu-virt-vga
# Connect to localhost:5901
```

### VNC Shows Blank/Black Screen

**Symptom:** VNC connects but display is black

**Causes:**
1. Framebuffer not initialized
2. PCI enumeration failed
3. Writing to wrong memory address
4. Using Apple Screen Sharing (incompatible)

**Solutions:**

```bash
# 1. Check UART output for framebuffer init
# Look for:
#   "framebufferInit (QEMU): Framebuffer mapped successfully"
#   "findBochsDisplay: Found bochs-display device"

# 2. If PCI enumeration failed:
#   "findBochsDisplay: bochs-display device not found"
#   "Using fallback address 0x40000000"
# This is OK - fallback should work

# 3. Verify bochs-display is added to QEMU
# Check runqemu-virt-vga has: -device bochs-display

# 4. Try different VNC client (not Apple Screen Sharing)
brew install --cask vnc-viewer
# Connect with RealVNC Viewer
```

### Framebuffer Writes Don't Appear

**Symptom:** UART shows framebuffer initialized, but writing doesn't show up

**Causes:**
1. Wrong framebuffer address
2. Cache not flushed
3. Display device not configured

**Debug:**

```bash
# 1. Check PCI BAR0 address in UART output
# Look for: "BAR0: 0x..."
# This is where you should write pixels

# 2. Add cache flush after writing (if needed)
# ARM64 may cache writes - add DSB (Data Synchronization Barrier)

# 3. Verify QEMU has display device
# Check docker run command has: -device bochs-display

# 4. Try writing test pattern
# See "Simple Test: Draw to Framebuffer" above
```

### Apple Screen Sharing Issues

**Symptom:** Screen Sharing shows incompatibility error or blank screen

**Solution:**
Don't use Apple Screen Sharing with QEMU. Use RealVNC Viewer or TigerVNC instead.

```bash
# Install RealVNC Viewer
brew install --cask vnc-viewer

# Or TigerVNC
brew install tiger-vnc
```

See [Apple VNC Viewer Compatibility Issues](#apple-vnc-viewer-compatibility-issues) for details.

### Docker Container Keeps Running

**Symptom:** After Ctrl+C, container still running in background

**Solution:**

```bash
# List running containers
docker ps | grep mazarin

# Kill and remove
docker kill <container-name>
docker rm <container-name>

# Or kill all mazarin containers
docker ps -a --format '{{.Names}}' | grep '^mazarin-' | xargs docker kill
docker ps -a --format '{{.Names}}' | grep '^mazarin-' | xargs docker rm
```

The `runqemu-virt-vga` script automatically cleans up old containers before starting.

### Port Already in Use

**Symptom:** "Error: port 5900 is already allocated"

**Solutions:**

```bash
# 1. Check what's using port 5900
lsof -i :5900

# 2. If it's another QEMU instance, kill it
docker ps | grep alpine-qemu
docker kill <container-name>

# 3. Use a different port
VNC_PORT=5901 runqemu-virt-vga
```

## Advanced Topics

### Multiple QEMU Instances

Run multiple instances with different VNC ports:

```bash
# Terminal 1: Instance 1 on port 5900
VNC_PORT=5900 runqemu-virt-vga

# Terminal 2: Instance 2 on port 5901
VNC_PORT=5901 runqemu-virt-vga

# Connect to each separately:
vncviewer localhost:5900  # Instance 1
vncviewer localhost:5901  # Instance 2
```

### VNC Over Network

To access VNC from another machine on your network:

```bash
# Modify runqemu-virt-vga to bind to all interfaces:
# Change: -vnc ":0"
# To:     -vnc "0.0.0.0:0"

# Then connect from another machine:
vncviewer <your-ip>:5900
```

**Security warning:** VNC with `password=off` is insecure. Only use on trusted networks.

### VNC with Password

For added security:

```bash
# Add password to VNC:
# 1. Modify runqemu-virt-vga:
#    Change: -vnc ":0,password=off"
#    To:     -vnc ":0"

# 2. Set password in QEMU monitor after boot:
#    (Press Ctrl+A then C to access QEMU monitor)
#    (qemu) change vnc password
#    Password: ********
#    (qemu) quit  # or Ctrl+A then C to return

# 3. Connect with VNC client (will prompt for password)
```

## Summary

**Key Points:**
1. ✅ Use `runqemu-virt-vga` script for VNC framebuffer display
2. ✅ Kernel finds framebuffer via PCI enumeration (bochs-display device)
3. ❌ Don't use Apple Screen Sharing - use RealVNC Viewer or TigerVNC instead
4. ✅ Connect to `localhost:5900` (or custom port via `VNC_PORT`)
5. ✅ Check UART output for framebuffer initialization messages
6. ✅ Default resolution is 640x480, 24-bit RGB

**Quick Start:**
```bash
source enable-mazzy
cd src && make kernel-qemu.elf && make push-qemu
runqemu-virt-vga
# In another terminal/app:
vncviewer localhost:5900  # (using TigerVNC or RealVNC)
```

**Next Steps:**
- Implement graphics primitives (lines, rectangles, text)
- Add font rendering
- Build a simple terminal emulator
- Port to real Raspberry Pi 4 hardware









