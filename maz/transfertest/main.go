// transfertest is the SERVER side of the MAZ-50 boot-time smoke for the
// mazarin/transfer Mode 1 (Reserve/Commit) IPC path.
//
// It registers a transfer.Server on its uring dispatcher and waits for a
// Reserve from transferprobe (the client side). On the matching Commit it
// checks the magic bytes, prints PASS/FAIL, and releases the Slab.
//
// PASS condition: the server-side Slab.Bytes() begins with the magic
// payload that transferprobe wrote. Any failure prints [transfertest] FAIL
// ... to the UART and the boot smoke catches it.
package main

import (
	"bytes"

	"mazzy/maz/internal/transferpayload"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
)

const tag = "[transfertest] "

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start (server)\n")

	srv := transfer.NewServer(4)
	disp := uring.NewDispatcher()
	srv.RegisterDispatch(disp)
	disp.Start()

	sys.SetReady(true)

	// Block here for the round-trip. The dispatcher feeds newly-Reserved
	// Slabs into srv.NewSlabs; Wait blocks until the matching Commit
	// arrives. Single Slab expected; loop just in case for robustness.
	for slab := range srv.NewSlabs() {
		if err := slab.Wait(); err != nil {
			sys.UartWriteString(tag + "FAIL Wait: " + err.Error() + "\n")
			slab.Release()
			continue
		}
		got := slab.Bytes()
		if got == nil || !bytes.HasPrefix(got, transferpayload.Magic) {
			sys.UartWriteString(tag + "FAIL content mismatch\n")
			slab.Release()
			continue
		}
		sys.UartWriteString(tag + "PASS Mode 1 round-trip\n")
		slab.Release()
	}
}
