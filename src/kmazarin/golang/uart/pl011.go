package uart

import (
	"fmt"
	"kmazarin/asm"
	"kmazarin/deviceapi"
	"kmazarin/dtb"
	"reflect"
	"runtime"
	"sync/atomic"
	_ "unsafe" // for go:linkname
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
	uart.WriteReg(RegIMSC, 0)            // Interrupts disabled initially
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
	txLock   uint32                         // Spinlock for TX buffer access
	rxLock   uint32                         // Spinlock for RX buffer access
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

	// Now enable UART interrupts (RX only initially - TX enabled when data is queued)
	u.WriteReg(RegIMSC, IRQ_RX)

	return nil
}

// rxLockAcquire acquires the RX spinlock
//
//go:nosplit
func (u *PL011) rxLockAcquire() {
	for !atomic.CompareAndSwapUint32(&u.rxLock, 0, 1) {
		// Spin until we get the lock
	}
}

// rxLockRelease releases the RX spinlock
//
//go:nosplit
func (u *PL011) rxLockRelease() {
	atomic.StoreUint32(&u.rxLock, 0)
}

// ByteStream implementation
func (u *PL011) Read(p []byte) (n int, err error) {
	u.rxLockAcquire()
	n, err = u.rxBuf.Read(p)
	u.rxLockRelease()
	return n, err
}

func (u *PL011) Write(p []byte) (n int, err error) {
	u.txLockAcquire()
	n = u.txBuf.Write(p)
	// Enable TX interrupt to drain buffer
	u.WriteReg(RegIMSC, IRQ_RX|IRQ_TX)
	u.txLockRelease()
	return n, nil
}

// Console interface implementation
// These methods allow PL011 to be used as a console.Console

// txLockAcquire acquires the TX spinlock
//
//go:nosplit
func (u *PL011) txLockAcquire() {
	for !atomic.CompareAndSwapUint32(&u.txLock, 0, 1) {
		// Spin until we get the lock
	}
}

// txLockRelease releases the TX spinlock
//
//go:nosplit
func (u *PL011) txLockRelease() {
	atomic.StoreUint32(&u.txLock, 0)
}

// WriteByte writes a single byte to the TX ring buffer.
// If buffer is full, falls back to direct MMIO write.
// Implements console.Console interface.
//
//go:nosplit
func (u *PL011) WriteByte(c byte) {
	u.txLockAcquire()

	// Try to write to ring buffer
	success := u.txBuf.WriteByte(c)

	if success {
		// Enable TX interrupt to drain buffer
		u.WriteReg(RegIMSC, IRQ_RX|IRQ_TX)
		u.txLockRelease()
	} else {
		u.txLockRelease()
		// Buffer full - fall back to direct MMIO (busy-wait for FIFO space)
		// Note: Release lock before busy-waiting to avoid deadlock
		// IMPORTANT: Yield to scheduler so event poller can run
		for u.ReadReg(RegFR)&FR_TXFF != 0 {
			runtime.Gosched() // Allow other goroutines (event poller) to run
		}
		u.WriteReg(RegDR, uint32(c))
	}
}

// WriteString writes a string to the TX ring buffer.
// Implements console.Console interface.
//
//go:nosplit
//go:noinline
func (u *PL011) WriteString(s string) {
	for i := 0; i < len(s); i++ {
		u.WriteByte(s[i])
	}
}

// AsConsole returns a console.Console adapter for this PL011.
// The adapter uses the interrupt-driven ring buffer for output.
func (u *PL011) AsConsole() *PL011Console {
	return &PL011Console{uart: u}
}

// PL011Console adapts PL011 to the console.Console interface.
// This wrapper is needed because ByteStream.Write and Console.Write
// have different signatures.
type PL011Console struct {
	uart *PL011
}

// KWrite implements console.Console.KWrite
//
//go:nosplit
func (c *PL011Console) KWrite(p []byte) int {
	for _, b := range p {
		c.uart.WriteByte(b)
	}
	return len(p)
}

// KWriteByte implements console.Console.KWriteByte
//
//go:nosplit
func (c *PL011Console) KWriteByte(b byte) {
	c.uart.WriteByte(b)
}

