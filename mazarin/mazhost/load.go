// Package mazhost provides host-side support for loading .maz modules
// into a shepherd's address space. It wraps mazdl.OpenBytes, looks up
// MazarinMain/MazarinShepherd via the plugin's dynsym, and returns a
// callable func() built from a funcval.
//
// Build tag rationale: no `mazhost` tag here. The package is imported
// by both the generic shepherd (which links mazhost in and owns the
// resident copy — see dlopen-host-packages.txt) AND by plugins that
// want to spawn further plugins (rachel loads fontsvc, fs loads disk,
// etc.). In plugin builds the symbols compile but resolve through GOT
// to the shepherd's resident copy at load time, so the single
// modules/globalSyms registry stays consistent across the process.
package mazhost

import (
	"fmt"
	"hash/crc32"
	"runtime"
	"time"

	merror "mazzy/mazarin/error"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mazdl"
	"mazzy/shared/constants"
)

// LoadMazBootstrap loads a .maz module and returns (MazarinMain, MazarinShepherd-addr).
// The fc must already be connected. The shepherd argument is unused (historical API);
// the caller is responsible for invoking MazarinShepherd with its own injector by
// constructing a funcval from the returned second return value.
//
// On success, returns a func() that the caller should run as a goroutine.
func LoadMazBootstrap(fc fsclient.FSClient, filename string, _ any) (func(), uintptr, *merror.Error) {
	return loadMazInternal(fc, filename)
}

// LaunchMaz loads a .maz module by name and runs its MazarinMain in a new
// goroutine with a pre-grown stack. fc must already be connected.
func LaunchMaz(fc fsclient.FSClient, name string) {
	path := "/" + name + ".maz"
	mazMain, _, err := loadMazInternal(fc, path)
	if err != nil {
		panic(fmt.Sprintf("[mazhost] LaunchMaz(%q) failed: %v", name, err))
	}
	go runWithLargeStack(mazMain)
}

// RingInfo describes a uring ring passed from the generic shepherd to the
// replacement shepherd (.maz plugin) via injection.
type RingInfo struct {
	Number int     // ring index (0 = kernel-allocated inbound, 1 = fs)
	VA     uintptr // userspace VA of the ring's mapped pages
	Len    int     // number of 4KB pages
}

// ShepherdInjector is the interface the generic shepherd uses to pass
// ring and fsclient info to the replacement shepherd. Using an interface
// (not a concrete struct) works across .maz module boundaries.
type ShepherdInjector interface {
	GetRing0() RingInfo
	GetRing1() RingInfo
	GetFSClient() fsclient.FSClient
	GetSID() int
	GetArgs() []string
}

// ShepherdInit implements ShepherdInjector. The generic shepherd creates
// this and passes it to MazarinShepherd before calling MazarinMain.
type ShepherdInit struct {
	Ring0    RingInfo
	Ring1    RingInfo
	FSClient fsclient.FSClient
	SID      int
	Args     []string
}

// GetRing0 implements ShepherdInjector.
func (s *ShepherdInit) GetRing0() RingInfo { return s.Ring0 }

// GetRing1 implements ShepherdInjector.
func (s *ShepherdInit) GetRing1() RingInfo { return s.Ring1 }

// GetFSClient implements ShepherdInjector.
func (s *ShepherdInit) GetFSClient() fsclient.FSClient { return s.FSClient }

// GetSID implements ShepherdInjector.
func (s *ShepherdInit) GetSID() int { return s.SID }

// GetArgs implements ShepherdInjector.
func (s *ShepherdInit) GetArgs() []string { return s.Args }

// HostFSClient is set by the shepherd host before the .maz plugin runs.
// Plugins that need fs access (rachel, fontsvc) use this instead of
// creating their own connection — the host plugin share a SID, and
// fs only allows one connection per SID.
var HostFSClient fsclient.FSClient

// RunMaz runs an already-loaded .maz func on a pre-grown stack.
func RunMaz(fn func()) {
	runWithLargeStack(fn)
}

