package rtc

import (
	"kmazarin/asm"
	"kmazarin/deviceapi"
	"kmazarin/dtb"
)

// PL031 register offsets
const (
	RTC_DR   = 0x000 // Data Register - current time value
	RTC_MR   = 0x004 // Match Register - alarm value
	RTC_LR   = 0x008 // Load Register - set time value
	RTC_CR   = 0x00C // Control Register
	RTC_IMSC = 0x010 // Interrupt Mask Set/Clear
	RTC_RIS  = 0x014 // Raw Interrupt Status
	RTC_MIS  = 0x018 // Masked Interrupt Status
	RTC_ICR  = 0x01C // Interrupt Clear
)

// PL031Driver implements deviceapi.Discoverable
type PL031Driver struct{}

func (d *PL031Driver) Compatible() []string {
	return []string{"arm,pl031"}
}

func (d *PL031Driver) Probe(node *dtb.Node) bool {
	// PL031 needs one register region
	return node.Reg != nil && len(node.Reg) >= 1
}

func (d *PL031Driver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	// Convert physical address to high-memory kernel address
	// Physical: 0x09010000 → Kernel: 0xFFFFFFFF09010000
	const KernelMMIOOffset = 0xFFFFFFFF00000000
	baseAddr := node.Reg[0].Address + KernelMMIOOffset

	rtc := &PL031{
		name:     node.Name,
		baseAddr: baseAddr,
	}

	return rtc, nil
}

// PL031 implements Clock interface
type PL031 struct {
	name     string
	baseAddr uintptr
}

// Closable implementation
func (r *PL031) Name() string {
	return r.name
}

func (r *PL031) Close() error {
	// Nothing to clean up
	return nil
}

// Clock implementation
func (r *PL031) Now() (seconds, nanoseconds uint64) {
	// Read current time from RTC_DR register
	// PL031 returns seconds since epoch (1970-01-01 00:00:00 UTC)
	seconds = uint64(r.ReadReg(RTC_DR))
	nanoseconds = 0 // PL031 only provides second resolution
	return
}

func (r *PL031) SetTime(seconds, nanoseconds uint64) error {
	// Write to RTC_LR register to set time
	// PL031 only supports second resolution, ignore nanoseconds
	r.WriteReg(RTC_LR, uint32(seconds))
	return nil
}

// Hardware access
//
//go:nosplit
func (r *PL031) ReadReg(offset uintptr) uint32 {
	return asm.MmioRead32(r.baseAddr + offset)
}

//go:nosplit
func (r *PL031) WriteReg(offset uintptr, value uint32) {
	asm.MmioWrite32(r.baseAddr+offset, value)
}