// KWriteString implements console.Console.KWriteString
//
//go:nosplit
//go:noinline
func (c *PL011Console) KWriteString(s string) {
	c.uart.WriteString(s)
}

// KPrintf implements console.Console.KPrintf
func (c *PL011Console) KPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.uart.WriteString(s)
}

// KErrPrintf implements console.Console.KErrPrintf
func (c *PL011Console) KErrPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.uart.WriteString(s)
}

// KPrintHex implements console.Console.KPrintHex
func (c *PL011Console) KPrintHex(value interface{}) {
	v := reflect.ValueOf(value)
	kind := v.Kind()

	var s string
	switch kind {
	case reflect.Int8, reflect.Uint8:
		s = fmt.Sprintf("0x%02X", value)
	case reflect.Int16, reflect.Uint16:
		s = fmt.Sprintf("0x%04X", value)
	case reflect.Int32, reflect.Uint32:
		s = fmt.Sprintf("0x%08X", value)
	case reflect.Int64, reflect.Uint64, reflect.Int, reflect.Uint, reflect.Uintptr:
		s = fmt.Sprintf("0x%016X", value)
	case reflect.Ptr, reflect.UnsafePointer:
		s = fmt.Sprintf("0x%016X", v.Pointer())
	default:
		s = "not hex value"
	}
	c.uart.WriteString(s)
}

// Breadcrumb implements console.Console.Breadcrumb
// Uses the private breadcrumb function from console package via linkname
//
//go:nosplit
func (c *PL011Console) Breadcrumb(b byte) {
	breadcrumb(b)
}

// breadcrumb is linked from console package's private breadcrumb function
//go:linkname breadcrumb kmazarin/console.breadcrumb
func breadcrumb(b byte)

// Hardware access
//
//go:nosplit
func (u *PL011) ReadReg(offset uintptr) uint32 {
	return asm.MmioRead32(u.baseAddr + offset)
}

//go:nosplit
func (u *PL011) WriteReg(offset uintptr, value uint32) {
	asm.MmioWrite32(u.baseAddr+offset, value)
}

// txTryLock attempts to acquire the TX spinlock without blocking.
// Returns true if lock acquired, false if already held.
//
//go:nosplit
func (u *PL011) txTryLock() bool {
	return atomic.CompareAndSwapUint32(&u.txLock, 0, 1)
}

// handleInterrupt is called from the bottom-half dispatcher when UART IRQ fires.
// It runs in a goroutine context (safe for Go code), not in IRQ context.
// CRITICAL: Must clear the interrupt condition BEFORE returning, since
// GICC_EOIR is written after this handler returns.
//
//go:nosplit
func (u *PL011) handleInterrupt() {
	status := u.ReadReg(RegMIS)

	// Handle receive interrupt - drain RX FIFO into ring buffer
	if status&IRQ_RX != 0 {
		u.rxLockAcquire()
		for u.ReadReg(RegFR)&FR_RXFE == 0 {
			data := u.ReadReg(RegDR)
			u.rxBuf.WriteByte(byte(data))
		}
		u.rxLockRelease()
	}

	// Handle transmit interrupt - drain ring buffer to TX FIFO
	if status&IRQ_TX != 0 {
		// Try to get lock - if held by WriteByte, skip and try next interrupt
		if u.txTryLock() {
			for u.txBuf.Available() > 0 && u.ReadReg(RegFR)&FR_TXFF == 0 {
				data := u.txBuf.ReadByte()
				u.WriteReg(RegDR, uint32(data))
			}
			// CRITICAL: If buffer empty, disable TX interrupt to clear the condition.
			// For level-triggered interrupts, EOIR is written after this handler.
			// If TX interrupt stays enabled and FIFO has space, GIC will re-fire.
			if u.txBuf.Available() == 0 {
				u.WriteReg(RegIMSC, IRQ_RX)
			}
			u.txLockRelease()
		}
		// If lock not acquired, TX interrupt stays enabled and will retry
	}

	// Clear the interrupt flags in the UART's ICR register
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

func (r *RingBuffer) WriteByte(b byte) bool {
	if (r.tail+1)%r.size == r.head {
		return false // Buffer full
	}
	r.buf[r.tail] = b
	r.tail = (r.tail + 1) % r.size
	return true
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
