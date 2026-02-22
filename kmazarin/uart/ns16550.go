package uart

import (
	"fmt"
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/dtb"
	"mazzy/kmazarin/kmem"
	"sync/atomic"
)

// NS16550 register offsets (byte-addressable, reg-shift=0)
const (
	NS_RBR uintptr = 0x00 // Receiver Buffer Register (read)
	NS_THR uintptr = 0x00 // Transmitter Holding Register (write)
	NS_IER uintptr = 0x01 // Interrupt Enable Register
	NS_IIR uintptr = 0x02 // Interrupt Identification Register (read)
	NS_FCR uintptr = 0x02 // FIFO Control Register (write)
	NS_LCR uintptr = 0x03 // Line Control Register
	NS_MCR uintptr = 0x04 // Modem Control Register
	NS_LSR uintptr = 0x05 // Line Status Register

	// LSR bits
	NS_LSR_DR   byte = 1 << 0 // Data Ready
	NS_LSR_THRE byte = 1 << 5 // Transmitter Holding Register Empty

	// IER bits
	NS_IER_RDI  byte = 1 << 0 // Received Data Available interrupt
	NS_IER_THRI byte = 1 << 1 // Transmitter Holding Register Empty interrupt

	// IIR bits
	NS_IIR_NO_INT byte = 1 << 0 // No interrupt pending

	// FCR bits
	NS_FCR_FIFO_EN    byte = 1 << 0 // FIFO enable
	NS_FCR_RXFIFO_RST byte = 1 << 1 // RX FIFO reset
	NS_FCR_TXFIFO_RST byte = 1 << 2 // TX FIFO reset
)

// NS16550Driver implements deviceapi.Discoverable for NS16550 UART
type NS16550Driver struct{}

func (d *NS16550Driver) Compatible() []string {
	return []string{"ns16550a", "ns16550"}
}

func (d *NS16550Driver) Probe(node *dtb.Node) bool {
	return node.Reg != nil && len(node.Reg) > 0
}

func (d *NS16550Driver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	var irq uint32
	if len(node.Interrupts) > 0 {
		irq = node.Interrupts[0]
	}

	reg := node.Reg[0]

	// Map MMIO region
	if err := kmem.MapDeviceMMIO(reg.Address, reg.Size); err != nil {
		fmt.Printf("[NS16550] MapDeviceMMIO failed: %s\r\n", err.Error())
		return nil, err
	}

	// Convert physical address to high-memory kernel address
	const KernelMMIOOffset = 0xFFFFFFFF00000000
	baseAddr := reg.Address + KernelMMIOOffset

	u := &NS16550{
		name:     node.Name,
		baseAddr: baseAddr,
		irq:      irq,
		rxBuf:    NewRingBuffer(4096),
		txBuf:    NewRingBuffer(4096),
	}

	// Initialize hardware
	// Enable and reset FIFOs
	u.WriteReg(NS_FCR, NS_FCR_FIFO_EN|NS_FCR_RXFIFO_RST|NS_FCR_TXFIFO_RST)
	// 8N1
	u.WriteReg(NS_LCR, 0x03)
	// No interrupts initially
	u.WriteReg(NS_IER, 0)
	// DTR + RTS
	u.WriteReg(NS_MCR, 0x03)

	return u, nil
}

// NS16550 implements ByteStream and InterruptUser interfaces
type NS16550 struct {
	name     string
	baseAddr uintptr
	irq      uint32
	rxBuf    *RingBuffer
	txBuf    *RingBuffer
	ic       deviceapi.InterruptController
	txLock   uint32
	rxLock   uint32
}

// IRQ returns the IRQ number for this UART.
func (u *NS16550) IRQ() uint32 {
	return u.irq
}

func (u *NS16550) Name() string {
	return u.name
}

func (u *NS16550) Close() error {
	if u.ic != nil {
		u.ic.DisableIRQ(u.irq)
	}
	u.WriteReg(NS_IER, 0)
	return nil
}

// WireInterrupts implements InterruptUser interface.
func (u *NS16550) WireInterrupts(ic deviceapi.InterruptController) error {
	u.ic = ic

	// Drain any pending data
	for u.ReadReg(NS_LSR)&NS_LSR_DR != 0 {
		_ = u.ReadReg(NS_RBR)
	}

	ic.RegisterHandler(u.irq, u.handleInterrupt)
	ic.EnableIRQ(u.irq)

	// Enable RX interrupt
	u.WriteReg(NS_IER, NS_IER_RDI)

	return nil
}

//go:nosplit
func (u *NS16550) rxLockAcquire() {
	for !atomic.CompareAndSwapUint32(&u.rxLock, 0, 1) {
	}
}

