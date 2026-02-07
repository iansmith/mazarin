// go-cp copies files, similar to cp.
//
// Usage:
//
//	go tool go-cp source dest
//	go tool go-cp source... directory
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-cp source dest\n")
		fmt.Fprintf(os.Stderr, "       go-cp source... directory\n")
		fmt.Fprintf(os.Stderr, "Copy files.\n")
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		flag.Usage()
		os.Exit(1)
	}

	dest := args[len(args)-1]
	sources := args[:len(args)-1]

	destInfo, err := os.Stat(dest)
	destIsDir := err == nil && destInfo.IsDir()

	if len(sources) > 1 && !destIsDir {
		fmt.Fprintf(os.Stderr, "go-cp: target '%s' is not a directory\n", dest)
		os.Exit(1)
	}

	exitCode := 0
	for _, src := range sources {
		target := dest
		if destIsDir {
			target = filepath.Join(dest, filepath.Base(src))
		}
		if err := copyFile(src, target); err != nil {
			fmt.Fprintf(os.Stderr, "go-cp: %v\n", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
