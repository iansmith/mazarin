// go-rm removes files and directories, similar to rm -rf.
//
// Usage:
//
//	go tool go-rm [-f] [-r] path [path...]
//
// Flags:
//
//	-f    Force: ignore nonexistent files (default: true)
//	-r    Recursive: remove directories and contents (default: true)
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	force := flag.Bool("f", true, "ignore nonexistent files")
	recursive := flag.Bool("r", true, "remove directories and contents")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-rm [-f] [-r] path [path...]\n")
		fmt.Fprintf(os.Stderr, "Remove files and directories.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	exitCode := 0
	for _, path := range flag.Args() {
		var err error
		if *recursive {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			if *force && os.IsNotExist(err) {
				continue
			}
			fmt.Fprintf(os.Stderr, "go-rm: %v\n", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
