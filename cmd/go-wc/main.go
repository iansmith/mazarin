// go-wc counts lines, words, and bytes in files, similar to wc.
//
// Usage:
//
//	go tool go-wc [-l] [-w] [-c] [file...]
//
// With no file, reads from stdin.
//
// Flags:
//
//	-l    Count lines only
//	-w    Count words only
//	-c    Count bytes only
//
// If no flags specified, shows lines, words, and bytes.
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
	lines := flag.Bool("l", false, "count lines")
	words := flag.Bool("w", false, "count words")
	bytes := flag.Bool("c", false, "count bytes")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-wc [-l] [-w] [-c] [file...]\n")
		fmt.Fprintf(os.Stderr, "Count lines, words, and bytes.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// If no flags, show all
	showAll := !*lines && !*words && !*bytes

	files := flag.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	var totalLines, totalWords, totalBytes int64
	exitCode := 0

	for _, file := range files {
		var r io.Reader
		if file == "-" {
			r = os.Stdin
		} else {
			f, err := os.Open(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "go-wc: %v\n", err)
				exitCode = 1
				continue
			}
			defer f.Close()
			r = f
		}

		l, w, b := count(r)
		totalLines += l
		totalWords += w
		totalBytes += b

		printCounts(l, w, b, file, *lines, *words, *bytes, showAll)
	}

	if len(files) > 1 {
		printCounts(totalLines, totalWords, totalBytes, "total", *lines, *words, *bytes, showAll)
	}

	os.Exit(exitCode)
}

func count(r io.Reader) (lines, words, bytes int64) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		lines++
		bytes += int64(len(line)) + 1 // +1 for newline
		words += int64(len(strings.Fields(line)))
	}
	return
}

func printCounts(l, w, b int64, name string, showLines, showWords, showBytes, showAll bool) {
	var parts []string
	if showAll || showLines {
		parts = append(parts, fmt.Sprintf("%8d", l))
	}
	if showAll || showWords {
		parts = append(parts, fmt.Sprintf("%8d", w))
	}
	if showAll || showBytes {
		parts = append(parts, fmt.Sprintf("%8d", b))
	}
	if name != "-" {
		parts = append(parts, name)
	}
	fmt.Println(strings.Join(parts, " "))
}
