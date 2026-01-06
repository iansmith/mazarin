// buffer.go - Parameter buffer format and parsing
//
// Cardinal uses a text-based parameter buffer to pass boot-time information
// to Kmazarin. The buffer format is simple and human-readable:
//
//   KEY value\n
//   # comment\n
//   KEY2 value2\n
//   \0 (null terminator)
//
// The buffer is allocated by Cardinal and passed to Kmazarin via a custom
// auxv entry (AT_CARDINAL_PARAMS).

package param

import (
	"strconv"
	"strings"
)

// ============================================================================
// Buffer Format
// ============================================================================
// Format: KEY value\n
// Comments: Lines starting with # are ignored
// Termination: Null byte (\0) terminates the buffer

// ============================================================================
// Writing (Cardinal side)
// ============================================================================

// WriteParam writes a key-value pair to the buffer at the given offset.
// Returns the number of bytes written (including newline).
// Format: "KEY value\n"
func WriteParam(buf []byte, key string, value uint64) int {
	// Format: "KEY 0xVALUE\n"
	line := key + " 0x" + strconv.FormatUint(value, 16) + "\n"
	copy(buf, []byte(line))
	return len(line)
}

// WriteParamString writes a key-value pair with a string value.
// Returns the number of bytes written (including newline).
// Format: "KEY value\n"
func WriteParamString(buf []byte, key string, value string) int {
	line := key + " " + value + "\n"
	copy(buf, []byte(line))
	return len(line)
}

// WriteComment writes a comment line to the buffer.
// Returns the number of bytes written (including newline).
// Format: "# comment\n"
func WriteComment(buf []byte, comment string) int {
	line := "# " + comment + "\n"
	copy(buf, []byte(line))
	return len(line)
}

// TerminateBuffer writes a null terminator at the given offset.
// Returns 1 (the number of bytes written).
func TerminateBuffer(buf []byte) int {
	buf[0] = 0
	return 1
}

// ============================================================================
// Reading (Kmazarin side)
// ============================================================================

// ParseParamBuffer parses the parameter buffer and returns a map of key-value pairs.
// Lines starting with # are treated as comments and ignored.
// The buffer is expected to be null-terminated.
func ParseParamBuffer(buf []byte, length int) map[string]string {
	params := make(map[string]string)

	// Find the null terminator (if length includes it)
	endIdx := length
	for i := 0; i < length; i++ {
		if buf[i] == 0 {
			endIdx = i
			break
		}
	}

	// Convert to string and split by newlines
	content := string(buf[:endIdx])
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse "KEY value" format
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			params[key] = value
		}
	}

	return params
}

// GetParamUint64 retrieves a parameter value as uint64.
// Supports hex (0x prefix) and decimal formats.
// Returns (value, true) if found and parseable, (0, false) otherwise.
func GetParamUint64(params map[string]string, key string) (uint64, bool) {
	valueStr, ok := params[key]
	if !ok {
		return 0, false
	}

	// Try hex first (0x prefix)
	if strings.HasPrefix(valueStr, "0x") || strings.HasPrefix(valueStr, "0X") {
		val, err := strconv.ParseUint(valueStr[2:], 16, 64)
		if err != nil {
			return 0, false
		}
		return val, true
	}

	// Try decimal
	val, err := strconv.ParseUint(valueStr, 10, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

// GetParamString retrieves a parameter value as a string.
// Returns (value, true) if found, ("", false) otherwise.
func GetParamString(params map[string]string, key string) (string, bool) {
	value, ok := params[key]
	return value, ok
}
