// go-nc is a simple netcat for TCP connections, similar to nc.
//
// Usage:
//
//	go tool go-nc host port
//	echo "command" | go tool go-nc host port
//
// Connects to host:port, sends stdin, prints response to stdout.
// This is a simplified version for interacting with QEMU monitor.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

func main() {
	timeout := flag.Duration("w", 5*time.Second, "connection timeout")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-nc [-w timeout] host port\n")
		fmt.Fprintf(os.Stderr, "Simple TCP client (netcat-like).\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(1)
	}

	host := flag.Arg(0)
	port := flag.Arg(1)
	addr := net.JoinHostPort(host, port)

	conn, err := net.DialTimeout("tcp", addr, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-nc: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Set read/write deadlines
	conn.SetDeadline(time.Now().Add(*timeout))

	// Copy stdin to connection
	go func() {
		io.Copy(conn, os.Stdin)
		// Close write side to signal EOF
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	// Copy connection to stdout
	io.Copy(os.Stdout, conn)
}
