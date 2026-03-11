#include "textflag.h"

// haltForever loops forever using WFI to save power.
// Called when a process exits cleanly.
TEXT ·haltForever(SB), NOSPLIT, $0-0
loop:
	WORD	$0x10500073		// WFI
	JMP	loop
