
// helloworld is the simplest userspace test program.
// It calls GetTime (Mazzy syscall) and Printf (Linux syscall via priest).
package main

import (
	"fmt"
	"mazzy/mazarin/sys"
)

func main() {
	t, err := sys.GetTime()
	if err != nil {
		fmt.Printf("GetTime error: %v\n", err)
		return
	}
	fmt.Printf("hello world %d.%09d\n", t.Seconds, t.Nanoseconds)
}
