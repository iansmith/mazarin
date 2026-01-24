// go-filesize prints file sizes in human-readable format.
//
// Usage:
//
//	go tool go-filesize [-h] file...
//
// Flags:
//
//	-h    Human-readable sizes (1K, 2.3M, etc.) [default]
//	-b    Raw bytes only
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	human := flag.Bool("h", true, "human-readable sizes")
	bytes := flag.Bool("b", false, "raw bytes only")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-filesize [-h] [-b] file...\n")
		fmt.Fprintf(os.Stderr, "Print file sizes.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	exitCode := 0
	for _, path := range flag.Args() {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-filesize: %v\n", err)
			exitCode = 1
			continue
		}

		size := info.Size()
		if *bytes || !*human {
			fmt.Printf("%d\n", size)
		} else {
			fmt.Printf("%s\n", humanSize(size))
		}
	}

	os.Exit(exitCode)
}

func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(bytes)/float64(div), "KMGTPE"[exp])
}
