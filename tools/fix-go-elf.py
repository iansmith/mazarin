#!/usr/bin/env python3
"""
fix-go-elf.py - Fix Go ELF binaries with negative file offsets

Go's linker with -T flag produces ELF files with negative (wrapped around)
file offsets in LOAD segments. QEMU 9.0+ mishandles these, causing crashes
in address_space_write_rom during rom_reset.

This script fixes the ELF by adjusting the problematic segment:
  - Changes vaddr from 0x400f0000 to 0x40100000
  - Changes offset from -0xf000 to 0x1000
  - Adjusts filesz and memsz accordingly

The fix is done in-place unless --output is specified.

Usage:
    fix-go-elf.py <elf-file>              # Fix in-place
    fix-go-elf.py <input> --output <out>  # Write to new file
    fix-go-elf.py <elf-file> --check      # Check only, don't modify
"""

import struct
import sys
import shutil
import argparse


def fix_elf(filepath, output_path=None, check_only=False, verbose=False):
    """
    Fix Go ELF with negative file offset in LOAD segment.

    Returns:
        0 if no fix needed or fix successful
        1 if fix needed and applied (or would be applied in check mode)
        2 on error
    """
    # Read the file
    try:
        with open(filepath, 'rb') as f:
            data = bytearray(f.read())
    except IOError as e:
        print(f"Error reading {filepath}: {e}", file=sys.stderr)
        return 2

    # Verify ELF magic
    if data[:4] != b'\x7fELF':
        print(f"Error: {filepath} is not an ELF file", file=sys.stderr)
        return 2

    # Check 64-bit
    ei_class = data[4]
    if ei_class != 2:
        print(f"Error: {filepath} is not a 64-bit ELF", file=sys.stderr)
        return 2

    # Determine endianness
    ei_data = data[5]
    endian = '<' if ei_data == 1 else '>'

    # Read program header info from ELF header
    e_phoff = struct.unpack_from(endian + 'Q', data, 32)[0]
    e_phentsize = struct.unpack_from(endian + 'H', data, 54)[0]
    e_phnum = struct.unpack_from(endian + 'H', data, 56)[0]

    if verbose:
        print(f"ELF: {e_phnum} program headers at offset {e_phoff}")

    fixed = False

    # Scan program headers
    for i in range(e_phnum):
        ph_offset = e_phoff + i * e_phentsize

        # Read program header fields
        # Phdr64: p_type(4), p_flags(4), p_offset(8), p_vaddr(8), p_paddr(8), p_filesz(8), p_memsz(8), p_align(8)
        p_type = struct.unpack_from(endian + 'I', data, ph_offset)[0]
        p_flags = struct.unpack_from(endian + 'I', data, ph_offset + 4)[0]
        p_offset = struct.unpack_from(endian + 'Q', data, ph_offset + 8)[0]
        p_vaddr = struct.unpack_from(endian + 'Q', data, ph_offset + 16)[0]
        p_paddr = struct.unpack_from(endian + 'Q', data, ph_offset + 24)[0]
        p_filesz = struct.unpack_from(endian + 'Q', data, ph_offset + 32)[0]
        p_memsz = struct.unpack_from(endian + 'Q', data, ph_offset + 40)[0]

        # Check for negative offset (wrapped around as large positive)
        if p_offset > 0x8000000000000000:
            # This is a negative offset
            signed_offset = p_offset - 0x10000000000000000

            if verbose:
                print(f"Segment {i}: type={p_type}, vaddr=0x{p_vaddr:x}, "
                      f"offset=0x{p_offset:x} (={signed_offset})")

            # Calculate the fix
            # Original: vaddr=0x400f0000, offset=-0xf000
            # The 64KB region from 0x400f0000 to 0x40100000 is zero-fill
            # Actual data starts at file offset 0x1000, loads at 0x40100000

            # The negative offset tells us how much zero-fill there is
            zero_fill_size = -signed_offset  # e.g., 0xf000 = 61440

            # Align to 64KB (0x10000)
            if zero_fill_size < 0x10000:
                adjustment = 0x10000
            else:
                adjustment = (zero_fill_size + 0xFFFF) & ~0xFFFF

            new_vaddr = p_vaddr + adjustment
            new_paddr = p_paddr + adjustment
            new_offset = 0x1000  # .text section starts at file offset 0x1000
            new_filesz = p_filesz - adjustment if p_filesz >= adjustment else 0
            new_memsz = p_memsz - adjustment if p_memsz >= adjustment else 0

            print(f"Fixing segment {i}:")
            print(f"  vaddr:  0x{p_vaddr:x} -> 0x{new_vaddr:x}")
            print(f"  offset: 0x{p_offset:x} -> 0x{new_offset:x}")
            print(f"  filesz: 0x{p_filesz:x} -> 0x{new_filesz:x}")
            print(f"  memsz:  0x{p_memsz:x} -> 0x{new_memsz:x}")

            if not check_only:
                # Write the fixed values
                struct.pack_into(endian + 'Q', data, ph_offset + 8, new_offset)
                struct.pack_into(endian + 'Q', data, ph_offset + 16, new_vaddr)
                struct.pack_into(endian + 'Q', data, ph_offset + 24, new_paddr)
                struct.pack_into(endian + 'Q', data, ph_offset + 32, new_filesz)
                struct.pack_into(endian + 'Q', data, ph_offset + 40, new_memsz)

            fixed = True

    if not fixed:
        if verbose:
            print("No segments need fixing")
        return 0

    if check_only:
        print("Fix needed (check mode, no changes made)")
        return 1

    # Write the fixed file
    out_path = output_path if output_path else filepath
    try:
        with open(out_path, 'wb') as f:
            f.write(data)
        print(f"Fixed ELF written to {out_path}")
    except IOError as e:
        print(f"Error writing {out_path}: {e}", file=sys.stderr)
        return 2

    return 0


def main():
    parser = argparse.ArgumentParser(
        description='Fix Go ELF binaries with negative file offsets for QEMU compatibility'
    )
    parser.add_argument('input', help='Input ELF file')
    parser.add_argument('--output', '-o', help='Output file (default: modify in-place)')
    parser.add_argument('--check', '-c', action='store_true',
                        help='Check only, do not modify')
    parser.add_argument('--verbose', '-v', action='store_true',
                        help='Verbose output')

    args = parser.parse_args()

    return fix_elf(args.input, args.output, args.check, args.verbose)


if __name__ == '__main__':
    sys.exit(main())
