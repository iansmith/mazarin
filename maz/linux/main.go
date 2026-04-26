// linux is a userspace shepherd that handles Linux file syscalls (open, read,
// write, close, seek, etc.) via delegation, and owns the serial port soft IRQ.
// Console output lines are forwarded to linux-ui.maz for display.
package main

import (
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/linuxio"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/serial"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/queue"
	"mazzy/shared/sysid"
)

// suppressSerialCopy is set via SUPPRESS_SERIAL_STDIO_COPY env var.
var suppressSerialCopy bool

// delegateMsg carries processed delegate data to the line accumulator.
type delegateMsg struct {
	fd   byte
	data []byte
}

// readDataResponse is a pending read(0) request waiting for stdin input.
type readDataResponse struct {
	req       sys.SyscallRequest
	callerSID int16 // tracked for death-cleanup refcounting
}

// deathNotification is a sentinel type sent through delegateCh when a shepherd
// dies. Routing through the delegate channel ensures all state access
// (sidStates map) happens on a single goroutine.
type deathNotification struct {
	deadSID int16
}

// stdinDecRefNotification is sent through delegateCh when fulfillRead completes
// a stdin read. Routes the sidDecRef call to the delegate handler goroutine.
type stdinDecRefNotification struct {
	sid int16
}

// idleFlushNotification is sent through delegateCh when the kernel is about to
// enter WFI (no ready threads). The delegate handler flushes one write buffer.
type idleFlushNotification struct{}

// shepherdState tracks per-SID refcount for the two-phase death protocol.
// refcount starts at 1 ("alive" baseline). Each in-flight read/write: +1.
// Each completed read/write: -1. When dying && refcount<=1: ACK to kernel.
type shepherdState struct {
	refcount int32
	dying    bool
}

// sidStates maps shepherd SIDs to their refcount state.
// Accessed from both the file-lane and stdout-lane delegate goroutines, so
// every touch goes through sidStatesMu. It's a counter map (plus dying bit),
// not fs state — the lock surface is trivial.
var (
	sidStatesMu sync.Mutex
	sidStates   = make(map[int16]*shepherdState)
)

// globalHandler is the syscall handler, set during main() startup.
// Accessed from the delegate handler goroutine for per-SID cleanup on death.
var globalHandler *syscallHandler

// reqQueue holds outstanding read(0) delegate requests.
// The delegate handler enqueues; the drainer (main goroutine) dequeues.
var reqQueue = queue.New[*readDataResponse]()

// getOrCreateSIDStateLocked returns the shepherdState for the given SID,
// creating one with refcount=1 if it doesn't exist. Caller must hold
// sidStatesMu.
func getOrCreateSIDStateLocked(sid int16) *shepherdState {
	st := sidStates[sid]
	if st == nil {
		st = &shepherdState{refcount: 1}
		sidStates[sid] = st
	}
	return st
}

// sidIncRef increments the refcount for the given SID.
func sidIncRef(sid int16) {
	sidStatesMu.Lock()
	st := getOrCreateSIDStateLocked(sid)
	st.refcount++
	sidStatesMu.Unlock()
}

// sidDecRef decrements the refcount for the given SID. If the shepherd is
// dying and refcount has reached 1 (baseline), all I/O has drained — clean
// up per-SID state and ACK to the kernel.
func sidDecRef(sid int16) {
	sidStatesMu.Lock()
	st := sidStates[sid]
	if st == nil {
		sidStatesMu.Unlock()
		return
	}
	st.refcount--
	drained := st.dying && st.refcount <= 1
	if drained {
		delete(sidStates, sid)
	}
	sidStatesMu.Unlock()
	if drained {
		if globalHandler != nil {
			globalHandler.cleanupShepherd(sid)
		}
		sys.DeathAck(sid)
		fmt.Printf("[linux] DeathAck sent for SID %d\n", sid)
	}
}