//go:nosplit
func (u *NS16550) rxLockRelease() {
	atomic.StoreUint32(&u.rxLock, 0)
}

//go:nosplit
func (u *NS16550) txLockAcquire() {
	for !atomic.CompareAndSwapUint32(&u.txLock, 0, 1) {
	}
}

//go:nosplit
func (u *NS16550) txLockRelease() {
	atomic.StoreUint32(&u.txLock, 0)
}

//go:nosplit
func (u *NS16550) txTryLock() bool {
	return atomic.CompareAndSwapUint32(&u.txLock, 0, 1)
}

func (u *NS16550) Read(p []byte) (n int, err error) {
	u.rxLockAcquire()
	n, err = u.rxBuf.Read(p)
	u.rxLockRelease()
	return n, err
}

func (u *NS16550) Write(p []byte) (n int, err error) {
	u.txLockAcquire()
	n = u.txBuf.Write(p)
	// Enable TX interrupt to drain buffer
	u.WriteReg(NS_IER, NS_IER_RDI|NS_IER_THRI)
	u.txLockRelease()
	return n, nil
}

// WriteByte writes a single byte to the TX ring buffer.
// Falls back to direct MMIO if buffer is full.
//
//go:nosplit
func (u *NS16550) WriteByte(c byte) {
	u.txLockAcquire()

	success := u.txBuf.WriteByte(c)

	if success {
		u.WriteReg(NS_IER, NS_IER_RDI|NS_IER_THRI)
		u.txLockRelease()
	} else {
		u.txLockRelease()
		// Buffer full - busy-wait for THR empty
		for u.ReadReg(NS_LSR)&NS_LSR_THRE == 0 {
		}
		u.WriteReg(NS_THR, c)
	}
}

// WriteString writes a string to the UART.
//
//go:nosplit
//go:noinline
func (u *NS16550) WriteString(s string) {
	for i := 0; i < len(s); i++ {
		u.WriteByte(s[i])
	}
}

// AsConsole returns a console.Console adapter for this NS16550.
func (u *NS16550) AsConsole() *NS16550Console {
	return &NS16550Console{uart: u}
}

// NS16550Console adapts NS16550 to the console.Console interface.
type NS16550Console struct {
	uart *NS16550
}

//go:nosplit
func (c *NS16550Console) KWrite(p []byte) int {
	for _, b := range p {
		c.uart.WriteByte(b)
	}
	return len(p)
}

//go:nosplit
func (c *NS16550Console) KWriteByte(b byte) {
	c.uart.WriteByte(b)
}

//go:nosplit
//go:noinline
func (c *NS16550Console) KWriteString(s string) {
	c.uart.WriteString(s)
}

func (c *NS16550Console) KPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.uart.WriteString(s)
}

func (c *NS16550Console) KErrPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.uart.WriteString(s)
}

func (c *NS16550Console) KPrintHex(value interface{}) {
	s := fmt.Sprintf("0x%X", value)
	c.uart.WriteString(s)
}

// Hardware access — use byte I/O since NS16550 registers are at byte offsets.
// RISC-V requires naturally aligned accesses; byte offsets 1,2,3,5 are not
// 32-bit aligned so MmioRead32/MmioWrite32 would cause misaligned faults.
//
//go:nosplit
func (u *NS16550) ReadReg(offset uintptr) byte {
	return asm.MmioRead8(u.baseAddr + offset)
}

//go:nosplit
func (u *NS16550) WriteReg(offset uintptr, value byte) {
	asm.MmioWrite8(u.baseAddr+offset, value)
}

// handleInterrupt is called from the interrupt dispatcher.
//
//go:nosplit
func (u *NS16550) handleInterrupt() {
	iir := u.ReadReg(NS_IIR)
	if iir&NS_IIR_NO_INT != 0 {
		return // No interrupt pending
	}

	// Handle receive - drain RX FIFO
	for u.ReadReg(NS_LSR)&NS_LSR_DR != 0 {
		u.rxLockAcquire()
		data := u.ReadReg(NS_RBR)
		u.rxBuf.WriteByte(data)
		u.rxLockRelease()
	}

	// Handle transmit - drain TX ring buffer
	if u.txTryLock() {
		for u.txBuf.Available() > 0 && u.ReadReg(NS_LSR)&NS_LSR_THRE != 0 {
			data := u.txBuf.ReadByte()
			u.WriteReg(NS_THR, data)
		}
		if u.txBuf.Available() == 0 {
			u.WriteReg(NS_IER, NS_IER_RDI) // Disable TX interrupt
		}
		u.txLockRelease()
	}
}
