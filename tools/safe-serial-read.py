#!/usr/bin/env python3
"""
Safely read /tmp/cardinal-serial.log, detecting and truncating infinite loops.
"""

import sys
import re

def safe_read(filepath, max_bytes=100000):
    """Read serial log, detecting repetition loops."""
    try:
        with open(filepath, 'rb') as f:
            data = f.read(max_bytes)
    except FileNotFoundError:
        print(f"File not found: {filepath}")
        return None

    # Detect common infinite loop patterns
    loop_patterns = [
        rb'\(62\)\(62\)\(62\)',  # (62)(62)(62)
        rb'\(00\)\(00\)\(00\)',  # (00)(00)(00)
        rb'(.)\1{50,}',          # Same char 50+ times
    ]

    truncate_at = len(data)
    for pattern in loop_patterns:
        match = re.search(pattern, data)
        if match:
            truncate_at = min(truncate_at, match.start())

    if truncate_at < len(data):
        print(f"[Detected infinite loop at byte {truncate_at}, truncating]")
        data = data[:truncate_at]

    # Decode safely
    try:
        text = data.decode('utf-8', errors='replace')
    except:
        text = data.decode('latin-1')

    return text


if __name__ == '__main__':
    filepath = sys.argv[1] if len(sys.argv) > 1 else '/tmp/cardinal-serial.log'
    max_bytes = int(sys.argv[2]) if len(sys.argv) > 2 else 100000

    content = safe_read(filepath, max_bytes)
    if content:
        print(content)
