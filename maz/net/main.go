// net is the standalone ethernet-layer shepherd. It owns the VirtIO-net
// device and exposes a raw L2 io_uring send/receive surface that higher
// protocol layers (gvisor tcpip, quic-go, future transports) attach to as
// .maz plugins via dependency injection at load time.
//
// MAZ-28 step 1: skeleton only. The eth-layer + plugin loader land in
// later steps (RX path, TX path) and MAZ-27 (plugin loader, injection
// API, first protocol plugin).
package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("net: up")

	// Idle until the kernel tears us down. Real work lands in step 2 (RX).
	for {
		time.Sleep(1 * time.Hour)
	}
}
