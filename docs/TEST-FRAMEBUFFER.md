# Testing the VNC Framebuffer

## Test Pattern

The kernel now draws a colorful test pattern to verify the framebuffer is working:

### Pattern Description

```
┌──────────────────────────────────────────────────┐
│  RED   │  GREEN  │  BLUE   │  WHITE              │
│        │         │         │                      │
│   ──────────── YELLOW CROSS ────────────────     │
│        │         │         │                      │
│        │         │         │                      │
└──────────────────────────────────────────────────┘
```

- **Red** (left quarter): 0-160 pixels
- **Green** (second quarter): 160-320 pixels  
- **Blue** (third quarter): 320-480 pixels
- **White** (right quarter): 480-640 pixels
- **Yellow cross**: Horizontal and vertical lines through center (20 pixels thick)

Resolution: 640x480 pixels
Format: RGB, 24-bit (3 bytes per pixel)

## How to Test

### 1. Start QEMU with VNC

```bash
# In terminal 1:
source enable-mazzy
runqemu-virt-vga

# You should see:
# "VNC framebuffer is ready!"
# "Connect to: localhost:5900"
# "Program will loop forever."
# "Press Ctrl+C in terminal to exit."
```

### 2. Connect with VNC Client

**Option A: TigerVNC (recommended)**
```bash
# In terminal 2:
brew install tiger-vnc  # If not installed
vncviewer localhost:5900
```

**Option B: RealVNC Viewer**
```bash
# Install if needed:
brew install --cask vnc-viewer

# Open app and enter:
localhost:5900
```

### 3. What You Should See

In the VNC window:
- ✅ 640x480 window appears
- ✅ Four colored vertical stripes (red, green, blue, white)
- ✅ Yellow cross through the center
- ✅ Clear, crisp colors (not blurry or corrupted)

### 4. Stop the Test

The program loops forever to give you time to view the display.

**To stop:**
- Press `Ctrl+C` in the terminal running QEMU
- Or close the Docker container: `docker kill $(docker ps -q --filter ancestor=alpine-qemu:3.22)`

## Expected Terminal Output

```
SB
K
Hello, Mazarin!

Testing write barrier...
Write barrier flag: enabled
SUCCESS: Global pointer assignment works!
Heap initialized at RAM region

Initializing framebuffer for VNC...
framebufferInit (QEMU): Initializing framebuffer
findBochsDisplay: Scanning PCI bus...
findBochsDisplay: Found bochs-display device
  Bus: 1, Slot: 0, Func: 0
  BAR0: 0x10000000
  Framebuffer address: 0x0000000010000000
framebufferInit (QEMU): Framebuffer mapped successfully
  Address: 0x0000000010000000
  Size: 921600 bytes
  Dimensions: 640x480
  Note: Writing directly to QEMU's framebuffer memory
Framebuffer initialized successfully!
Drawing test pattern...
Test pattern drawn!

============================================
VNC framebuffer is ready!
Connect to: localhost:5900
============================================

Program will loop forever.
Press Ctrl+C in terminal to exit.

(program continues running indefinitely)
```

## Troubleshooting

### "No display in VNC window"

Check terminal output for framebuffer initialization:
```bash
# Look for:
"findBochsDisplay: Found bochs-display device"
"Framebuffer initialized successfully!"
"Test pattern drawn!"
```

If you see "Framebuffer initialization failed", the bochs-display device is not available. Make sure:
- Using `runqemu-virt-vga` (not `runqemu`)
- Docker command includes `-device bochs-display`

### "VNC connection refused"

```bash
# Check if QEMU is running:
docker ps | grep alpine-qemu

# Check if port is mapped:
docker ps --format "table {{.Names}}\t{{.Ports}}" | grep mazarin
# Should show: 0.0.0.0:5900->5900/tcp

# Try different port:
VNC_PORT=5901 runqemu-virt-vga
# Connect to localhost:5901
```

### "Colors look wrong"

This indicates a pixel format issue. Check:
- Using RealVNC Viewer or TigerVNC (not Apple Screen Sharing)
- VNC client color settings (should be automatic)
- If colors are swapped (BGR instead of RGB), this is a VNC client issue

### "Display is black"

Possible causes:
1. Test pattern not drawn (check terminal output)
2. Framebuffer address incorrect (check BAR0 in terminal)
3. VNC client not receiving updates (try reconnecting)

## What This Tests

✅ **PCI enumeration works** - Kernel finds bochs-display device  
✅ **Framebuffer memory mapping works** - BAR0 provides valid address  
✅ **Pixel writes work** - Can write RGB values to framebuffer  
✅ **QEMU VNC server works** - Display updates transmitted to client  
✅ **VNC client compatibility** - RealVNC/TigerVNC can decode properly

## Next Steps

Now that framebuffer is confirmed working:

1. **Add font rendering** - Draw text characters to screen
2. **Implement graphics primitives** - Lines, rectangles, circles
3. **Build terminal emulator** - Display UART output on screen
4. **Add color text support** - Multiple colors for syntax highlighting
5. **Implement scrolling** - Text buffer with scroll history

## Code Location

Test pattern code: `src/go/mazarin/kernel.go`
- `drawTestPattern()` function draws the colored rectangles and cross
- Called from `KernelMain()` after heap initialization
- Loops forever to keep display visible

Framebuffer init: `src/go/mazarin/framebuffer_qemu.go`
- `framebufferInit()` finds display device and maps memory
- Uses PCI enumeration to discover framebuffer address

PCI enumeration: `src/go/mazarin/pci_qemu.go`
- `findBochsDisplay()` scans PCI bus for bochs-display
- Reads BAR0 to get framebuffer physical address

## Quick Commands

```bash
# Full test sequence:
source enable-mazzy
cd src
make kernel-qemu.elf && make push-qemu
runqemu-virt-vga

# In another terminal:
vncviewer localhost:5900

# To stop:
# Press Ctrl+C in QEMU terminal
```

## Success Criteria

✅ Terminal shows "VNC framebuffer is ready!"  
✅ Terminal shows "Program will loop forever"  
✅ VNC window displays four colored stripes  
✅ VNC window shows yellow cross in center  
✅ Colors are correct (not black/white/corrupted)  
✅ Program continues running (doesn't exit)

If all criteria met: **Framebuffer is working correctly!** 🎉