// runWithLargeStack allocates a 256KB stack frame before calling fn,
// preventing .maz code from hitting its broken morestack (which hangs
// forever due to uninitialized runtime globals in the PIE binary).
// The buffer is kept alive across fn() so GC's shrinkstack doesn't
// shrink the goroutine stack while .maz code is running.
//
//go:noinline
func runWithLargeStack(fn func()) {
	var buf [262144]byte
	buf[0] = 1
	buf[len(buf)-1] = 1
	if buf[131072] != 0 {
		panic("unreachable")
	}
	fn()
	runtime.KeepAlive(&buf)
}

// loadMazInternal is the private implementation shared by LoadMazBootstrap and
// LaunchMaz. Flow:
//
//  1. Ensure RegisterHost has been called (idempotent).
//  2. Read plugin bytes via fsclient.ReadFile.
//  3. Hand them to mazdl.OpenBytes, which maps segments, applies
//     relocations, runs DT_INIT_ARRAY (moduledata registration), and
//     publishes DEFINED exports.
//  4. Look up MazarinMain (required) and MazarinShepherd (optional).
//  5. Build funcvals and return.
func loadMazInternal(fc fsclient.FSClient, filename string) (func(), uintptr, *merror.Error) {
	if _, err := mazdl.RegisterHost(); err != nil {
		return nil, 0, merror.ErrInvalidELF.Wrap(fmt.Sprintf("%s: RegisterHost: %v", filename, err))
	}

	tRead := time.Now()
	data, err := fc.ReadFile(filename)
	if err != nil {
		return nil, 0, merror.ErrFileNotFound.Wrap(fmt.Sprintf("%s: %v", filename, err))
	}
	if len(data) == 0 {
		return nil, 0, merror.ErrFileNotFound.Wrap(fmt.Sprintf("%s: ReadFile returned 0 bytes", filename))
	}
	dRead := time.Since(tRead)

	// MAZ-15 instrumentation (gated): checksum the module image as received,
	// and again after OpenBytes maps it. crcIn vs the build-time file CRC
	// bisects the pipeline (disk→fs→transfer-IPC→heap vs mapping); crcIn vs
	// crcPost detects the shepherd's own heap copy being scribbled DURING
	// mapping.
	var crcIn uint32
	if constants.Maz15Debug {
		crcIn = crc32.ChecksumIEEE(data)
	}

	tOpen := time.Now()
	h, err := mazdl.OpenBytes(filename, data)
	if err != nil {
		return nil, 0, merror.ErrInvalidELF.Wrap(fmt.Sprintf("%s: OpenBytes: %v", filename, err))
	}
	dOpen := time.Since(tOpen)
	// PERF instrumentation: split .maz load into IPC-read vs relocate phases to
	// locate the ~30s boot stall. Times are captured before printing, so print
	// latency does not skew them.
	summary := fmt.Sprintf("[mazhost] %s: ReadFile=%dms (%d bytes) OpenBytes=%dms",
		filename, dRead.Milliseconds(), len(data), dOpen.Milliseconds())
	if constants.Maz15Debug {
		crcPost := crc32.ChecksumIEEE(data)
		summary += fmt.Sprintf(" crcIn=%08x crcPost=%08x", crcIn, crcPost)
	}
	fmt.Println(summary)

	entryAddr, err := h.Sym("MazarinMain")
	if err != nil {
		return nil, 0, merror.ErrNoSymbol.Wrap(fmt.Sprintf("%s: MazarinMain: %v", filename, err))
	}

	// MazarinShepherd is optional — plugins without injection (e.g. prefs, helloworld)
	// don't define it. h.Sym returns an error in that case and we leave shepherdAddr==0.
	var shepherdAddr uintptr
	if addr, sErr := h.Sym("MazarinShepherd"); sErr == nil {
		shepherdAddr = addr
	}

	mazMain := mazdl.Funcval[func()](entryAddr)

	return mazMain, shepherdAddr, nil
}
