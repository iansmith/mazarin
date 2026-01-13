// Stop any running QEMU instances
//
// Usage: go run scripts/stop.go
package main

import (
	"fmt"
	"os/exec"
	"time"
)

func main() {
	// Try graceful kill first
	exec.Command("pkill", "-f", "qemu-system-aarch64").Run()
	time.Sleep(300 * time.Millisecond)

	// Check if any are still running, force kill if needed
	if err := exec.Command("pgrep", "-f", "qemu-system-aarch64").Run(); err == nil {
		fmt.Println("Warning: QEMU still running, sending SIGKILL...")
		exec.Command("pkill", "-9", "-f", "qemu-system-aarch64").Run()
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Println("QEMU stopped")
}
