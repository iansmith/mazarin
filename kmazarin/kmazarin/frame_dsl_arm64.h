// frame_dsl_arm64.h — named-field access for the arm64 ThreadContext.
//
// Parallel to frame_dsl_amd64.h. The Go struct ThreadContext
// (thread_context_arm64.go) is the single source of truth for the layout; the
// compiler emits a symbolic offset for every field into go_asm.h
// (ThreadContext_X, ThreadContext_SP, ThreadContext_ELR, ThreadContext_SPSR),
// so the assembly never names a numeric ctx offset. Reorder or extend the struct
// and these accesses follow automatically.
//
// arm64 keeps the GPRs in a single array field `X [31]uint64`, so the whole
// register file is ThreadContext_X + n*8 (one struct field). The restore paths
// copy the context with LDP/STP register pairs; FLD/FST below cover the
// single-register special-reg accesses (SP, ELR, SPSR, and the X28 g load).
//
// The cmd/frameaudit gate enforces that every ThreadContext field symbol is
// named by the Go saver and by each FRAME-SAVE/RESTORE asm region. SPSR is
// loaded adjacently via the ELR LDP pair (ThreadContext_ELR+8) in the
// ctx->frame restore blocks, so it carries frameaudit:exempt-asm there; it is
// still checked on the Go save side and by the value-flow boot selftest.

#include "go_asm.h"

#define FLD(base, field, reg)  MOVD field(base), reg
#define FST(base, field, reg)  MOVD reg, field(base)

// CTX_RESTORE_TO_FRAME copies the whole ThreadContext at `ctx` into the exception
// frame at RSP (EXC_FRAME_* offsets are defined by the includer). Defined ONCE
// and expanded at every ctx->frame restore site so they cannot drift — the arm64
// analogue of amd64's CTX_GPRS list. Uses R0/R1 as scratch. The WORD-encoded
// stores cover x18/x19/x28/x29 (no Plan9 register names in this context). SPSR
// (ThreadContext_ELR+8) rides the ELR LDP pair — frameaudit:exempt-asm there.
#define CTX_RESTORE_TO_FRAME(ctx) \
	LDP	ThreadContext_X+0(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X0(RSP); \
	LDP	ThreadContext_X+16(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X0+16(RSP); \
	LDP	ThreadContext_X+32(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X0+32(RSP); \
	LDP	ThreadContext_X+48(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X0+48(RSP); \
	LDP	ThreadContext_X+64(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8(RSP); \
	LDP	ThreadContext_X+80(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+16(RSP); \
	LDP	ThreadContext_X+96(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+32(RSP); \
	LDP	ThreadContext_X+112(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+48(RSP); \
	LDP	ThreadContext_X+128(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+64(RSP); \
	LDP	ThreadContext_X+144(ctx), (R0, R1); \
	WORD	$0xf9004be0; \
	WORD	$0xf9004fe1; \
	LDP	ThreadContext_X+160(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+96(RSP); \
	LDP	ThreadContext_X+176(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+112(RSP); \
	LDP	ThreadContext_X+192(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+128(RSP); \
	LDP	ThreadContext_X+208(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_X8+144(RSP); \
	MOVD	ThreadContext_X+224(ctx), R0; \
	WORD	$0xf90073e0; \
	LDP	ThreadContext_X+232(ctx), (R0, R1); \
	WORD	$0xf90077e0; \
	MOVD	R1, EXC_FRAME_X28+16(RSP); \
	MOVD	ThreadContext_SP(ctx), R0; \
	MOVD	R0, EXC_FRAME_SP_EL0(RSP); \
	LDP	ThreadContext_ELR(ctx), (R0, R1); \
	STP	(R0, R1), EXC_FRAME_ELR_SPSR(RSP)
