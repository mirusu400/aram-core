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
		// Register-count shifts and carry-in ALU ops (Go assembler ground truth).
		{"regshift/lslVX", emit(func(e *arm64emitter) { e.lslVX(0, 0, 1) }), words(0x9AC12000)},
		{"regshift/lsrVX", emit(func(e *arm64emitter) { e.lsrVX(0, 0, 1) }), words(0x9AC12400)},
		{"regshift/asrVX", emit(func(e *arm64emitter) { e.asrVX(0, 0, 1) }), words(0x9AC12800)},
		{"regshift/lslX32", emit(func(e *arm64emitter) { e.lslX32(0, 0) }), words(0xD3607C00)},
		{"regshift/lsrX32", emit(func(e *arm64emitter) { e.lsrX32(0, 0) }), words(0xD360FC00)},
		{"regshift/sxtw", emit(func(e *arm64emitter) { e.sxtw(0, 0) }), words(0x93407C00)},
		{"regshift/rorV", emit(func(e *arm64emitter) { e.rorV(0, 0, 1) }), words(0x1AC12C00)},
		{"regshift/cselEQ", emit(func(e *arm64emitter) { e.csel(3, 4, 3, a64CondEQ) }), words(0x1A830083)},
		{"regshift/cselHI", emit(func(e *arm64emitter) { e.csel(1, 2, 1, a64CondHI) }), words(0x1A818041)},
		{"regshift/cmpReg", emit(func(e *arm64emitter) { e.subsReg(a64WZR, 1, 2) }), words(0x6B02003F)},
		{"regshift/andByte", emit(func(e *arm64emitter) { e.andMask(1, 1, a64MaskByte) }), words(0x12001C21)},
		{"regshift/lsrI#29", emit(func(e *arm64emitter) { e.lsrI(4, 4, 29) }), words(0x531D7C84)},
		{"regshift/msrNZCV", emit(func(e *arm64emitter) { e.msrNZCV(1) }), words(0xD51B4201)},
		{"regshift/adcsReg", emit(func(e *arm64emitter) { e.adcsReg(0, 0, 1) }), words(0x3A010000)},
		{"regshift/sbcsReg", emit(func(e *arm64emitter) { e.sbcsReg(0, 0, 1) }), words(0x7A010000)},
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
		{"ldarX", emit(func(e *arm64emitter) { e.ldarX(0, 0) }), words(0xC8DFFC00)},
		{"br", emit(func(e *arm64emitter) { e.br(0) }), words(0xD61F0000)},
		{"ldrWoff#4096", emit(func(e *arm64emitter) { e.ldrWoff(4, 3, 4096) }), words(0xB9500064)},
		{"ldrXoff#8", emit(func(e *arm64emitter) { e.ldrXoff(3, 3, 8) }), words(0xF9400463)},
		{"ldrXoff#4104", emit(func(e *arm64emitter) { e.ldrXoff(3, 3, 4104) }), words(0xF9480463)},
		{"maskTLBSet", emit(func(e *arm64emitter) { e.andMask(2, 1, a64MaskTLBSet) }), words(0x12003022)},
		{"addLSL64#5", emit(func(e *arm64emitter) { e.addLSL64(3, 11, 2, 5) }), words(0x8B021563)},
		{"ldrWoff#16", emit(func(e *arm64emitter) { e.ldrWoff(4, 3, 16) }), words(0xB9401064)},
		{"addImm12X#16", emit(func(e *arm64emitter) { e.addImm12X(3, 3, 16) }), words(0x91004063)},
		{"bCondEQ+20", emit(func(e *arm64emitter) { e.bCond(a64CondEQ, 20) }), words(0x540000A0)},
		{"maskPageOff", emit(func(e *arm64emitter) { e.andMask(2, 0, a64MaskPageOff) }), words(0x12002402)},
		{"lsrI#12", emit(func(e *arm64emitter) { e.lsrI(1, 0, 12) }), words(0x530C7C01)},
		{"cmpW-reg", emit(func(e *arm64emitter) { e.subsReg(a64WZR, 4, 1) }), words(0x6B01009F)},
		{"cmpW#4092", emit(func(e *arm64emitter) { e.subsImm12(a64WZR, 2, 4092) }), words(0x713FF05F)},
		{"cmpW#4094", emit(func(e *arm64emitter) { e.subsImm12(a64WZR, 2, 4094) }), words(0x713FF85F)},
		// ARM shifter-operand and carry-arithmetic primitives. Same ground
		// truth: assembled as WORDs for GOARCH=arm64 and read back with
		// `go tool objdump`, which prints RORW/LSLW/ADCSW/CSELW for these.
		{"rorI#5", emit(func(e *arm64emitter) { e.rorI(3, 3, 5) }), words(0x13831463)},
		{"rorI#31", emit(func(e *arm64emitter) { e.rorI(1, 1, 31) }), words(0x13817C21)},
		{"lslV", emit(func(e *arm64emitter) { e.lslV(3, 3, 4) }), words(0x1AC42063)},
		{"lsrV", emit(func(e *arm64emitter) { e.lsrV(3, 3, 4) }), words(0x1AC42463)},
		{"asrV", emit(func(e *arm64emitter) { e.asrV(3, 3, 4) }), words(0x1AC42863)},
		{"rorV", emit(func(e *arm64emitter) { e.rorV(3, 3, 4) }), words(0x1AC42C63)},
		{"adcsReg", emit(func(e *arm64emitter) { e.adcsReg(0, 0, 1) }), words(0x3A010000)},
		{"sbcsReg", emit(func(e *arm64emitter) { e.sbcsReg(0, 0, 1) }), words(0x7A010000)},
		{"maskByte", emit(func(e *arm64emitter) { e.andMask(4, 4, a64MaskByte) }), words(0x12001C84)},
		// csel with condition 3 (LO/CC) is what selects the shifted value only
		// when a register-specified shift count is below 32.
		{"csel-lo-zero", emit(func(e *arm64emitter) { e.csel(3, 3, a64WZR, 3) }), words(0x1A9F3063)},
		{"csel-lo-reg", emit(func(e *arm64emitter) { e.csel(3, 3, 5, 3) }), words(0x1A853063)},
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
