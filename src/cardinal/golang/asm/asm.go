// Package asm re-exports assembly functions from subpackages for convenience.
// This allows existing code to continue using "asm.X" syntax.
package asm

import (
	"unsafe"

	"cardinal/asm/arch/arm64"
	"cardinal/asm/dev"
	"cardinal/asm/kernel"
)

// Re-export kernel package functions with proper signatures
//
// NOTE: Bzero4K is implemented in main/mmu.go, not in the asm package.
// It's exported here only for documentation - actual calls go directly to main.bzero4K

// MemmoveBytes copies size bytes from src to dst
func MemmoveBytes(dst, src unsafe.Pointer, size uint32) { kernel.MemmoveBytes(dst, src, size) }

// SwitchToGoroutine switches to the given goroutine
func SwitchToGoroutine(g unsafe.Pointer) { kernel.SwitchToGoroutine(g) }

// RunOnGoroutine runs a function on the given goroutine
func RunOnGoroutine(g unsafe.Pointer, fn func()) { kernel.RunOnGoroutine(g, fn) }

var (
	GetAsyncPreemptBMAddr = kernel.GetAsyncPreemptBMAddr
	SyncExceptionEntry    = kernel.SyncExceptionEntry
	SyscallReturn         = kernel.SyscallReturn
	ExceptionReturn       = kernel.ExceptionReturn
	LoadContextAndEret    = kernel.LoadContextAndEret
	PrintHex64            = kernel.PrintHex64
	PrintString           = kernel.PrintString
	PrintDecimalUart      = kernel.PrintDecimalUart
	PrintHexByteUart      = kernel.PrintHexByteUart
	CallOnG0Stack         = kernel.CallOnG0Stack
	GetGRegister            = kernel.GetGRegister
	DcZva                   = kernel.DcZva
	CallMallocinit          = kernel.CallMallocinit
	CallRuntimeArgs         = kernel.CallRuntimeArgs
	CallRuntimeOsinit       = kernel.CallRuntimeOsinit
	CallRuntimeSchedinit    = kernel.CallRuntimeSchedinit
	CallRuntimeNewproc      = kernel.CallRuntimeNewproc
	CallNewprocSimpleMain   = kernel.CallNewprocSimpleMain
	CallRuntimeMstart       = kernel.CallRuntimeMstart
	KernelMain              = kernel.KernelMain
	JumpToKmazarin          = kernel.JumpToKmazarin
	GetStartAddr            = kernel.GetStartAddr
	GetTextStartAddr        = kernel.GetTextStartAddr
	GetTextEndAddr          = kernel.GetTextEndAddr
	GetRodataStartAddr      = kernel.GetRodataStartAddr
	GetRodataEndAddr        = kernel.GetRodataEndAddr
	GetDataStartAddr        = kernel.GetDataStartAddr
	GetDataEndAddr          = kernel.GetDataEndAddr
	GetBssStartAddr         = kernel.GetBssStartAddr
	GetBssEndAddr           = kernel.GetBssEndAddr
	GetEndAddr              = kernel.GetEndAddr
	GetDtbBootAddr          = kernel.GetDtbBootAddr
	GetDtbSize              = kernel.GetDtbSize
	GetCardinalEnd          = kernel.GetCardinalEnd
	GetCardinalAllocationSize = kernel.GetCardinalAllocationSize
	GetKmazarinLoadAddr     = kernel.GetKmazarinLoadAddr
	GetKmazarinStart        = kernel.GetKmazarinStart
	GetKmazarinSize         = kernel.GetKmazarinSize
	GetPageTablesStartAddr  = kernel.GetPageTablesStartAddr
	GetPageTablesEndAddr    = kernel.GetPageTablesEndAddr
	GetRamStart             = kernel.GetRamStart
	GetStackTopAddr         = kernel.GetStackTopAddr
	GetG0StackBottom        = kernel.GetG0StackBottom
	GetUartBase             = kernel.GetUartBase
	GetUartSize             = kernel.GetUartSize
	GetGicBase              = kernel.GetGicBase
	GetGicSize              = kernel.GetGicSize
	GetRtcBase              = kernel.GetRtcBase
	GetFwcfgBase            = kernel.GetFwcfgBase
	GetFwcfgSize            = kernel.GetFwcfgSize
	GetBochsDisplayBase     = kernel.GetBochsDisplayBase
	GetBochsDisplaySize     = kernel.GetBochsDisplaySize
	GetPciBarBase           = kernel.GetPciBarBase
	GetPciBarSize           = kernel.GetPciBarSize
	GetG0Addr               = kernel.GetG0Addr
	GetM0Addr               = kernel.GetM0Addr
	GetMcache0Addr          = kernel.GetMcache0Addr
	GetPhysPageSizeAddr     = kernel.GetPhysPageSizeAddr
	GetRuntimeMheapAddr     = kernel.GetRuntimeMheapAddr
	GetEmptymspanAddr       = kernel.GetEmptymspanAddr
	GetRuntimeSaveGAddr     = kernel.GetRuntimeSaveGAddr
	GetRuntimeLoadGAddr     = kernel.GetRuntimeLoadGAddr
	GetCallerStackPointer   = kernel.GetCallerStackPointer
)

