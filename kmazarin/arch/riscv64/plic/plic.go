// Package plic implements the RISC-V Platform-Level Interrupt Controller
// for the QEMU virt machine.
//
// The PLIC aggregates external interrupt sources and routes them to
// hart contexts. Each hart has two contexts: M-mode and S-mode.
// Since kmazarin runs in S-mode, we use context 2*hartID + 1.
//
// QEMU virt PLIC base address: 0x0C000000
//
// Register layout:
//
//	0x000000 + 4*source     Priority (32-bit, 0=disabled, 1-7)
//	0x001000 + 4*(src/32)   Pending bits (read-only)
//	0x002000 + 0x80*ctx     Enable bits per context
//	0x200000 + 0x1000*ctx   Priority threshold per context
//	0x200004 + 0x1000*ctx   Claim/complete per context
package plic

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/dtb"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
)

// PLIC register region offsets
const (
	PLIC_PRIORITY_BASE  = 0x000000 // Priority: base + 4*source
	PLIC_PENDING_BASE   = 0x001000 // Pending: base + 4*(source/32)
	PLIC_ENABLE_BASE    = 0x002000 // Enable: base + 0x80*context + 4*(source/32)
	PLIC_THRESHOLD_BASE = 0x200000 // Threshold: base + 0x1000*context
	PLIC_CLAIM_BASE     = 0x200004 // Claim/Complete: base + 0x1000*context
)

// QEMU virt PLIC defaults
const (
	PLICDefaultPhysBase = 0x0C000000
	PLICDefaultSize     = 0x4000000 // 64MB (covers all contexts)

	// Maximum interrupt sources for QEMU virt PLIC
	PLICMaxSources = 1024

	// Kernel VA offset
	KernelMMIOOffset = 0xFFFFFFFF00000000
)

// PLICDriver implements device.Discoverable for the RISC-V PLIC.
type PLICDriver struct{}

func (d *PLICDriver) Compatible() []string {
	return []string{"sifive,plic-1.0.0", "riscv,plic0"}
}

func (d *PLICDriver) Probe(node *dtb.Node) bool {
	// PLIC needs at least one register region
	return node.Reg != nil && len(node.Reg) >= 1
}

func (d *PLICDriver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	reg := node.Reg[0]

	// Map PLIC MMIO region
	// The PLIC is large — map the priority, pending, enable, and threshold/claim regions
	mapSize := reg.Size
	if mapSize < PLICDefaultSize {
		mapSize = PLICDefaultSize
	}
	if err := kmem.MapDeviceMMIO(reg.Address, mapSize); err != nil {
		return nil, err
	}

	dev := &PLIC{
		base: reg.Address + KernelMMIOOffset,
		// S-mode context for hart 0 = context 1 (M=0, S=1, M=2, S=3, ...)
		context: 1,
	}

	dev.initHardware()
	return dev, nil
}

// PLIC implements InterruptController for RISC-V.
type PLIC struct {
	base     uintptr
	context  uint32       // S-mode context ID (2*hartID + 1)
	handlers [1024]func() // Handler per interrupt source
}

func (p *PLIC) Name() string { return "riscv-plic" }

func (p *PLIC) Close() error {
	// Disable all interrupt sources by setting priority to 0
	for i := uint32(1); i < PLICMaxSources; i++ {
		p.setPriority(i, 0)
	}
	// Set threshold to maximum to block all interrupts
	p.setThreshold(7)
	return nil
}

// RegisterHandler registers an interrupt handler for the given IRQ source.
func (p *PLIC) RegisterHandler(irq uint32, handler func()) {
	if irq > 0 && irq < PLICMaxSources {
		p.handlers[irq] = handler
		kirq.RegisterSimpleHandler(irq, handler)
	}
}

// EnableIRQ enables interrupt source delivery to this context.
//
//go:nosplit
func (p *PLIC) EnableIRQ(irq uint32) {
	if irq == 0 || irq >= PLICMaxSources {
		return
	}
	reg := irq / 32
	bit := irq % 32
	offset := PLIC_ENABLE_BASE + uintptr(p.context)*0x80 + uintptr(reg)*4
	val := asm.MmioRead32(p.base + offset)
	val |= (1 << bit)
	asm.MmioWrite32(p.base+offset, val)
}

// DisableIRQ disables interrupt source delivery to this context.
//
//go:nosplit
func (p *PLIC) DisableIRQ(irq uint32) {
	if irq == 0 || irq >= PLICMaxSources {
		return
	}
	reg := irq / 32
	bit := irq % 32
	offset := PLIC_ENABLE_BASE + uintptr(p.context)*0x80 + uintptr(reg)*4
	val := asm.MmioRead32(p.base + offset)
	val &^= (1 << bit)
	asm.MmioWrite32(p.base+offset, val)
}

