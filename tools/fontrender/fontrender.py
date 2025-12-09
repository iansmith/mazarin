#!/usr/bin/env python3
"""
Font Renderer - Converts TTF fonts to binary format for kernel embedding

Output format:
  Header:
    [4 bytes: num_chars (uint32 little-endian)]
    [4 bytes: first_char_code (uint32 little-endian)]
    [4 bytes: last_char_code (uint32 little-endian)]
  
  For each character:
    [4 bytes: width (uint32 little-endian)]
    [4 bytes: height (uint32 little-endian)]
    [4 bytes: advance_x (uint32 little-endian)] - horizontal advance
    [4 bytes: bearing_x (int32 little-endian)] - x offset from cursor
    [4 bytes: bearing_y (int32 little-endian)] - y offset from baseline
    [width*height*4 bytes: ARGB8888 pixels (uint32 little-endian per pixel)]
"""

import argparse
import struct
import sys
from pathlib import Path

try:
    from PIL import Image, ImageDraw, ImageFont
except ImportError:
    print("Error: PIL (Pillow) is required. Install with: pip install Pillow", file=sys.stderr)
    sys.exit(1)


def render_character(font, char_code, point_size, antialias=True):
    """
    Render a single character and return its bitmap and metrics.
    
    Returns:
        (image, advance_x, bearing_x, bearing_y)
        image: PIL Image in RGBA mode
        advance_x: horizontal advance in pixels
        bearing_x: x offset from cursor (can be negative)
        bearing_y: y offset from baseline (can be negative)
    """
    # Get character
    char = chr(char_code)
    
    # Create a temporary image to measure the character
    # Use a large temporary canvas to capture full glyph including negative bearings
    temp_size = (point_size * 3, point_size * 3)
    temp_img = Image.new('RGBA', temp_size, (0, 0, 0, 0))
    temp_draw = ImageDraw.Draw(temp_img)
    
    # Get font metrics
    try:
        bbox = temp_draw.textbbox((0, 0), char, font=font)
        metrics = temp_draw.textlength(char, font=font)
    except Exception as e:
        # Fallback for characters that can't be rendered
        return None, 0, 0, 0
    
    # Calculate bounding box
    if bbox[0] == bbox[2] and bbox[1] == bbox[3]:
        # Empty character (e.g., space)
        # Use font metrics to estimate advance
        try:
            # Try to get advance width from font metrics
            advance_x = int(metrics) if metrics > 0 else point_size // 2
        except:
            advance_x = point_size // 2
        return None, advance_x, 0, 0
    
    # Calculate actual character dimensions
    char_width = bbox[2] - bbox[0]
    char_height = bbox[3] - bbox[1]
    
    # Calculate bearings (offsets from origin)
    bearing_x = bbox[0] - temp_size[0] // 3  # Offset from left edge of temp canvas
    bearing_y = bbox[1] - temp_size[1] // 3  # Offset from top edge of temp canvas
    
    # Get advance width
    advance_x = int(metrics) if metrics > 0 else char_width
    
    # Render the character on a properly sized canvas
    # Add padding to handle negative bearings
    padding = max(abs(bearing_x), abs(bearing_y), 0) + 2
    img_width = char_width + padding * 2
    img_height = char_height + padding * 2
    
    img = Image.new('RGBA', (img_width, img_height), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    
    # Draw character at offset to account for bearings
    draw.text((padding + bearing_x, padding + bearing_y), char, font=font, fill=(255, 255, 255, 255))
    
    # Adjust bearings to account for padding
    bearing_x = bearing_x - padding
    bearing_y = bearing_y - padding
    
    return img, advance_x, bearing_x, bearing_y


def convert_to_argb8888(img):
    """Convert PIL RGBA image to ARGB8888 format (list of uint32)."""
    if img is None:
        return []
    
    pixels = []
    width, height = img.size
    
    for y in range(height):
        for x in range(width):
            r, g, b, a = img.getpixel((x, y))
            # ARGB8888: [A:8][R:8][G:8][B:8] = 0xAARRGGBB
            pixel = (a << 24) | (r << 16) | (g << 8) | b
            pixels.append(pixel)
    
    return pixels


def render_font(ttf_path, output_path, point_size, first_char=0, last_char=127, antialias=True):
    """
    Render a TTF font and save to binary file.
    
    Args:
        ttf_path: Path to TTF font file
        output_path: Path to output binary file
        point_size: Font size in points
        first_char: First character code to render (default: 0)
        last_char: Last character code to render (default: 127)
        antialias: Whether to use antialiasing (default: True)
    """
    # Load font
    try:
        font = ImageFont.truetype(str(ttf_path), point_size)
    except Exception as e:
        print(f"Error loading font: {e}", file=sys.stderr)
        sys.exit(1)
    
    # Determine character range
    num_chars = last_char - first_char + 1
    
    print(f"Rendering font: {ttf_path}")
    print(f"Point size: {point_size}")
    print(f"Character range: {first_char} (0x{first_char:02x}) to {last_char} (0x{last_char:02x})")
    print(f"Total characters: {num_chars}")
    
    # Open output file
    with open(output_path, 'wb') as f:
        # Write header
        f.write(struct.pack('<I', num_chars))  # num_chars
        f.write(struct.pack('<I', first_char))  # first_char_code
        f.write(struct.pack('<I', last_char))   # last_char_code
        
        # Render each character
        rendered_count = 0
        empty_count = 0
        
        for char_code in range(first_char, last_char + 1):
            img, advance_x, bearing_x, bearing_y = render_character(
                font, char_code, point_size, antialias
            )
            
            if img is None:
                # Empty character (e.g., space, control characters)
                width = 0
                height = 0
                empty_count += 1
            else:
                width, height = img.size
                rendered_count += 1
            
            # Write character header
            f.write(struct.pack('<I', width))           # width
            f.write(struct.pack('<I', height))          # height
            f.write(struct.pack('<I', advance_x))      # advance_x
            f.write(struct.pack('<i', bearing_x))      # bearing_x (signed)
            f.write(struct.pack('<i', bearing_y))      # bearing_y (signed)
            
            # Write pixel data
            if img is not None:
                pixels = convert_to_argb8888(img)
                for pixel in pixels:
                    f.write(struct.pack('<I', pixel))
            
            # Progress indicator
            if (char_code - first_char + 1) % 32 == 0:
                print(f"  Progress: {char_code - first_char + 1}/{num_chars} characters", end='\r')
        
        print(f"\nRendered {rendered_count} characters, {empty_count} empty")
    
    # Print file info
    file_size = Path(output_path).stat().st_size
    print(f"Output file: {output_path}")
    print(f"File size: {file_size:,} bytes ({file_size / 1024:.2f} KB)")


def main():
    parser = argparse.ArgumentParser(
        description='Render TTF fonts to binary format for kernel embedding',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Output format:
  Header:
    [4 bytes: num_chars]
    [4 bytes: first_char_code]
    [4 bytes: last_char_code]
  
  For each character:
    [4 bytes: width]
    [4 bytes: height]
    [4 bytes: advance_x]
    [4 bytes: bearing_x (signed)]
    [4 bytes: bearing_y (signed)]
    [width*height*4 bytes: ARGB8888 pixels]

Example:
  %(prog)s font.ttf output.bin --size 24
  %(prog)s font.ttf output.bin --size 16 --range 32 126
        """
    )
    
    parser.add_argument('font', type=str, help='Path to TTF font file')
    parser.add_argument('output', type=str, help='Path to output binary file')
    parser.add_argument('--size', '-s', type=int, required=True,
                       help='Font size in points')
    parser.add_argument('--range', '-r', nargs=2, type=int, metavar=('FIRST', 'LAST'),
                       default=[0, 127],
                       help='Character code range (default: 0 127)')
    parser.add_argument('--no-antialias', action='store_true',
                       help='Disable antialiasing (faster, but lower quality)')
    
    args = parser.parse_args()
    
    # Validate inputs
    font_path = Path(args.font)
    if not font_path.exists():
        print(f"Error: Font file not found: {font_path}", file=sys.stderr)
        sys.exit(1)
    
    if args.size <= 0:
        print("Error: Font size must be positive", file=sys.stderr)
        sys.exit(1)
    
    first_char, last_char = args.range
    if first_char < 0 or last_char < first_char or last_char > 0x10FFFF:
        print("Error: Invalid character range", file=sys.stderr)
        sys.exit(1)
    
    # Render font
    render_font(
        font_path,
        args.output,
        args.size,
        first_char,
        last_char,
        antialias=not args.no_antialias
    )


if __name__ == '__main__':
    main()


