# VNC Quick Start Guide

Quick reference for viewing QEMU framebuffer output via VNC.

## TL;DR

```bash
# 1. Build and push kernel
source enable-mazzy
cd src && make kernel-qemu.elf && make push-qemu

# 2. Start QEMU with VNC
runqemu-virt-vga  # or runqemu-fb

# 3. Connect with VNC client
# RECOMMENDED: RealVNC Viewer or TigerVNC (NOT Apple Screen Sharing)
vncviewer localhost:5900
```

## Install VNC Client

Apple's Screen Sharing **does NOT work** with QEMU VNC. Use one of these:

```bash
# Option 1: RealVNC Viewer (recommended)
brew install --cask vnc-viewer

# Option 2: TigerVNC (open source)
brew install tiger-vnc
```

## Why Not Apple Screen Sharing?

Apple's built-in VNC viewer has **known incompatibilities** with QEMU:
- Protocol version mismatch (QEMU uses RFB 3.8, Apple expects 3.889)
- Authentication method incompatibility
- Encoding/pixel format issues

**Result:** Connection refused, blank screen, or immediate disconnect.

**Sources:**
- [QEMU Bug Report #925405](https://bugs.launchpad.net/bugs/925405)
- [LogCG - macOS VNC Issues](https://www.logcg.com/en/archives/3807.html)
- [Apple StackExchange](https://apple.stackexchange.com/questions/310625/screen-sharing-app-can-t-connect-to-vnc)

## Framebuffer Location

The kernel finds the framebuffer automatically via PCI enumeration:

1. **Scans PCI bus** at config space 0x30000000
2. **Finds bochs-display device** (vendor 0x1234, device 0x1111)
3. **Reads BAR0** to get framebuffer address (typically 0x40000000+)
4. **Writes pixels** to framebuffer (RGB, 24-bit, 640x480)

Check UART output for:
```
findBochsDisplay: Found bochs-display device
  BAR0: 0x...
framebufferInit (QEMU): Framebuffer mapped successfully
```

## Troubleshooting

### "Connection refused"
- Check container is running: `docker ps | grep alpine-qemu`
- Check port is mapped: should show `0.0.0.0:5900->5900/tcp`
- Try different port: `VNC_PORT=5901 runqemu-virt-vga`

### "Blank/black screen"
- Check UART for framebuffer init messages
- Verify using RealVNC/TigerVNC (NOT Screen Sharing)
- Make sure `-device bochs-display` is in QEMU command
- Check kernel is writing to framebuffer

### "Port already in use"
- Kill old containers: `docker kill $(docker ps -q --filter ancestor=alpine-qemu:3.22)`
- Use different port: `VNC_PORT=5901 runqemu-virt-vga`

## Full Documentation

See `docs/qemu-vnc-framebuffer-setup.md` for complete details:
- How VNC works with QEMU
- Memory map and PCI enumeration
- Apple VNC compatibility details (with sources)
- Advanced configuration
- Testing and debugging

## Scripts

- `runqemu-virt-vga` - QEMU with bochs-display and VNC (recommended)
- `runqemu-fb` - QEMU with VNC (now includes bochs-display)
- Both scripts support custom VNC port via `VNC_PORT` environment variable

Both scripts automatically:
- Clean up old containers
- Add bochs-display device
- Map VNC port 5900 to host
- Show UART output in terminal
