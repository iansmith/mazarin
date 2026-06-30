// xmm_frame_amd64.h — XMM (X0–X15) save/restore macros for the exception path.
//
//   SAVE_XMM_AT(slot)    : MOVOU X0..X15 → 0..240(slot)   (registers → 256-byte region @ slot)
//   RESTORE_XMM_AT(slot) : MOVOU 0..240(slot) → X0..X15   (256-byte region @ slot → registers)
//
// `slot` is a register holding the base address of a 256-byte (16 × 16) XMM
// region. Sharing one pair of macros keeps the production per-exception-frame
// save/restore (MAZ-139 item 2: framePtr-256) and the MAZ-139 RED self-test
// exercising byte-identical logic. amd64 only.

#define SAVE_XMM_AT(slot) \
	MOVOU	X0,  0(slot);   \
	MOVOU	X1,  16(slot);  \
	MOVOU	X2,  32(slot);  \
	MOVOU	X3,  48(slot);  \
	MOVOU	X4,  64(slot);  \
	MOVOU	X5,  80(slot);  \
	MOVOU	X6,  96(slot);  \
	MOVOU	X7,  112(slot); \
	MOVOU	X8,  128(slot); \
	MOVOU	X9,  144(slot); \
	MOVOU	X10, 160(slot); \
	MOVOU	X11, 176(slot); \
	MOVOU	X12, 192(slot); \
	MOVOU	X13, 208(slot); \
	MOVOU	X14, 224(slot); \
	MOVOU	X15, 240(slot)

#define RESTORE_XMM_AT(slot) \
	MOVOU	0(slot),   X0;  \
	MOVOU	16(slot),  X1;  \
	MOVOU	32(slot),  X2;  \
	MOVOU	48(slot),  X3;  \
	MOVOU	64(slot),  X4;  \
	MOVOU	80(slot),  X5;  \
	MOVOU	96(slot),  X6;  \
	MOVOU	112(slot), X7;  \
	MOVOU	128(slot), X8;  \
	MOVOU	144(slot), X9;  \
	MOVOU	160(slot), X10; \
	MOVOU	176(slot), X11; \
	MOVOU	192(slot), X12; \
	MOVOU	208(slot), X13; \
	MOVOU	224(slot), X14; \
	MOVOU	240(slot), X15
