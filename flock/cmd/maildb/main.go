// maildb is a userspace shepherd that owns a SQLite database of mail
// messages. It loads mail-ui.maz for the console UI and processes
// query strings received via the MailDBIO QueryChannel.
package main

import (
	"fmt"
	"unsafe"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/maildbio"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/flock/cmd/maildb/shared"
)

// forceMailDBIOItab ensures the linker includes the MailDBIO interface type
// descriptor and all method wrappers for *MailDBIOInit.
//
//go:noinline
func forceMailDBIOItab(v interface{}) {
	io, ok := v.(maildbio.MailDBIO)
	if !ok {
		return
	}
	_ = io.QueryChannel()
	_ = io.ResponseChannel()
	_ = io.StatusChannel()
	_ = io.WMChannel()
	_ = io.FontReplyChannel()
	io.SetChannels(nil, nil, nil, nil) //nolint:staticcheck
	_ = io.GetRachelSID()
}

// startUringDispatcher sets up the uring Dispatcher for shared.
// WM and font response messages are forwarded to mail-ui via the
// injection channels.
func startUringDispatcher(fsClient *fsclient.Client, wmCh chan any, fontReplyCh chan any) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, decodeRawPayload, wmCh)
	d.On(ipc.ProtoFontResponse, decodeRawPayload, fontReplyCh)
	d.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fsClient.RespCh)
	d.OnDeath(func(deadSID int16) {
		fmt.Printf("[maildb] shepherd %d died\n", deadSID)
	})
	d.Start()
}

// decodeRawPayload copies the raw UringIPCMsg payload without interpreting it.
func decodeRawPayload(msg *ipc.UringIPCMsg) any {
	raw := make([]byte, len(msg.Payload))
	copy(raw, msg.Payload[:])
	return raw
}

// handleQuery processes a single query string and sends a Response.
// For now this is a stub that returns a hard-coded test message.
func handleQuery(query string, respCh chan<- shared.Response) {
	fmt.Printf("[maildb] query: %s\n", query)

	// TODO: execute query against SQLite database.
	// For now, return a stub response to prove the plumbing works.
	resultCh := make(chan shared.MailMessage, 1)
	respCh <- shared.Response{Results: resultCh}

	resultCh <- shared.MailMessage{
		MessageId: "stub-001",
		From:      "test@example.com",
		Sender:    "Test Sender",
		Subject:   "Stub response for: " + query,
	}
	close(resultCh)
}

func main() {
	fmt.Println("[maildb] main() entered")

	// 1. Wait for fs and rachel.
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: fs: %v", err))
	}
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: rachel: %v", err))
	}
	rachelSID := sys.MustGetShepherdByName("rachel")
	fsSID := sys.MustGetShepherdByName("fs")
	fsClient := fsclient.New(fsSID)

	// 2. Prepare MailDBIO injection struct.
	statusCh := make(chan string, 32)
	ioInit := &maildbio.MailDBIOInit{
		RachelSIDVal: rachelSID,
		StatusCh:     statusCh,
	}
	forceMailDBIOItab(ioInit)

	fmt.Println("[maildb] MailDBIO config prepared")

	// 2b. Import mbox into BadgerDB before loading UI.
	go func() {
		err := mboxImport(
			"/data/mail/mbox/gmail/important.partial.mbox",
			"/data/mail/db",
			statusCh,
		)
		if err != nil {
			statusCh <- fmt.Sprintf("mbox import error: %v", err)
			fmt.Printf("[maildb] mbox import error: %v\n", err)
		}
	}()

	// 3. Set up uring dispatcher BEFORE loading .maz.
	tempWMCh := make(chan any, 8)
	tempFontReplyCh := make(chan any, 8)
	startUringDispatcher(fsClient, tempWMCh, tempFontReplyCh)
	if err := fsClient.Connect(); err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: fsclient.Connect: %v", err))
	}
	fmt.Println("[maildb] fs IPC connected via uring")

	// 4. Load mail-ui.maz and inject MailDBIO.
	uiPath := sys.LoadMazByName("/mail-ui")
	fmt.Printf("[maildb] loading mail-ui from %s...\n", uiPath)
	uiMain, uiInitAddr, uiErr := mazhost.LoadMazBootstrap(uiPath, nil)
	if uiErr != nil {
		panic(fmt.Sprintf("[maildb] LoadMazBootstrap(mail-ui) failed: %v", uiErr))
	}

	if uiInitAddr != 0 {
		type funcval struct{ fn uintptr }
		fv := &funcval{fn: uiInitAddr}
		shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
		if err := shepherdInit(ioInit); err != nil {
			fmt.Printf("[maildb] mail-ui MazarinShepherd failed: %v\n", err)
		}
	}

	// Read channels back from the .maz-filled struct.
	queryCh := ioInit.QueryCh
	respCh := ioInit.RespCh
	wmCh := ioInit.WMCh
	fontReplyCh := ioInit.FontReplyCh

	if queryCh == nil || respCh == nil || wmCh == nil || fontReplyCh == nil {
		panic("[maildb] FATAL: mail-ui did not create channels")
	}

	// Forward WM and font messages from uring dispatcher to mail-ui's
	// typed []byte channels.
	go func() {
		for msg := range tempWMCh {
			if payload, ok := msg.([]byte); ok {
				wmCh <- payload
			}
		}
	}()
	go func() {
		for msg := range tempFontReplyCh {
			if payload, ok := msg.([]byte); ok {
				fontReplyCh <- payload
			}
		}
	}()

	// Launch mail-ui.maz goroutine.
	go mazhost.RunMaz(uiMain)
	fmt.Println("[maildb] mail-ui.maz launched")

	// 5. Signal readiness.
	sys.SetReady(true)
	fmt.Println("[maildb] Ready=true")

	// 6. Main goroutine: process queries from mail-ui.
	fmt.Println("[maildb] entering query loop")
	for query := range queryCh {
		handleQuery(query, respCh)
	}
}
