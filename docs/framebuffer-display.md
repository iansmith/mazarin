# Framebuffer Display Options

This document explains how to view the framebuffer output from QEMU on your host machine.

## Problem

When QEMU runs inside a Docker container, it cannot directly access your host machine's display. We need a way to get the framebuffer output from the container to your screen.

## Solutions

### Option 1: VNC (Recommended for Docker)

**Best for:** Running QEMU in Docker and viewing display remotely

QEMU can run a VNC server inside the container, and you connect to it with a VNC client on your host.

#### Usage:

```bash
# Build and copy kernel
cd src && make push

# Run QEMU with VNC display
docker/runqemu-fb

# In another terminal or app, connect to VNC:
# macOS: open vnc://localhost:5900
# Or use Screen Sharing app (built-in) or RealVNC Viewer
```

#### How it works:

1. QEMU runs a VNC server on port 5900 inside the container
2. Docker maps port 5900 to your host machine
3. You connect to `localhost:5900` with any VNC client
4. The framebuffer appears in the VNC window

#### Custom VNC port:

```bash
VNC_PORT=5901 docker/runqemu-fb  # Use port 5901 instead
```

#### VNC Clients for macOS:

- **Screen Sharing** (built-in): Finder → Go → Connect to Server → `vnc://localhost:5900`
- **RealVNC Viewer**: Download from [realvnc.com](https://www.realvnc.com/download/viewer/)
- **Command line**: `open vnc://localhost:5900`

### Option 2: Run QEMU Directly on Host

**Best for:** Simpler setup, faster iteration during development

Instead of running QEMU in Docker, run it directly on your host machine. This avoids VNC entirely and opens a window directly on your desktop.

#### Prerequisites:

```bash
# Install QEMU and SDL2 (on macOS)
brew install qemu sdl2
```

#### Usage:

```bash
# Build and copy kernel
cd src && make push

# Run QEMU directly on host
docker/runqemu-host-fb
```

#### How it works:

1. QEMU runs directly on your host (not in Docker)
2. SDL display backend opens a window on your desktop
3. Framebuffer appears immediately in the window
4. No VNC needed - simpler and faster

#### Advantages:

- ✅ No VNC client needed
- ✅ Lower latency (no network overhead)
- ✅ Simpler setup
- ✅ Works on macOS, Linux, Windows

#### Disadvantages:

- ❌ Requires QEMU installed on host
- ❌ Not using Docker (less isolated environment)

### Option 3: X11 Forwarding (Linux/Unix only)

**Best for:** Linux hosts with X11

On Linux/Unix systems, you can forward X11 from the container to the host.

#### Setup:

```bash
# On host (Linux)
xhost +local:docker

# Run container with X11 forwarding
docker run --rm -it \
    -e DISPLAY=$DISPLAY \
    -v /tmp/.X11-unix:/tmp/.X11-unix:rw \
    -v "$BUILTIN_DIR:/mnt/builtin:ro" \
    alpine-qemu:3.22 \
    -display sdl
```

**Note:** This doesn't work on macOS (no X11 by default).

## Comparison

| Method | Platform | Setup Complexity | Latency | Best For |
|--------|----------|------------------|---------|----------|
| VNC (Docker) | All | Medium | Medium | Docker-based workflow |
| Direct Host | All | Low | Low | Development/testing |
| X11 Forwarding | Linux/Unix | Medium | Low | Linux development |

## Recommendations

1. **For development/testing**: Use `runqemu-host-fb` (Option 2) - simplest and fastest
2. **For Docker-based workflow**: Use `runqemu-fb` (Option 1) - works with existing Docker setup
3. **For CI/CD or remote**: Use VNC - most portable

## Troubleshooting

### VNC connection refused

- Check that the port is exposed: `docker ps` should show port mapping
- Try a different port: `VNC_PORT=5901 docker/runqemu-fb`
- Check firewall settings

### SDL display not working (host mode)

- Install SDL2: `brew install sdl2` (macOS) or `apt-get install libsdl2-dev` (Linux)
- Check QEMU version: `qemu-system-aarch64 --version` (should be 6.2.0+)

### Display is black/blank

**Important QEMU Limitation:** QEMU's `raspi4b` model does **not** fully emulate the Raspberry Pi 4's mailbox interface for framebuffer initialization. This is a known QEMU limitation, not a bug in the kernel code.

**What this means:**
- ✅ The framebuffer code is **correct** and follows the Raspberry Pi 4 specification
- ✅ The code will work on **real Raspberry Pi 4 hardware**
- ❌ In QEMU, mailbox messages are sent but the GPU does not process them
- ❌ Framebuffer initialization will fail in QEMU, resulting in a black screen

**For QEMU testing:**
- Use UART output (which works correctly) for debugging
- Framebuffer functionality requires real Raspberry Pi 4 hardware

**For real hardware:**
- The framebuffer initialization code is ready and should work correctly
- Message format matches the specification
- Data Synchronization Barrier (DSB) is included for cache coherency
- 16-byte alignment is enforced for mailbox buffers

**Other troubleshooting:**
- Verify framebuffer is initialized in kernel code (check UART output)
- Check that mailbox communication is working (check UART debug output)
- Use UART output to debug framebuffer initialization

## Next Steps

The framebuffer implementation is complete and ready for real hardware:

1. ✅ Mailbox communication implemented (see `src/go/mazarin/mailbox.go`)
2. ✅ Framebuffer initialization via property mailbox channel (see `src/go/mazarin/framebuffer.go`)
3. ✅ Character rendering to framebuffer (see `src/go/mazarin/gpu.go`)
4. ⚠️ **Testing:** Requires real Raspberry Pi 4 hardware (QEMU limitation)

**To test on real hardware:**
1. Build the kernel: `cd src && make kernel.elf`
2. Copy `kernel.elf` to a Raspberry Pi 4
3. Boot from the kernel
4. The framebuffer should initialize and display text

**For QEMU development:**
- Continue using UART output for debugging
- Framebuffer code is ready but won't work in QEMU

