// maildb is a userspace shepherd that owns a BadgerDB database of mail
// messages. It loads mail-ui.maz for the console UI and processes
// query strings received via the MailDBIO QueryChannel. It also serves
// mail protocol requests (GetHeaders, GetBody) from the mail shepherd
// over uring IPC. Full-text indexing is delegated to the fti shepherd.
package main

import (
	"fmt"
	"time"
	"unsafe"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/maz/maildb/shared"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/maildbio"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/fti"
	"mazzy/shared/ipc"
	"mazzy/shared/mailproto"
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

// startUringDispatcher sets up uring Dispatchers across two rings:
//
//   - Ring 0: shepherd IPC (WM, Font, Mail, FTI, Death)
//   - Ring 1: fs IPC responses (ProtoFSIPCResp → fsClient.RespCh).
//     Isolated so a backed-up WM/Font channel on Ring 0 cannot
//     deadlock fsclient callers.
func startUringDispatcher(fsClient *fsclient.Client, wmCh chan any, fontReplyCh chan any, mh *mailHandler, ftiRespCh chan any, ftiSID int, tracker *ftiTracker) {
	// Ring 0: shepherd IPC
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, decodeRawPayload, wmCh)
	d.On(ipc.ProtoFontResponse, decodeRawPayload, fontReplyCh)
	d.OnFunc(ipc.ProtoMailReq, decodeMailReqWithSID, func(v any) {
		if tagged, ok := v.(taggedMailReq); ok {
			mh.handleMailReq(tagged.req, tagged.senderSID)
		}
	})
	d.On(ipc.ProtoFTIResp, fti.DecodeFTIResp, ftiRespCh)
	d.OnDeath(func(deadSID int16) {
		mlogErrorf("[maildb] shepherd %d died", deadSID)
		if int(deadSID) == ftiSID {
			tracker.MarkFTIDied()
		}
	})
	d.Start()

	// Ring 1: fs responses only — isolated from WM/Font/Mail on Ring 0.
	fsRespDispatcher := uring.NewDispatcherWithRing(1)
	fsRespDispatcher.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fsClient.RespCh)
	fsRespDispatcher.Start()
}

// taggedMailReq wraps a decoded mail request with the sender's SID.
type taggedMailReq struct {
	req       any
	senderSID int16
}

