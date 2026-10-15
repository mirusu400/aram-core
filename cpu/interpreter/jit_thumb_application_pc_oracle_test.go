package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

func newThumbPCOraclePair(t *testing.T, codeSize uint32, traced bool) (*Backend, *Backend) {
	t.Helper()
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, codeSize, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		resetThumbApplicationChainOracle(t, b, traced)
		check(t, b.WriteRegister(cpu.RegisterSP, 0x2800))
	}
	return precise, jit
}

func thumbPCOracleBudgets(n uint64) []uint64 {
	budgets := make([]uint64, n)
	for i := range budgets {
		budgets[i] = uint64(i + 1)
	}
	return append(budgets, 254, 255, 256, 257, 258)
}

// A fetch/unsupported boundary terminates a block of thirty-two pure instructions.
// Refetch failure leaves PC at the end; an unsupported opcode advances PC by 2.
func TestThumbApplicationPCPureFallthroughMatchesPrecise(t *testing.T) {
	pure := []uint16{0x3401, 0x3502, 0x467a, 0x447a, 0xa301, 0xab01, 0xb001, 0xb081}
	for _, end := range []struct {
		name string
		raw  uint16
	}{
		{"unmapped", 0}, {"unsupported", 0xde00}, {"bkpt", 0xbe00},
	} {
		for _, traced := range []bool{false, true} {
			for _, budget := range thumbPCOracleBudgets(34) {
				t.Run(fmt.Sprintf("%s/trace=%t/budget=%d", end.name, traced, budget), func(t *testing.T) {
					words := make([]uint16, 32)
					for i := range words {
						words[i] = pure[i%len(pure)]
					}
					if end.raw != 0 {
						words = append(words, end.raw)
					}
					precise, jit := newThumbPCOraclePair(t, uint32(len(words)*2), traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if budget > 32 {
						wantPC := uint32(0x1042)
						if end.name == "unmapped" {
							wantPC = 0x1040
						}
						if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterPC); got != wantPC {
							t.Fatalf("end PC=%08x, want %08x", got, wantPC)
						}
					}
				})
			}
		}
	}
}

func TestThumbApplicationPCPureBatchBudgetsMatchPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		for _, budget := range append(thumbPCOracleBudgets(65), 511, 512, 513, 514) {
			t.Run(fmt.Sprintf("trace=%t/budget=%d", traced, budget), func(t *testing.T) {
				precise, jit := newThumbPCOraclePair(t, 0x402, traced)
				for _, b := range []*Backend{precise, jit} {
					words := make([]uint16, 512, 513)
					for i := range words {
						words[i] = 0x3001
					}
					check(t, b.WriteMemory(0x1000, thumbTestCode(append(words, 0xbe00)...)))
				}
				assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
			})
		}
	}
}

func TestThumbApplicationPCOperandsAndTargetsMatchPrecise(t *testing.T) {
	for _, fixture := range []struct {
		name string
		raw  uint16
	}{
		{"mov-source-pc", 0x467a}, {"add-source-pc", 0x447a}, {"adr", 0xa301},
		{"mov-destination-pc", 0x4687}, {"add-destination-pc", 0x4487},
		{"mov-pc-pc", 0x46ff}, {"add-pc-pc", 0x44ff},
		{"bx-pc", 0x4778}, {"blx-lr-alias", 0x47f0}, {"bx-arm", 0x4700},
		{"pop-pc-cold", 0xbd00}, {"pop-pc-warm", 0xbd00},
	} {
		for _, offset := range []uint32{0, 2} {
			for _, traced := range []bool{false, true} {
				for _, budget := range thumbPCOracleBudgets(8) {
					t.Run(fmt.Sprintf("%s/offset=%d/trace=%t/budget=%d", fixture.name, offset, traced, budget), func(t *testing.T) {
						precise, jit := newThumbPCOraclePair(t, 0x100, traced)
						start := uint32(0x1000) + offset
						for _, b := range []*Backend{precise, jit} {
							check(t, b.WriteMemory(start, thumbTestCode(0x3401, 0x3502, fixture.raw, 0xbe00, 0xbe00)))
							target := uint32(0x2000)
							arm := false
							check(t, b.WriteRegister(cpu.RegisterR0, 0x2001))
							check(t, b.WriteRegister(cpu.RegisterLR, 0x2001))
							switch fixture.name {
							case "add-destination-pc":
								check(t, b.WriteRegister(cpu.RegisterR0, 0x2001-(start+8)))
							case "mov-pc-pc":
								target = start + 8
							case "add-pc-pc":
								target = (start + 8) * 2
							case "bx-pc":
								target, arm = (start+8)&^3, true
							case "bx-arm":
								arm = true
								check(t, b.WriteRegister(cpu.RegisterR0, target))
							case "pop-pc-cold", "pop-pc-warm":
								check(t, b.WriteMemory(0x2800, armTestCode(0x2001)))
								if fixture.name == "pop-pc-warm" {
									_, err := b.read32(0x2800, cpu.PermissionRead)
									check(t, err)
								}
							}
							if arm {
								check(t, b.WriteMemory(target, armTestCode(0xe3a03007, 0xe1200070)))
							} else {
								check(t, b.WriteMemory(target, thumbTestCode(0xbe00)))
							}
						}
						assertThumbSMCOracleParity(t, precise, jit, start, budget)
					})
				}
			}
		}
	}
}

