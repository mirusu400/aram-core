//go:build amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x

package interpreter

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// These golden encodings use full 64-bit host pointers. Primitive AArch64
// encodings and the portable interpreter/Go JIT tests also run on 32-bit hosts.

func TestARM64InterruptPollEncoding(t *testing.T) {
	e := &arm64emitter{interruptLines: 0x1234567890abcdef}
	e.prologue()
	e.interruptPoll(0x1234, 5)
	compareWords(t, "interrupt-poll", e.code(), words(
		0xAA0003E9, 0xAA0103EA, // x9 = regs, x10 = remain
		0xD299BDED, 0xF2B2156D, 0xF2CACF0D, 0xF2E2468D, // x13 = &interruptLines
		0x88DFFDA1, // ldar w1,[x13]
		0x34000141, // cbz  w1,continue
		0xB9404122, // ldr  w2,[x9,#64] (CPSR)
		0x36080041, // tbz  w1,#1,irq_test
		0x36300062, // tbz  w2,#6,service
		0x360000C1, // tbz  w1,#0,continue
		0x373800A2, // tbnz w2,#7,continue
		0x52824680, // movz w0,#0x1234 (boundary PC)
		0xB9003D20, // str  w0,[x9,#60]
		0x5280A080, // movz w0,#0x504 (IRQ status, 5 retired)
		0xD65F03C0, // ret
	))
}

