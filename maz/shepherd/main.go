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
	"strconv"
	"unsafe"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

func main() {
	if len(os.Args) < 3 {
		sys.UartWriteString("[shepherd] usage: shepherd <sid> <plugin.maz>\n")
		panic("[shepherd] missing plugin path")
	}
	pluginPath := os.Args[2]
	pluginSID, _ := strconv.Atoi(os.Args[1])
	fmt.Printf("[shepherd] loading %s (sid=%d)\n", pluginPath, pluginSID)

	// Ring 1: fs responses. Ring 0 is kernel-allocated and already mapped.
	const fsRing = 1
	if err := uring.Setup(fsRing); err != nil {
		panic(fmt.Sprintf("[shepherd] uring.Setup(%d) failed: %v", fsRing, err))
	}

	fsSID := sys.MustGetShepherdByName("fs")
	fc := fsclient.New(fsSID)
	fc.RespRing = uint8(fsRing)

	disp := uring.NewDispatcherWithRing(fsRing)
	disp.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fc.RespCh)
	disp.Start()

	if err := fc.Connect(); err != nil {
		panic(fmt.Sprintf("[shepherd] fsclient.Connect: %v", err))
	}
	fmt.Printf("[shepherd] fsclient connected on ring %d\n", fsRing)

	mazhost.HostFSClient = fc

	mazMain, mazShepherdAddr, err := mazhost.LoadMazBootstrap(fc, pluginPath, nil)
	if err != nil {
		panic(fmt.Sprintf("[shepherd] LoadMazBootstrap(%q): %v", pluginPath, err))
	}

	// Inject ShepherdInit before MazMain so the replacement gets rings + fsclient.
	if mazShepherdAddr != 0 {
		type funcval struct{ fn uintptr }
		fv := &funcval{fn: mazShepherdAddr}
		shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
		init := &mazhost.ShepherdInit{
			Ring0:    mazhost.RingInfo{Number: 0},
			Ring1:    mazhost.RingInfo{Number: fsRing},
			FSClient: fc,
			SID:      pluginSID,
			Args:     os.Args,
		}
		if err := shepherdInit(init); err != nil {
			fmt.Printf("[shepherd] MazarinShepherd injection failed: %v\n", err)
		}
	}

	// Run MazarinMain synchronously on a pre-grown stack. MazarinMain
	// is expected to enter its own event loop and never return.
	mazhost.RunMaz(mazMain)
	os.Exit(0)
}
