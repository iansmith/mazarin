#!/usr/bin/env python3
"""
Filter /tmp/cardinal-serial.log:
1. Truncate repetitive patterns early
2. Break into ~80 character lines
"""

import sys

def filter_log(input_path, output_path, line_width=80, max_repeats=1000):
    """Filter and format serial log."""

    with open(input_path, 'rb') as f:
        data = f.read()

    print(f"Input size: {len(data):,} bytes")

    # First pass: detect and truncate repetitive patterns
    # Look for (XX)(XX)(XX)... patterns
    result = bytearray()
    i = 0
    n = len(data)

    while i < n:
        # Check for (XX) pattern start
        if (i + 4 <= n and data[i] == ord('(') and data[i+3] == ord(')') and
            all(chr(data[i+j]) in '0123456789ABCDEFabcdef' for j in [1, 2])):

            # Found a (XX) pattern, check for repetition
            pattern = data[i:i+4]
            repeat_count = 0
            j = i

            while j + 4 <= n and data[j:j+4] == pattern:
                repeat_count += 1
                j += 4

            if repeat_count > max_repeats:
                # Truncate: keep first max_repeats, add message
                result.extend(pattern * max_repeats)
                msg = f"[...({pattern[1:3].decode()}) repeated {repeat_count - max_repeats} more times, truncated]\n"
                result.extend(msg.encode())
                i = j
            else:
                result.extend(data[i:j])
                i = j
        else:
            result.append(data[i])
            i += 1

    text = result.decode('latin-1')
    print(f"After truncation: {len(text):,} chars")

    # Second pass: break into lines
    output_lines = []

    for line in text.split('\n'):
        if len(line) <= line_width:
            output_lines.append(line)
            continue

        # Break long lines at good points
        pos = 0
        while pos < len(line):
            end = min(pos + line_width, len(line))
            chunk = line[pos:end]

            if end < len(line) and len(chunk) == line_width:
                # Look for good break points
                for bc in ['><', '][', 'BS', '|']:
                    idx = chunk.rfind(bc)
                    if idx > line_width // 2:
                        chunk = chunk[:idx + len(bc)]
                        break

            output_lines.append(chunk)
            pos += len(chunk)

    output_text = '\n'.join(output_lines)

    with open(output_path, 'w') as f:
        f.write(output_text)

    print(f"Output size: {len(output_text):,} bytes")
    print(f"Output lines: {len(output_lines):,}")
    print(f"Written to: {output_path}")


if __name__ == '__main__':
    input_path = sys.argv[1] if len(sys.argv) > 1 else '/tmp/cardinal-serial.log'
    output_path = sys.argv[2] if len(sys.argv) > 2 else '/tmp/cardinal-serial-filtered.log'

    filter_log(input_path, output_path)