// handleDeathNotification processes a death notification for a shepherd.
// Drains any queued stdin reads for the dead SID (they'll never be fulfilled),
// then checks if all I/O has drained. ACKs immediately if refcount<=1.
func handleDeathNotification(deadSID int16) {
	sidStatesMu.Lock()
	st := getOrCreateSIDStateLocked(deadSID)
	st.dying = true
	sidStatesMu.Unlock()

	// Drain queued stdin reads for the dead SID. These will never be
	// fulfilled since the shepherd is gone. Reply with 0 (EOF) and
	// decrement the refcount for each.
	staleReads := reqQueue.RemoveWhere(func(rdr *readDataResponse) bool {
		return rdr.callerSID == deadSID
	})
	for _, rdr := range staleReads {
		rdr.req.Reply(0) // SyscallReply no-ops the wake for dead threads
	}

	sidStatesMu.Lock()
	st.refcount -= int32(len(staleReads)) // paired with sidIncRef in sysRead stdin branch
	refcount := st.refcount
	drained := refcount <= 1
	if drained {
		delete(sidStates, deadSID)
	}
	sidStatesMu.Unlock()

	if len(staleReads) > 0 {
		fmt.Printf("[linux] drained %d stale stdin reads for SID %d\n", len(staleReads), deadSID)
	}

	fmt.Printf("[linux] death notification for SID %d, refcount=%d\n", deadSID, refcount)
	if drained {
		// All I/O drained — clean up per-shepherd filesystem state (FDs, flocks).
		if globalHandler != nil {
			globalHandler.cleanupShepherd(deadSID)
		}
		sys.DeathAck(deadSID)
		fmt.Printf("[linux] DeathAck sent immediately for SID %d\n", deadSID)
	}
}

// startUringDelegateHandler spawns two goroutines that consume the delegate
// lanes split in startUringDispatchers:
//
//   - File lane (delegateCh): file-system syscalls (may block on fsclient),
//     stdin reads (async via reqQueue), and notifications (death, idleFlush,
//     stdinDecRef). Single goroutine — preserves existing serialization
//     required by syscallHandler's per-shepherd maps and by fsclient's shared
//     data area.
//
//   - Stdout lane (stdoutCh): only Write(fd ≤ 2). Runs concurrently with the
//     file lane so fs.maz's fmt.Printf can complete even while the file lane
//     is blocked inside fsclient.call waiting for fs to respond. Touches
//     only sidStates (mutex-guarded) and dataCh — never fsclient.
//
// suppressSerialCopy is retained for API parity but no longer consulted in
// this function (the kernel already pushed bytes to the TX ring before
// delegating; we just reply and forward to the line accumulator).
func startUringDelegateHandler(delegateCh chan any, stdoutCh chan sys.SyscallRequest, handler *syscallHandler, suppressSerialCopy bool) <-chan delegateMsg {
	_ = suppressSerialCopy
	dataCh := make(chan delegateMsg, 32)
	// Forward stdin decRef completions to the delegate (file) channel.
	go func() {
		for sid := range stdinDecRefCh {
			delegateCh <- stdinDecRefNotification{sid: sid}
		}
	}()
	// Stdout lane — handles fd≤2 Writes only; never blocks on fsclient.
	//
	// Write delegation is pipe-buffered (kernel returned the byte count to
	// the caller at delegate time; the caller did not block waiting for us).
	// We consume the bytes, forward them into the line accumulator, and
	// release the shared data page via ReleaseDelegatePage. There is no
	// Reply() call — the caller is not blocked.
	go func() {
		for v := range stdoutCh {
			sid := v.CallerPID
			fd := byte(v.Arg0())
			pageVA := v.DataVA()
			sidIncRef(sid)
			data := v.Data()
			if data == nil {
				sidDecRef(sid)
				if pageVA != 0 {
					sys.ReleaseDelegatePage(pageVA, 1)
				}
				continue
			}
			dataCopy := make([]byte, len(data))
			copy(dataCopy, data)
			// Kernel already pushed bytes to PL011 TX ring before
			// delegating — no need to echo to UART here.
			sidDecRef(sid)
			if pageVA != 0 {
				sys.ReleaseDelegatePage(pageVA, 1)
			}
			select {
			case dataCh <- delegateMsg{fd: fd, data: dataCopy}:
			default:
			}
		}
	}()
	// File lane — everything else.
	go func() {
		for raw := range delegateCh {
			switch v := raw.(type) {
			case deathNotification:
				handleDeathNotification(v.deadSID)

			case stdinDecRefNotification:
				sidDecRef(v.sid)

			case idleFlushNotification:
				handler.flushOneBuffer()

			case sys.SyscallRequest:
				sid := v.CallerPID
				// For stdin reads, sidIncRef happens at enqueue time in sysRead,
				// and sidDecRef happens in fulfillRead. For all other syscalls,
				// the request completes synchronously here.
				if v.SysID == sysid.Read && handler.isStdinRead(v) {
					// Don't wrap with inc/dec — sysRead handles it.
					handler.handle(v)
				} else {
					sidIncRef(sid)
					handler.handle(v)
					sidDecRef(sid)
				}
			}
		}
	}()
	return dataCh
}

