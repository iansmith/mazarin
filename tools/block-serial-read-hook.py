#!/usr/bin/env python3
"""
Claude Code PreToolUse hook to block direct reads of /tmp/cardinal-serial.log.

This file can contain lines millions of characters long and terminal control
sequences that will freeze Claude Code. ALWAYS use the safe reader instead.
"""
import json
import sys

BLOCKED_FILE = "/tmp/cardinal-serial.log"
SAFE_READER = "python3 tools/safe-serial-read.py"

try:
    input_data = json.load(sys.stdin)
except json.JSONDecodeError:
    sys.exit(0)  # Allow if we can't parse

tool_name = input_data.get("tool_name", "")
file_path = input_data.get("tool_input", {}).get("file_path", "")

# Only check Read tool
if tool_name != "Read":
    sys.exit(0)

# Block the dangerous file
if file_path == BLOCKED_FILE or file_path.endswith("cardinal-serial.log"):
    print(f"""BLOCKED: Cannot read {file_path} directly!

This file often contains lines millions of characters long that will freeze your session.

Use the safe reader instead:
    python3 tools/safe-serial-read.py

Or use the compiled tool:
    build/tools/safe-serial-read
""", file=sys.stderr)
    sys.exit(2)  # Exit code 2 = block

# Allow all other reads
sys.exit(0)
