#!/usr/bin/env python3
"""Analyze the distribution of 1s, 2s, and 3s in the preemption test output."""

import sys
import re

def main():
    # Read from stdin (piped from safe-serial-read)
    content = sys.stdin.read()

    # Find the preemption test section
    if 'Starting preemption test' in content:
        idx = content.rfind('Starting preemption test')
        test_output = content[idx:]

        # Get just the numeric output by finding sequences of 1s, 2s, and 3s
        # Look for lines or sequences with mostly these digits
        digit_chars = []
        in_digit_seq = False

        for char in test_output:
            if char in '123':
                digit_chars.append(char)
                in_digit_seq = True
            elif in_digit_seq and char in '\n\r\t ':
                # Allow whitespace within digit sequences
                continue
            elif in_digit_seq:
                # End of digit sequence
                in_digit_seq = False

        # Count the digits
        digit_str = ''.join(digit_chars)
        ones = digit_str.count('1')
        twos = digit_str.count('2')
        threes = digit_str.count('3')
        total = ones + twos + threes

        print(f'=== Preemption Test Output Analysis ===')
        print(f'After "Starting preemption test"...\n')
        print(f'Total digits found: {total}')
        print(f'  1s: {ones:6d} ({100*ones/total if total > 0 else 0:5.1f}%)')
        print(f'  2s: {twos:6d} ({100*twos/total if total > 0 else 0:5.1f}%)')
        print(f'  3s: {threes:6d} ({100*threes/total if total > 0 else 0:5.1f}%)')
        print(f'\nExpected for fair scheduling:')
        print(f'  1s: ~25% (kernel goroutine 1)')
        print(f'  2s: ~25% (kernel goroutine 2)')
        print(f'  3s: ~50% (userspace thread)')

        # Show a sample of the digit sequence
        if len(digit_str) > 0:
            print(f'\nFirst 200 digits: {digit_str[:200]}')
            print(f'Last 200 digits: {digit_str[-200:]}')
        else:
            print('\nNo digit sequence found!')
            print(f'\nLast 500 chars of output:\n{test_output[-500:]}')
    else:
        print('ERROR: "Starting preemption test" not found in output')
        print(f'\nLast 1000 chars:\n{content[-1000:]}')

if __name__ == '__main__':
    main()
