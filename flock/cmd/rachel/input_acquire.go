package main

import (
	"fmt"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
	"mazzy/shared/iouring"
	"sync/atomic"
	"unsafe"
)

// InputAcquirer reads HID events from an io_uring input ring.
// The kernel IRQ top-half writes CQEs encoding events as:
//
//	UserData: type<<48 | code<<32 | value
//
// The reader goroutine blocks in IOUringEnter(ringID, 0, 1, 0) until
// at least one CQE arrives, drains all available CQEs, and sends them
// to the eventCh channel as hid.HIDEvent values.
type InputAcquirer struct {
	ringPage *iouring.IORing
	ringID   int
	eventCh  chan hid.HIDEvent
}

// NewInputAcquirer allocates a shared page, sets up an io_uring with
// device type = input (1), and returns the acquirer. Start the reader
// goroutine with go acq.Run().
func NewInputAcquirer(bufSize int) (*InputAcquirer, error) {
	page, err := mem.AllocPages(1, mem.PageShared)
	if err != nil {
		return nil, fmt.Errorf("AllocPages for input ring: %w", err)
	}
	ringPage := (*iouring.IORing)(page)

	// flags=1 → device type input
	ringID, err := sys.IOUringSetup(ringPage, 1)
	if err != nil {
		return nil, fmt.Errorf("IOUringSetup(input): %w", err)
	}

	return &InputAcquirer{
		ringPage: ringPage,
		ringID:   ringID,
		eventCh:  make(chan hid.HIDEvent, bufSize),
	}, nil
}

// Events returns the channel on which decoded HID events are delivered.
func (a *InputAcquirer) Events() <-chan hid.HIDEvent {
	return a.eventCh
}

// Run is the reader loop. It blocks on IOUringEnter waiting for CQEs,
// decodes each CQE into a hid.HIDEvent, and sends it on eventCh.
// Run never returns under normal operation.
func (a *InputAcquirer) Run() {
	sys.UartWriteString(fmt.Sprintf("[input:acq] Run() started, ringID=%d\n", a.ringID))
	ring := a.ringPage
	for {
		// Block until at least 1 CQE is available. Uses entersyscallblock
		// to release the P so other goroutines (wmEventLoop, uring dispatcher,
		// etc.) can run while we wait for the next HID event.
		_, err := sys.IOUringEnterBlocking(a.ringID, 0, 1, 0)
		if err != nil {
			// Timeout or transient error — retry.
			continue
		}

		// Drain all available CQEs.
		for {
			head := atomic.LoadUint32(&ring.CQHead)
			tail := atomic.LoadUint32(&ring.CQTail)
			if head == tail {
				break
			}
			cqe := (*iouring.CQEntry)(unsafe.Pointer(&ring.CQEntries[head&iouring.CQMask]))
			ud := cqe.UserData

			ev := hid.HIDEvent{
				Type:  uint16(ud >> 48),
				Code:  uint16(ud >> 32),
				Value: uint32(ud),
			}

			a.eventCh <- ev

			atomic.StoreUint32(&ring.CQHead, head+1)
		}
	}
}
