// go-test evaluates conditional expressions, similar to test or [.
//
// Usage:
//
//	go tool go-test -d path    # True if path is a directory
//	go tool go-test -f path    # True if path is a regular file
//	go tool go-test -e path    # True if path exists
//	go tool go-test -z string  # True if string is empty
//	go tool go-test -n string  # True if string is non-empty
//	go tool go-test s1 = s2    # True if strings are equal
//	go tool go-test s1 != s2   # True if strings are not equal
//
// Exit codes:
//
//	0 - Expression is true
//	1 - Expression is false
//	2 - Error
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	isDir := flag.Bool("d", false, "true if path is a directory")
	isFile := flag.Bool("f", false, "true if path is a regular file")
	exists := flag.Bool("e", false, "true if path exists")
	isEmpty := flag.Bool("z", false, "true if string is empty")
	isNonEmpty := flag.Bool("n", false, "true if string is non-empty")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-test [option] argument\n")
		fmt.Fprintf(os.Stderr, "       go-test string1 = string2\n")
		fmt.Fprintf(os.Stderr, "       go-test string1 != string2\n")
		fmt.Fprintf(os.Stderr, "Evaluate conditional expressions.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()

	// String comparison: s1 = s2 or s1 != s2
	if len(args) == 3 {
		switch args[1] {
		case "=":
			if args[0] == args[2] {
				os.Exit(0)
			}
			os.Exit(1)
		case "!=":
			if args[0] != args[2] {
				os.Exit(0)
			}
			os.Exit(1)
		}
	}

	// Unary tests
	if len(args) != 1 {
		flag.Usage()
		os.Exit(2)
	}

	arg := args[0]

	if *isDir {
		info, err := os.Stat(arg)
		if err != nil || !info.IsDir() {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *isFile {
		info, err := os.Stat(arg)
		if err != nil || !info.Mode().IsRegular() {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *exists {
		if _, err := os.Stat(arg); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *isEmpty {
		if arg == "" {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if *isNonEmpty {
		if arg != "" {
			os.Exit(0)
		}
		os.Exit(1)
	}

	// No flag specified, treat as -n (non-empty test)
	if arg != "" {
		os.Exit(0)
	}
	os.Exit(1)
}
