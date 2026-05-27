// protocol-http.maz — MAZ-49 shepherd that owns HTTP/1.1 framing, TLS,
// x509, and the per-process CA bundle. Consumers call into it via uring
// IPC through mazarin/httpclient; protocol-http translates each Do
// request into a TLS connection over mazarin/netclient and writes the
// response back into the caller's pre-posted Slab.
//
// Go package directories can have hyphens; Go package identifiers cannot.
// The package declaration is main (it's a buildmode=plugin binary);
// the binary is protocol-http.maz.
package main

import (
	"crypto/x509"
	"fmt"

	"mazzy/maz/protocol-http/internal"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/netclient"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

const tag = "[protocol-http] "

// Dedicated rings, one per traffic class — keeps any single producer from
// stalling another behind head-of-line. The MEMORY.md "FS dedicated
// response ring" rule applies to fs; we extend the same pattern to net
// and the inbound client/share traffic so a slow Do call can't wedge
// fs/net responses.
const (
	clientRing = 0 // default — inbound ProtoHttpIPCReq + ProtoShareReq from clients
	fsRing     = 1 // fs responses (used only at boot to load the CA bundle)
	netRing    = 2 // net.elf responses (TCP connect, stream RX/TX completions)
)

const caBundlePath = "/protocol-http/ssl/cacert.pem"

// Globals owned by MazarinMain; handleDo reads them. Single-writer
// (boot) → many-reader (dispatch) so a plain variable is fine.
var (
	caPool *x509.CertPool
	nc     netclient.NetClient
)

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start\n")
	if err := boot(); err != nil {
		sys.UartWriteString(tag + "FAIL: " + err.Error() + "\n")
		// Even on failure signal ready so the kernel doesn't deadlock other
		// shepherds waiting on us. The FAIL above is the diagnostic.
		sys.SetReady(true)
		select {}
	}
	sys.UartWriteString(tag + "ready\n")
	sys.SetReady(true)
	select {}
}

func boot() error {
	// Order matters: fs first (we read the CA from disk), net second
	// (Do dials TCP through it). Don't claim the inbound client ring
	// (ring 0, the default) for a dispatcher until both upstreams are
	// up — otherwise a client might race us with a Do before we have a
	// CA pool to verify against.
	if err := sys.WaitForShepherdReady("fs", 5); err != nil {
		return fmt.Errorf("WaitForShepherdReady(fs): %w", err)
	}
	if err := sys.WaitForShepherdReady("net", 5); err != nil {
		return fmt.Errorf("WaitForShepherdReady(net): %w", err)
	}

	fc, err := setupFS()
	if err != nil {
		return fmt.Errorf("setupFS: %w", err)
	}
	caBytes, err := fc.ReadFile(caBundlePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", caBundlePath, err)
	}
	caPool, err = internal.LoadCAPool(caBytes)
	if err != nil {
		return fmt.Errorf("LoadCAPool: %w", err)
	}
	sys.UartWriteString(tag + "loaded CA bundle from " + caBundlePath + "\n")

	nc, err = setupNet()
	if err != nil {
		return fmt.Errorf("setupNet: %w", err)
	}

	// Wire the inbound client ring: ProtoHttpIPCReq dispatch + share
	// consumer + share-release (so our own outbound shares, if we ever
	// publish any, can be released by their consumers).
	disp := uring.NewDispatcher() // ring 0 = clientRing
	disp.OnFunc(ipc.ProtoHttpIPCReq, decodeHttpDoReq, handleDo)
	transfer.RegisterShareConsumer(disp)
	transfer.RegisterShareRelease(disp)
	disp.Start()
	return nil
}

// setupFS wires fsclient on the dedicated fs response ring.
func setupFS() (fsclient.FSClient, error) {
	if err := uring.Setup(fsRing); err != nil {
		return nil, fmt.Errorf("uring.Setup(%d): %w", fsRing, err)
	}
	fsSID, err := sys.GetShepherdByName("fs")
	if err != nil {
		return nil, fmt.Errorf("GetShepherdByName(fs): %w", err)
	}
	fc := fsclient.New(fsSID)
	fc.SetRespRing(uint8(fsRing))

	disp := uring.NewDispatcherWithRing(fsRing)
	disp.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fc.GetRespCh())
	disp.Start()

	if err := fc.Connect(); err != nil {
		return nil, fmt.Errorf("fc.Connect: %w", err)
	}
	return fc, nil
}

// setupNet wires netclient on the dedicated net response ring.
func setupNet() (netclient.NetClient, error) {
	if err := uring.Setup(netRing); err != nil {
		return nil, fmt.Errorf("uring.Setup(%d): %w", netRing, err)
	}
	netSID, err := sys.GetShepherdByName("net")
	if err != nil {
		return nil, fmt.Errorf("GetShepherdByName(net): %w", err)
	}
	c := netclient.New(netSID)

	disp := uring.NewDispatcherWithRing(netRing)
	disp.OnFunc(ipc.ProtoNetIPCResp, netclient.DecodeAny, c.HandleResp)
	disp.Start()

	if err := c.Connect(uint8(netRing), 0); err != nil {
		return nil, fmt.Errorf("nc.Connect: %w", err)
	}
	return c, nil
}