func TestThumbApplicationPCFaultBoundariesMatchPrecise(t *testing.T) {
	for _, fixture := range []struct {
		name string
		raw  uint16
		sp   uint32
	}{
		{"ldr-literal", 0x4800, 0x2800},
		{"str-register", 0x5088, 0x2800}, {"strh-register", 0x5288, 0x2800},
		{"strb-register", 0x5488, 0x2800}, {"ldrsb-register", 0x5688, 0x2800},
		{"ldr-register", 0x5888, 0x2800}, {"ldrh-register", 0x5a88, 0x2800},
		{"ldrb-register", 0x5c88, 0x2800}, {"ldrsh-register", 0x5e88, 0x2800},
		{"str-immediate", 0x6008, 0x2800}, {"ldr-immediate", 0x6808, 0x2800},
		{"strb-immediate", 0x7008, 0x2800}, {"ldrb-immediate", 0x7808, 0x2800},
		{"strh-immediate", 0x8008, 0x2800}, {"ldrh-immediate", 0x8808, 0x2800},
		{"str-stack", 0x9000, 0x4000}, {"ldr-stack", 0x9800, 0x4000},
		{"push", 0xb401, 0x4004}, {"pop", 0xbc01, 0x4000},
		{"stm", 0xc101, 0x2800}, {"ldm", 0xc901, 0x2800},
	} {
		for _, traced := range []bool{false, true} {
			for _, budget := range thumbPCOracleBudgets(6) {
				t.Run(fmt.Sprintf("%s/trace=%t/budget=%d", fixture.name, traced, budget), func(t *testing.T) {
					precise, jit := newThumbPCOraclePair(t, 8, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(0x3401, 0x3502, fixture.raw, 0xbe00)))
						check(t, b.WriteRegister(cpu.RegisterR0, 0xfeedbeef))
						check(t, b.WriteRegister(cpu.RegisterR1, 0x4000))
						check(t, b.WriteRegister(cpu.RegisterR2, 0))
						check(t, b.WriteRegister(cpu.RegisterSP, fixture.sp))
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if budget >= 3 && immediateMemoryRegisterValue(t, jit, cpu.RegisterPC) != 0x1006 {
						t.Fatal("fault did not retain the memory instruction's pre-incremented PC")
					}
				})
			}
		}
	}
}

func TestThumbApplicationPCLiteralFaultAlignmentMatchesPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		for _, budget := range thumbPCOracleBudgets(5) {
			t.Run(fmt.Sprintf("trace=%t/budget=%d", traced, budget), func(t *testing.T) {
				precise, jit := newThumbPCOraclePair(t, 8, traced)
				for _, b := range []*Backend{precise, jit} {
					// LDR at 1006 reads aligned literal 1008, not pc+4=100a.
					check(t, b.WriteMemory(0x1002, thumbTestCode(0x3401, 0x3502, 0x4800)))
				}
				assertThumbSMCOracleParity(t, precise, jit, 0x1002, budget)
			})
		}
	}
}

func TestThumbApplicationPCPartialStoreSelfModifyFaultMatchesPrecise(t *testing.T) {
	for _, writer := range []uint16{0xc103, 0xb403} { // STMIA r1!,{r0,r1}; PUSH {r0,r1}.
		for _, traced := range []bool{false, true} {
			for _, budget := range thumbPCOracleBudgets(8) {
				t.Run(fmt.Sprintf("writer=%04x/trace=%t/budget=%d", writer, traced, budget), func(t *testing.T) {
					precise, jit := newThumbPCOraclePair(t, 12, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(0x3401, 0x3502, writer, 0x4642, 0x2301, 0xbe00)))
						check(t, b.WriteRegister(cpu.RegisterR0, 0xbe002302))
						check(t, b.WriteRegister(cpu.RegisterR1, 0x1008))
						check(t, b.WriteRegister(cpu.RegisterSP, 0x1010))
					}
					generation := jit.jitGen
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if budget >= 3 && jit.jitGen == generation {
						t.Fatal("first committed store did not invalidate code before the second fault")
					}
				})
			}
		}
	}
}

