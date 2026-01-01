// Package arm64 contains ARM64 architecture-specific assembly routines for Cardinal.
// This includes boot code, exception vectors, barriers, MMU, and system register access.
package arm64

import _ "unsafe" // for go:linkname

// Memory barriers (global symbols)

//go:linkname Dsb dsb
//go:nosplit
func Dsb()

//go:linkname Isb isb
//go:nosplit
func Isb()

//go:linkname Wfi Wfi
//go:nosplit
func Wfi()

//go:linkname Nop Nop
//go:nosplit
func Nop()

// Cache operations (global symbols)

//go:linkname DcCvau DcCvau
//go:nosplit
func DcCvau(addr uintptr)

//go:linkname CleanDcacheVa clean_dcache_va
//go:nosplit
func CleanDcacheVa(addr uintptr)

//go:linkname CleanDataCacheVA CleanDataCacheVA
//go:nosplit
func CleanDataCacheVA(addr uintptr)

//go:linkname IcIvau IcIvau
//go:nosplit
func IcIvau(addr uintptr)

//go:linkname InvalidateInstructionCacheAll InvalidateInstructionCacheAll
//go:nosplit
func InvalidateInstructionCacheAll()

// TLB operations (global symbols)

//go:linkname InvalidateTlbAll invalidate_tlb_all
//go:nosplit
func InvalidateTlbAll()

//go:linkname InvalidateTlbVa invalidate_tlb_va
//go:nosplit
func InvalidateTlbVa(addr uintptr)

// MMU operations (global symbols)

//go:linkname EnableMmuMinimal enable_mmu_minimal
//go:nosplit
func EnableMmuMinimal(value uint64)

//go:linkname ReadTtbr0El1 read_ttbr0_el1
//go:nosplit
func ReadTtbr0El1() uint64

//go:linkname WriteTtbr0El1 write_ttbr0_el1
//go:nosplit
func WriteTtbr0El1(value uint64)

//go:linkname WriteTtbr1El1 write_ttbr1_el1
//go:nosplit
func WriteTtbr1El1(value uint64)

//go:linkname ReadTcrEl1 read_tcr_el1
//go:nosplit
func ReadTcrEl1() uint64

//go:linkname WriteTcrEl1 write_tcr_el1
//go:nosplit
func WriteTcrEl1(value uint64)

//go:linkname ReadMairEl1 read_mair_el1
//go:nosplit
func ReadMairEl1() uint64

//go:linkname WriteMairEl1 write_mair_el1
//go:nosplit
func WriteMairEl1(value uint64)

//go:linkname ReadSctlrEl1 read_sctlr_el1
//go:nosplit
func ReadSctlrEl1() uint64

//go:linkname WriteSctlrEl1 write_sctlr_el1
//go:nosplit
func WriteSctlrEl1(value uint64)

// System register accessors (global symbols)

//go:linkname ReadCurrentEl read_current_el
//go:nosplit
func ReadCurrentEl() uint64

//go:linkname GetStackPointer get_stack_pointer
//go:nosplit
func GetStackPointer() uintptr

//go:linkname SetStackPointer set_stack_pointer
//go:nosplit
func SetStackPointer(value uintptr)

//go:linkname GetCurrentSP getCurrentSP
//go:nosplit
func GetCurrentSP() uintptr

//go:linkname GetCurrentG get_current_g
//go:nosplit
func GetCurrentG() uintptr

//go:linkname SetCurrentG set_current_g
//go:nosplit
func SetCurrentG(g uintptr)

//go:linkname SetGPointer set_g_pointer
//go:nosplit
func SetGPointer(g uintptr)

//go:linkname ReadElrEl1 read_elr_el1
//go:nosplit
func ReadElrEl1() uint64

//go:linkname WriteElrEl1 write_elr_el1
//go:nosplit
func WriteElrEl1(value uint64)

//go:linkname ReadSpsrEl1 read_spsr_el1
//go:nosplit
func ReadSpsrEl1() uint64

//go:linkname WriteSpsrEl1 write_spsr_el1
//go:nosplit
func WriteSpsrEl1(value uint64)

//go:linkname ReadEsrEl1 read_esr_el1
//go:nosplit
func ReadEsrEl1() uint64

//go:linkname ReadFarEl1 read_far_el1
//go:nosplit
func ReadFarEl1() uint64

//go:linkname ReadVbarEl1 read_vbar_el1
//go:nosplit
func ReadVbarEl1() uint64

//go:linkname SetVbarEl1 set_vbar_el1
//go:nosplit
func SetVbarEl1(value uint64)

//go:linkname SetVbarEl1ToAddr set_vbar_el1_to_addr
//go:nosplit
func SetVbarEl1ToAddr(addr uintptr)

//go:linkname GetExceptionVectorsAddr get_exception_vectors_addr
//go:nosplit
func GetExceptionVectorsAddr() uintptr

