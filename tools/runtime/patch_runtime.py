#!/usr/bin/env python3
"""
Generalized runtime patching tool for replacing Go runtime functions.

This script patches ELF binaries to redirect function calls from one function
to another. It's used to replace Go runtime functions with bare-metal implementations.

Usage:
    patch_runtime.py <elf_file> <old_func1> <new_func1> [<old_func2> <new_func2> ...]

Example:
    patch_runtime.py kernel.elf runtime.gcWriteBarrier2 runtime.gcWriteBarrier2
"""

import struct
import sys
import subprocess
import os
import re

def find_bin_dir():
    """Find the bin directory containing target-* tools."""
    script_dir = os.path.dirname(os.path.abspath(__file__))
    # tools/runtime -> tools -> project root -> bin
    project_root = os.path.dirname(os.path.dirname(script_dir))
    bin_dir = os.path.join(project_root, 'bin')
    return bin_dir

def setup_env():
    """Set up environment with bin directory in PATH."""
    bin_dir = find_bin_dir()
    env = os.environ.copy()
    if 'PATH' in env:
        env['PATH'] = bin_dir + ':' + env['PATH']
    else:
        env['PATH'] = bin_dir
    return env

def get_text_section_info(elf_path, env):
    """Get .text section file offset and virtual address."""
    result = subprocess.run(['target-readelf', '-S', elf_path],
                          capture_output=True, text=True, env=env, check=True)
    
    for line in result.stdout.split('\n'):
        if '.text' in line and 'PROGBITS' in line:
            parts = line.split()
            try:
                for i, part in enumerate(parts):
                    if part == 'PROGBITS' and i + 2 < len(parts):
                        vaddr_str = parts[i+1].strip()
                        offset_str = parts[i+2].strip()
                        vaddr = int(vaddr_str, 16)
                        offset = int(offset_str, 16)
                        return (offset, vaddr)
            except (ValueError, IndexError) as e:
                print(f"Warning: Error parsing line: {line}, error: {e}")
                continue
    
    raise RuntimeError("Could not find .text section")

def find_symbol_address(elf_path, symbol_name, env, prefer_type=None):
    """Find the address of a symbol using target-nm.
    
    Args:
        elf_path: Path to ELF file
        symbol_name: Name of symbol to find
        env: Environment with PATH set
        prefer_type: Preferred symbol type ('T' for global, 't' for local/weak)
                    If None, returns first match. If specified, tries preferred type first.
    """
    result = subprocess.run(['target-nm', elf_path],
                          capture_output=True, text=True, env=env, check=True)
    
    # nm output format: <address> <type> <name>
    # Collect all matches first
    matches = []
    for line in result.stdout.split('\n'):
        parts = line.split()
        if len(parts) >= 3:
            addr_str, sym_type, name = parts[0], parts[1], ' '.join(parts[2:])
            if name == symbol_name and sym_type in 'Tt':
                try:
                    addr = int(addr_str, 16)
                    matches.append((addr, sym_type))
                except ValueError:
                    continue
    
    if not matches:
        raise RuntimeError(f"Could not find symbol: {symbol_name}")
    
    # If preference specified, try to find matching type first
    if prefer_type:
        for addr, sym_type in matches:
            if sym_type == prefer_type:
                return addr
    
    # Return first match (or only match if no preference)
    return matches[0][0]

def find_call_sites(elf_path, target_addr, env):
    """Find all call sites (bl instructions) that call the target address."""
    result = subprocess.run(['target-objdump', '-d', elf_path],
                          capture_output=True, text=True, env=env, check=True)
    
    call_sites = []
    # Pattern: <address>: <instruction> <target_addr> <symbol_name>
    # Example: "27c2a4:	97ffca93 	bl	26ecf0 <runtime.gcWriteBarrier2>"
    pattern = re.compile(r'^\s*([0-9a-f]+):\s+([0-9a-f]{8})\s+bl\s+([0-9a-f]+)')
    
    for line in result.stdout.split('\n'):
        match = pattern.match(line)
        if match:
            call_addr_str, insn_str, target_str = match.groups()
            call_addr = int(call_addr_str, 16)
            target = int(target_str, 16)
            if target == target_addr:
                call_sites.append((call_addr, int(insn_str, 16)))
    
    return call_sites

def encode_bl_instruction(pc, target):
    """Encode a bl (branch with link) instruction.
    
    bl encoding: 0x94000000 | ((target - pc) >> 2) & 0x3ffffff
    """
    offset = target - pc
    if offset % 4 != 0:
        raise ValueError(f"Target address must be 4-byte aligned (offset={offset})")
    if not (-0x8000000 <= offset <= 0x7ffffff):
        raise ValueError(f"Branch offset out of range: {offset} (max ±128MB)")
    
    imm26 = (offset >> 2) & 0x3ffffff
    return 0x94000000 | imm26

