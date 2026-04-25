// fti is a userspace shepherd that owns a bleve full-text index.
// Other shepherds (e.g. maildb) send IndexDocument requests via uring IPC
// with document content in shared memory pages. fti indexes the document
// and replies with IndexingCompleted (or IndexError).
//
// fti has no UI and no filesystem dependencies beyond /tmp (ramdisk).
package main

import (
	"fmt"
	"os"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"

	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/fti"
	"mazzy/shared/ipc"
)

// taggedFTIReq wraps a decoded FTI request with the sender's SID.
// payload is either fti.IndexDocument or fti.SearchMail.
type taggedFTIReq struct {
	payload   any
	senderSID int16
}

// decodeFTIReqWithSID decodes a ProtoFTIReq (IndexDocument or SearchMail)
// and preserves the sender SID.
func decodeFTIReqWithSID(msg *ipc.UringIPCMsg) any {
	decoded := fti.DecodeFTIReq(msg)
	if decoded == nil {
		return nil
	}
	return taggedFTIReq{payload: decoded, senderSID: msg.SenderSID}
}

func main() {
	fmt.Println("[fti] main() entered")

	// Wait for core services before issuing any path syscalls.
	if err := sys.WaitForCoreServices(20); err != nil {
		panic(fmt.Sprintf("[fti] FATAL: core services: %v", err))
	}
	scratch, err := sys.SetupScratchDir(true)
	if err != nil {
		panic(fmt.Sprintf("[fti] FATAL: scratchdir: %v", err))
	}
	fmt.Printf("[fti] scratch dir: %s\n", scratch)

	// Bleve index lives inside the per-shepherd scratch dir so fti is fully
	// self-contained and doesn't depend on rachel pre-creating /tmp/data/fti.
	blevePath := scratch + "/bleve"
	os.RemoveAll(blevePath) // clean stale index from previous boot

	// Register async error callback so persister/merger crashes are visible.
	scorch.RegistryAsyncErrorCallbacks["log"] = func(err error, path string) {
		fmt.Printf("[fti] SCORCH ASYNC ERROR: %v (path=%s)\n", err, path)
		sys.DumpKernelStatus()
	}

	// Create bleve index with per-type document mappings.
	mapping := buildIndexMapping()
	index, err := bleve.NewUsing(blevePath, mapping, "scorch", "scorch", map[string]interface{}{
		"asyncErrorCallbackName": "log",
	})
	if err != nil {
		panic(fmt.Sprintf("[fti] FATAL: create bleve index: %v", err))
	}
	defer index.Close()
	fmt.Println("[fti] bleve index created")

	// Create the index and search handlers.
	indexH := newIndexHandler(index)
	searchH := newSearchHandler(index)

	// Set up uring dispatcher.
	d := uring.NewDispatcher()
	d.OnFunc(ipc.ProtoFTIReq, decodeFTIReqWithSID, func(v any) {
		tagged, ok := v.(taggedFTIReq)
		if !ok {
			return
		}
		switch req := tagged.payload.(type) {
		case fti.IndexDocument:
			indexH.handleIndexDocument(&req, tagged.senderSID)
		case fti.SearchMail:
			searchH.handleSearchMail(&req, tagged.senderSID)
		}
	})
	d.OnDeath(func(deadSID int16) {
		fmt.Printf("[fti] shepherd %d died\n", deadSID)
	})
	d.Start()
	fmt.Println("[fti] dispatcher started")

	// Signal readiness.
	sys.SetReady(true)
	fmt.Println("[fti] Ready=true")
	sys.StartMemStatsLogger("fti", 0)

	// Block forever — the dispatcher goroutine handles everything.
	select {}
}