// addCRBeforeLF inserts \r before each \n for serial terminal compatibility.
func addCRBeforeLF(data []byte) []byte {
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	if n == 0 {
		return data
	}
	out := make([]byte, len(data)+n)
	j := 0
	for _, b := range data {
		if b == '\n' {
			out[j] = '\r'
			j++
		}
		out[j] = b
		j++
	}
	return out
}

// startUringDispatchers sets up three uring Dispatchers, one per ring,
// each with its own reader goroutine. Splitting traffic by class avoids
// a single-reader bottleneck where slow file-lane operations would
// backpressure pipe-buffered stdio (and vice versa).
//
//   - Ring 0: shepherd IPC (fs responses, WM, fonts, death notifications,
//     idle-flush hints). Reader feeds the relevant typed channels.
//   - Ring 1: blocking delegated syscalls (Read, Openat, Close, Mkdirat,
//     Fstat, MmapPageFill, Pwrite64, fd>2 Write, …). Reader feeds
//     delegateCh; the file-lane goroutine drains it via fsclient.
//   - Ring 2: pipe-buffered Writes only (Write fd<=2). Reader feeds
//     stdoutCh; the stdout-lane goroutine consumes the data and
//     releases the shared page via SysReleaseDelegatePage.
//
// The kernel routes Write fd<=2 to ring 2 because the linux shepherd
// calls SysRegisterStdioWriteRing(2). All other syscalls — including
// Write fd>2 — flow through ring 1 per their normal RegisterSyscall-
// Handler ringIdx (1).
func startUringDispatchers(fsClient *fsclient.Client, delegateCh chan any, stdoutCh chan sys.SyscallRequest, wmCh chan any, fontReplyCh chan any) {
	// Ring 0: shepherd IPC
	ipcDispatcher := uring.NewDispatcher() // ring 0 (default)
	ipcDispatcher.On(ipc.ProtoShepherdNotify, decodeRawPayload, wmCh)
	ipcDispatcher.On(ipc.ProtoFontResponse, decodeRawPayload, fontReplyCh)
	ipcDispatcher.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fsClient.RespCh)
	ipcDispatcher.OnDeath(func(deadSID int16) {
		fmt.Printf("[linux] shepherd %d died\n", deadSID)
		// Route through delegateCh so death handling runs on the file lane.
		delegateCh <- deathNotification{deadSID: deadSID}
	})
	// Kernel sends this just before WFI when no threads are ready.
	// Route to delegate handler so flush runs on the same goroutine as writes.
	decodeNoop := func(_ *ipc.UringIPCMsg) any { return nil }
	ipcDispatcher.OnFunc(ipc.ProtoIdleFlushHint, decodeNoop, func(v any) {
		select {
		case delegateCh <- idleFlushNotification{}:
		default: // drop if channel full — shepherd is already busy
		}
	})
	ipcDispatcher.Start()

	// Ring 1: blocking delegated syscalls. Includes fd>2 Write (the
	// kernel routes fd<=2 Writes to ring 2 separately).
	delegateDispatcher := uring.NewDispatcherWithRing(1)
	delegateDispatcher.OnFunc(ipc.ProtoFSDelegateReq, sys.DecodeFSDelegateReq, func(v any) {
		req, ok := v.(sys.SyscallRequest)
		if !ok {
			return
		}
		delegateCh <- req
	})
	delegateDispatcher.Start()

	// Ring 2: pipe-buffered stdio Writes only (Write fd<=2 via the
	// SysRegisterStdioWriteRing override). The reader does the minimum
	// amount of work — hand the request to the stdout-lane goroutine
	// over stdoutCh and return — so stdio can't be backpressured by a
	// busy file lane on ring 1.
	stdioDispatcher := uring.NewDispatcherWithRing(2)
	stdioDispatcher.OnFunc(ipc.ProtoFSDelegateReq, sys.DecodeFSDelegateReq, func(v any) {
		req, ok := v.(sys.SyscallRequest)
		if !ok {
			return
		}
		stdoutCh <- req
	})
	stdioDispatcher.Start()
}

