// Package kernel contains kernel-level assembly routines for Cardinal.
// This includes memory operations, syscall handling, goroutine support,
// write barriers, and linker symbol accessors.
package kernel

import "unsafe"

// Memory operations (global symbols - need linkname)
//
// NOTE: bzero4K is now implemented as a pure Go function in mmu.go
// and doesn't need a linkname declaration

//go:linkname MemmoveBytes MemmoveBytes
//go:nosplit
func MemmoveBytes(dst, src unsafe.Pointer, size uint32)

//go:linkname DcZva dc_zva
//go:nosplit
func DcZva(addr uintptr)

// Async preemption (global symbol)

//go:linkname GetAsyncPreemptBMAddr get_asyncPreemptBM_addr
//go:nosplit
func GetAsyncPreemptBMAddr() uintptr

// Exception handling (global symbols)

//go:linkname SyncExceptionEntry sync_exception_entry
//go:nosplit
func SyncExceptionEntry()

//go:linkname SyscallReturn syscall_return
//go:nosplit
func SyscallReturn()

//go:linkname ExceptionReturn exception_return
//go:nosplit
func ExceptionReturn()

//go:linkname LoadContextAndEret load_context_and_eret
//go:nosplit
func LoadContextAndEret()

// Debug printing (global symbols)

//go:linkname PrintHex64 print_hex64
//go:nosplit
func PrintHex64(val uint64)

//go:linkname PrintString print_string
//go:nosplit
func PrintString(s uintptr)

//go:linkname PrintDecimalUart print_decimal_uart
//go:nosplit
func PrintDecimalUart(val uint64)

//go:linkname PrintHexByteUart print_hex_byte_uart
//go:nosplit
func PrintHexByteUart(val byte)

// Goroutine support (global symbols)

//go:linkname SwitchToGoroutine switchToGoroutine
//go:nosplit
func SwitchToGoroutine(g unsafe.Pointer)

//go:linkname RunOnGoroutine runOnGoroutine
//go:nosplit
func RunOnGoroutine(g unsafe.Pointer, fn func())

//go:linkname CallOnG0Stack callOnG0Stack
//go:nosplit
func CallOnG0Stack()

//go:linkname GetGRegister getGRegister
//go:nosplit
func GetGRegister() uintptr

// Runtime initialization calls (global symbols)

//go:linkname CallMallocinit call_mallocinit
//go:nosplit
func CallMallocinit()

//go:linkname CallRuntimeArgs call_runtime_args
//go:nosplit
func CallRuntimeArgs() int

//go:linkname CallRuntimeOsinit call_runtime_osinit
//go:nosplit
func CallRuntimeOsinit() int

//go:linkname CallRuntimeSchedinit call_runtime_schedinit
//go:nosplit
func CallRuntimeSchedinit() int

//go:linkname CallRuntimeNewproc call_runtime_newproc
//go:nosplit
func CallRuntimeNewproc() int

//go:linkname CallNewprocSimpleMain call_newproc_simple_main
//go:nosplit
func CallNewprocSimpleMain() int

//go:linkname CallRuntimeMstart call_runtime_mstart
//go:nosplit
func CallRuntimeMstart()

//go:linkname KernelMain kernel_main
//go:nosplit
func KernelMain()

//go:linkname JumpToKmazarin jump_to_kmazarin
//go:nosplit
func JumpToKmazarin(entry, sp, argc, argv uintptr)

// Linker symbol accessors (package-local symbols with · prefix in assembly)
// These match ·get_xxx_addr in linker_symbols_arm64.s

//go:nosplit
func get_start_addr() uintptr

//go:nosplit
func get_text_start_addr() uintptr

//go:nosplit
func get_text_end_addr() uintptr

//go:nosplit
func get_rodata_start_addr() uintptr

//go:nosplit
func get_rodata_end_addr() uintptr

//go:nosplit
func get_data_start_addr() uintptr

//go:nosplit
func get_data_end_addr() uintptr

//go:nosplit
func get_bss_start_addr() uintptr

//go:nosplit
func get_bss_end_addr() uintptr

//go:nosplit
func get_end_addr() uintptr

//go:nosplit
func get_dtb_boot_addr() uintptr

//go:nosplit
func get_dtb_size() uintptr

//go:nosplit
func get_cardinal_end() uintptr

//go:nosplit
func get_cardinal_allocation_size() uintptr

//go:nosplit
func get_kmazarin_load_addr() uintptr

//go:nosplit
func get_kmazarin_start() uintptr

//go:nosplit
func get_kmazarin_size() uintptr

//go:nosplit
func get_page_tables_start_addr() uintptr

//go:nosplit
func get_page_tables_end_addr() uintptr

