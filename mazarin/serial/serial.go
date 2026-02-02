// Package serial provides a channel-based API for serial (UART) data.
// It auto-initializes on first use, discovering the serial device,
// registering a soft IRQ slot, and launching a background goroutine
// that delivers bytes on a channel.
//
// Usage:
//
//	ch, _ := serial.Chars()
//	for b := range ch {
//	    fmt.Printf("%c", b)
//	}
package serial

import (
	"errors"
	"fmt"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
	"sync"
)

// Soft IRQ slot — near input's 28/29 and timer's 30, away from dapope's 0-3.
const serialSlot = 27

var (
	once sync.Once
	err  error
	ch   chan byte
)

// Chars returns a receive-only channel of bytes (ASCII characters from UART).
// The first call triggers device discovery, slot registration, and a background
// goroutine. Subsequent calls return the same channel.
func Chars() (<-chan byte, error) {
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
			ch = make(chan byte, 256)
			go serialLoop()
			return
		}
	}
	err = errors.New("serial: no serial device found")
}

func serialLoop() {
	var buf hid.SoftIRQReturn
	for {
		n, waitErr := sys.WaitSoftIRQ(serialSlot, &buf)
		if waitErr != nil || n == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			b := byte(buf.Events[i].Value)
			select {
			case ch <- b:
			default:
				// Drop oldest to make room
				<-ch
				ch <- b
			}
		}
	}
}
