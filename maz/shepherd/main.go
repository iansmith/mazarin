// Package main implements the generic Mazarin shepherd — a thin ELF that
// provides the Go runtime and loads a single .maz plugin handed to it via
// argv. The plugin's MazarinMain runs on a pre-grown stack and owns the
// rest of the shepherd's lifetime.
//
// Argv layout (set by the kernel's SysRunShepherd path in
// kmazarin/ksyscall/launch.go:752):
//
//	os.Args[0] = "/shepherd.elf"   (filename used to launch)
//	os.Args[1] = "<shepherdID>"    (numeric string)
//	os.Args[2] = "/rachel.maz"     (plugin path — the thing this shepherd
//	                                will actually run)
//
// Shepherd.elf is built via mazgo + mazlink with -dlopen-host-exports so
// its own dynsym publishes runtime.* / internal/runtime.* / internal/abi.*
// for the plugin's PLT to resolve against. See design/MAZARIN-DLOPEN.md §9.
package main

import (
	"fmt"
	"os"

	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
)

func main() {
	if len(os.Args) < 3 {
		sys.UartWriteString("[shepherd] usage: shepherd <sid> <plugin.maz>\n")
		panic("[shepherd] missing plugin path")
	}
	pluginPath := os.Args[2]
	sys.UartWriteString(fmt.Sprintf("[shepherd] loading %s (sid=%s)\n", pluginPath, os.Args[1]))

	mazMain, _, err := mazhost.LoadMazBootstrap(pluginPath, nil)
	if err != nil {
		panic(fmt.Sprintf("[shepherd] LoadMazBootstrap(%q): %v", pluginPath, err))
	}

	// Run MazarinMain synchronously on a pre-grown stack. MazarinMain
	// is expected to enter its own event loop and never return.
	mazhost.RunMaz(mazMain)
}