// decodeRawPayload copies the raw UringIPCMsg payload without interpreting it.
// The .maz decodes the font response in its own address space so concrete
// type assertions (wm.OpenFontReply, etc.) match its own type descriptors.
func decodeRawPayload(msg *ipc.UringIPCMsg) any {
	raw := make([]byte, len(msg.Payload))
	copy(raw, msg.Payload[:])
	return raw
}

// forceLinuxIOItab ensures the linker includes the LinuxIO interface type
// descriptor and all method wrappers for *LinuxIOInit.
//
//go:noinline
func forceLinuxIOItab(v interface{}) {
	io, ok := v.(linuxio.LinuxIO)
	if !ok {
		return
	}
	_ = io.ReadChannel()
	_ = io.WriteChannel()
	_ = io.WMChannel()
	_ = io.FontReplyChannel()
	_ = io.NotifyChannel()
	io.SetChannels(nil, nil, nil, nil, nil) //nolint:staticcheck
	_ = io.GetRachelSID()
}

// lineAccumulator reads serial bytes and delegate messages, accumulates
// characters, and flushes complete lines (on \n) to the WriteCh.
func lineAccumulator(serialCh <-chan serial.SerialByte, delegateCh <-chan delegateMsg, writeCh chan<- linuxio.LineLine, notifyCh chan<- struct{}) {
	var stdoutBuf, stderrBuf []byte

	flush := func(fd byte) {
		buf := &stdoutBuf
		if fd == 2 {
			buf = &stderrBuf
		}
		if len(*buf) == 0 {
			return
		}
		line := make([]byte, len(*buf))
		copy(line, *buf)
		select {
		case writeCh <- linuxio.LineLine{Fd: fd, Data: line}:
			// Wake the runLoop's select non-blocking; if a poke is
			// already pending, the next drain() will pick up this
			// line along with the previous one.
			select {
			case notifyCh <- struct{}{}:
			default:
			}
		default:
			// WriteCh full — drop oldest line if channel is backed up.
		}
		*buf = nil
	}

	accum := func(b byte, fd byte) {
		buf := &stdoutBuf
		if fd == 2 {
			buf = &stderrBuf
		}
		*buf = append(*buf, b)
		if b == '\n' {
			flush(fd)
		}
	}

	for {
		select {
		case sb := <-serialCh:
			accum(sb.B, sb.Fd)

		case msg := <-delegateCh:
			for _, b := range msg.data {
				accum(b, msg.fd)
			}
		}
	}
}

// MazEntryPoint holds a reference to MazarinMain to prevent DCE of the
// plugin entry point. The generic shepherd host looks up MazarinMain via
// mazdl; without this reference the linker would drop the symbol.
var MazEntryPoint func() = MazarinMain

