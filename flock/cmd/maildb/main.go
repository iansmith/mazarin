// maildb is a userspace shepherd that owns a BadgerDB database of mail
// messages. It loads mail-ui.maz for the console UI and processes
// query strings received via the MailDBIO QueryChannel. It also serves
// mail protocol requests (GetHeaders, GetBody) from the mail shepherd
// over uring IPC. Full-text indexing is delegated to the fti shepherd.
package main

import (
	"encoding/json"
	"fmt"
	"unsafe"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/maildbio"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/fti"
	"mazzy/shared/ipc"
	"mazzy/shared/mail"
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
	_ = io.NotifyChannel()
	_ = io.WMChannel()
	_ = io.FontReplyChannel()
	io.SetChannels(nil, nil, nil, nil) //nolint:staticcheck
	_ = io.GetRachelSID()
}

// startUringDispatcher sets up the uring Dispatcher for shared.
// WM and font response messages are forwarded to mail-ui via the
// injection channels. Mail protocol requests are dispatched to the
// mailHandler. FTI responses are sent to ftiRespCh.
func startUringDispatcher(fsClient *fsclient.Client, wmCh chan any, fontReplyCh chan any, mh *mailHandler, ftiRespCh chan any) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, decodeRawPayload, wmCh)
	d.On(ipc.ProtoFontResponse, decodeRawPayload, fontReplyCh)
	d.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fsClient.RespCh)
	d.OnFunc(ipc.ProtoMailReq, decodeMailReqWithSID, func(v any) {
		if tagged, ok := v.(taggedMailReq); ok {
			mh.handleMailReq(tagged.req, tagged.senderSID)
		}
	})
	d.On(ipc.ProtoFTIResp, fti.DecodeFTIResp, ftiRespCh)
	d.OnDeath(func(deadSID int16) {
		fmt.Printf("[maildb] shepherd %d died\n", deadSID)
	})
	d.Start()
}

// taggedMailReq wraps a decoded mail request with the sender's SID.
type taggedMailReq struct {
	req       any
	senderSID int16
}

// decodeMailReqWithSID decodes a ProtoMailReq and preserves the sender SID.
func decodeMailReqWithSID(msg *ipc.UringIPCMsg) any {
	decoded := mail.DecodeMailReq(msg)
	if decoded == nil {
		return nil
	}
	return taggedMailReq{req: decoded, senderSID: msg.SenderSID}
}

// decodeRawPayload copies the raw UringIPCMsg payload without interpreting it.
func decodeRawPayload(msg *ipc.UringIPCMsg) any {
	raw := make([]byte, len(msg.Payload))
	copy(raw, msg.Payload[:])
	return raw
}

// handleList sends all messages in reverse chronological order.
// Uses the date-indexed keys ("date:<RFC3339>:<messageId>") for ordering.
func handleList(db *badger.DB, respCh chan<- shared.Response) {
	// First, count messages.
	var total int
	_ = db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte("date:")
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek([]byte("date:")); it.Valid(); it.Next() {
			total++
		}
		return nil
	})

	resultCh := make(chan shared.MailMessage, 64)
	respCh <- shared.Response{Total: total, Results: resultCh}

	_ = db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("date:")
		opts.Reverse = true
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek([]byte("date:\xff")); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			// Key: "date:<RFC3339>:<messageId>" — extract messageId after 2nd colon.
			if len(key) < 6 {
				continue
			}
			rest := key[5:] // skip "date:"
			colonIdx := -1
			for i := 0; i < len(rest); i++ {
				if rest[i] == ':' {
					colonIdx = i
					break
				}
			}
			if colonIdx < 0 || colonIdx+1 >= len(rest) {
				continue
			}
			msgID := rest[colonIdx+1:]

			item, err := txn.Get([]byte(msgID))
			if err != nil {
				continue
			}
			var msg shared.MailMessage
			_ = item.Value(func(val []byte) error {
				return json.Unmarshal(val, &msg)
			})
			if msg.MessageId != "" {
				resultCh <- msg
			}
		}
		return nil
	})
	close(resultCh)
	fmt.Printf("[maildb] list: sent %d messages\n", total)
}

