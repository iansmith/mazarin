#include "textflag.h"

// haltForever loops forever using HLT to save power.
// Called when a process exits cleanly.
TEXT ·haltForever(SB), NOSPLIT, $0-0
loop:
	HLT
	JMP	loop