func TestThumbApplicationPCPartialReadFaultMatchesPrecise(t *testing.T) {
	for _, reader := range []uint16{0xc903, 0xbd01} { // LDMIA r1!,{r0,r1}; POP {r0,pc}.
		for _, traced := range []bool{false, true} {
			for _, budget := range thumbPCOracleBudgets(6) {
				t.Run(fmt.Sprintf("reader=%04x/trace=%t/budget=%d", reader, traced, budget), func(t *testing.T) {
					precise, jit := newThumbPCOraclePair(t, 8, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(0x3401, 0x3502, reader, 0xbe00)))
						check(t, b.WriteMemory(0x2ffc, armTestCode(0xfeedbeef)))
						check(t, b.WriteRegister(cpu.RegisterR1, 0x2ffc))
						check(t, b.WriteRegister(cpu.RegisterSP, 0x2ffc))
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if budget >= 3 && (immediateMemoryRegisterValue(t, jit, cpu.RegisterPC) != 0x1006 ||
						immediateMemoryRegisterValue(t, jit, cpu.RegisterR0) != 0xfeedbeef) {
						t.Fatal("second read fault lost the first loaded register or fault PC")
					}
				})
			}
		}
	}
}

func TestThumbApplicationPCBranchSuffixFaultMatchesPrecise(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		for _, traced := range []bool{false, true} {
			for _, budget := range thumbPCOracleBudgets(6) {
				t.Run(fmt.Sprintf("invalid=%t/trace=%t/budget=%d", invalid, traced, budget), func(t *testing.T) {
					words := []uint16{0x3401, 0x3502, 0xf000}
					if invalid {
						words = append(words, 0x2000)
					}
					precise, jit := newThumbPCOraclePair(t, uint32(len(words)*2), traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
						check(t, b.WriteRegister(cpu.RegisterLR, 0xdeadbeef))
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if budget >= 3 && (immediateMemoryRegisterValue(t, jit, cpu.RegisterPC) != 0x1006 ||
						immediateMemoryRegisterValue(t, jit, cpu.RegisterLR) != 0xdeadbeef) {
						t.Fatal("faulting BL prefix changed PC or LR")
					}
				})
			}
		}
	}
}

// A deterministic context cancels at the second Backend.Run batch poll, not at
// a wall-clock-dependent point inside either executor.
type thumbPCSecondPollContext struct{ polls int }

func (*thumbPCSecondPollContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*thumbPCSecondPollContext) Done() <-chan struct{}       { return nil }
func (*thumbPCSecondPollContext) Value(any) any               { return nil }
func (c *thumbPCSecondPollContext) Err() error {
	c.polls++
	if c.polls >= 2 {
		return context.Canceled
	}
	return nil
}

func TestThumbApplicationPCBatchCancellationMatchesPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		t.Run(fmt.Sprint(traced), func(t *testing.T) {
			precise, jit := newThumbPCOraclePair(t, 0x402, traced)
			for _, b := range []*Backend{precise, jit} {
				words := make([]uint16, 512, 513)
				for i := range words {
					words[i] = 0x3001
				}
				check(t, b.WriteMemory(0x1000, thumbTestCode(append(words, 0xbe00)...)))
			}
			want := precise.Run(&thumbPCSecondPollContext{}, 0x1000, cpu.ModeThumb, 513)
			got := jit.Run(&thumbPCSecondPollContext{}, 0x1000, cpu.ModeThumb, 513)
			if got.Reason != want.Reason || got.Instructions != 256 || got.PC != 0x1200 || !sameError(got.Err, want.Err) {
				t.Fatalf("cancellation result=%+v want=%+v", got, want)
			}
			wantState, err := precise.SaveContext()
			check(t, err)
			gotState, err := jit.SaveContext()
			check(t, err)
			if !bytes.Equal(gotState, wantState) || !slices.Equal(jit.PCHistory(), precise.PCHistory()) || !reflect.DeepEqual(jit.pcHits, precise.pcHits) {
				t.Fatal("canceled batch changed architectural context or instruction trace")
			}
		})
	}
}
