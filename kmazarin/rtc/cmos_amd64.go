package rtc

import (
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/dtb"
)

// CMOS RTC register indices
const (
	cmosSeconds = 0x00
	cmosMinutes = 0x02
	cmosHours   = 0x04
	cmosDay     = 0x07
	cmosMonth   = 0x08
	cmosYear    = 0x09
	cmosStatusB = 0x0B
)

// Assembly-implemented I/O port access
func cmosRead(index uint8) uint8
func cmosWrite(index uint8, val uint8)

// CMOSRTCDriver implements deviceapi.Discoverable for x86 CMOS RTC
type CMOSRTCDriver struct{}

func (d *CMOSRTCDriver) Compatible() []string {
	return []string{"x86,cmos-rtc"}
}

func (d *CMOSRTCDriver) Probe(node *dtb.Node) bool {
	// Always present on x86 QEMU
	return true
}

func (d *CMOSRTCDriver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	return &CMOSRTC{name: node.Name}, nil
}

// CMOSRTC implements the Clock interface using x86 CMOS I/O ports
type CMOSRTC struct {
	name string
}

func (c *CMOSRTC) Name() string { return c.name }
func (c *CMOSRTC) Close() error { return nil }

// Now reads the CMOS RTC registers and returns Unix seconds and nanoseconds.
func (c *CMOSRTC) Now() (seconds, nanoseconds uint64) {
	// Read status register B to check BCD vs binary mode
	statusB := cmosRead(cmosStatusB)
	bcdMode := statusB&0x04 == 0 // bit 2 clear = BCD mode

	sec := uint64(cmosRead(cmosSeconds))
	min := uint64(cmosRead(cmosMinutes))
	hrs := uint64(cmosRead(cmosHours))
	day := uint64(cmosRead(cmosDay))
	mon := uint64(cmosRead(cmosMonth))
	yr := uint64(cmosRead(cmosYear))

	if bcdMode {
		sec = bcdToBin(sec)
		min = bcdToBin(min)
		hrs = bcdToBin(hrs)
		day = bcdToBin(day)
		mon = bcdToBin(mon)
		yr = bcdToBin(yr)
	}

	// QEMU CMOS year is 2-digit; assume 2000+
	yr += 2000

	// Convert to Unix timestamp (days since 1970-01-01)
	days := daysSinceEpoch(yr, mon, day)
	seconds = days*86400 + hrs*3600 + min*60 + sec
	nanoseconds = 0
	return
}

func (c *CMOSRTC) SetTime(seconds, nanoseconds uint64) error {
	return nil
}

func bcdToBin(v uint64) uint64 {
	return (v>>4)*10 + (v & 0x0F)
}

// daysSinceEpoch computes days from 1970-01-01 to the given date.
func daysSinceEpoch(year, month, day uint64) uint64 {
	// Days in each month (non-leap)
	mdays := [12]uint64{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

	var days uint64
	// Full years
	for y := uint64(1970); y < year; y++ {
		if isLeapYear(y) {
			days += 366
		} else {
			days += 365
		}
	}
	// Months in current year
	for m := uint64(1); m < month; m++ {
		days += mdays[m-1]
		if m == 2 && isLeapYear(year) {
			days++
		}
	}
	// Days in current month (1-indexed)
	days += day - 1
	return days
}

func isLeapYear(y uint64) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}
