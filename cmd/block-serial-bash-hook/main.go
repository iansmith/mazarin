// block-serial-bash-hook is a Claude Code PreToolUse hook that blocks
// dangerous Bash commands on /tmp/cardinal-serial.log.
//
// Commands like cat, head, tail, less on this file will freeze Claude Code
// because the file can contain lines millions of characters long.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const blockedFile = "/tmp/cardinal-serial.log"

var dangerousCommands = []string{"cat", "head", "tail", "less", "more", "vi", "vim", "nano", "read"}

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

	// Only check Bash tool
	if input.ToolName != "Bash" {
		os.Exit(0)
	}

	command, ok := input.ToolInput["command"].(string)
	if !ok {
		os.Exit(0)
	}

	// Check for dangerous patterns
	if isDangerous(command) {
		truncated := command
		if len(truncated) > 200 {
			truncated = truncated[:200] + "..."
		}
		fmt.Fprintf(os.Stderr, `BLOCKED: Dangerous command on %s!

This file often contains lines millions of characters long that will freeze your session.

Use the safe reader instead:
    go tool safe-serial-read

Command was: %s
`, blockedFile, truncated)
		os.Exit(2) // Exit code 2 = block
	}

	// Allow all other commands
	os.Exit(0)
}

func isDangerous(command string) bool {
	// Build pattern for dangerous commands
	// Matches: cat /tmp/cardinal-serial.log, cat < /tmp/cardinal-serial.log, etc.
	cmdPattern := `\b(` + strings.Join(dangerousCommands, "|") + `)\s+[^|]*` + regexp.QuoteMeta(blockedFile)
	if matched, _ := regexp.MatchString("(?i)"+cmdPattern, command); matched {
		return true
	}

	// Input redirection: < /tmp/cardinal-serial.log
	redirectPattern := `<\s*` + regexp.QuoteMeta(blockedFile)
	if matched, _ := regexp.MatchString(redirectPattern, command); matched {
		return true
	}

	// File as pipe source (unlikely but possible)
	pipePattern := regexp.QuoteMeta(blockedFile) + `\s*\|`
	if matched, _ := regexp.MatchString(pipePattern, command); matched {
		return true
	}

	return false
}
