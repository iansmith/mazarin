// go-sleep pauses for a specified duration, similar to sleep.
//
// Usage:
//
//	go tool go-sleep seconds
//	go tool go-sleep 0.5
//	go tool go-sleep 1.5s
//	go tool go-sleep 100ms
//
// Supports integer or decimal seconds, or duration strings (s, ms, us, ns).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-sleep duration\n")
		fmt.Fprintf(os.Stderr, "Pause for specified duration.\n\n")
		fmt.Fprintf(os.Stderr, "Duration can be:\n")
		fmt.Fprintf(os.Stderr, "  N      Seconds (integer or decimal)\n")
		fmt.Fprintf(os.Stderr, "  Ns     Seconds\n")
		fmt.Fprintf(os.Stderr, "  Nms    Milliseconds\n")
		fmt.Fprintf(os.Stderr, "  Nus    Microseconds\n")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	duration, err := parseDuration(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-sleep: invalid duration: %v\n", err)
		os.Exit(1)
	}

	if duration == 0 {
		// Sleep forever (block until killed via signal)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		return
	}
	time.Sleep(duration)
}

func parseDuration(s string) (time.Duration, error) {
	// Bare unit suffix with no number (e.g., "s" from empty TIMEOUT= expansion)
	// means wait forever (duration 0).
	if s == "s" || s == "ms" || s == "us" || s == "ns" || s == "" {
		return 0, nil
	}

	// Try parsing as Go duration first (e.g., "1s", "100ms")
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Try parsing as plain number (seconds)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(f * float64(time.Second)), nil
	}

	return 0, fmt.Errorf("cannot parse %q as duration", s)
}
