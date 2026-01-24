// safe-serial-read reads /tmp/cardinal-serial.log safely, detecting and
// truncating infinite loops that could crash tools or freeze terminals.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	filepath := "/tmp/cardinal-serial.log"
	maxBytes := 100000

	if len(os.Args) > 1 {
		filepath = os.Args[1]
	}
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			maxBytes = n
		}
	}

	content := safeRead(filepath, maxBytes)
	if content != "" {
		fmt.Print(content)
	}
}

func safeRead(filepath string, maxBytes int) string {
	f, err := os.Open(filepath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "File not found: %s\n", filepath)
		return ""
	}
	defer f.Close()

	data := make([]byte, maxBytes)
	n, err := io.ReadFull(f, data)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		return ""
	}
	data = data[:n]

	// Detect common infinite loop patterns
	truncateAt := detectLoop(data)
	if truncateAt < len(data) {
		fmt.Fprintf(os.Stderr, "[Detected infinite loop at byte %d, truncating]\n", truncateAt)
		data = data[:truncateAt]
	}

	// Convert to string, replacing invalid UTF-8 with replacement char
	return sanitizeUTF8(data)
}

// detectLoop looks for repetitive patterns that indicate an infinite loop
func detectLoop(data []byte) int {
	// Pattern 1: (62)(62)(62) - common timer output in infinite loop
	if idx := bytes.Index(data, []byte("(62)(62)(62)")); idx >= 0 {
		return idx
	}

	// Pattern 2: (00)(00)(00) - null byte spam
	if idx := bytes.Index(data, []byte("(00)(00)(00)")); idx >= 0 {
		return idx
	}

	// Pattern 3: Same character repeated 50+ times in a row
	if idx := findRepeatedChar(data, 50); idx >= 0 {
		return idx
	}

	return len(data)
}

// findRepeatedChar finds the first occurrence of any character repeated n+ times
func findRepeatedChar(data []byte, minRepeat int) int {
	if len(data) < minRepeat {
		return -1
	}

	count := 1
	for i := 1; i < len(data); i++ {
		if data[i] == data[i-1] {
			count++
			if count >= minRepeat {
				return i - minRepeat + 1
			}
		} else {
			count = 1
		}
	}
	return -1
}

// sanitizeUTF8 converts bytes to string, replacing invalid UTF-8 sequences
func sanitizeUTF8(data []byte) string {
	// Fast path: if valid UTF-8, return directly
	if isValidUTF8(data) {
		return string(data)
	}

	// Slow path: replace invalid bytes
	var result bytes.Buffer
	for i := 0; i < len(data); {
		r, size := decodeRune(data[i:])
		if r == '\uFFFD' && size == 1 {
			// Invalid byte - replace with ?
			result.WriteByte('?')
			i++
		} else {
			result.Write(data[i : i+size])
			i += size
		}
	}
	return result.String()
}

// isValidUTF8 checks if data is valid UTF-8
func isValidUTF8(data []byte) bool {
	for i := 0; i < len(data); {
		if data[i] < 0x80 {
			i++
			continue
		}
		_, size := decodeRune(data[i:])
		if size == 1 {
			return false // Invalid sequence
		}
		i += size
	}
	return true
}

// decodeRune decodes the first UTF-8 rune from data
func decodeRune(data []byte) (r rune, size int) {
	if len(data) == 0 {
		return '\uFFFD', 0
	}

	b := data[0]
	if b < 0x80 {
		return rune(b), 1
	}

	// Multi-byte sequence
	var n int
	switch {
	case b&0xE0 == 0xC0:
		n = 2
	case b&0xF0 == 0xE0:
		n = 3
	case b&0xF8 == 0xF0:
		n = 4
	default:
		return '\uFFFD', 1 // Invalid start byte
	}

	if len(data) < n {
		return '\uFFFD', 1 // Truncated sequence
	}

	// Validate continuation bytes
	for i := 1; i < n; i++ {
		if data[i]&0xC0 != 0x80 {
			return '\uFFFD', 1 // Invalid continuation byte
		}
	}

	// Decode
	switch n {
	case 2:
		r = rune(b&0x1F)<<6 | rune(data[1]&0x3F)
	case 3:
		r = rune(b&0x0F)<<12 | rune(data[1]&0x3F)<<6 | rune(data[2]&0x3F)
	case 4:
		r = rune(b&0x07)<<18 | rune(data[1]&0x3F)<<12 | rune(data[2]&0x3F)<<6 | rune(data[3]&0x3F)
	}

	return r, n
}
