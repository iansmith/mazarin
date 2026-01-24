// go-mkdir creates directories, similar to mkdir -p.
//
// Usage:
//
//	go tool go-mkdir [-p] dir [dir...]
//
// Flags:
//
//	-p    Create parent directories as needed (default: true)
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	parents := flag.Bool("p", true, "create parent directories as needed")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-mkdir [-p] dir [dir...]\n")
		fmt.Fprintf(os.Stderr, "Create directories.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	exitCode := 0
	for _, dir := range flag.Args() {
		var err error
		if *parents {
			err = os.MkdirAll(dir, 0755)
		} else {
			err = os.Mkdir(dir, 0755)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-mkdir: %v\n", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