// TestARM64MemoryEncodings pins the whole emitted software-TLB sequence, not
// just its primitives: the branch displacements over the bail stub, which
// half-table a store probes, the widths and sign extensions, and the register
// slots the address and result come from. Every word below was disassembled
// with `go tool objdump` on a GOARCH=arm64 object built from these exact bytes
// and read back instruction by instruction, which is the closest thing to
// executing it that the amd64 development host allows.
//
// This matters more than the amd64 equivalent: darwin/arm64 registers the
// native backend by default, and nothing on this host can run its code, so a
// wrong word would ship silently.
func TestARM64MemoryEncodings(t *testing.T) {
	const tlb = 0x1234567890AB0000
	// X11 and X12 are the read and write half-tables; the write half sits
	// tlbWriteOffset (0x40000 at 16384 entries) above the read half.
	prologue := words(
		0xAA0003E9, 0xAA0103EA,
		0xD280000B, 0xF2B2156B, 0xF2CACF0B, 0xF2E2468B, // movz/movk x11, tlb
		0xD280000C, 0xF2B215EC, 0xF2CACF0C, 0xF2E2468C, // movz/movk x12, tlb+0x40000
	)
	single := []struct {
		name string
		m    memAccess
		body []uint32
	}{
		{
			// ldr w0,[r1+r2] -> regs[3]
			name: "ldr-register-offset",
			m:    memAccess{size: 4, rd: 3, base: 1, index: 2, hasIndex: true},
			body: []uint32{
				0xB9400520, // ldr  w0,[x9,#4]      (base register)
				0xB9400921, // ldr  w1,[x9,#8]      (index register)
				0x0B010000, // add  w0,w0,w1
				0x530A7C01, // lsr  w1,w0,#10       (guest subpage)
				0x12003022, // and  w2,w1,#0x1fff   (table index)
				0x8B021563, // add  x3,x11,x2,lsl#5 (READ half entry)
				0xB9400064, // ldr  w4,[x3]         (way 0 tag)
				0x6B01009F, // cmp  w4,w1
				0x540000A0, // b.eq found           (+5 words)
				0xB9401064, // ldr  w4,[x3,#16]     (way 1 tag)
				0x6B01009F, // cmp  w4,w1
				0x54000121, // b.ne bail            (+9 words)
				0x91004063, // add  x3,x3,#16       (way 1 entry)
				0x12002402, // and  w2,w0,#0x3ff    (in-page offset)
				0x710FF05F, // cmp  w2,#1020
				0x540000A8, // b.hi bail            (+5 words)
				0xF9400463, // ldr  x3,[x3,#8]      (host page)
				0xB8626860, // ldr  w0,[x3,x2]
				0xB9000D20, // str  w0,[x9,#12]     (destination register)
				0x14000005, // b    done            (over the bail stub)
				0x52824680, // movz w0,#0x1234      (bail: the faulting PC)
				0xB9003D20, // str  w0,[x9,#60]     (regs[PC])
				0x5280A060, // movz w0,#0x503       (bail status, retired=5)
				0xD65F03C0, // ret
			},
		},
		{
			// strh regs[0] -> [r5+#12], probing the write half
			name: "strh-immediate-offset",
			m:    memAccess{store: true, size: 2, rd: 0, base: 5, offset: 12},
			body: []uint32{
				0xB9401520, // ldr  w0,[x9,#20]
				0x11003000, // add  w0,w0,#12
				0x530A7C01, // lsr  w1,w0,#10
				0x12003022, // and  w2,w1,#0x1fff
				0x8B021583, // add  x3,x12,x2,lsl#5 (WRITE half entry)
				0xB9400064, // ldr  w4,[x3]         (way 0 tag)
				0x6B01009F, // cmp  w4,w1
				0x540000A0, // b.eq found           (+5 words)
				0xB9401064, // ldr  w4,[x3,#16]     (way 1 tag)
				0x6B01009F, // cmp  w4,w1
				0x54000121, // b.ne bail            (+9 words)
				0x91004063, // add  x3,x3,#16       (way 1 entry)
				0x12002402, // and  w2,w0,#0x3ff
				0x710FF85F, // cmp  w2,#1022        (2-byte access)
				0x540000A8, // b.hi bail
				0xF9400463, // ldr  x3,[x3,#8]
				0xB9400124, // ldr  w4,[x9]         (value register)
				0x78226864, // strh w4,[x3,x2]
				0x14000005, // b    done
				0x52824680, 0xB9003D20, 0x5280A060, 0xD65F03C0,
			},
		},
		{
			// ldrsb regs[6] -> regs[7]; a byte access needs no crossing check
			name: "ldrsb-no-crossing-check",
			m:    memAccess{size: 1, signed: true, rd: 7, base: 6},
			body: []uint32{
				0xB9401920, // ldr  w0,[x9,#24]
				0x530A7C01, // lsr  w1,w0,#10
				0x12003022, // and  w2,w1,#0x1fff
				0x8B021563, // add  x3,x11,x2,lsl#5
				0xB9400064, // ldr  w4,[x3]         (way 0 tag)
				0x6B01009F, // cmp  w4,w1
				0x540000A0, // b.eq found           (+5 words)
				0xB9401064, // ldr  w4,[x3,#16]     (way 1 tag)
				0x6B01009F, // cmp  w4,w1
				0x540000E1, // b.ne bail            (+7 words)
				0x91004063, // add  x3,x3,#16       (way 1 entry)
				0x12002402, // and  w2,w0,#0x3ff
				0xF9400463, // ldr  x3,[x3,#8]
				0x38A26860, // ldrsb w0,[x3,x2]
				0xB9001D20, // str  w0,[x9,#28]
				0x14000005, // b    done
				0x52824680, 0xB9003D20, 0x5280A060, 0xD65F03C0,
			},
		},
	}
	for _, c := range single {
		got := emit(func(e *arm64emitter) {
			e.tlb = tlb
			e.prologue()
			e.memory(c.m, 0x1234, 5)
		})
		compareWords(t, c.name, got, append(append([]byte{}, prologue...), words(c.body...)...))
	}

	// PUSH {r0, r2, lr}: one probe covering all three words, a range check on
	// the whole span, then ascending stores and the pre-decremented writeback.
	push := []uint32{
		0xB9403520, // ldr  w0,[x9,#52]     (SP)
		0x51003000, // sub  w0,w0,#12       (3 words below SP)
		0x530A7C01, // lsr  w1,w0,#10
		0x12003022, // and  w2,w1,#0x1fff
		0x8B021583, // add  x3,x12,x2,lsl#5 (WRITE half entry)
		0xB9400064, // ldr  w4,[x3]         (way 0 tag)
		0x6B01009F, // cmp  w4,w1
		0x540000A0, // b.eq found           (+5 words)
		0xB9401064, // ldr  w4,[x3,#16]     (way 1 tag)
		0x6B01009F, // cmp  w4,w1
		0x540001E1, // b.ne bail            (+15 words)
		0x91004063, // add  x3,x3,#16       (way 1 entry)
		0x12002402, // and  w2,w0,#0x3ff
		0x710FD05F, // cmp  w2,#1012        (1024 - 12: the WHOLE list)
		0x54000168, // b.hi bail            (+11 words)
		0xF9400463, // ldr  x3,[x3,#8]      (host page)
		0x8B020063, // add  x3,x3,x2        (host page + in-page offset)
		0xB9400124, // ldr  w4,[x9]         (regs[0])
		0xB9000064, // str  w4,[x3]
		0xB9400924, // ldr  w4,[x9,#8]      (regs[2])
		0xB9000464, // str  w4,[x3,#4]
		0xB9403924, // ldr  w4,[x9,#56]     (regs[14] = LR)
		0xB9000864, // str  w4,[x3,#8]
		0xB9003520, // str  w0,[x9,#52]     (SP = the decremented base)
		0x14000005, // b    done
		0x52824680, 0xB9003D20, 0x5280A060, 0xD65F03C0,
	}
	got := emit(func(e *arm64emitter) {
		e.tlb = tlb
		e.prologue()
		e.multi(multiAccess{
			store: true, regs: []uint32{0, 2, cpu.RegisterLR},
			base: cpu.RegisterSP, startOffset: -12, writeback: true,
		}, 0x1234, 5)
	})
	compareWords(t, "push", got, append(append([]byte{}, prologue...), words(push...)...))
}
