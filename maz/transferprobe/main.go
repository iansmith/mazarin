// transferprobe is the CLIENT side of the MAZ-50 boot-time smoke for the
// mazarin/transfer Mode 1 (Reserve/Commit) IPC path.
//
// It waits for the transfertest shepherd to be ready, calls Reserve for a
// page-backed buffer, writes the magic payload, and Commits. The server
// (transfertest) checks the bytes and prints PASS or FAIL.
//
// transferprobe itself prints only its own success / failure path
// (Reserve / Writer / Commit errors), so a boot log without `[transferprobe]
// FAIL` plus `[transfertest] PASS Mode 1 round-trip` means the full
// Mode 1 round-trip worked.
package main

import (
	"mazzy/maz/internal/transferpayload"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

const tag = "[transferprobe] "

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start (client)\n")

	if err := sys.WaitForShepherdReady("transfertest", 5); err != nil {
		sys.UartWriteString(tag + "FAIL transfertest not ready: " + err.Error() + "\n")
		sys.SetReady(true)
		select {}
	}
	serverSID, err := sys.GetShepherdByName("transfertest")
	if err != nil {
		sys.UartWriteString(tag + "FAIL GetShepherdByName: " + err.Error() + "\n")
		sys.SetReady(true)
		select {}
	}
	server := transfer.ShepherdID(serverSID)

	// Wire the dispatcher to route transfer responses into transferprobe's
	// per-server respCh.
	disp := uring.NewDispatcher()
	disp.On(ipc.ProtoTransferResp, transfer.DecodeResp, transfer.RespCh(server))
	disp.Start()

	sys.SetReady(true)

	h, err := transfer.Reserve(server, 0, len(transferpayload.Magic))
	if err != nil {
		sys.UartWriteString(tag + "FAIL Reserve: " + err.Error() + "\n")
		select {}
	}
	if _, err := h.Writer().Write(transferpayload.Magic); err != nil {
		sys.UartWriteString(tag + "FAIL Write: " + err.Error() + "\n")
		select {}
	}
	if err := h.Commit(); err != nil {
		sys.UartWriteString(tag + "FAIL Commit: " + err.Error() + "\n")
		select {}
	}
	sys.UartWriteString(tag + "PASS Reserve+Write+Commit\n")
	select {}
}