// decodeMailReqWithSID decodes a v2 ProtoMailReq and preserves the sender SID.
func decodeMailReqWithSID(msg *ipc.UringIPCMsg) any {
	decoded := mailproto.DecodeMailReq(msg)
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

// handleList sends messages in reverse chronological order using the
// collection + MessageStore infrastructure.
func handleList(cs *collectionStore, ms *MessageStore, respCh chan<- shared.Response) {
	var filterArg [64]byte
	coll, err := cs.createCollection(mailproto.FilterAll, mailproto.SortDesc, filterArg, -1)
	if err != nil {
		respCh <- shared.Response{Error: fmt.Sprintf("handleList: %v", err)}
		return
	}

	total := coll.totalSize
	resultCh := make(chan shared.MailMessage, 64)
	respCh <- shared.Response{Total: total, Results: resultCh}

	go func() {
		defer close(resultCh)
		if total == 0 {
			return
		}

		// Load up to the first 500 entries in windows of 128.
		limit := total
		if limit > 500 {
			limit = 500
		}
		for start := 0; start < limit; start += maxWindowLoad {
			end := start + maxWindowLoad - 1
			if end >= limit {
				end = limit - 1
			}
			if err := cs.loadWindow(coll, uint32(start), uint32(end)); err != nil {
				mlogErrorf("[maildb] handleList loadWindow error: %v", err)
				return
			}
			for i := start; i <= end; i++ {
				msgId, ok := coll.entries[uint32(i)]
				if !ok || msgId == "" {
					continue
				}
				headers, err := ms.LoadHeaders(msgId)
				if err != nil {
					continue
				}
				resultCh <- *headers
			}
		}
		mlogInfo("[maildb] list: sent up to %d messages", limit)
	}()
}

// MazarinMain is the .maz plugin entry point. Identical semantics to main();
// the dual spelling lets maildb build both as legacy ET_EXEC (uses main) and
// as a .maz plugin (uses MazarinMain via mazdl).
//
// MazEntryPoint keeps the symbol alive in plugin builds — without this
// reference the linker would drop it.
var MazEntryPoint func() = MazarinMain

func MazarinMain() { main() }

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
	// Pass the absolute scratch path to openImportBadger — see comment
	// on importBadgerDir for why we can't rely on cwd-relative "./badger".
	SetImportBadgerDir(scratch + "/badger")
	rachelSID := sys.MustGetShepherdByName("rachel")
	fsSID := sys.MustGetShepherdByName("fs")
	ftiSID := sys.MustGetShepherdByName("fti")
	fsClient := fsclient.New(fsSID)

	// 2. Prepare MailDBIO injection struct.
	statusCh := make(chan maildbio.StatusLine, 256)
	notifyCh := make(chan struct{}, 1)
	ioInit := &maildbio.MailDBIOInit{
		RachelSIDVal: rachelSID,
		StatusCh:     statusCh,
		NotifyCh:     notifyCh,
	}
	forceMailDBIOItab(ioInit)
	mlogSetSink(statusCh, notifyCh)

	mlogInfo("[maildb] MailDBIO config prepared")

	// 2a. Run mmap coherence test before badger (which also uses mmap).
	if !testMmapCoherence() {
		mlogErrorf("[maildb] WARNING: mmap coherence test FAILED")
	}

	// 2b. Import mbox into BadgerDB; indexing is delegated to fti.

	// FTI response channel — fed by the uring dispatcher.
	ftiRespCh := make(chan any, 16)

	// FTI tracker: drains fti responses, frees shared pages, notifies UI.
	ftiTracker := newFTITracker(ftiRespCh, ftiSID)

	// Channel to receive db handle from import goroutine.
	type importResult struct {
		db  *badger.DB
		err error
	}
	importDone := make(chan importResult, 1)

	// 3. Set up uring dispatchers BEFORE loading .maz.
	// Ring 1 carries fs IPC responses, isolated from Ring 0
	// WM/Font/Mail/FTI traffic so backpressure can't deadlock fsclient.
	if err := uring.Setup(1); err != nil {
		panic("[maildb] uring.Setup(1) failed: " + err.Error())
	}
	fsClient.RespRing = 1
	mh := newMailHandler(nil)
	tempWMCh := make(chan any, 8)
	tempFontReplyCh := make(chan any, 8)
	startUringDispatcher(fsClient, tempWMCh, tempFontReplyCh, mh, ftiRespCh, ftiSID, ftiTracker)
	if err := fsClient.Connect(); err != nil {
		panic(fmt.Sprintf("[maildb] FATAL: fsclient.Connect: %v", err))
	}
	mlogInfo("[maildb] fs IPC connected via uring")

	// importCS holds the collectionStore once onFirstCommit fires.
	// Accessed only from the import goroutine, so no synchronisation needed.
	var importCS *collectionStore

	// onFirstCommit is called from the import goroutine after the first
	// message is flushed to badger. It sets up the in-memory data structures
	// and signals shepherd readiness so the mail program can connect.
	onFirstCommit := func(db *badger.DB) {
		msNew := newMessageStore(db)
		csNew := newCollectionStore(db, msNew)
		importCS = csNew
		mh.setStores(db, msNew, csNew)
		sys.SetReady(true)
		mlogInfo("[maildb] Ready=true (first message in badger, fti+import in background)")
		sys.StartMemStatsLogger("maildb", 0)
	}

	// onMessage is called from the import goroutine after each message is
	// committed. It sends CollectionAdd to any live subscribed collections.
	onMessage := func(msgId string, ts time.Time) {
		if importCS != nil {
			importCS.addMessage(msgId, ts)
		}
	}

	// Start import goroutine. mboxImport calls onFirstCommit after the first
	// message, then calls onMessage for each subsequent message. FTI indexing
	// continues in the background via the ftiTracker goroutine.
	go func() {
		bdb, err := mailImport(
			"/data/mail/mbox/current.mbox",
			ftiTracker,
			onFirstCommit,
			onMessage,
		)
		if err != nil {
			mlogErrorf("[maildb] mbox import error: %v", err)
		}
		importDone <- importResult{db: bdb, err: err}
	}()

	// 4. Load mail-ui.maz and inject MailDBIO.
	uiPath := sys.LoadMazByName("/mail-ui")
	mlogInfo("[maildb] loading mail-ui from %s...", uiPath)
	uiMain, uiInitAddr, uiErr := mazhost.LoadMazBootstrap(fsClient, uiPath, nil)
	if uiErr != nil {
		panic(fmt.Sprintf("[maildb] LoadMazBootstrap(mail-ui) failed: %v", uiErr))
	}

	if uiInitAddr != 0 {
		type funcval struct{ fn uintptr }
		fv := &funcval{fn: uiInitAddr}
		shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
		if err := shepherdInit(ioInit); err != nil {
			mlogErrorf("[maildb] mail-ui MazarinShepherd failed: %v", err)
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
	mlogInfo("[maildb] mail-ui.maz launched")

	// 5. Wait for badger import to finish.
	// Ready was already signalled (or will be) by onFirstCommit when the
	// first message lands in badger. FTI indexing continues in the background.
	mlogInfo("[maildb] waiting for badger import to finish...")
	ir := <-importDone
	if ir.err != nil {
		mlogErrorf("[maildb] import failed: %v", ir.err)
	}

	// Retrieve the current stores from the mail handler (set by onFirstCommit).
	mh.mu.Lock()
	ms := mh.ms
	cs := mh.cs
	var db *badger.DB
	if mh.db != nil {
		db = mh.db
	} else if ir.db != nil {
		db = ir.db
	}
	mh.mu.Unlock()

	if db != nil {
		defer db.Close()
	}

	// If the import finished without onFirstCommit firing (e.g. empty mbox or
	// error before first message), set up stores and signal ready now.
	if ms == nil && ir.db != nil {
		ms = newMessageStore(ir.db)
		cs = newCollectionStore(ir.db, ms)
		mh.setStores(ir.db, ms, cs)
		sys.SetReady(true)
		mlogInfo("[maildb] Ready=true (import complete, no early-ready)")
	}

	mlogInfo("[maildb] import complete: ms=%v cs=%v", ms != nil, cs != nil)

	// 6. Enter query loop.
	mlogInfo("[maildb] entering query loop")
	for query := range queryCh {
		if db == nil || cs == nil {
			respCh <- shared.Response{Error: "database not available"}
			continue
		}
		if query == "list" {
			handleList(cs, ms, respCh)
		} else {
			// Search queries will be forwarded to fti in a future phase.
			respCh <- shared.Response{Error: "search not yet available (migrating to fti)"}
		}
	}
}
