// protocol-http.maz — MAZ-49 shepherd that owns HTTP/1.1 framing, TLS,
// x509, and the per-process CA bundle. Consumers call into it via uring
// IPC through mazarin/httpclient; protocol-http translates each Do
// request into a TLS connection over mazarin/netclient and writes the
// response back into the caller's pre-posted Slab.
//
// This file is item 2 of the MAZ-49 plan: shepherd skeleton. It boots,
// claims its dedicated response ring, calls sys.SetReady, and idles.
// The ProtoHttpIPCReq dispatch handler lands in item 7 (dispatch.go).
//
// Go package directories can have hyphens; Go package identifiers cannot.
// The package declaration is protocolhttp; the binary is protocol-http.maz.
package main

import (
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
)

const tag = "[protocol-http] "

// protoHttpRespRing is the dedicated uring ring protocol-http uses for
// outbound netclient calls and inbound ShareConsumer notifications. Per
// the FS-dedicated-response-ring rule, every shepherd that talks to
// another shepherd allocates a non-default ring to avoid head-of-line
// deadlock on ring 0. Ring 1 is the conventional first non-default ring;
// dispatch.go (item 7) will use it.
const protoHttpRespRing = 1

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start\n")
	if err := boot(); err != nil {
		sys.UartWriteString(tag + "FAIL: " + err.Error() + "\n")
		// Even on failure, signal ready so the kernel doesn't deadlock other
		// shepherds waiting on us. The FAIL message above is the diagnostic.
		sys.SetReady(true)
		select {}
	}
	sys.UartWriteString(tag + "ready\n")
	sys.SetReady(true)
	select {}
}

// boot performs the one-time setup: claims the dedicated response ring.
// The IPC dispatcher and netclient/share-consumer wiring land in item 7.
func boot() error {
	// Wait for net to come up — protocol-http will dial TCP through it
	// once item 7 lands. Doing the WaitForShepherdReady at boot keeps the
	// "ready" line on the serial log honest: when we print it, net is
	// genuinely available.
	if err := sys.WaitForShepherdReady("net", 5); err != nil {
		return wrap("WaitForShepherdReady(net)", err)
	}

	if err := uring.Setup(protoHttpRespRing); err != nil {
		return wrap("uring.Setup(response ring)", err)
	}
	return nil
}

// wrap is a tiny error-with-context helper — fmt.Errorf would pull
// fmt's transitive dependencies into the skeleton for one call site.
// Kept local; replace with fmt.Errorf once item 7 imports fmt anyway.
func wrap(what string, err error) error {
	return &wrappedErr{what: what, err: err}
}

type wrappedErr struct {
	what string
	err  error
}

func (w *wrappedErr) Error() string { return w.what + ": " + w.err.Error() }
func (w *wrappedErr) Unwrap() error { return w.err }
