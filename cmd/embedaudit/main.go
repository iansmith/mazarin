// Command embedaudit verifies that a built kernel image embeds only
// payloads of its own architecture: the fs shepherd ELF and the arch-specific
// kernel.toml (matched by its header-line marker). It runs at the end of each
// kernel build target and fails the build on a mismatch, so a wrong-arch
// staging regression (MAZ-153) can never ship silently.
package main

import (
	"fmt"
	"os"

	"mazzy/shared/embedcheck"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: embedaudit <kernel.elf> [<kernel.elf> ...]")
		os.Exit(2)
	}
	failed := false
	for _, path := range os.Args[1:] {
		if err := embedcheck.CheckKernelELF(path); err != nil {
			fmt.Fprintf(os.Stderr, "embedaudit: %s: embedded fs: %v\n", path, err)
			failed = true
		}
		if err := embedcheck.CheckKernelConfig(path); err != nil {
			fmt.Fprintf(os.Stderr, "embedaudit: %s: embedded config: %v\n", path, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
