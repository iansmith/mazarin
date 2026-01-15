#!/usr/bin/env python3
"""
Analyze /tmp/cardinal-serial.log to find problematic characters.
"""

import sys
from collections import Counter

def analyze_file(filepath):
    """Read file as bytes and analyze character distribution."""
    try:
        with open(filepath, 'rb') as f:
            data = f.read()
    except FileNotFoundError:
        print(f"File not found: {filepath}")
        return
    except Exception as e:
        print(f"Error reading file: {e}")
        return

    print(f"File size: {len(data)} bytes")
    print()

    # Count all bytes
    byte_counts = Counter(data)

    # Categorize characters
    printable_ascii = []  # 0x20-0x7E (space through tilde)
    newlines = []         # 0x0A (LF), 0x0D (CR)
    tabs = []             # 0x09 (TAB)
    null_bytes = []       # 0x00
    control_chars = []    # 0x01-0x08, 0x0B-0x0C, 0x0E-0x1F
    high_bytes = []       # 0x80-0xFF
    del_char = []         # 0x7F (DEL)

    for byte_val, count in sorted(byte_counts.items()):
        if byte_val == 0x00:
            null_bytes.append((byte_val, count))
        elif byte_val == 0x09:
            tabs.append((byte_val, count))
        elif byte_val == 0x0A or byte_val == 0x0D:
            newlines.append((byte_val, count))
        elif byte_val >= 0x20 and byte_val <= 0x7E:
            printable_ascii.append((byte_val, count))
        elif byte_val == 0x7F:
            del_char.append((byte_val, count))
        elif byte_val >= 0x80:
            high_bytes.append((byte_val, count))
        else:
            control_chars.append((byte_val, count))

    # Print summary
    print("=" * 60)
    print("CHARACTER CATEGORY SUMMARY")
    print("=" * 60)

    print(f"\nPrintable ASCII (0x20-0x7E): {sum(c for _, c in printable_ascii)} bytes")
    print(f"Newlines (LF/CR): {sum(c for _, c in newlines)} bytes")
    print(f"Tabs: {sum(c for _, c in tabs)} bytes")

    # Problematic categories
    print()
    print("=" * 60)
    print("POTENTIALLY PROBLEMATIC CHARACTERS")
    print("=" * 60)

    if null_bytes:
        print(f"\n*** NULL BYTES (0x00): {sum(c for _, c in null_bytes)} occurrences ***")
        print("    These can truncate strings in many tools!")

    if control_chars:
        print(f"\n*** CONTROL CHARACTERS: {sum(c for _, c in control_chars)} occurrences ***")
        for byte_val, count in control_chars:
            name = get_control_name(byte_val)
            print(f"    0x{byte_val:02X} ({name}): {count} times")

    if del_char:
        print(f"\n*** DEL CHARACTER (0x7F): {sum(c for _, c in del_char)} occurrences ***")

    if high_bytes:
        print(f"\n*** HIGH BYTES (0x80-0xFF): {sum(c for _, c in high_bytes)} occurrences ***")
        print("    These are non-ASCII and may be garbage/uninitialized memory")
        for byte_val, count in high_bytes:
            print(f"    0x{byte_val:02X}: {count} times")

    # Look for ANSI escape sequences
    print()
    print("=" * 60)
    print("ANSI ESCAPE SEQUENCE ANALYSIS")
    print("=" * 60)

    esc_count = byte_counts.get(0x1B, 0)
    if esc_count:
        print(f"\nESC (0x1B) found {esc_count} times - likely terminal control sequences")
        # Find escape sequences
        find_escape_sequences(data)
    else:
        print("\nNo ESC characters found.")

    # Show context around problematic bytes
    print()
    print("=" * 60)
    print("SAMPLE CONTEXTS OF PROBLEMATIC BYTES")
    print("=" * 60)

    show_contexts(data, null_bytes, "NULL")
    show_contexts(data, control_chars, "CONTROL")
    show_contexts(data, high_bytes, "HIGH BYTE")


def get_control_name(byte_val):
    """Get human-readable name for control characters."""
    names = {
        0x00: "NUL", 0x01: "SOH", 0x02: "STX", 0x03: "ETX",
        0x04: "EOT", 0x05: "ENQ", 0x06: "ACK", 0x07: "BEL",
        0x08: "BS",  0x09: "TAB", 0x0A: "LF",  0x0B: "VT",
        0x0C: "FF",  0x0D: "CR",  0x0E: "SO",  0x0F: "SI",
        0x10: "DLE", 0x11: "DC1", 0x12: "DC2", 0x13: "DC3",
        0x14: "DC4", 0x15: "NAK", 0x16: "SYN", 0x17: "ETB",
        0x18: "CAN", 0x19: "EM",  0x1A: "SUB", 0x1B: "ESC",
        0x1C: "FS",  0x1D: "GS",  0x1E: "RS",  0x1F: "US",
        0x7F: "DEL"
    }
    return names.get(byte_val, f"CTRL-{byte_val}")


