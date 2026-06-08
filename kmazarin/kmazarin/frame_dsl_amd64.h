// frame_dsl_amd64.h — named-field access for the amd64 ThreadContext.
//
// The Go struct ThreadContext (thread_context_amd64.go) is the single source of
// truth for the layout. The compiler emits a symbolic offset for every field
// into go_asm.h (ThreadContext_RAX, ThreadContext_TLSG, ...), so the assembly
// below never names a numeric offset. Reorder or extend the struct and these
// accesses follow automatically — no magic numbers to keep in sync.
//
// Vocabulary:
//
//	FST(base, ThreadContext_FIELD, reg)   store reg into ctx.FIELD   (FrameStore)
//	FLD(base, ThreadContext_FIELD, reg)   load  ctx.FIELD into reg   (FrameLoad)
//	CTX_GPRS(OP, base)                     apply OP to every saved GPR (one list,
//	                                       expanded by both save and restore)
//
// CTX_GPRS is the uniform register set, defined ONCE. A save site writes it with
// CTX_GPRS(FST, base); a restore site reads it with CTX_GPRS(FLD, base). Because
// both directions and every path expand the same list, they cannot disagree on
// which registers are covered. The cmd/frameaudit checker enforces that every
// other (non-list) field symbol is named in each FRAME-SAVE/FRAME-RESTORE region.
//
// R12 is the base-pointer register for ThreadContext access, so the ctx.R12
// field is handled positionally (saved as a placeholder, restored last) outside
// CTX_GPRS — see the frameaudit:exempt-asm note on the struct field.

#include "go_asm.h"

#define FST(base, field, reg)  MOVQ reg, field(base)
#define FLD(base, field, reg)  MOVQ field(base), reg

// CTX_GPRS — every general-purpose register persisted in ThreadContext except
// R12 (the base pointer). Order mirrors the struct.
#define CTX_GPRS(OP, base) \
	OP(base, ThreadContext_RAX, AX) \
	OP(base, ThreadContext_RBX, BX) \
	OP(base, ThreadContext_RCX, CX) \
	OP(base, ThreadContext_RDX, DX) \
	OP(base, ThreadContext_RSI, SI) \
	OP(base, ThreadContext_RDI, DI) \
	OP(base, ThreadContext_RBP, BP) \
	OP(base, ThreadContext_R8,  R8) \
	OP(base, ThreadContext_R9,  R9) \
	OP(base, ThreadContext_R10, R10) \
	OP(base, ThreadContext_R11, R11) \
	OP(base, ThreadContext_R13, R13) \
	OP(base, ThreadContext_R14, R14) \
	OP(base, ThreadContext_R15, R15)
