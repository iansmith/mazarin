// exc_utils.s - Exception handling utility functions (Go/Plan9 assembly)
//
// This file will contain:
// - print_hex64: Print 64-bit hex value to UART
// - print_string: Print null-terminated string to UART
// - print_decimal_uart: Print decimal number
// - print_hex_byte_uart: Print single hex byte
//
// Migrated from: asm/aarch64/exceptions.s (print_hex64, print_string, etc.)

#include "textflag.h"

// TODO: Migrate print utility functions from exceptions.s
// Currently kept in GCC assembly
