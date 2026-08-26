package interpreter

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// The AArch64 emitter only executes on arm64, but it is pure byte generation, so
// its encodings are validated here on the dev host. Every expected word was
// produced by a real assembler (clang --target=aarch64-linux-gnu) and pasted in;
// this test guards the Go encoders against drift from those ground-truth bytes.

func emit(fn func(e *arm64emitter)) []byte {
	var e arm64emitter
	fn(&e)
	return e.buf
}

func words(ws ...uint32) []byte {
	out := make([]byte, 0, len(ws)*4)
	for _, w := range ws {
		out = append(out, byte(w), byte(w>>8), byte(w>>16), byte(w>>24))
	}
	return out
}

func TestARM64PrimitiveEncodings(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"prologue", emit(func(e *arm64emitter) { e.prologue() }), words(0xAA0003E9, 0xAA0103EA)},
		{"movz", emit(func(e *arm64emitter) { e.movz(2, 0x1234) }), words(0x52824682)},
		{"movk16", emit(func(e *arm64emitter) { e.movk16(2, 0xABCD) }), words(0x72B579A2)},
		{"ldrW", emit(func(e *arm64emitter) { e.ldrW(3, 3) }), words(0xB9400D23)},
		{"strW", emit(func(e *arm64emitter) { e.strW(3, cpu.RegisterCPSR) }), words(0xB9004123)},
		{"addImm12", emit(func(e *arm64emitter) { e.addImm12(0, 0, 4) }), words(0x11001000)},
		{"subImm12", emit(func(e *arm64emitter) { e.subImm12(13, 13, 8) }), words(0x510021AD)},
		{"addsImm12", emit(func(e *arm64emitter) { e.addsImm12(0, 0, 0xFF) }), words(0x3103FC00)},
		{"subsImm12(cmp)", emit(func(e *arm64emitter) { e.subsImm12(31, 1, 1) }), words(0x7100043F)},
		{"addsReg", emit(func(e *arm64emitter) { e.addsReg(0, 1, 2) }), words(0x2B020020)},
		{"subsReg", emit(func(e *arm64emitter) { e.subsReg(0, 1, 2) }), words(0x6B020020)},
		{"negs", emit(func(e *arm64emitter) { e.subsReg(0, a64WZR, 2) }), words(0x6B0203E0)},
		{"andReg", emit(func(e *arm64emitter) { e.andReg(0, 1, 2) }), words(0x0A020020)},
		{"orrReg", emit(func(e *arm64emitter) { e.orrReg(0, 1, 2) }), words(0x2A020020)},
		{"eorReg", emit(func(e *arm64emitter) { e.eorReg(0, 1, 2) }), words(0x4A020020)},
		{"bicReg", emit(func(e *arm64emitter) { e.bicReg(0, 1, 2) }), words(0x0A220020)},
		{"mvn", emit(func(e *arm64emitter) { e.mvn(0, 2) }), words(0x2A2203E0)},
		{"mul", emit(func(e *arm64emitter) { e.mul(0, 1, 2) }), words(0x1B027C20)},
		{"lslI#5", emit(func(e *arm64emitter) { e.lslI(0, 1, 5) }), words(0x531B6820)},
		{"lslI#29", emit(func(e *arm64emitter) { e.lslI(1, 3, 29) }), words(0x53030861)},
		{"lslI#30", emit(func(e *arm64emitter) { e.lslI(1, 1, 30) }), words(0x53020421)},
		{"lsrI#5", emit(func(e *arm64emitter) { e.lsrI(0, 1, 5) }), words(0x53057C20)},
		{"lsrI#31", emit(func(e *arm64emitter) { e.lsrI(3, 0, 31) }), words(0x531F7C03)},
		{"lsrI#0", emit(func(e *arm64emitter) { e.lsrI(3, 0, 0) }), words(0x53007C03)},
		{"asrI#5", emit(func(e *arm64emitter) { e.asrI(0, 1, 5) }), words(0x13057C20)},
		{"asrI#31", emit(func(e *arm64emitter) { e.asrI(0, 0, 31) }), words(0x131F7C00)},
		{"csetEQ", emit(func(e *arm64emitter) { e.csetEQ(2) }), words(0x1A9F17E2)},
		{"csel-ne", emit(func(e *arm64emitter) { e.csel(0, 2, 3, 1) }), words(0x1A831040)},
		{"cmp0", emit(func(e *arm64emitter) { e.cmp0(0) }), words(0x7100001F)},
		{"mrsNZCV", emit(func(e *arm64emitter) { e.mrsNZCV(1) }), words(0xD53B4201)},
		{"msrNZCV", emit(func(e *arm64emitter) { e.msrNZCV(1) }), words(0xD51B4201)},
		{"ret", emit(func(e *arm64emitter) { e.ret() }), words(0xD65F03C0)},
		{"maskClearNZ", emit(func(e *arm64emitter) { e.andMask(2, 2, a64MaskClearNZ) }), words(0x12007442)},
		{"maskClearNZC", emit(func(e *arm64emitter) { e.andMask(2, 2, a64MaskClearNZC) }), words(0x12007042)},
		{"maskClearNZCV", emit(func(e *arm64emitter) { e.andMask(2, 2, a64MaskClearNZCV) }), words(0x12006C42)},
		{"maskN", emit(func(e *arm64emitter) { e.andMask(1, 0, a64MaskN) }), words(0x12010001)},
		{"maskTop4", emit(func(e *arm64emitter) { e.andMask(1, 1, a64MaskTop4) }), words(0x12040C21)},
		{"mask1", emit(func(e *arm64emitter) { e.andMask(3, 3, a64Mask1) }), words(0x12000063)},
		// loadConst: single MOVZ when it fits in 16 bits, MOVZ+MOVK otherwise.
		{"loadConst16", emit(func(e *arm64emitter) { e.loadConst(0, 0xBEEF) }), words(0x5297DDE0)},
		{"loadConst32", emit(func(e *arm64emitter) { e.loadConst(0, 0xDEADBEEF) }), words(0x5297DDE0, 0x72BBD5A0)},
		// Software-TLB primitives (native_tlb.go). Ground truth from the Go
		// assembler for GOARCH=arm64, disassembled with `go tool objdump`.
		{"loadConst64", emit(func(e *arm64emitter) { e.loadConst64(11, 0x1234567890ABCDEF) }),
			words(0xD299BDEB, 0xF2B2156B, 0xF2CACF0B, 0xF2E2468B)},
		{"addReg", emit(func(e *arm64emitter) { e.addReg(0, 0, 1) }), words(0x0B010000)},
		{"addLSL64", emit(func(e *arm64emitter) { e.addLSL64(3, 11, 2, 4) }), words(0x8B021163)},
		{"ldrWoff#0", emit(func(e *arm64emitter) { e.ldrWoff(4, 3, 0) }), words(0xB9400064)},
		{"ldarW", emit(func(e *arm64emitter) { e.ldarW(1, 8) }), words(0x88DFFD01)},
		{"ldrWoff#4096", emit(func(e *arm64emitter) { e.ldrWoff(4, 3, 4096) }), words(0xB9500064)},
		{"ldrXoff#8", emit(func(e *arm64emitter) { e.ldrXoff(3, 3, 8) }), words(0xF9400463)},
		{"ldrXoff#4104", emit(func(e *arm64emitter) { e.ldrXoff(3, 3, 4104) }), words(0xF9480463)},
		{"maskTLBIndex", emit(func(e *arm64emitter) { e.andMask(2, 1, a64MaskTLBIndex) }), words(0x12002C22)},
		{"maskPageOff", emit(func(e *arm64emitter) { e.andMask(2, 0, a64MaskPageOff) }), words(0x12002C02)},
		{"lsrI#12", emit(func(e *arm64emitter) { e.lsrI(1, 0, 12) }), words(0x530C7C01)},
		{"cmpW-reg", emit(func(e *arm64emitter) { e.subsReg(a64WZR, 4, 1) }), words(0x6B01009F)},
		{"cmpW#4092", emit(func(e *arm64emitter) { e.subsImm12(a64WZR, 2, 4092) }), words(0x713FF05F)},
		{"cmpW#4094", emit(func(e *arm64emitter) { e.subsImm12(a64WZR, 2, 4094) }), words(0x713FF85F)},
	}
	for _, c := range cases {
		if len(c.got) != len(c.want) {
			t.Errorf("%s: length %d, want %d (bytes % x)", c.name, len(c.got), len(c.want), c.got)
			continue
		}
		for i := range c.want {
			if c.got[i] != c.want[i] {
				t.Errorf("%s: byte %d = 0x%02x, want 0x%02x (got % x, want % x)",
					c.name, i, c.got[i], c.want[i], c.got, c.want)
				break
			}
		}
	}
}