func main() {
	fmt.Println("[maildb] main() entered")

	// 1. Wait for core services, then fti.
	if err := sys.WaitForCoreServices(20); err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: core services: %v", err))
	}
	if err := sys.WaitForShepherdReady("fti", 20); err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: fti: %v", err))
	}
	scratch, err := sys.SetupScratchDir(true)
	if err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: scratchdir: %v", err))
	}
	fmt.Printf("[maildb] scratch dir: %s\n", scratch)
	rachelSID := sys.MustGetShepherdByName("rachel")
	fsSID := sys.MustGetShepherdByName("fs")
	ftiSID := sys.MustGetShepherdByName("fti")
	fsClient := fsclient.New(fsSID)

	// 2. Prepare MailDBIO injection struct.
	statusCh := make(chan string, 32)
	notifyCh := make(chan struct{}, 1)
	ioInit := &maildbio.MailDBIOInit{
		RachelSIDVal: rachelSID,
		StatusCh:     statusCh,
		NotifyCh:     notifyCh,
	}
	forceMailDBIOItab(ioInit)

	fmt.Println("[maildb] MailDBIO config prepared")

	// 2a. Run mmap coherence test before badger (which also uses mmap).
	if !testMmapCoherence() {
		fmt.Println("[maildb] WARNING: mmap coherence test FAILED")
	}

	// 2b. Import mbox into BadgerDB; indexing is delegated to fti.
	notifyStatus := func(msg string) {
		statusCh <- msg
		select {
		case notifyCh <- struct{}{}:
		default:
		}
	}

	// FTI response channel — fed by the uring dispatcher.
	ftiRespCh := make(chan any, 16)

	// FTI tracker: drains fti responses, frees shared pages, notifies UI.
	ftiTracker := newFTITracker(ftiRespCh, ftiSID, notifyStatus)

	// Channel to receive db handle from import goroutine.
	type importResult struct {
		db  *badger.DB
		err error
	}
	importDone := make(chan importResult, 1)

	// 3. Set up uring dispatcher BEFORE loading .maz.
	mh := newMailHandler(nil)
	tempWMCh := make(chan any, 8)
	tempFontReplyCh := make(chan any, 8)
	startUringDispatcher(fsClient, tempWMCh, tempFontReplyCh, mh, ftiRespCh)
	if err := fsClient.Connect(); err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: fsclient.Connect: %v", err))
	}
	fmt.Println("[maildb] fs IPC connected via uring")

	// Start import goroutine. mboxImport returns as soon as all messages
	// are written to badger (fast). FTI indexing continues in the background
	// via the ftiTracker goroutine.
	go func() {
		bdb, err := mboxImport(
			"/data/mail/mbox/gmail/important.partial.mbox",
			ftiTracker,
			notifyStatus,
		)
		if err != nil {
			notifyStatus(fmt.Sprintf("mbox import error: %v", err))
			fmt.Printf("[maildb] mbox import error: %v\n", err)
		}
		importDone <- importResult{db: bdb, err: err}
	}()

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

	// 5. Wait for badger import to finish, then signal readiness.
	// FTI indexing continues in the background — that's fine, the mail
	// program only needs badger (headers, bodies) to be queryable.
	fmt.Println("[maildb] waiting for badger import to finish...")
	ir := <-importDone
	if ir.err != nil {
		fmt.Printf("[maildb] import failed: %v\n", ir.err)
	}
	if ir.db != nil {
		defer ir.db.Close()
		// Enable the mail protocol handler now that the db is ready.
		mh.mu.Lock()
		mh.db = ir.db
		mh.mu.Unlock()
	}

	// Signal readiness now that badger is queryable. The mail program
	// (and any other shepherd waiting on maildb) can proceed.
	sys.SetReady(true)
	fmt.Println("[maildb] Ready=true (badger queryable, fti indexing in background)")

	// 6. Enter query loop.
	fmt.Println("[maildb] entering query loop")
	for query := range queryCh {
		if ir.db == nil {
			respCh <- shared.Response{Error: "database not available"}
			continue
		}
		if query == "list" {
			handleList(ir.db, respCh)
		} else {
			// Search queries will be forwarded to fti in a future phase.
			respCh <- shared.Response{Error: "search not yet available (migrating to fti)"}
		}
	}
}
