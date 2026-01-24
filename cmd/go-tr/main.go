// go-tr translates or deletes characters, similar to tr.
//
// Usage:
//
//	go tool go-tr [-d] set1 [set2]
//	echo "hello" | go tool go-tr a-z A-Z
//	echo "hello123" | go tool go-tr -d 0-9
//
// Flags:
//
//	-d    Delete characters in set1 (don't translate)
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	delete := flag.Bool("d", false, "delete characters in set1")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-tr [-d] set1 [set2]\n")
		fmt.Fprintf(os.Stderr, "Translate or delete characters.\n\n")
		fmt.Fprintf(os.Stderr, "Set syntax:\n")
		fmt.Fprintf(os.Stderr, "  a-z     Range from a to z\n")
		fmt.Fprintf(os.Stderr, "  abc     Literal characters a, b, c\n")
		fmt.Fprintf(os.Stderr, "  \\n\\t\\r  Escape sequences\n")
		fmt.Fprintf(os.Stderr, "  \\000-\\037  Octal ranges\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	set1 := expandSet(args[0])
	var set2 string
	if len(args) >= 2 {
		set2 = expandSet(args[1])
	}

	if *delete {
		deleteChars(os.Stdin, os.Stdout, set1)
	} else {
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "go-tr: translation requires two sets\n")
			os.Exit(1)
		}
		translate(os.Stdin, os.Stdout, set1, set2)
	}
}

func expandSet(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			// Handle escape sequences
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
			case '0', '1', '2', '3', '4', '5', '6', '7':
				// Octal escape
				val := 0
				j := i + 1
				for j < len(s) && j < i+4 && s[j] >= '0' && s[j] <= '7' {
					val = val*8 + int(s[j]-'0')
					j++
				}
				result.WriteByte(byte(val))
				i = j
			default:
				result.WriteByte(s[i+1])
				i += 2
			}
		} else if i+2 < len(s) && s[i+1] == '-' {
			// Range like a-z
			start := s[i]
			end := s[i+2]
			for c := start; c <= end; c++ {
				result.WriteByte(c)
			}
			i += 3
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

func deleteChars(r io.Reader, w io.Writer, set string) {
	deleteSet := make(map[byte]bool)
	for i := 0; i < len(set); i++ {
		deleteSet[set[i]] = true
	}

	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	for {
		b, err := reader.ReadByte()
		if err != nil {
			break
		}
		if !deleteSet[b] {
			writer.WriteByte(b)
		}
	}
}

func translate(r io.Reader, w io.Writer, set1, set2 string) {
	transMap := make(map[byte]byte)
	for i := 0; i < len(set1); i++ {
		if i < len(set2) {
			transMap[set1[i]] = set2[i]
		} else if len(set2) > 0 {
			// If set2 is shorter, use last char of set2
			transMap[set1[i]] = set2[len(set2)-1]
		}
	}

	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	for {
		b, err := reader.ReadByte()
		if err != nil {
			break
		}
		if replacement, ok := transMap[b]; ok {
			writer.WriteByte(replacement)
		} else {
			writer.WriteByte(b)
		}
	}
}
