# Font Renderer

Python tool to render TTF fonts and convert them to binary format for kernel embedding.

## Requirements

Install Pillow (PIL) for font rendering:

```bash
pip install Pillow
```

## Usage

```bash
python3 fontrender.py <font.ttf> <output.bin> --size <point_size> [options]
```

### Options

- `--size`, `-s`: Font size in points (required)
- `--range`, `-r`: Character code range (default: 0 127)
  - Example: `--range 32 126` for printable ASCII
- `--no-antialias`: Disable antialiasing (faster, lower quality)

### Examples

Render a font at 24pt for ASCII characters (0-127):
```bash
python3 fontrender.py font.ttf font-24pt.bin --size 24
```

Render only printable ASCII (32-126):
```bash
python3 fontrender.py font.ttf font-24pt.bin --size 24 --range 32 126
```

Render a larger Unicode range:
```bash
python3 fontrender.py font.ttf font-16pt.bin --size 16 --range 0 255
```

## Output Format

The binary file format is:

**Header:**
- `[4 bytes: num_chars]` - Number of characters (uint32 little-endian)
- `[4 bytes: first_char_code]` - First character code (uint32 little-endian)
- `[4 bytes: last_char_code]` - Last character code (uint32 little-endian)

**For each character:**
- `[4 bytes: width]` - Character bitmap width (uint32 little-endian)
- `[4 bytes: height]` - Character bitmap height (uint32 little-endian)
- `[4 bytes: advance_x]` - Horizontal advance (how much to move cursor) (uint32 little-endian)
- `[4 bytes: bearing_x]` - X offset from cursor position (int32 little-endian, can be negative)
- `[4 bytes: bearing_y]` - Y offset from baseline (int32 little-endian, can be negative)
- `[width*height*4 bytes: ARGB8888 pixels]` - Pixel data (uint32 little-endian per pixel)

**Note:** Empty characters (like space or control characters) have width=0 and height=0, but still include advance_x, bearing_x, and bearing_y values.

## Pixel Format

Pixels are stored in ARGB8888 format:
- Each pixel is 4 bytes (uint32)
- Format: `0xAARRGGBB` (Alpha, Red, Green, Blue)
- Little-endian byte order

This matches the format used by `boot-mazarin.bin` and the kernel's `RenderImageData` function.