func TestARM64InterruptPollEncoding(t *testing.T) {
	e := &arm64emitter{interruptLines: 0x1234567890abcdef}
	e.prologue()
	e.interruptPoll(0x1234, 5)
	compareWords(t, "interrupt-poll", e.code(), words(
		0xAA0003E9, 0xAA0103EA, // x9 = regs, x10 = remain
		0xD299BDED, 0xF2B2156D, 0xF2CACF0D, 0xF2E2468D, // x13 = &interruptLines
		0x88DFFDA1, // ldar w1,[x13]
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
	// tlbWriteOffset (0x10000 at 4096 entries) above the read half.
	prologue := words(
		0xAA0003E9, 0xAA0103EA,
		0xD280000B, 0xF2B2156B, 0xF2CACF0B, 0xF2E2468B, // movz/movk x11, tlb
		0xD280000C, 0xF2B2158C, 0xF2CACF0C, 0xF2E2468C, // movz/movk x12, tlb+0x10000
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
				0x530C7C01, // lsr  w1,w0,#12       (guest page)
				0x12002C22, // and  w2,w1,#0xfff    (table index)
				0x8B021163, // add  x3,x11,x2,lsl#4 (READ half entry)
				0xB9400064, // ldr  w4,[x3]         (entry.tag)
				0x6B01009F, // cmp  w4,w1
				0x54000101, // b.ne bail            (+8 words)
				0x12002C02, // and  w2,w0,#0xfff    (in-page offset)
				0x713FF05F, // cmp  w2,#4092
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
				0x530C7C01, // lsr  w1,w0,#12
				0x12002C22, // and  w2,w1,#0xfff
				0x8B021183, // add  x3,x12,x2,lsl#4 (WRITE half entry)
				0xB9400064, // ldr  w4,[x3]
				0x6B01009F, // cmp  w4,w1
				0x54000101, // b.ne bail
				0x12002C02, // and  w2,w0,#0xfff
				0x713FF85F, // cmp  w2,#4094        (2-byte access)
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
				0x530C7C01, // lsr  w1,w0,#12
				0x12002C22, // and  w2,w1,#0xfff
				0x8B021163, // add  x3,x11,x2,lsl#4
				0xB9400064, // ldr  w4,[x3]
				0x6B01009F, // cmp  w4,w1
				0x540000C1, // b.ne bail            (+6 words: one check fewer)
				0x12002C02, // and  w2,w0,#0xfff
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
		0x530C7C01, // lsr  w1,w0,#12
		0x12002C22, // and  w2,w1,#0xfff
		0x8B021183, // add  x3,x12,x2,lsl#4 (WRITE half entry)
		0xB9400064, // ldr  w4,[x3]
		0x6B01009F, // cmp  w4,w1
		0x540001C1, // b.ne bail            (+14 words)
		0x12002C02, // and  w2,w0,#0xfff
		0x713FD05F, // cmp  w2,#4084        (4096 - 12: the WHOLE list)
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
			base: cpu.RegisterSP, preDec: true, writeback: true,
		}, 0x1234, 5)
	})
	compareWords(t, "push", got, append(append([]byte{}, prologue...), words(push...)...))
}

func compareWords(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d bytes, want %d (got % x)", name, len(got), len(want), got)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: byte %d = 0x%02x, want 0x%02x (got % x, want % x)",
				name, i, got[i], want[i], got, want)
			return
		}
	}
}
