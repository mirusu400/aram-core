package interpreter

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Complete frozen original function; only the declaration identifier differs.
// Source SHA256: ffbda39910bc213b215bc03678d53f6bb8ebc700750d46a88afb56494a2377b1.
func (b *Backend) executeARMMicroSingleTransferFrozenWordOld(
	instruction, pc uint32,
) (bool, *cpu.StopReason, error) {
	preIndex := instruction&(1<<24) != 0
	up := instruction&(1<<23) != 0
	byteTransfer := instruction&(1<<22) != 0
	writeBack := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	rd := instruction >> 12 & 0xf
	offset := b.armSingleTransferOffset(instruction, pc)
	base := b.readOperandRegister(rn, pc, cpu.ModeARM)
	indexedAddress := base
	if up {
		indexedAddress += offset
	} else {
		indexedAddress -= offset
	}
	address := base
	if preIndex {
		address = indexedAddress
	}
	branched := false
	if load {
		if byteTransfer {
			value, err := b.read8(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			if rd == cpu.RegisterPC {
				b.branchExchange(uint32(value))
				branched = true
			} else {
				b.regs[rd] = uint32(value)
			}
		} else {
			value, err := b.read32(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			if rd == cpu.RegisterPC {
				b.branchExchange(value)
				branched = true
			} else {
				b.regs[rd] = value
			}
		}
	} else {
		value := b.regs[rd]
		if rd == cpu.RegisterPC {
			value = pc + 12
		}
		if byteTransfer {
			if err := b.write8(address, byte(value), cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
		} else if err := b.write32(address, value, cpu.PermissionWrite); err != nil {
			return false, nil, err
		}
	}
	if (!preIndex || writeBack) && !(load && rd == rn) {
		b.regs[rn] = indexedAddress
	}
	return branched, nil, nil
}

func TestARMCachedWordLoadMatchesFrozenOld(t *testing.T) {
	type fixture struct {
		name                      string
		address                   uint32
		permissions               cpu.Permissions
		warm, alignment, physical bool
		rn, rd                    uint32
		value                     uint32
	}
	fixtures := []fixture{
		{"warm_aligned", 0x2000, 3, true, false, false, 1, 0, 0x89abcdef},
		{"cold_aligned", 0x2000, 3, false, false, false, 1, 0, 0x89abcdef},
		{"lane1_rotate", 0x2001, 3, true, false, false, 1, 0, 0x89abcdef},
		{"lane2_rotate", 0x2002, 3, true, false, false, 1, 0, 0x89abcdef},
		{"lane3_rotate", 0x2003, 3, true, false, false, 1, 0, 0x89abcdef},
		{"last_region_word", 0x20fc, 3, true, false, false, 1, 0, 0x89abcdef},
		{"before_region_fault", 0x1ffc, 3, true, false, false, 1, 0, 0x89abcdef},
		{"after_region_fault", 0x2100, 3, true, false, false, 1, 0, 0x89abcdef},
		{"read_only", 0x2000, cpu.PermissionRead, true, false, false, 1, 0, 0x89abcdef},
		{"write_only_fault", 0x2000, cpu.PermissionWrite, true, false, false, 1, 0, 0x89abcdef},
		{"unaligned_checked_boundary", 0x20fe, 3, true, true, false, 1, 0, 0x89abcdef},
		{"top_word", 0xfffffffc, 3, true, false, false, 1, 0, 0x89abcdef},
		{"top_lane_rotation", 0xffffffff, 3, true, false, false, 1, 0, 0x89abcdef},
		{"pc_thumb_exchange", 0x2000, 3, true, false, false, 1, cpu.RegisterPC, 0x3001},
		{"pc_arm_exchange", 0x2000, 3, true, false, false, 1, cpu.RegisterPC, 0x3000},
		{"base_destination_alias", 0x2000, 3, true, false, false, 0, 0, 0x89abcdef},
		{"pc_base_literal", 0x1008, 3, true, false, false, cpu.RegisterPC, 0, 0x89abcdef},
		{"physical_fallback", 0x2000, 3, true, false, true, 1, 0, 0x89abcdef},
	}
	for _, f := range fixtures {
		for _, controls := range []uint32{0, 1 << 21, 1 << 23, 1 << 24, 1<<24 | 1<<21 | 1<<23} {
			for _, offset := range []uint32{0, 4} {
				t.Run(fmt.Sprintf("%s/controls_%x/offset_%x", f.name, controls, offset), func(t *testing.T) {
					got, want := NewJIT(), NewJIT()
					for _, b := range []*Backend{got, want} {
						t.Cleanup(func() { _ = b.Close() })
						check(t, b.Map(0x1000, 0x100, 7))
						check(t, b.Map(0x2000, 0x100, f.permissions))
						check(t, b.Map(0xfffffffc, 4, 3))
						for i := range b.regs {
							b.regs[i] = uint32(i) * 0x10203
						}
						b.mode = cpu.ModeARM
						b.regs[cpu.RegisterCPSR] = flagN | flagC | flagV | uint32(processorModeSystem)
						b.regs[cpu.RegisterPC] = 0x1004
						b.regs[f.rn] = f.address
						if f.rn == cpu.RegisterPC {
							b.regs[f.rn] = 0x1004
						}
						if f.alignment {
							b.cp15.control |= 1 << 1
						} else {
							b.cp15.control &^= 1 << 1
						}
						for i := range b.regions {
							region := &b.regions[i]
							for at := 0; at+4 <= len(region.data); at += 4 {
								binary.LittleEndian.PutUint32(region.data[at:], f.value)
							}
							if f.warm {
								// Deliberately include below/above bounds and missing read permission.
								// The entry still describes real Map-owned storage; these must miss.
								cacheBase := uint32(0x2000)
								if f.address >= 0xfffffffc {
									cacheBase = 0xfffffffc
								} else if f.rn == cpu.RegisterPC {
									cacheBase = 0x1000
								}
								if region.address == cacheBase {
									b.cacheData(region, f.address, cpu.PermissionRead)
								}
							}
						}
						b.physicalAccess = f.physical
					}
					if f.name == "warm_aligned" {
						for _, b := range []*Backend{got, want} {
							entry := &b.privateDataCache[privateDataCacheIndex(f.address, cpu.PermissionRead)]
							if value, hit := cachedJITWord(entry, f.address); !hit || value != f.value {
								t.Fatal("warm fixture did not admit the cached word")
							}
						}
					}
					raw := uint32(0xe4100000) | controls | offset | f.rn<<16 | f.rd<<12
					gb, gr, ge := got.executeARMMicroSingleTransfer(raw, 0x1000)
					wb, wr, we := want.executeARMMicroSingleTransferFrozenWordOld(raw, 0x1000)
					if gb != wb || !reflect.DeepEqual(gr, wr) || !sameError(ge, we) {
						t.Fatalf("outcome differs: %t/%v/%v vs %t/%v/%v", gb, gr, ge, wb, wr, we)
					}
					if got.regs != want.regs || got.mode != want.mode || !reflect.DeepEqual(got.cp15, want.cp15) ||
						!reflect.DeepEqual(got.regions, want.regions) || !reflect.DeepEqual(got.privateDataCache, want.privateDataCache) ||
						!reflect.DeepEqual(got.ExecutionStatistics(), want.ExecutionStatistics()) {
						t.Fatal("registers/mode/CP15/memory/cache/statistics differ")
					}
				})
			}
		}
	}
}