// Re-export dev package functions

var (
	UartInitPl011         = dev.UartInitPl011
	UartPutcPl011         = dev.UartPutcPl011
	ReadCntvCtlEl0        = dev.ReadCntvCtlEl0
	WriteCntvCtlEl0       = dev.WriteCntvCtlEl0
	ReadCntvTvalEl0       = dev.ReadCntvTvalEl0
	WriteCntvTvalEl0      = dev.WriteCntvTvalEl0
	ReadCntvCvalEl0       = dev.ReadCntvCvalEl0
	WriteCntvCvalEl0      = dev.WriteCntvCvalEl0
	ReadCntvctEl0         = dev.ReadCntvctEl0
	ReadCntfrqEl0         = dev.ReadCntfrqEl0
	ReadCntpCtlEl0        = dev.ReadCntpCtlEl0
	WriteCntpCtlEl0       = dev.WriteCntpCtlEl0
	ReadCntpTvalEl0       = dev.ReadCntpTvalEl0
	WriteCntpTvalEl0      = dev.WriteCntpTvalEl0
	ReadCntpCvalEl0       = dev.ReadCntpCvalEl0
	WriteCntpCvalEl0      = dev.WriteCntpCvalEl0
	ReadCntpctEl0         = dev.ReadCntpctEl0
	MmioRead              = dev.MmioRead
	MmioWrite             = dev.MmioWrite
	MmioRead16            = dev.MmioRead16
	MmioWrite16           = dev.MmioWrite16
	MmioWrite64           = dev.MmioWrite64
	StorePointerNobarrier = dev.StorePointerNobarrier
	StorePointerNoBarrier = dev.StorePointerNobarrier // Alias with capital B
	QemuExit              = dev.QemuExit
	SemihostingExit       = dev.SemihostingExit
	JumpToNull            = dev.JumpToNull
	ImageDataStart        = dev.ImageDataStart
	ImageDataEnd          = dev.ImageDataEnd
	KmazarinBinaryStart   = dev.KmazarinBinaryStart
	KmazarinBinaryEnd     = dev.KmazarinBinaryEnd
)

// Re-export arm64 package functions

