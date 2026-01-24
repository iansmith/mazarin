// go-ls lists directory contents, similar to ls.
//
// Usage:
//
//	go tool go-ls [-l] [-h] [-a] [path...]
//
// Flags:
//
//	-l    Long format (permissions, size, date, name)
//	-h    Human-readable sizes (use with -l)
//	-a    Show hidden files (starting with .)
//	-1    One file per line (default when not a terminal)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	long := flag.Bool("l", false, "long format")
	human := flag.Bool("h", false, "human-readable sizes")
	all := flag.Bool("a", false, "show hidden files")
	onePerLine := flag.Bool("1", false, "one file per line")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-ls [-l] [-h] [-a] [-1] [path...]\n")
		fmt.Fprintf(os.Stderr, "List directory contents.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	paths := flag.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	exitCode := 0
	showPath := len(paths) > 1

	for i, path := range paths {
		if showPath {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("%s:\n", path)
		}

		if err := listPath(path, *long, *human, *all, *onePerLine); err != nil {
			fmt.Fprintf(os.Stderr, "go-ls: %v\n", err)
			exitCode = 1
		}
	}

	os.Exit(exitCode)
}

func listPath(path string, long, human, all, onePerLine bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		// Single file
		printEntry(path, info, long, human)
		return nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	// Sort entries
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !all && strings.HasPrefix(name, ".") {
			continue
		}

		if long {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			printEntry(name, info, long, human)
		} else {
			names = append(names, name)
		}
	}

	if !long {
		if onePerLine {
			for _, name := range names {
				fmt.Println(name)
			}
		} else {
			fmt.Println(strings.Join(names, "  "))
		}
	}

	return nil
}

func printEntry(name string, info os.FileInfo, long, human bool) {
	if !long {
		fmt.Println(name)
		return
	}

	mode := info.Mode()
	size := info.Size()
	modTime := info.ModTime()

	sizeStr := fmt.Sprintf("%12d", size)
	if human {
		sizeStr = fmt.Sprintf("%7s", humanSize(size))
	}

	fmt.Printf("%s %s %s %s\n",
		mode.String(),
		sizeStr,
		modTime.Format(time.Stamp),
		filepath.Base(name))
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
