// go-cat concatenates and displays files, similar to cat.
//
// Usage:
//
//	go tool go-cat [file...]
//	go tool go-cat -o output file...
//	echo "content" | go tool go-cat -o output -
//
// With no file arguments, reads from stdin.
// Use "-" to explicitly read from stdin among other files.
//
// Flags:
//
//	-o file    Write output to file instead of stdout
//	-a         Append to output file (use with -o)
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	output := flag.String("o", "", "write output to file")
	appendMode := flag.Bool("a", false, "append to output file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-cat [file...]\n")
		fmt.Fprintf(os.Stderr, "       go-cat -o output file...\n")
		fmt.Fprintf(os.Stderr, "Concatenate files to stdout or to output file.\n\n")
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
			fmt.Fprintf(os.Stderr, "go-cat: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	// If no files specified, read from stdin
	files := flag.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	exitCode := 0
	for _, file := range files {
		var r io.Reader
		if file == "-" {
			r = os.Stdin
		} else {
			f, err := os.Open(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "go-cat: %v\n", err)
				exitCode = 1
				continue
			}
			defer f.Close()
			r = f
		}

		if _, err := io.Copy(out, r); err != nil {
			fmt.Fprintf(os.Stderr, "go-cat: %v\n", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
