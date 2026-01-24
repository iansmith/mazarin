// go-mv moves/renames files, similar to mv.
//
// Usage:
//
//	go tool go-mv source dest
//	go tool go-mv source... directory
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-mv source dest\n")
		fmt.Fprintf(os.Stderr, "       go-mv source... directory\n")
		fmt.Fprintf(os.Stderr, "Move/rename files.\n")
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		flag.Usage()
		os.Exit(1)
	}

	dest := args[len(args)-1]
	sources := args[:len(args)-1]

	// Check if destination is a directory
	destInfo, err := os.Stat(dest)
	destIsDir := err == nil && destInfo.IsDir()

	// If multiple sources, destination must be a directory
	if len(sources) > 1 && !destIsDir {
		fmt.Fprintf(os.Stderr, "go-mv: target '%s' is not a directory\n", dest)
		os.Exit(1)
	}

	exitCode := 0
	for _, src := range sources {
		target := dest
		if destIsDir {
			target = filepath.Join(dest, filepath.Base(src))
		}

		if err := os.Rename(src, target); err != nil {
			fmt.Fprintf(os.Stderr, "go-mv: %v\n", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
