// block-serial-read-hook is a Claude Code PreToolUse hook that blocks
// direct reads of /tmp/cardinal-serial.log.
//
// This file can contain lines millions of characters long and terminal control
// sequences that will freeze Claude Code. ALWAYS use the safe reader instead.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const blockedFile = "/tmp/cardinal-serial.log"

type ToolInput struct {
	ToolName  string                 `json:"tool_name"`
	ToolInput map[string]interface{} `json:"tool_input"`
}

func main() {
	var input ToolInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		// Allow if we can't parse
		os.Exit(0)
	}

	// Only check Read tool
	if input.ToolName != "Read" {
		os.Exit(0)
	}

	filePath, ok := input.ToolInput["file_path"].(string)
	if !ok {
		os.Exit(0)
	}

	// Block the dangerous file
	if filePath == blockedFile || strings.HasSuffix(filePath, "cardinal-serial.log") {
		fmt.Fprintf(os.Stderr, `BLOCKED: Cannot read %s directly!

This file often contains lines millions of characters long that will freeze your session.

Use the safe reader instead:
    go tool safe-serial-read
`, filePath)
		os.Exit(2) // Exit code 2 = block
	}

	// Allow all other reads
	os.Exit(0)
}
