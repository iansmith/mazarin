package rtc

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/dtb"
	"mazzy/kmazarin/kmem"
)

// Goldfish RTC register offsets
const (
	GF_TIME_LOW  uintptr = 0x00 // Low 32 bits of nanoseconds since epoch (read latches HIGH)
	GF_TIME_HIGH uintptr = 0x04 // High 32 bits of nanoseconds since epoch
)

// GoldfishDriver implements deviceapi.Discoverable for Google Goldfish RTC
type GoldfishDriver struct{}

func (d *GoldfishDriver) Compatible() []string {
	return []string{"google,goldfish-rtc"}
}

func (d *GoldfishDriver) Probe(node *dtb.Node) bool {
	return node.Reg != nil && len(node.Reg) >= 1
}

func (d *GoldfishDriver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	reg := node.Reg[0]

	if err := kmem.MapDeviceMMIO(reg.Address, reg.Size); err != nil {
		return nil, err
	}

	const KernelMMIOOffset = 0xFFFFFFFF00000000
	baseAddr := reg.Address + KernelMMIOOffset

	g := &Goldfish{
		name:     node.Name,
		baseAddr: baseAddr,
	}

	return g, nil
}

// Goldfish implements the Clock interface for google,goldfish-rtc
type Goldfish struct {
	name     string
	baseAddr uintptr
}

func (g *Goldfish) Name() string  { return g.name }
func (g *Goldfish) Close() error  { return nil }

// Now returns seconds and nanoseconds since Unix epoch.
// Reading TIME_LOW latches TIME_HIGH for atomic 64-bit reads.
func (g *Goldfish) Now() (seconds, nanoseconds uint64) {
	low := uint64(asm.MmioRead32(g.baseAddr + GF_TIME_LOW))
	high := uint64(asm.MmioRead32(g.baseAddr + GF_TIME_HIGH))
	totalNs := (high << 32) | low
	seconds = totalNs / 1_000_000_000
	nanoseconds = totalNs % 1_000_000_000
	return
}

func (g *Goldfish) SetTime(seconds, nanoseconds uint64) error {
	// Goldfish RTC is read-only in QEMU
	return nil
}
