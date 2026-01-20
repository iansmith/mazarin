#!/usr/bin/env python3
"""
Claude Code PreToolUse hook to block dangerous Bash commands on /tmp/cardinal-serial.log.

Commands like cat, head, tail, less on this file will freeze Claude Code because
the file can contain lines millions of characters long.
"""
import json
import sys
import re

BLOCKED_FILE = "/tmp/cardinal-serial.log"
DANGEROUS_COMMANDS = ["cat", "head", "tail", "less", "more", "vi", "vim", "nano", "read"]

# Pattern to detect dangerous commands on the serial log
# Matches: cat /tmp/cardinal-serial.log, cat < /tmp/cardinal-serial.log, etc.
PATTERNS = [
    rf'\b({"|".join(DANGEROUS_COMMANDS)})\s+[^|]*{re.escape(BLOCKED_FILE)}',
    rf'<\s*{re.escape(BLOCKED_FILE)}',  # Input redirection
    rf'{re.escape(BLOCKED_FILE)}\s*\|',  # File as pipe source (unlikely but possible)
]

try:
    input_data = json.load(sys.stdin)
except json.JSONDecodeError:
    sys.exit(0)  # Allow if we can't parse

tool_name = input_data.get("tool_name", "")
command = input_data.get("tool_input", {}).get("command", "")

# Only check Bash tool
if tool_name != "Bash":
    sys.exit(0)

# Check for dangerous patterns
for pattern in PATTERNS:
    if re.search(pattern, command, re.IGNORECASE):
        print(f"""BLOCKED: Dangerous command on {BLOCKED_FILE}!

This file often contains lines millions of characters long that will freeze your session.

Use the safe reader instead:
    python3 tools/safe-serial-read.py

Or use the compiled tool:
    build/tools/safe-serial-read

Command was: {command[:200]}{'...' if len(command) > 200 else ''}
""", file=sys.stderr)
        sys.exit(2)  # Exit code 2 = block

# Allow all other commands
sys.exit(0)
