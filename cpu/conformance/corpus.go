package conformance

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

// Corpus is a curated Tier-1 differential corpus. It concentrates on the
// architectural corners a fast/recompiler backend most easily gets wrong —
// condition flags (carry/overflow survival, ADC chains, shift carry, MUL
// leaving C/V), conditional-branch resolution, memory transfers, and the
// ARM/Thumb split. Each program ends in BKPT so Run stops at a breakpoint.
//
// This is not exhaustive: the deep coverage is the golden interpreter unit
// suite plus running whole games through both backends (Tier 2). The Corpus
// gives a fast, self-validating first gate — a bad encoding here faults and
// fails the interpreter self-test.
var Corpus = []Program{
	{
		Name: "thumb/adds-carry-overflow-survive-movs",
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{cpu.RegisterR1: 0x80000000, cpu.RegisterR2: 0x80000000},
		Code: []byte{
			0x88, 0x18, // adds r0, r1, r2   ; r0=0 C=1 V=1 Z=1
			0x05, 0x23, // movs r3, #5       ; N=0 Z=0, keep C=1 V=1
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "thumb/subs-borrow-negative",
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{cpu.RegisterR0: 1},
		Code: []byte{
			0x81, 0x1e, // subs r1, r0, #2   ; r1=0xffffffff N=1 C=0(borrow) Z=0
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "thumb/adc-consumes-carry",
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{cpu.RegisterR0: 0xffffffff},
		Code: []byte{
			0x01, 0x30, // adds r0, #1       ; r0=0 C=1 Z=1
			0x00, 0x21, // movs r1, #0       ; keep C=1
			0x41, 0x41, // adcs r1, r0       ; r1 = 0+0+C = 1
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "thumb/lsls-shifts-carry-out",
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{cpu.RegisterR0: 0xc0000000},
		Code: []byte{
			0x41, 0x00, // lsls r1, r0, #1   ; r1=0x80000000 N=1 C=1
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "thumb/muls-sets-nz-preserves-cv",
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{
			cpu.RegisterR0: 0x80000000, cpu.RegisterR1: 0x80000000,
			cpu.RegisterR3: 3, cpu.RegisterR4: 5,
		},
		Code: []byte{
			0x42, 0x18, // adds r2, r0, r1   ; C=1 V=1 Z=1
			0x63, 0x43, // muls r3, r4       ; r3=15 N=0 Z=0, C/V preserved
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "thumb/beq-taken-skips-store",
		Mode: cpu.ModeThumb,
		Code: []byte{
			0x00, 0x20, // movs r0, #0
			0x00, 0x28, // cmp r0, #0        ; Z=1
			0x00, 0xd0, // beq +0            ; target = pc+4 -> skip next
			0x09, 0x21, // movs r1, #9       ; skipped
			0x00, 0xbe, // bkpt              ; r1 stays 0
		},
	},
	{
		Name: "thumb/bne-taken-skips-store",
		Mode: cpu.ModeThumb,
		Code: []byte{
			0x01, 0x20, // movs r0, #1
			0x00, 0x28, // cmp r0, #0        ; Z=0
			0x00, 0xd1, // bne +0            ; taken -> skip next
			0x09, 0x21, // movs r1, #9       ; skipped
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "thumb/str-ldr-roundtrip",
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{cpu.RegisterR0: 0xdeadbeef, cpu.RegisterR1: DataBase},
		Code: []byte{
			0x08, 0x60, // str r0, [r1, #0]
			0x0a, 0x68, // ldr r2, [r1, #0]  ; r2=0xdeadbeef
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "thumb/push-pop",
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{cpu.RegisterR0: 0x11111111, cpu.RegisterR1: 0x22222222},
		Code: []byte{
			0x03, 0xb4, // push {r0, r1}
			0x0c, 0xbc, // pop {r2, r3}      ; r2=0x11111111 r3=0x22222222
			0x00, 0xbe, // bkpt
		},
	},
	{
		Name: "arm/mov-adds-bkpt",
		Mode: cpu.ModeARM,
		Code: armCode(
			0xe3a00005, // mov r0, #5
			0xe0901000, // adds r1, r0, r0   ; r1=10
			0xe1200070, // bkpt
		),
	},
	{
		Name: "arm/conditions-and-shifter",
		Mode: cpu.ModeARM,
		Regs: map[uint32]uint32{cpu.RegisterLR: 0x1001, cpu.RegisterR2: 3},
		Code: armCode(
			0xe31e0001, // tst lr, #1          ; Z=0
			0x03a00001, // moveq r0, #1        ; skipped
			0x13a00002, // movne r0, #2
			0xe1b02f82, // movs r2, r2, lsl #31 ; N=1 C=1
			0xe1200070, // bkpt
		),
	},
	{
		Name: "arm/single-and-halfword-transfers",
		Mode: cpu.ModeARM,
		Regs: map[uint32]uint32{
			cpu.RegisterR1:   DataBase,
			cpu.RegisterLR:   DataBase,
			cpu.RegisterR2:   1,
			cpu.RegisterR6:   0xdead1234,
			cpu.RegisterCPSR: 1 << 29,
		},
		Data: map[uint32][]byte{DataBase: {0x80, 0x00, 0x00, 0xff, 0x78, 0x56, 0x34, 0x12}},
		Code: armCode(
			0x24913004, // ldrcs r3, [r1], #4
			0xe7917102, // ldr r7, [r1, r2, lsl #2]
			0xe1de30d0, // ldrsb r3, [lr]
			0xe1de40f2, // ldrsh r4, [lr, #2]
			0xe1de50b2, // ldrh r5, [lr, #2]
			0xe0ce60b4, // strh r6, [lr], #4
			0xe1200070, // bkpt
		),
	},
	{
		Name: "arm/multiply-long-clz",
		Mode: cpu.ModeARM,
		Regs: map[uint32]uint32{cpu.RegisterR1: 0xfffffffe, cpu.RegisterR2: 3},
		Code: armCode(
			0xe0030291, // mul r3, r1, r2
			0xe0242391, // mla r4, r1, r3, r2
			0xe0c65291, // smull r5, r6, r1, r2
			0xe16f7f11, // clz r7, r1
			0xe1200070, // bkpt
		),
	},
	{
		Name: "arm/block-transfer-and-branch-exchange",
		Mode: cpu.ModeARM,
		Regs: map[uint32]uint32{
			cpu.RegisterR0: 0x12345678,
			cpu.RegisterLR: CodeBase + 16,
		},
		Code: armCode(
			0xe92d4001, // stmdb sp!, {r0, lr}
			0xe3a00000, // mov r0, #0
			0xe8bd4001, // ldmia sp!, {r0, lr}
			0xe12fff1e, // bx lr
			0xe1200070, // bkpt
		),
	},
}

func armCode(words ...uint32) []byte {
	encoded := make([]byte, 4*len(words))
	for index, word := range words {
		binary.LittleEndian.PutUint32(encoded[index*4:], word)
	}
	return encoded
}
