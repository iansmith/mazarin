// maildb is a userspace shepherd that owns a SQLite database of mail
// messages. It loads mail-ui.maz for the console UI and processes
// query strings received via the MailDBIO QueryChannel.
package main

import (
	"encoding/json"
	"fmt"
	"unsafe"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/blevesearch/bleve/v2"

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
	_ = io.NotifyChannel()
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

// handleQuery searches the bleve index and sends matching messages
// as a Response on respCh.
func handleQuery(query string, respCh chan<- shared.Response, index bleve.Index, db *badger.DB) {
	fmt.Printf("[maildb] query: %s\n", query)

	req := bleve.NewSearchRequest(bleve.NewQueryStringQuery(query))
	req.Size = 50
	result, err := index.Search(req)
	if err != nil {
		respCh <- shared.Response{Error: fmt.Sprintf("search error: %v", err)}
		return
	}

	resultCh := make(chan shared.MailMessage, len(result.Hits)+1)
	respCh <- shared.Response{Results: resultCh}

	for _, hit := range result.Hits {
		var msg shared.MailMessage
		_ = db.View(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(hit.ID))
			if err != nil {
				return err
			}
			return item.Value(func(val []byte) error {
				return json.Unmarshal(val, &msg)
			})
		})
		if msg.MessageId != "" {
			resultCh <- msg
		}
	}
	close(resultCh)
}

// testSearch runs a bleve search and sends results to the console via notify.
func testSearch(term string, index bleve.Index, db *badger.DB, notify func(string)) {
	fmt.Printf("[maildb] testSearch: %q\n", term)
	notify(fmt.Sprintf("--- Search: \"%s\" ---", term))
	req := bleve.NewSearchRequest(bleve.NewQueryStringQuery(term))
	req.Size = 10
	result, err := index.Search(req)
	if err != nil {
		notify(fmt.Sprintf("  error: %v", err))
		return
	}
	fmt.Printf("[maildb] testSearch %q: %d hits\n", term, result.Total)
	if len(result.Hits) == 0 {
		notify("  (no results)")
		return
	}
	for _, hit := range result.Hits {
		var msg shared.MailMessage
		_ = db.View(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(hit.ID))
			if err != nil {
				return err
			}
			return item.Value(func(val []byte) error {
				return json.Unmarshal(val, &msg)
			})
		})
		if msg.MessageId != "" {
			notify(fmt.Sprintf("  %.2f  %s — %s", hit.Score, msg.From, msg.Subject))
		}
	}
	notify(fmt.Sprintf("  %d hits (showing top %d)", result.Total, len(result.Hits)))
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
	notifyCh := make(chan struct{}, 1)
	ioInit := &maildbio.MailDBIOInit{
		RachelSIDVal: rachelSID,
		StatusCh:     statusCh,
		NotifyCh:     notifyCh,
	}
	forceMailDBIOItab(ioInit)

	fmt.Println("[maildb] MailDBIO config prepared")

	// 2b. Import mbox into BadgerDB + bleve FTI.
	// notifyStatus sends a status message and pokes the UI event loop.
	notifyStatus := func(msg string) {
		statusCh <- msg
		select {
		case notifyCh <- struct{}{}:
		default:
		}
	}

	const dbDir = "/tmp/mail/db"

	// Channel to receive index+db handles from import goroutine.
	type importResult struct {
		index bleve.Index
		db    *badger.DB
		err   error
	}
	importDone := make(chan importResult, 1)

	go func() {
		idx, bdb, err := mboxImport(
			"/data/mail/mbox/gmail/important.partial.mbox",
			dbDir,
			notifyStatus,
		)
		if err != nil {
			notifyStatus(fmt.Sprintf("mbox import error: %v", err))
			fmt.Printf("[maildb] mbox import error: %v\n", err)
		}
		importDone <- importResult{index: idx, db: bdb, err: err}
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

	// 6. Wait for import to finish, then enter query loop.
	fmt.Println("[maildb] waiting for import to finish...")
	ir := <-importDone
	if ir.err != nil {
		fmt.Printf("[maildb] import failed: %v\n", ir.err)
	}
	if ir.db != nil {
		defer ir.db.Close()
	}
	if ir.index != nil {
		defer ir.index.Close()
	}

	// Run test searches to prove the bleve index works.
	if ir.index != nil {
		for _, term := range []string{"banking", "account", "security"} {
			testSearch(term, ir.index, ir.db, notifyStatus)
		}
	}

	fmt.Println("[maildb] entering query loop")
	for query := range queryCh {
		if ir.index == nil || ir.db == nil {
			respCh <- shared.Response{Error: "index not available"}
			continue
		}
		handleQuery(query, respCh, ir.index, ir.db)
	}
}