var (
	Dsb                        = arm64.Dsb
	DsbIsh                     = arm64.DsbIsh
	Isb                        = arm64.Isb
	Wfi                        = arm64.Wfi
	Nop                        = arm64.Nop
	DcCvau                     = arm64.DcCvau
	CleanDcacheVa              = arm64.CleanDcacheVa
	CleanDataCacheVA           = arm64.CleanDataCacheVA
	IcIvau                     = arm64.IcIvau
	InvalidateInstructionCacheAll = arm64.InvalidateInstructionCacheAll
	InvalidateTlbAll           = arm64.InvalidateTlbAll
	InvalidateTlbVa            = arm64.InvalidateTlbVa
	EnableMmuMinimal           = arm64.EnableMmuMinimal
	ReadTtbr0El1               = arm64.ReadTtbr0El1
	ReadTtbr1El1               = arm64.ReadTtbr1El1
	WriteTtbr0El1              = arm64.WriteTtbr0El1
	WriteTtbr1El1              = arm64.WriteTtbr1El1
	ReadTcrEl1                 = arm64.ReadTcrEl1
	WriteTcrEl1                = arm64.WriteTcrEl1
	ReadMairEl1                = arm64.ReadMairEl1
	WriteMairEl1               = arm64.WriteMairEl1
	ReadSctlrEl1               = arm64.ReadSctlrEl1
	WriteSctlrEl1              = arm64.WriteSctlrEl1
	ReadCurrentEl              = arm64.ReadCurrentEl
	GetStackPointer            = arm64.GetStackPointer
	SetStackPointer            = arm64.SetStackPointer
	GetCurrentG                = arm64.GetCurrentG
	SetCurrentG                = arm64.SetCurrentG
	SetGPointer                = arm64.SetGPointer
	ReadElrEl1                 = arm64.ReadElrEl1
	WriteElrEl1                = arm64.WriteElrEl1
	ReadSpsrEl1                = arm64.ReadSpsrEl1
	WriteSpsrEl1               = arm64.WriteSpsrEl1
	ReadEsrEl1                 = arm64.ReadEsrEl1
	ReadFarEl1                 = arm64.ReadFarEl1
	ReadVbarEl1                = arm64.ReadVbarEl1
	SetVbarEl1                 = arm64.SetVbarEl1
	SetVbarEl1ToAddr           = arm64.SetVbarEl1ToAddr
	GetExceptionVectorsAddr    = arm64.GetExceptionVectorsAddr
	ReadDaif                   = arm64.ReadDaif
	ReadCtrEl0                 = arm64.ReadCtrEl0
	ReadIdAa64pfr0El1          = arm64.ReadIdAa64pfr0El1
	ReadScrEl3                 = arm64.ReadScrEl3
	ReadTpidrEl0               = arm64.ReadTpidrEl0
	WriteTpidrEl0              = arm64.WriteTpidrEl0
	ReadTpidrEl1               = arm64.ReadTpidrEl1
	WriteTpidrEl1              = arm64.WriteTpidrEl1
	EnableIrqs                 = arm64.EnableIrqs
	DisableIrqs                = arm64.DisableIrqs
	EnableIrqsAsm              = arm64.EnableIrqsAsm
	DisableAlignmentCheck      = arm64.DisableAlignmentCheck
	SetMaxstacksize            = arm64.SetMaxstacksize
	SetMaxstackceiling         = arm64.SetMaxstackceiling
	ExcHang                    = arm64.ExcHang
	SyncExceptionHandler       = arm64.SyncExceptionHandler
	SyncExceptionHandlerEl0    = arm64.SyncExceptionHandlerEl0
	IrqExceptionHandlerEl0     = arm64.IrqExceptionHandlerEl0
	IrqExceptionEl1            = arm64.IrqExceptionEl1
	VecSyncSpEl0               = arm64.VecSyncSpEl0
	VecIrqSpEl0                = arm64.VecIrqSpEl0
	VecFiqSpEl0                = arm64.VecFiqSpEl0
	VecSerrorSpEl0             = arm64.VecSerrorSpEl0
	VecSyncSpEl1               = arm64.VecSyncSpEl1
	VecIrqSpEl1                = arm64.VecIrqSpEl1
	VecFiqSpEl1                = arm64.VecFiqSpEl1
	VecSerrorSpEl1             = arm64.VecSerrorSpEl1
	VecSyncLowerA64            = arm64.VecSyncLowerA64
	VecIrqLowerA64             = arm64.VecIrqLowerA64
	VecFiqLowerA64             = arm64.VecFiqLowerA64
	VecSerrorLowerA64          = arm64.VecSerrorLowerA64
	VecSyncLowerA32            = arm64.VecSyncLowerA32
	VecIrqLowerA32             = arm64.VecIrqLowerA32
	VecFiqLowerA32             = arm64.VecFiqLowerA32
	VecSerrorLowerA32          = arm64.VecSerrorLowerA32
	CardinalBoot               = arm64.CardinalBoot
)