func MazarinMain() {
	fmt.Printf("[linux] MazarinMain() entered\n")

	// 1. Wait for fs (needed by syscallHandler for file operations).
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[linux] FATAL: fs: %v", err))
	}
	// 2. Wait for rachel (needed by linux-ui for window management).
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[linux] FATAL: rachel: %v", err))
	}
	rachelSID := sys.MustGetShepherdByName("rachel")
	fsSID := sys.MustGetShepherdByName("fs")
	fsClient := fsclient.New(fsSID)

	// 3. Prepare LinuxIO injection struct — shepherd only provides rachelSID.
	// All font/glyph infrastructure lives in the .maz.
	ioInit := &linuxio.LinuxIOInit{
		RachelSIDVal: rachelSID,
	}

	// Force itab so cross-.maz type assertion works.
	forceLinuxIOItab(ioInit)

	// 4. Set up uring dispatchers BEFORE loading .maz.
	// Ring 0 is auto-created; ring 1 carries blocking delegated syscalls;
	// ring 2 carries pipe-buffered stdio Writes (Write fd<=2). The two
	// extra rings exist so stdio backpressure can't starve the blocking-
	// delegate path (or vice versa).
	if err := uring.Setup(1); err != nil {
		panic("[linux] uring.Setup(1) failed: " + err.Error())
	}
	if err := uring.Setup(2); err != nil {
		panic("[linux] uring.Setup(2) failed: " + err.Error())
	}
	// WM and font response messages go to temp channels that will be
	// forwarded to the .maz's channels after injection.
	tempWMCh := make(chan any, 8)
	tempFontReplyCh := make(chan any, 8)
	delegateCh := make(chan any, 8)
	// stdoutCh receives Write(fd≤2) requests so they bypass the file lane
	// and can be processed while the file lane is blocked on fsclient.
	stdoutCh := make(chan sys.SyscallRequest, 32)
	startUringDispatchers(fsClient, delegateCh, stdoutCh, tempWMCh, tempFontReplyCh)
	if err := fsClient.Connect(); err != nil {
		panic(fmt.Sprintf("[linux] FATAL: fsclient.Connect: %v", err))
	}
	// 5. Load linux-ui.maz and inject LinuxIO.
	uiPath := sys.LoadMazByName("/linux-ui")
	fmt.Printf("[linux] loading linux-ui from %s...\n", uiPath)
	uiMain, uiInitAddr, uiErr := mazhost.LoadMazBootstrap(uiPath, nil)
	if uiErr != nil {
		panic(fmt.Sprintf("[linux] LoadMazBootstrap(linux-ui) failed: %v", uiErr))
	}

	if uiInitAddr != 0 {
		type funcval struct{ fn uintptr }
		fv := &funcval{fn: uiInitAddr}
		shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
		if err := shepherdInit(ioInit); err != nil {
			fmt.Printf("[linux] linux-ui MazarinShepherd failed: %v\n", err)
		}
	}

	// Read channels back from the .maz-filled struct.
	writeCh := ioInit.WriteCh
	readCh := ioInit.ReadCh
	wmCh := ioInit.WMCh
	fontReplyCh := ioInit.FontReplyCh
	notifyCh := ioInit.NotifyCh

	if writeCh == nil || readCh == nil || wmCh == nil || fontReplyCh == nil || notifyCh == nil {
		panic("[linux] FATAL: linux-ui did not create channels")
	}

	// Forward WM and font messages from uring dispatcher to linux-ui's
	// typed []byte channels. The dispatcher produces any-wrapped []byte;
	// we extract the []byte here so the .maz doesn't need a cross-module
	// type assertion ([]byte type descriptors differ across .maz boundaries).
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

	// Launch linux-ui.maz goroutine.
	go mazhost.RunMaz(uiMain)
	fmt.Printf("[linux] linux-ui.maz launched\n")

	// 6. Register syscall delegates.
	suppressSerialCopy = os.Getenv("SUPPRESS_SERIAL_STDIO_COPY") == "1"
	globalHandler = newSyscallHandler(fsClient)
	handler := globalHandler
	delegateErr := sys.RegisterSyscallHandlersWithRing(1,
		sysid.Write, sysid.Read, sysid.Openat, sysid.Close,
		sysid.Lseek, sysid.Fstat, sysid.Fstatat,
		sysid.Mkdirat, sysid.Unlinkat, sysid.Renameat,
		sysid.Ftruncate, sysid.Getdents64, sysid.Readlinkat,
		sysid.Faccessat, sysid.Fchmodat, sysid.Utimensat,
		sysid.Getcwd, sysid.Chdir, sysid.Fchdir,
		sysid.Ioctl, sysid.Writev, sysid.Readv,
		sysid.Statfs, sysid.Fstatfs, sysid.Fsync, sysid.Fdatasync,
		sysid.Flock, sysid.Pread64, sysid.Pwrite64,
	)
	if delegateErr != nil {
		sys.UartWriteString(fmt.Sprintf("[linux] RegisterSyscallHandlers failed: %v\n", delegateErr))
	}

	// Route pipe-buffered stdio Writes (Write fd<=2) to ring 2 instead
	// of the default ring (ring 1). The kernel checks this on every
	// Write delegate; fd>2 Writes still flow through ring 1.
	if err := sys.RegisterStdioWriteRing(2); err != nil {
		sys.UartWriteString(fmt.Sprintf("[linux] RegisterStdioWriteRing(2) failed: %v\n", err))
	}

	var delegateDataCh <-chan delegateMsg
	if delegateErr == nil {
		delegateDataCh = startUringDelegateHandler(delegateCh, stdoutCh, handler, suppressSerialCopy)
	}

	// 7. Serial port soft IRQ.
	serialCh, err := serial.Chars()
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[linux] serial.Chars failed: %v\n", err))
		return
	}

	// 8. Signal readiness.
	sys.SetReady(true)
	sys.StartMemStatsLogger("linux", 0) // default 30s

	// 9. Launch helloworld.maz.
	mazhost.LaunchMaz("helloworld")

	// 10. Line accumulator goroutine — serial + delegates -> WriteCh.
	go lineAccumulator(serialCh, delegateDataCh, writeCh, notifyCh)

	// 11. ReadChannel watcher — moves input lines from channel to queue.
	lineQueue := queue.New[[]byte]()
	go func() {
		for line := range readCh {
			lineQueue.Enqueue(line)
		}
	}()

	// 12. Main goroutine becomes the stdin drainer.
	// Pairs input lines with outstanding read(0) requests.
	for {
		select {
		case <-lineQueue.Wake():
			// Input line arrived — check for pending read(0) request.
			for lineQueue.Len() > 0 && reqQueue.Len() > 0 {
				line, _ := lineQueue.Dequeue()
				rdr, _ := reqQueue.Dequeue()
				fulfillRead(rdr, line)
			}
		case <-reqQueue.Wake():
			// Read(0) request arrived — check for pending input line.
			for lineQueue.Len() > 0 && reqQueue.Len() > 0 {
				line, _ := lineQueue.Dequeue()
				rdr, _ := reqQueue.Dequeue()
				fulfillRead(rdr, line)
			}
		case <-time.After(500 * time.Millisecond):
			// Periodic check — pair any accumulated items.
			for lineQueue.Len() > 0 && reqQueue.Len() > 0 {
				line, _ := lineQueue.Dequeue()
				rdr, _ := reqQueue.Dequeue()
				fulfillRead(rdr, line)
			}
		}
	}
}

// stdinDecRefCh carries SIDs back to the delegate handler goroutine for decRef.
// fulfillRead runs on the main goroutine but sidDecRef must run on the delegate
// handler goroutine for single-goroutine safety of sidStates.
var stdinDecRefCh = make(chan int16, 32)

// fulfillRead responds to a pending read(0) delegate request with the given line.
// Sends the callerSID to stdinDecRefCh for the delegate handler to do sidDecRef.
func fulfillRead(rdr *readDataResponse, line []byte) {
	buf := rdr.req.DataBuf()
	if buf == nil {
		rdr.req.Reply(0)
		stdinDecRefCh <- rdr.callerSID
		return
	}
	n := copy(buf, line)
	rdr.req.Reply(int64(n))
	stdinDecRefCh <- rdr.callerSID
}