def patch_call_site(data, file_offset_base, vaddr_base, call_vaddr, old_target, new_target):
    """Patch a single call site to redirect from old_target to new_target."""
    # Calculate file offset for the call instruction
    file_offset = file_offset_base + (call_vaddr - vaddr_base)
    
    if file_offset < 0 or file_offset >= len(data):
        print(f"Warning: Invalid file offset {file_offset} for call at 0x{call_vaddr:x}")
        return False
    
    # Read current instruction
    current_insn = struct.unpack_from('<I', data, file_offset)[0]
    
    # Verify it's a bl instruction
    if (current_insn & 0xfc000000) != 0x94000000:
        print(f"Warning: Instruction at 0x{call_vaddr:x} is not a bl instruction: 0x{current_insn:x}")
        return False
    
    # Decode current target to verify
    imm26 = current_insn & 0x3ffffff
    # Sign extend 26-bit immediate
    if imm26 & 0x2000000:
        imm26 |= 0xfc000000  # Sign extend
    current_offset = imm26 << 2
    current_target = call_vaddr + current_offset
    
    if current_target != old_target:
        print(f"Warning: Call at 0x{call_vaddr:x} targets 0x{current_target:x}, expected 0x{old_target:x}")
        # Continue anyway - maybe we still want to patch it
    
    # Encode new instruction
    try:
        new_insn = encode_bl_instruction(call_vaddr, new_target)
    except ValueError as e:
        print(f"Error: Cannot encode bl instruction for call at 0x{call_vaddr:x}: {e}")
        return False
    
    # Write the new instruction
    struct.pack_into('<I', data, file_offset, new_insn)
    
    print(f"Patched call at 0x{call_vaddr:x} (file offset 0x{file_offset:x}): "
          f"0x{old_target:x} -> 0x{new_target:x}")
    return True

def patch_runtime(elf_path, replacements):
    """
    Patch ELF binary to redirect function calls.
    
    Args:
        elf_path: Path to ELF file to patch
        replacements: List of (old_func_name, new_func_name) tuples
    """
    env = setup_env()
    
    # Get .text section info
    file_offset_base, vaddr_base = get_text_section_info(elf_path, env)
    print(f"Found .text section: vaddr=0x{vaddr_base:x}, file_offset=0x{file_offset_base:x}")
    
    # Read ELF file
    with open(elf_path, 'rb') as f:
        data = bytearray(f.read())
    
    total_patches = 0
    
    # Process each replacement
    for old_func, new_func in replacements:
        print(f"\nProcessing replacement: {old_func} -> {new_func}")
        
        try:
            # Find addresses
            # Old function: prefer weak/local symbols (type 't') from Go runtime
            # New function: prefer strong/global symbols (type 'T') from our code
            old_addr = find_symbol_address(elf_path, old_func, env, prefer_type='t')
            new_addr = find_symbol_address(elf_path, new_func, env, prefer_type='T')
            print(f"  Old function address: 0x{old_addr:x}")
            print(f"  New function address: 0x{new_addr:x}")
            
            # Find all call sites
            call_sites = find_call_sites(elf_path, old_addr, env)
            print(f"  Found {len(call_sites)} call site(s)")
            
            if not call_sites:
                print(f"  Warning: No call sites found for {old_func}")
                continue
            
            # Patch each call site
            for call_vaddr, _ in call_sites:
                if patch_call_site(data, file_offset_base, vaddr_base, call_vaddr, old_addr, new_addr):
                    total_patches += 1
        
        except RuntimeError as e:
            print(f"  Error: {e}")
            continue
    
    # Write patched file
    if total_patches > 0:
        with open(elf_path, 'wb') as f:
            f.write(data)
        print(f"\nSuccessfully patched {total_patches} call site(s)")
        return True
    else:
        print("\nNo patches applied")
        return False

def main():
    if len(sys.argv) < 4 or len(sys.argv) % 2 != 0:
        print("Usage: patch_runtime.py <elf_file> <old_func1> <new_func1> [<old_func2> <new_func2> ...]")
        print("\nExample:")
        print("  patch_runtime.py kernel.elf runtime.gcWriteBarrier2 runtime.gcWriteBarrier2")
        sys.exit(1)
    
    elf_path = sys.argv[1]
    replacements = []
    
    # Parse function pairs
    for i in range(2, len(sys.argv), 2):
        old_func = sys.argv[i]
        new_func = sys.argv[i + 1]
        replacements.append((old_func, new_func))
    
    if patch_runtime(elf_path, replacements):
        sys.exit(0)
    else:
        sys.exit(1)

if __name__ == '__main__':
    main()

