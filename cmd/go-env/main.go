// go-env checks or displays environment variables.
//
// Usage:
//
//	go tool go-env NAME                    # Print value of NAME
//	go tool go-env -check NAME=VALUE       # Exit 0 if NAME equals VALUE, 1 otherwise
//	go tool go-env -check NAME=VALUE -msg "error message"  # Print message on failure
//
// Exit codes:
//
//	0 - Success (variable exists or check passed)
//	1 - Failure (variable not set or check failed)
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	check := flag.String("check", "", "Check that NAME=VALUE")
	msg := flag.String("msg", "", "Error message to print on check failure")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-env [-check NAME=VALUE [-msg MSG]] [NAME]\n")
		fmt.Fprintf(os.Stderr, "Check or display environment variables.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Check mode
	if *check != "" {
		parts := strings.SplitN(*check, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "go-env: -check requires NAME=VALUE format\n")
			os.Exit(1)
		}
		name, expected := parts[0], parts[1]
		actual := os.Getenv(name)
		if actual != expected {
			if *msg != "" {
				fmt.Fprintln(os.Stderr, *msg)
			} else {
				fmt.Fprintf(os.Stderr, "go-env: %s is %q, expected %q\n", name, actual, expected)
			}
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Display mode
	if flag.NArg() < 1 {
		// Print all environment variables
		for _, env := range os.Environ() {
			fmt.Println(env)
		}
		os.Exit(0)
	}

	name := flag.Arg(0)
	value := os.Getenv(name)
	if value == "" {
		// Check if variable exists but is empty vs not set
		_, exists := os.LookupEnv(name)
		if !exists {
			os.Exit(1)
		}
	}
	fmt.Println(value)
	os.Exit(0)
}