def find_escape_sequences(data):
    """Find and categorize ANSI escape sequences."""
    sequences = Counter()
    i = 0
    while i < len(data):
        if data[i] == 0x1B:  # ESC
            # Try to capture the sequence
            seq_start = i
            i += 1
            if i < len(data):
                if data[i] == ord('['):  # CSI sequence
                    i += 1
                    while i < len(data) and (0x30 <= data[i] <= 0x3F or data[i] == ord(';')):
                        i += 1
                    while i < len(data) and 0x20 <= data[i] <= 0x2F:
                        i += 1
                    if i < len(data) and 0x40 <= data[i] <= 0x7E:
                        i += 1
                    seq = data[seq_start:i]
                    sequences[bytes(seq)] += 1
                elif data[i] == ord('(') or data[i] == ord(')'):  # Character set
                    i += 2
                    seq = data[seq_start:i]
                    sequences[bytes(seq)] += 1
                else:
                    seq = data[seq_start:i+1]
                    sequences[bytes(seq)] += 1
                    i += 1
        else:
            i += 1

    if sequences:
        print("\nFound escape sequences:")
        for seq, count in sequences.most_common(20):
            readable = seq.decode('latin-1').replace('\x1b', 'ESC')
            hex_str = ' '.join(f'{b:02X}' for b in seq)
            desc = describe_escape_sequence(seq)
            print(f"    {hex_str:30s} ({count:4d}x) - {desc}")


def describe_escape_sequence(seq):
    """Describe what an escape sequence does."""
    if len(seq) < 2:
        return "incomplete"

    if seq[1:2] == b'[':
        # CSI sequence
        if seq.endswith(b'm'):
            return "SGR (color/style)"
        elif seq.endswith(b'H') or seq.endswith(b'f'):
            return "Cursor position"
        elif seq.endswith(b'J'):
            return "Erase display"
        elif seq.endswith(b'K'):
            return "Erase line"
        elif seq.endswith(b'A'):
            return "Cursor up"
        elif seq.endswith(b'B'):
            return "Cursor down"
        elif seq.endswith(b'C'):
            return "Cursor forward"
        elif seq.endswith(b'D'):
            return "Cursor back"
        elif seq.endswith(b'?h') or b'?25h' in seq:
            return "Show cursor"
        elif seq.endswith(b'?l') or b'?25l' in seq:
            return "Hide cursor"
        elif seq.endswith(b'r'):
            return "Set scroll region"
        return f"CSI command '{chr(seq[-1])}'"
    elif seq[1:2] == b'(':
        return "Set G0 character set"
    elif seq[1:2] == b')':
        return "Set G1 character set"

    return "unknown"


def show_contexts(data, byte_list, label):
    """Show context around problematic bytes."""
    if not byte_list:
        return

    print(f"\n{label} byte contexts (first 3 occurrences each):")

    for byte_val, count in byte_list[:5]:  # Only first 5 byte types
        occurrences = 0
        for i, b in enumerate(data):
            if b == byte_val:
                occurrences += 1
                if occurrences <= 3:
                    start = max(0, i - 10)
                    end = min(len(data), i + 10)
                    context = data[start:end]
                    # Make it safe to print
                    safe_context = ''.join(
                        chr(c) if 0x20 <= c <= 0x7E else f'<{c:02X}>'
                        for c in context
                    )
                    pos_in_context = i - start
                    print(f"    At offset {i}: ...{safe_context}...")
                if occurrences >= 3:
                    break


def create_clean_file(input_path, output_path):
    """Create a cleaned version of the file."""
    with open(input_path, 'rb') as f:
        data = f.read()

    # Remove problematic bytes
    cleaned = bytearray()
    i = 0
    while i < len(data):
        b = data[i]

        # Skip escape sequences entirely
        if b == 0x1B and i + 1 < len(data):
            # Skip ESC and following sequence
            i += 1
            if data[i] == ord('['):
                i += 1
                while i < len(data) and (0x30 <= data[i] <= 0x3F or data[i] == ord(';')):
                    i += 1
                while i < len(data) and 0x20 <= data[i] <= 0x2F:
                    i += 1
                if i < len(data) and 0x40 <= data[i] <= 0x7E:
                    i += 1
            elif i < len(data):
                i += 1  # Skip one more char after ESC
            continue

        # Keep printable ASCII, newlines, tabs
        if b == 0x0A or b == 0x0D or b == 0x09 or (0x20 <= b <= 0x7E):
            cleaned.append(b)
        # Replace others with space to preserve structure
        elif b == 0x00:
            pass  # Skip nulls entirely
        else:
            cleaned.append(ord(' '))

        i += 1

    with open(output_path, 'wb') as f:
        f.write(cleaned)

    print(f"\nCleaned file written to: {output_path}")
    print(f"Original size: {len(data)}, Cleaned size: {len(cleaned)}")


if __name__ == '__main__':
    filepath = sys.argv[1] if len(sys.argv) > 1 else '/tmp/cardinal-serial.log'

    analyze_file(filepath)

    # Also create a clean version
    if len(sys.argv) > 2:
        output_path = sys.argv[2]
    else:
        output_path = filepath + '.clean'

    print()
    print("=" * 60)
    print("CREATING CLEANED VERSION")
    print("=" * 60)
    create_clean_file(filepath, output_path)
