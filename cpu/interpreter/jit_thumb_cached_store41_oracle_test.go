package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// These synthetic oracles contain no private corpus addresses or bytes.
func TestCachedStore41LiteralBytesBoundsAndPermissions(t *testing.T) {
	for _, width := range []int{1, 2, 4} {
		for permission := cpu.Permissions(0); permission < 8; permission++ {
			t.Run(fmt.Sprintf("width%d/perms%d", width, permission), func(t *testing.T) {
				for _, base := range []uint32{0x3000, 0xfffffff0} {
					for _, length := range []int{0, 1, 2, 3, 4, 7, 16} {
						for _, address := range []uint32{base - 1, base, base + 1, base + 2, base + 3, base + uint32(length) - 1, base + uint32(length), 0, 0xffffffff} {
							for _, value := range []uint32{0, 1, 0x7fffffff, 0x80000000, 0xffffffff, 0x01234567, 0x89abcdef} {
								backing := bytes.Repeat([]byte{0xd7}, length+8)
								want := bytes.Clone(backing)
								entry := dataRegionCache{address: base, perms: permission, data: backing[:length]}
								// Independent widened absolute bounds, not the helper's
								// wrapped subtraction or a call to privateDataHit.
								wantHit := permission&cpu.PermissionWrite != 0 && address >= base &&
									uint64(address)+uint64(width) <= uint64(base)+uint64(length) &&
									(width != 4 || address&3 == 0)
								wantPerms := cpu.Permissions(0)
								if wantHit {
									wantPerms = permission
									for lane := 0; lane < width; lane++ {
										want[int(uint64(address)-uint64(base))+lane] = byte(value >> (8 * lane))
									}
								}
								var gotPerms cpu.Permissions
								var gotHit bool
								switch width {
								case 1:
									gotPerms, gotHit = cachedStore8(&entry, address, byte(value))
								case 2:
									gotPerms, gotHit = cachedStore16(&entry, address, uint16(value))
								case 4:
									gotPerms, gotHit = cachedStore32(&entry, address, value)
								}
								if gotHit != wantHit || gotPerms != wantPerms || !bytes.Equal(backing, want) ||
									entry.address != base || entry.perms != permission || len(entry.data) != length || cap(entry.data) != len(backing) {
									t.Fatalf("base=%x len=%d address=%x value=%x got=(%d,%t) want=(%d,%t)", base, length, address, value, gotPerms, gotHit, wantPerms, wantHit)
								}
							}
						}
					}
				}
			})
		}
	}
	for _, data := range [][]byte{nil, make([]byte, 0, 16)} {
		entry := dataRegionCache{perms: cpu.PermissionWrite, data: data}
		if perms, hit := cachedStore8(&entry, 0, 1); hit || perms != 0 {
			t.Fatal("empty writable slot accepted")
		}
	}
	if reflect.TypeOf(thumbMicroInstr{}).Size() != 8 || reflect.TypeOf(jitThumbCompareBranchTail{}).Size() != 16 {
		t.Fatal("compact instruction/tail metadata changed")
	}
}

func TestCachedStore41HelpersAllocateZero(t *testing.T) {
	entry := dataRegionCache{address: 0x3000, perms: cpu.PermissionWrite, data: make([]byte, 8)}
	if got := testing.AllocsPerRun(100, func() {
		cachedStore8(&entry, 0x3000, 0x12)
		cachedStore16(&entry, 0x3001, 0x3456)
		cachedStore32(&entry, 0x3004, 0x789abcde)
	}); got != 0 {
		t.Fatalf("pure cached stores allocated %v times", got)
	}
}

func cachedStore41Pair(t *testing.T, raw uint16, address uint32, warm, traced bool) (*Backend, *Backend) {
	t.Helper()
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.Map(0x3000, 16, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, b.WriteMemory(0x1000, thumbTestCode(0x3401, raw, 0x3501, 0x2e00, 0xd1fa, 0xbe00)))
		check(t, b.WriteRegister(0, 0x89abcdef))
		check(t, b.WriteRegister(1, address))
		check(t, b.WriteRegister(2, 0))
		check(t, b.WriteRegister(4, 0x7fffffff))
		check(t, b.WriteRegister(5, 0))
		check(t, b.WriteRegister(6, 1))
		check(t, b.WriteRegister(cpu.RegisterSP, address))
		check(t, b.WriteRegister(cpu.RegisterCPSR, flagC|flagV|uint32(processorModeSystem)))
		b.clearDataCaches()
		if warm {
			mapped, _, err := b.findRegion(address, cpu.PermissionWrite)
			check(t, err)
			b.cacheData(mapped, address, cpu.PermissionWrite)
		}
		if traced {
			b.pcHits = make(map[uint32]uint64)
			check(t, b.SetPCHistoryLimit(32))
		}
	}
	block := jit.jitBlockAt(0x1000)
	if block == nil || len(block.thumb) != 5 || block.compareBranchTail == nil || block.extraLoop != nil || block.countedLoop != nil {
		t.Fatal("literal store loop did not form a full five-op application block")
	}
	if warm && address == 0x3000 {
		entry := &jit.privateDataCache[privateDataCacheIndex(address, cpu.PermissionWrite)]
		if entry.address != 0x3000 || len(entry.data) != 16 || entry.perms != cpu.PermissionRead|cpu.PermissionWrite ||
			jit.systemBus != nil || jit.physicalAccess || jit.tlb != nil {
			t.Fatal("positive cache/route admission was not established")
		}
	}
	return precise, jit
}

