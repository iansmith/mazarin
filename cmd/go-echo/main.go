// go-echo prints arguments to stdout, similar to echo.
//
// Usage:
//
//	go tool go-echo [-n] [-e] [string...]
//	go tool go-echo -o file [-a] string...
//
// Flags:
//
//	-n         Do not output trailing newline
//	-e         Enable interpretation of backslash escapes
//	-o file    Write to file instead of stdout
//	-a         Append to file (use with -o)
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	noNewline := flag.Bool("n", false, "do not output trailing newline")
	escapes := flag.Bool("e", false, "enable interpretation of backslash escapes")
	output := flag.String("o", "", "write to file instead of stdout")
	appendMode := flag.Bool("a", false, "append to file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-echo [-n] [-e] [string...]\n")
		fmt.Fprintf(os.Stderr, "       go-echo -o file [-a] string...\n")
		fmt.Fprintf(os.Stderr, "Print arguments to stdout or file.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Determine output destination
	var out io.Writer = os.Stdout
	if *output != "" {
		flags := os.O_CREATE | os.O_WRONLY
		if *appendMode {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		f, err := os.OpenFile(*output, flags, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-echo: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	// Join arguments with spaces
	text := strings.Join(flag.Args(), " ")

	// Process escape sequences if -e
	if *escapes {
		text = processEscapes(text)
	}

	// Output
	if *noNewline {
		fmt.Fprint(out, text)
	} else {
		fmt.Fprintln(out, text)
	}
}

// processEscapes handles common backslash escape sequences
func processEscapes(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				result.WriteByte('\n')
				i += 2
			case 't':
				result.WriteByte('\t')
				i += 2
			case 'r':
				result.WriteByte('\r')
				i += 2
			case '\\':
				result.WriteByte('\\')
				i += 2
			case '0':
				// Octal escape \0nnn
				if i+4 <= len(s) {
					if v, err := strconv.ParseInt(s[i+2:i+4], 8, 8); err == nil {
						result.WriteByte(byte(v))
						i += 4
						continue
					}
				}
				result.WriteByte('\x00')
				i += 2
			default:
				result.WriteByte(s[i])
				i++
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}