// SetIRQPriority sets the priority for an interrupt source.
// PLIC priority: 0 = disabled, 1 = lowest, 7 = highest.
// The priority parameter is mapped from the generic 0-255 range:
// 0 maps to PLIC priority 7 (highest), 255 maps to 1 (lowest).
//
//go:nosplit
func (p *PLIC) SetIRQPriority(irq uint32, priority uint8) {
	if irq == 0 || irq >= PLICMaxSources {
		return
	}
	// Map generic priority (0=highest, 255=lowest) to PLIC (7=highest, 1=lowest)
	plicPri := uint32(7 - (priority / 37)) // 0->7, 36->6, ..., 255->0
	if plicPri == 0 {
		plicPri = 1 // Minimum enabled priority
	}
	p.setPriority(irq, plicPri)
}

// SetIRQTarget is a no-op on PLIC — interrupt routing is per-context,
// controlled by the enable registers rather than per-IRQ target masks.
//
//go:nosplit
func (p *PLIC) SetIRQTarget(_ uint32, _ uint8) {
	// PLIC uses per-context enable bits instead of per-IRQ target routing.
	// To route an IRQ to a different hart, enable it in that hart's context.
}

// SetIRQEdgeTriggered is a no-op on PLIC.
// PLIC interrupt type (edge vs level) is determined by the source device,
// not configurable in the PLIC itself.
//
//go:nosplit
func (p *PLIC) SetIRQEdgeTriggered(_ uint32) {}

// SetIRQLevelTriggered is a no-op on PLIC.
//
//go:nosplit
func (p *PLIC) SetIRQLevelTriggered(_ uint32) {}

// Claim reads the highest-priority pending interrupt for this context.
// Returns 0 if no interrupt is pending.
//
//go:nosplit
func (p *PLIC) Claim() uint32 {
	offset := PLIC_CLAIM_BASE + uintptr(p.context)*0x1000
	return asm.MmioRead32(p.base + offset)
}

// Complete signals that interrupt handling is finished.
// The source ID must match what was returned by Claim.
//
//go:nosplit
func (p *PLIC) Complete(irq uint32) {
	offset := PLIC_CLAIM_BASE + uintptr(p.context)*0x1000
	asm.MmioWrite32(p.base+offset, irq)
}

// DispatchIRQ is called from the RISC-V trap handler when an external
// interrupt is received. It claims the interrupt, calls the handler,
// and completes it.
//
//go:nosplit
func (p *PLIC) DispatchIRQ() {
	irq := p.Claim()
	if irq == 0 {
		return // Spurious
	}

	if irq < PLICMaxSources && p.handlers[irq] != nil {
		p.handlers[irq]()
	}

	p.Complete(irq)
}

// CallHandler calls the registered handler for the given IRQ source.
// Returns true if a handler was found and called.
//
//go:nosplit
func (p *PLIC) CallHandler(irq uint32) bool {
	if irq > 0 && irq < PLICMaxSources && p.handlers[irq] != nil {
		p.handlers[irq]()
		return true
	}
	return false
}

// initHardware initializes the PLIC for S-mode operation.
func (p *PLIC) initHardware() {
	// Disable all interrupt sources (priority = 0)
	for i := uint32(1); i < PLICMaxSources; i++ {
		p.setPriority(i, 0)
	}

	// Disable all sources in our context's enable registers
	enableRegs := uint32((PLICMaxSources + 31) / 32)
	for i := uint32(0); i < enableRegs; i++ {
		offset := PLIC_ENABLE_BASE + uintptr(p.context)*0x80 + uintptr(i)*4
		asm.MmioWrite32(p.base+offset, 0)
	}

	// Set priority threshold to 0 (accept all priorities >= 1)
	p.setThreshold(0)

	// Drain any pending claims
	for {
		irq := p.Claim()
		if irq == 0 {
			break
		}
		p.Complete(irq)
	}
}

// setPriority sets the raw PLIC priority for a source.
//
//go:nosplit
func (p *PLIC) setPriority(source, priority uint32) {
	offset := PLIC_PRIORITY_BASE + uintptr(source)*4
	asm.MmioWrite32(p.base+offset, priority)
}

// setThreshold sets the priority threshold for this context.
// Interrupts with priority <= threshold are masked.
//
//go:nosplit
func (p *PLIC) setThreshold(threshold uint32) {
	offset := PLIC_THRESHOLD_BASE + uintptr(p.context)*0x1000
	asm.MmioWrite32(p.base+offset, threshold)
}
