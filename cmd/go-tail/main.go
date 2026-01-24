// go-tail outputs the last part of files, similar to tail.
//
// Usage:
//
//	go tool go-tail [-n lines] [file...]
//
// With no file, reads from stdin.
//
// Flags:
//
//	-n N    Output the last N lines (default 10)
//	-c N    Output the last N bytes
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	numLines := flag.Int("n", 10, "output the last N lines")
	numBytes := flag.Int("c", 0, "output the last N bytes")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-tail [-n lines] [-c bytes] [file...]\n")
		fmt.Fprintf(os.Stderr, "Output the last part of files.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	files := flag.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	exitCode := 0
	showFilenames := len(files) > 1

	for _, file := range files {
		if showFilenames {
			if file == "-" {
				fmt.Println("==> standard input <==")
			} else {
				fmt.Printf("==> %s <==\n", file)
			}
		}

		var err error
		if *numBytes > 0 {
			err = tailBytes(file, *numBytes)
		} else {
			err = tailLines(file, *numLines)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-tail: %v\n", err)
			exitCode = 1
		}
	}

	os.Exit(exitCode)
}

func tailLines(file string, n int) error {
	var r io.Reader
	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}

	// Use a circular buffer of lines
	lines := make([]string, n)
	pos := 0
	count := 0

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines[pos] = scanner.Text()
		pos = (pos + 1) % n
		count++
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Output lines in order
	if count < n {
		for i := 0; i < count; i++ {
			fmt.Println(lines[i])
		}
	} else {
		for i := 0; i < n; i++ {
			fmt.Println(lines[(pos+i)%n])
		}
	}

	return nil
}

func tailBytes(file string, n int) error {
	if file == "-" {
		// For stdin, we need to read everything into memory
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if len(data) <= n {
			os.Stdout.Write(data)
		} else {
			os.Stdout.Write(data[len(data)-n:])
		}
		return nil
	}

	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	size := stat.Size()
	if int64(n) >= size {
		_, err = io.Copy(os.Stdout, f)
		return err
	}

	_, err = f.Seek(-int64(n), io.SeekEnd)
	if err != nil {
		return err
	}

	_, err = io.Copy(os.Stdout, f)
	return err
}
