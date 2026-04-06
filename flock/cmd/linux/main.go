// linux is a userspace shepherd that handles Linux file syscalls (open, read,
// write, close, seek, etc.) via delegation, and owns the serial port soft IRQ.
// Console output lines are forwarded to linux-ui.maz for display.
package main

import (
	"fmt"
	"os"
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
	req sys.SyscallRequest
}

// reqQueue holds outstanding read(0) delegate requests.
// The delegate handler enqueues; the drainer (main goroutine) dequeues.
var reqQueue = queue.New[*readDataResponse]()

// startUringDelegateHandler runs a goroutine that processes delegated syscalls
// received via uring Dispatcher. Write to fd 1/2 is echoed to UART and
// forwarded to the line accumulator. All other syscalls go to syscallHandler.
func startUringDelegateHandler(delegateCh <-chan any, handler *syscallHandler, suppressSerialCopy bool) <-chan delegateMsg {
	dataCh := make(chan delegateMsg, 32)
	go func() {
		for raw := range delegateCh {
			req, ok := raw.(sys.SyscallRequest)
			if !ok {
				continue
			}
			if req.SysID == sysid.Write {
				fd := byte(req.Arg0())
				if fd <= 2 {
					data := req.Data()
					if data == nil {
						req.Reply(0)
						continue
					}
					dataCopy := make([]byte, len(data))
					copy(dataCopy, data)
					if fd == 2 || !suppressSerialCopy {
						sys.UartWrite(addCRBeforeLF(data))
					}
					req.Reply(int64(len(data)))
					select {
					case dataCh <- delegateMsg{fd: fd, data: dataCopy}:
					default:
					}
					continue
				}
			}
			handler.handle(req)
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

// startUringDispatcher sets up the uring Dispatcher.
// WM messages go to wmCh, font responses go to fontReplyCh (both forwarded
// to linux-ui via LinuxIO channels). The shepherd does not interpret these
// messages — it only routes them.
func startUringDispatcher(fsClient *fsclient.Client, delegateCh chan any, wmCh chan any, fontReplyCh chan any) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, decodeRawPayload, wmCh)
	d.On(ipc.ProtoFontResponse, decodeRawPayload, fontReplyCh)
	d.On(ipc.ProtoFSDelegateReq, sys.DecodeFSDelegateReq, delegateCh)
	d.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fsClient.RespCh)
	d.OnDeath(func(deadSID int16) {
		sys.UartWriteString(fmt.Sprintf("[linux] shepherd %d died\n", deadSID))
	})
	d.Start()
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
	io.SetChannels(nil, nil, nil, nil) //nolint:staticcheck
	_ = io.GetRachelSID()
}

// lineAccumulator reads serial bytes and delegate messages, accumulates
// characters, and flushes complete lines (on \n) to the WriteCh.
func lineAccumulator(serialCh <-chan serial.SerialByte, delegateCh <-chan delegateMsg, writeCh chan<- linuxio.LineLine) {
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

func main() {
	sys.UartWriteString("[linux] main() entered\n")

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

	sys.UartWriteString("[linux] LinuxIO config prepared\n")

	// 4. Set up uring dispatcher BEFORE loading .maz.
	// WM and font response messages go to temp channels that will be
	// forwarded to the .maz's channels after injection.
	tempWMCh := make(chan any, 8)
	tempFontReplyCh := make(chan any, 8)
	delegateCh := make(chan any, 8)
	startUringDispatcher(fsClient, delegateCh, tempWMCh, tempFontReplyCh)
	if err := fsClient.Connect(); err != nil {
		panic(fmt.Sprintf("[linux] FATAL: fsclient.Connect: %v", err))
	}
	sys.UartWriteString("[linux] fs IPC connected via uring\n")

	// 5. Load linux-ui.maz and inject LinuxIO.
	uiPath := sys.LoadMazByName("/linux-ui")
	sys.UartWriteString(fmt.Sprintf("[linux] loading linux-ui from %s...\n", uiPath))
	uiMain, uiInitAddr, uiErr := mazhost.LoadMazBootstrap(uiPath, nil)
	if uiErr != nil {
		panic(fmt.Sprintf("[linux] LoadMazBootstrap(linux-ui) failed: %v", uiErr))
	}

	if uiInitAddr != 0 {
		type funcval struct{ fn uintptr }
		fv := &funcval{fn: uiInitAddr}
		shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
		if err := shepherdInit(ioInit); err != nil {
			sys.UartWriteString("[linux] linux-ui MazarinShepherd failed: " + err.Error() + "\n")
		}
	}

	// Read channels back from the .maz-filled struct.
	writeCh := ioInit.WriteCh
	readCh := ioInit.ReadCh
	wmCh := ioInit.WMCh
	fontReplyCh := ioInit.FontReplyCh

	if writeCh == nil || readCh == nil || wmCh == nil || fontReplyCh == nil {
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
	sys.UartWriteString("[linux] linux-ui.maz launched\n")

	// 6. Register syscall delegates.
	suppressSerialCopy = os.Getenv("SUPPRESS_SERIAL_STDIO_COPY") == "1"
	handler := newSyscallHandler(fsClient)
	delegateErr := sys.RegisterSyscallHandlers(
		sysid.Write, sysid.Read, sysid.Openat, sysid.Close,
		sysid.Lseek, sysid.Fstat, sysid.Fstatat,
		sysid.Mkdirat, sysid.Unlinkat, sysid.Renameat,
		sysid.Ftruncate, sysid.Getdents64, sysid.Readlinkat,
		sysid.Faccessat, sysid.Fchmodat, sysid.Utimensat,
		sysid.Getcwd, sysid.Chdir, sysid.Fchdir,
		sysid.Ioctl, sysid.Writev, sysid.Readv,
		sysid.Statfs, sysid.Fstatfs, sysid.Fsync, sysid.Fdatasync,
	)
	if delegateErr != nil {
		sys.UartWriteString(fmt.Sprintf("[linux] RegisterSyscallHandlers failed: %v\n", delegateErr))
	}

	var delegateDataCh <-chan delegateMsg
	if delegateErr == nil {
		delegateDataCh = startUringDelegateHandler(delegateCh, handler, suppressSerialCopy)
	}

	// 7. Serial port soft IRQ.
	serialCh, err := serial.Chars()
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[linux] serial.Chars failed: %v\n", err))
		return
	}

	// 8. Signal readiness.
	sys.SetReady(true)
	sys.UartWriteString("[linux] Ready=true\n")

	// 9. Launch helloworld.maz.
	mazhost.LaunchMaz("helloworld")

	// 10. Line accumulator goroutine — serial + delegates -> WriteCh.
	go lineAccumulator(serialCh, delegateDataCh, writeCh)

	// 11. ReadChannel watcher — moves input lines from channel to queue.
	lineQueue := queue.New[[]byte]()
	go func() {
		for line := range readCh {
			lineQueue.Enqueue(line)
		}
	}()

	// 12. Main goroutine becomes the stdin drainer.
	// Pairs input lines with outstanding read(0) requests.
	sys.UartWriteString("[linux] entering stdin drainer loop\n")
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

// fulfillRead responds to a pending read(0) delegate request with the given line.
func fulfillRead(rdr *readDataResponse, line []byte) {
	buf := rdr.req.DataBuf()
	if buf == nil {
		rdr.req.Reply(0)
		return
	}
	n := copy(buf, line)
	rdr.req.Reply(int64(n))
}
