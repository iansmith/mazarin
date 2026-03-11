#include "textflag.h"

// haltForever loops forever using WFI to save power.
// Called when a process exits cleanly.
//
// WFI (Wait For Interrupt) puts the CPU in a low-power state until
// an interrupt occurs. We loop forever to ensure the CPU stays halted.
TEXT ·haltForever(SB), NOSPLIT, $0-0
loop:
	WFI              // Wait for interrupt (low power state)
	B loop           // Loop forever
