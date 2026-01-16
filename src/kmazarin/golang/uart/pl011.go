package uart

import (
	"kmazarin/deviceapi"
	"kmazarin/dtb"
	"unsafe"
)

// PL011 register offsets
const (
	RegDR   = 0x000 // Data register
	RegFR   = 0x018 // Flag register
	RegIBRD = 0x024 // Integer baud rate divisor
	RegFBRD = 0x028 // Fractional baud rate divisor
	RegLCRH = 0x02C // Line control register
	RegCR   = 0x030 // Control register
	RegIMSC = 0x038 // Interrupt mask set/clear
	RegMIS  = 0x040 // Masked interrupt status
	RegICR  = 0x044 // Interrupt clear

	// Flag register bits
	FR_RXFE = 1 << 4 // Receive FIFO empty
	FR_TXFF = 1 << 5 // Transmit FIFO full

	// Interrupt bits
	IRQ_RX = 1 << 4 // Receive interrupt
	IRQ_TX = 1 << 5 // Transmit interrupt
)

// PL011Driver implements deviceapi.Discoverable for PL011 UART
type PL011Driver struct{}

func (d *PL011Driver) Compatible() []string {
	// Only match on specific PL011 compatible string, not the generic primecell
	return []string{"arm,pl011"}
}

func (d *PL011Driver) Probe(node *dtb.Node) bool {
	// Verify required properties exist
	return node.Reg != nil && len(node.Reg) > 0 &&
		node.Interrupts != nil && len(node.Interrupts) > 0
}

func (d *PL011Driver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	var irq uint32
	if len(node.Interrupts) > 0 {
		irq = node.Interrupts[0]
	}

	// Convert physical address to high-memory kernel address
	// Physical: 0x09000000 → Kernel: 0xFFFFFFFF09000000
	const KernelMMIOOffset = 0xFFFFFFFF00000000
	baseAddr := node.Reg[0].Address + KernelMMIOOffset

	uart := &PL011{
		name:     node.Name,
		baseAddr: baseAddr,
		irq:      irq,
		rxBuf:    NewRingBuffer(4096),
		txBuf:    NewRingBuffer(4096),
	}

	// TODO: Register interrupt handler when interrupt controller is available

	// Initialize hardware
	uart.WriteReg(RegCR, 0)              // Disable UART
	uart.WriteReg(RegIBRD, 26)           // 115200 baud at 24MHz
	uart.WriteReg(RegFBRD, 3)            // Fractional part
	uart.WriteReg(RegLCRH, 0x70)         // 8N1, enable FIFOs
	uart.WriteReg(RegIMSC, IRQ_RX|IRQ_TX) // Enable RX/TX interrupts
	uart.WriteReg(RegCR, 0x301)          // Enable UART

	return uart, nil
}

// PL011 implements ByteStream and InterruptUser interfaces
type PL011 struct {
	name     string
	baseAddr uintptr
	irq      uint32
	rxBuf    *RingBuffer
	txBuf    *RingBuffer
	ic       deviceapi.InterruptController // Set by WireInterrupts
}

// Closable implementation
func (u *PL011) Name() string {
	return u.name
}

func (u *PL011) Close() error {
	// Disable IRQ at the interrupt controller if wired
	if u.ic != nil {
		u.ic.DisableIRQ(u.irq)
	}
	// Disable interrupts and UART
	u.WriteReg(RegIMSC, 0)
	u.WriteReg(RegCR, 0)
	return nil
}

// WireInterrupts implements InterruptUser interface.
// Registers the UART's interrupt handler with the interrupt controller.
func (u *PL011) WireInterrupts(ic deviceapi.InterruptController) error {
	u.ic = ic

	// Clear any pending UART interrupts
	u.WriteReg(RegICR, 0x7FF)

	// Register our interrupt handler with the GIC.
	// The GIC's RegisterHandler also registers with kirq's dispatch table,
	// so the exception handler can dispatch this IRQ.
	ic.RegisterHandler(u.irq, u.handleInterrupt)
	ic.EnableIRQ(u.irq)

	return nil
}

// ByteStream implementation
func (u *PL011) Read(p []byte) (n int, err error) {
	return u.rxBuf.Read(p)
}

func (u *PL011) Write(p []byte) (n int, err error) {
	n = u.txBuf.Write(p)
	// Enable TX interrupt to drain buffer
	u.WriteReg(RegIMSC, IRQ_RX|IRQ_TX)
	return n, nil
}

// Hardware access
func (u *PL011) ReadReg(offset uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(u.baseAddr + offset))
}

func (u *PL011) WriteReg(offset uintptr, value uint32) {
	*(*uint32)(unsafe.Pointer(u.baseAddr + offset)) = value
}

// Interrupt handler
//go:nosplit
func (u *PL011) handleInterrupt() {
	status := u.ReadReg(RegMIS)

	// Handle receive interrupt
	if status&IRQ_RX != 0 {
		for u.ReadReg(RegFR)&FR_RXFE == 0 {
			data := u.ReadReg(RegDR)
			u.rxBuf.WriteByte(byte(data))
		}
	}

	// Handle transmit interrupt
	if status&IRQ_TX != 0 {
		for u.txBuf.Available() > 0 && u.ReadReg(RegFR)&FR_TXFF == 0 {
			data := u.txBuf.ReadByte()
			u.WriteReg(RegDR, uint32(data))
		}
		// If buffer empty, disable TX interrupt
		if u.txBuf.Available() == 0 {
			u.WriteReg(RegIMSC, IRQ_RX)
		}
	}

	// Clear interrupts
	u.WriteReg(RegICR, status)
}

// RingBuffer for buffered I/O
type RingBuffer struct {
	buf  []byte
	head int
	tail int
	size int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

func (r *RingBuffer) Write(p []byte) int {
	n := 0
	for _, b := range p {
		if (r.tail+1)%r.size == r.head {
			break // Buffer full
		}
		r.buf[r.tail] = b
		r.tail = (r.tail + 1) % r.size
		n++
	}
	return n
}

func (r *RingBuffer) WriteByte(b byte) {
	if (r.tail+1)%r.size != r.head {
		r.buf[r.tail] = b
		r.tail = (r.tail + 1) % r.size
	}
}

func (r *RingBuffer) Read(p []byte) (int, error) {
	n := 0
	for i := range p {
		if r.head == r.tail {
			break // Buffer empty
		}
		p[i] = r.buf[r.head]
		r.head = (r.head + 1) % r.size
		n++
	}
	return n, nil
}

func (r *RingBuffer) ReadByte() byte {
	if r.head == r.tail {
		return 0
	}
	b := r.buf[r.head]
	r.head = (r.head + 1) % r.size
	return b
}

func (r *RingBuffer) Available() int {
	if r.tail >= r.head {
		return r.tail - r.head
	}
	return r.size - r.head + r.tail
}
