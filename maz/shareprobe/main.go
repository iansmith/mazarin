// shareprobe is the CONSUMER side of the MAZ-53 boot-time smoke for the
// mazarin/transfer Mode 2 (Share) IPC path.
//
// It loops on transfer.ReceiveShare(sharetest) and for each Share:
//  1. Verifies len(share.AsBytes()) == share.Bytes (contract).
//  2. Verifies all bytes equal sharepayload.PatternA (sender wrote it).
//  3. Overwrites all bytes with sharepayload.PatternB (bidirectional write check).
//  4. Calls share.Release().
//
// Sharetest (the sender) validates the post-release state on its side and
// prints PASS/FAIL. Shareprobe prints only its own FAIL path so a boot log
// without `[shareprobe] FAIL` is the expected clean output.
package main

import (
	"mazzy/maz/internal/sharepayload"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
)

const tag = "[shareprobe] "

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start (consumer)\n")

	if err := sys.WaitForShepherdReady("sharetest", 5); err != nil {
		sys.UartWriteString(tag + "FAIL sharetest not ready: " + err.Error() + "\n")
		sys.SetReady(true)
		select {}
	}
	senderSID, err := sys.GetShepherdByName("sharetest")
	if err != nil {
		sys.UartWriteString(tag + "FAIL GetShepherdByName(sharetest): " + err.Error() + "\n")
		sys.SetReady(true)
		select {}
	}
	sender := transfer.ShepherdID(senderSID)

	disp := uring.NewDispatcher()
	transfer.RegisterShareConsumer(disp)
	disp.Start()

	sys.SetReady(true)

	// Process each incoming share sequentially. Sharetest sends exactly the
	// shares needed for its stages; we loop until it stops sending.
	for {
		sh, err := transfer.ReceiveShare(sender)
		if err != nil {
			sys.UartWriteString(tag + "FAIL ReceiveShare: " + err.Error() + "\n")
			select {}
		}
		handleShare(sh)
	}
}

func handleShare(sh *transfer.Share) {
	b := sh.AsBytes()
	if len(b) != sh.Bytes {
		sys.UartWriteString(tag + "FAIL AsBytes len=" + sys.Itoa(int64(len(b))) +
			" want " + sys.Itoa(int64(sh.Bytes)) + "\n")
		_ = sh.Release()
		return
	}

	// Verify sender wrote PatternA throughout.
	for i, v := range b {
		if v != sharepayload.PatternA {
			sys.UartWriteString(tag + "FAIL byte[" + sys.Itoa(int64(i)) + "]=0x" +
				sys.Hex64(uint64(v)) + " want PatternA\n")
			_ = sh.Release()
			return
		}
	}

	// Write PatternB so sharetest can verify bidirectional mapping.
	for i := range b {
		b[i] = sharepayload.PatternB
	}

	if err := sh.Release(); err != nil {
		sys.UartWriteString(tag + "FAIL Release: " + err.Error() + "\n")
	}
}

func main() { MazarinMain() }