//go:linkname ReadDaif read_daif
//go:nosplit
func ReadDaif() uint64

//go:linkname ReadCtrEl0 read_ctr_el0
//go:nosplit
func ReadCtrEl0() uint64

//go:linkname ReadIdAa64pfr0El1 read_id_aa64pfr0_el1
//go:nosplit
func ReadIdAa64pfr0El1() uint64

//go:linkname ReadScrEl3 read_scr_el3
//go:nosplit
func ReadScrEl3() uint64

//go:linkname ReadTpidrEl0 read_tpidr_el0
//go:nosplit
func ReadTpidrEl0() uint64

//go:linkname WriteTpidrEl0 write_tpidr_el0
//go:nosplit
func WriteTpidrEl0(value uint64)

//go:linkname ReadTpidrEl1 read_tpidr_el1
//go:nosplit
func ReadTpidrEl1() uint64

//go:linkname WriteTpidrEl1 write_tpidr_el1
//go:nosplit
func WriteTpidrEl1(value uint64)

// Interrupt control (global symbols)

//go:linkname EnableIrqs enable_irqs
//go:nosplit
func EnableIrqs()

//go:linkname DisableIrqs disable_irqs
//go:nosplit
func DisableIrqs()

//go:linkname EnableIrqsAsm enable_irqs_asm
//go:nosplit
func EnableIrqsAsm()

//go:linkname DisableAlignmentCheck disable_alignment_check
//go:nosplit
func DisableAlignmentCheck()

// Stack control (global symbols)

//go:linkname SetMaxstacksize set_maxstacksize
//go:nosplit
func SetMaxstacksize(size uint64)

//go:linkname SetMaxstackceiling set_maxstackceiling
//go:nosplit
func SetMaxstackceiling(size uint64)

// Exception handling (global symbols)

//go:linkname ExcHang exc_hang
//go:nosplit
func ExcHang()

//go:linkname SyncExceptionHandler sync_exception_handler
//go:nosplit
func SyncExceptionHandler()

//go:linkname SyncExceptionHandlerEl0 sync_exception_handler_el0
//go:nosplit
func SyncExceptionHandlerEl0()

//go:linkname IrqExceptionHandlerEl0 irq_exception_handler_el0
//go:nosplit
func IrqExceptionHandlerEl0()

//go:linkname IrqExceptionEl1 irq_exception_el1
//go:nosplit
func IrqExceptionEl1()

// Exception vectors (global symbols)

//go:linkname VecSyncSpEl0 vec_sync_sp_el0
//go:nosplit
func VecSyncSpEl0()

//go:linkname VecIrqSpEl0 vec_irq_sp_el0
//go:nosplit
func VecIrqSpEl0()

//go:linkname VecFiqSpEl0 vec_fiq_sp_el0
//go:nosplit
func VecFiqSpEl0()

//go:linkname VecSerrorSpEl0 vec_serror_sp_el0
//go:nosplit
func VecSerrorSpEl0()

//go:linkname VecSyncSpEl1 vec_sync_sp_el1
//go:nosplit
func VecSyncSpEl1()

//go:linkname VecIrqSpEl1 vec_irq_sp_el1
//go:nosplit
func VecIrqSpEl1()

//go:linkname VecFiqSpEl1 vec_fiq_sp_el1
//go:nosplit
func VecFiqSpEl1()

//go:linkname VecSerrorSpEl1 vec_serror_sp_el1
//go:nosplit
func VecSerrorSpEl1()

//go:linkname VecSyncLowerA64 vec_sync_lower_a64
//go:nosplit
func VecSyncLowerA64()

//go:linkname VecIrqLowerA64 vec_irq_lower_a64
//go:nosplit
func VecIrqLowerA64()

//go:linkname VecFiqLowerA64 vec_fiq_lower_a64
//go:nosplit
func VecFiqLowerA64()

//go:linkname VecSerrorLowerA64 vec_serror_lower_a64
//go:nosplit
func VecSerrorLowerA64()

//go:linkname VecSyncLowerA32 vec_sync_lower_a32
//go:nosplit
func VecSyncLowerA32()

//go:linkname VecIrqLowerA32 vec_irq_lower_a32
//go:nosplit
func VecIrqLowerA32()

//go:linkname VecFiqLowerA32 vec_fiq_lower_a32
//go:nosplit
func VecFiqLowerA32()

//go:linkname VecSerrorLowerA32 vec_serror_lower_a32
//go:nosplit
func VecSerrorLowerA32()

// Boot entry point (global symbol)
// This is the bare-metal entry point that QEMU jumps to.
// After building, use objcopy to set the ELF entry point to this symbol.

//go:linkname CardinalBoot _cardinal_boot
//go:nosplit
func CardinalBoot()