func TestCachedStore41WholeApplicationBudgetsMatchPrecise(t *testing.T) {
	for _, raw := range []uint16{0x5088, 0x5288, 0x5488, 0x6008, 0x7008, 0x8008, 0x9000} {
		for _, warm := range []bool{false, true} {
			for _, traced := range []bool{false, true} {
				for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 7, 255, 256, 257} {
					t.Run(fmt.Sprintf("%04x/warm%t/trace%t/budget%d", raw, warm, traced, budget), func(t *testing.T) {
						precise, jit := cachedStore41Pair(t, raw, 0x3000, warm, traced)
						// Existing independent public New vs NewJIT full-state,
						// mapped-byte and actual trace observer comparison.
						assertThumbScalarStoreParity(t, precise, jit, budget)
						if jit.regs[4] != uint32(0x7fffffff+uint32((budget+4)/5)) ||
							jit.regs[5] != uint32(budget/5)+map[bool]uint32{false: 0, true: 1}[budget%5 >= 3] {
							t.Fatal("literal prefix/after-store retirement changed")
						}
					})
				}
			}
		}
	}
}

func TestCachedStore41FailureAndSuccessRawNextCapture(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprint(fault), func(t *testing.T) {
			address := uint32(0x3000)
			wantFlags := uint32(0x2000003f) // CMP r6=1,#0; taken BNE.
			nextPC := uint32(0x1000)
			if fault {
				address, wantFlags, nextPC = 0x300f, 0x9000003f, 0x1004
			}
			precise, jit := cachedStore41Pair(t, 0x8008, address, true, false)
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 5)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 5)
			if got.Reason != want.Reason || got.Instructions != want.Instructions || got.PC != want.PC || !sameError(got.Err, want.Err) {
				t.Fatalf("first execution differs: got=%+v want=%+v", got, want)
			}
			if fault && (got.Reason != cpu.StopFault || got.Instructions != 1 || got.PC != 0x1004) {
				t.Fatal("partial STRH failure changed attempt/retirement/PC")
			}
			// No CPSR/SaveContext/materializing getter between these Runs.
			for _, b := range []*Backend{precise, jit} {
				check(t, b.SetPCRegisterCapture(nextPC, 1))
				b.Run(context.Background(), nextPC, cpu.ModeThumb, 1)
				captures := b.PCRegisterCaptures()
				if len(captures) != 1 || captures[0].Registers[cpu.RegisterCPSR] != wantFlags {
					t.Fatalf("literal raw next capture differs: %+v want CPSR=%08x", captures, wantFlags)
				}
			}
			if !reflect.DeepEqual(jit.PCRegisterCaptures(), precise.PCRegisterCaptures()) {
				t.Fatal("actual pre-instruction capture differs")
			}
		})
	}
}

func TestCachedStore41WarmSMCInvalidatesBeforeOldTail(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.WriteMemory(0x1000, thumbTestCode(0x3401, 0x8008, 0x2a00, 0xd102, 0x2301, 0xbe00, 0x46c0, 0x2302, 0xbe00)))
		check(t, b.WriteRegister(0, 0x2a01))
		check(t, b.WriteRegister(1, 0x1004))
		check(t, b.WriteRegister(2, 0))
		check(t, b.WriteRegister(4, 0x7fffffff))
		mapped, _, err := b.findRegion(0x1004, cpu.PermissionWrite)
		check(t, err)
		b.cacheData(mapped, 0x1004, cpu.PermissionWrite)
	}
	stale := jit.jitBlockAt(0x1000)
	if stale == nil || len(stale.thumb) != 4 || stale.compareBranchTail == nil {
		t.Fatal("SMC did not own a full block with old fused CMP/BNE tail")
	}
	generation := jit.jitGen
	assertThumbScalarStoreParity(t, precise, jit, 257)
	if jit.jitGen == generation || jit.jitBlocks[0x1000] == stale || jit.regs[3] != 2 {
		t.Fatal("warm cached STRH reused stale tail/branch after SMC")
	}
}
