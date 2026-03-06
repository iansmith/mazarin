// Package serial provides a channel-based API for serial (UART) data.
// It auto-initializes on first use, discovering the serial device,
// registering a soft IRQ slot, and launching a background goroutine
// that delivers bytes on a channel.
//
// Usage:
//
//	ch, _ := serial.Chars()
//	for sb := range ch {
//	    fmt.Printf("%c", sb.B)
//	}
package serial

import (
	"errors"
	"fmt"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
	"runtime"
	"sync"
)

// SerialByte carries a byte from the serial ring along with the file
// descriptor it was written to. Fd=0 for UART RX input, Fd=1 for
// stdout, Fd=2 for stderr.
type SerialByte struct {
	Fd byte
	B  byte
}

// Soft IRQ slot — near input's 28/29 and timer's 30, away from dapope's 0-3.
const serialSlot = 27

var (
	once sync.Once
	err  error
	ch   chan SerialByte
)

// Chars returns a receive-only channel of SerialByte (bytes + fd from UART ring).
// The first call triggers device discovery, slot registration, and a background
// goroutine. Subsequent calls return the same channel.
func Chars() (<-chan SerialByte, error) {
	once.Do(doInit)
	return ch, err
}

func doInit() {
	devs, devErr := sys.QueryInputDevices()
	if devErr != nil {
		err = devErr
		return
	}

	for _, dev := range devs {
		if dev.DeviceType == hid.DeviceTypeSerial {
			if regErr := sys.RegisterSoftIRQ(dev.IRQNum, serialSlot); regErr != nil {
				err = fmt.Errorf("serial: register slot: %w", regErr)
				return
			}
			ch = make(chan SerialByte, 256)
			go serialLoop()
			return
		}
	}
	err = errors.New("serial: no serial device found")
}

func serialLoop() {
	runtime.LockOSThread()
	var buf hid.SoftIRQReturn
	for {
		n, waitErr := sys.WaitSoftIRQBlocking(serialSlot, &buf)
		if waitErr != nil || n == 0 {
			continue
		}
		sys.RawWrite(2, 'u') // diag: UART ring drained by stdio
		for i := 0; i < n; i++ {
			sb := SerialByte{
				Fd: byte(buf.Events[i].Code),
				B:  byte(buf.Events[i].Value),
			}
			select {
			case ch <- sb:
			default:
				// Drop oldest to make room
				<-ch
				ch <- sb
			}
		}
	}
}