//go:nosplit
func get_ram_start() uintptr

//go:nosplit
func get_stack_top_addr() uintptr

//go:nosplit
func get_g0_stack_bottom() uintptr

//go:nosplit
func get_uart_base() uintptr

//go:nosplit
func get_uart_size() uintptr

//go:nosplit
func get_gic_base() uintptr

//go:nosplit
func get_gic_size() uintptr

//go:nosplit
func get_rtc_base() uintptr

//go:nosplit
func get_fwcfg_base() uintptr

//go:nosplit
func get_fwcfg_size() uintptr

//go:nosplit
func get_bochs_display_base() uintptr

//go:nosplit
func get_bochs_display_size() uintptr

//go:nosplit
func get_pci_bar_base() uintptr

//go:nosplit
func get_pci_bar_size() uintptr

//go:nosplit
func get_g0_addr() uintptr

//go:nosplit
func get_m0_addr() uintptr

//go:nosplit
func get_mcache0_addr() uintptr

//go:nosplit
func get_phys_page_size_addr() uintptr

//go:nosplit
func get_runtime_mheap_addr() uintptr

//go:nosplit
func get_emptymspan_addr() uintptr

//go:nosplit
func get_runtime_save_g_addr() uintptr

//go:nosplit
func get_runtime_load_g_addr() uintptr

//go:nosplit
func get_caller_stack_pointer() uintptr

// Exported wrappers for package-local functions (so main can import them)

func GetStartAddr() uintptr              { return get_start_addr() }
func GetTextStartAddr() uintptr          { return get_text_start_addr() }
func GetTextEndAddr() uintptr            { return get_text_end_addr() }
func GetRodataStartAddr() uintptr        { return get_rodata_start_addr() }
func GetRodataEndAddr() uintptr          { return get_rodata_end_addr() }
func GetDataStartAddr() uintptr          { return get_data_start_addr() }
func GetDataEndAddr() uintptr            { return get_data_end_addr() }
func GetBssStartAddr() uintptr           { return get_bss_start_addr() }
func GetBssEndAddr() uintptr             { return get_bss_end_addr() }
func GetEndAddr() uintptr                { return get_end_addr() }
func GetDtbBootAddr() uintptr            { return get_dtb_boot_addr() }
func GetDtbSize() uintptr                { return get_dtb_size() }
func GetCardinalEnd() uintptr            { return get_cardinal_end() }
func GetCardinalAllocationSize() uintptr { return get_cardinal_allocation_size() }
func GetKmazarinLoadAddr() uintptr       { return get_kmazarin_load_addr() }
func GetKmazarinStart() uintptr          { return get_kmazarin_start() }
func GetKmazarinSize() uintptr           { return get_kmazarin_size() }
func GetPageTablesStartAddr() uintptr    { return get_page_tables_start_addr() }
func GetPageTablesEndAddr() uintptr      { return get_page_tables_end_addr() }
func GetRamStart() uintptr               { return get_ram_start() }
func GetStackTopAddr() uintptr           { return get_stack_top_addr() }
func GetG0StackBottom() uintptr          { return get_g0_stack_bottom() }
func GetUartBase() uintptr               { return get_uart_base() }
func GetUartSize() uintptr               { return get_uart_size() }
func GetGicBase() uintptr                { return get_gic_base() }
func GetGicSize() uintptr                { return get_gic_size() }
func GetRtcBase() uintptr                { return get_rtc_base() }
func GetFwcfgBase() uintptr              { return get_fwcfg_base() }
func GetFwcfgSize() uintptr              { return get_fwcfg_size() }
func GetBochsDisplayBase() uintptr       { return get_bochs_display_base() }
func GetBochsDisplaySize() uintptr       { return get_bochs_display_size() }
func GetPciBarBase() uintptr             { return get_pci_bar_base() }
func GetPciBarSize() uintptr             { return get_pci_bar_size() }
func GetG0Addr() uintptr                 { return get_g0_addr() }
func GetM0Addr() uintptr                 { return get_m0_addr() }
func GetMcache0Addr() uintptr            { return get_mcache0_addr() }
func GetPhysPageSizeAddr() uintptr       { return get_phys_page_size_addr() }
func GetRuntimeMheapAddr() uintptr       { return get_runtime_mheap_addr() }
func GetEmptymspanAddr() uintptr         { return get_emptymspan_addr() }
func GetRuntimeSaveGAddr() uintptr       { return get_runtime_save_g_addr() }
func GetRuntimeLoadGAddr() uintptr       { return get_runtime_load_g_addr() }
func GetCallerStackPointer() uintptr     { return get_caller_stack_pointer() }
