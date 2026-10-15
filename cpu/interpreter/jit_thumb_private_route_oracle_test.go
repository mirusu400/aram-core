package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// UNCOMPILED PRIVATE DRAFT. Literal code is interpreted independently by New().
// Do not turn a fixture or compiler failure into a skip when execution is later
// authorized. Translation/profile counters naturally differ between New/JIT.

func routeHoist26Pair(t *testing.T, raw uint16, nonnilTLB, warm bool) (*Backend, *Backend, uint32, uint32) {
	t.Helper()
	precise, jit := New(), NewJIT()
	start, dataAddress := uint32(0x1000), uint32(0x4000)
	if raw == 0x4801 {
		start, dataAddress = 0x1ffc, 0x2004
	}
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.Map(0x4000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, b.WriteMemory(start, thumbTestCode(raw, 0xe7fd))) // Load; B start.
		check(t, b.WriteMemory(0x4000, []byte{0xfe, 0x80, 3, 4}))
		if raw == 0x4801 {
			// The literal is on another complete native page, not the code
			// page whose instruction fetch may already populate an Exec TLB.
			check(t, b.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
			check(t, b.WriteMemory(0x2004, []byte{0x12, 0x34, 0x56, 0x78}))
		}
		check(t, b.WriteRegister(cpu.RegisterR1, 0x4000))
		check(t, b.WriteRegister(cpu.RegisterR2, 0))
		check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
		check(t, b.WriteRegister(cpu.RegisterSP, 0x4000))
		if warm {
			_, err := b.read32(dataAddress, cpu.PermissionRead)
			check(t, err)
		}
		// Allocate AFTER private-cache warming. A mistaken cached read would
		// return the right value but leave this new native TLB cold.
		if nonnilTLB {
			b.tlb = make([]tlbEntry, nativeTLBEntries*2)
			b.tlbClear()
			if b.tlbHit(dataAddress, cpu.PermissionRead) {
				t.Fatal("native-TLB positive control was already warm")
			}
		}
	}
	block := jit.jitBlockAt(start)
	if block == nil || len(block.thumb) != 2 || block.extraLoop != nil || block.countedLoop != nil || block.compareBranchTail != nil {
		t.Fatal("literal load loop is not an ordinary full-fit two-op block")
	}
	if jit.systemBus != nil || jit.physicalAccess || jit.tracing() {
		t.Fatal("literal load loop does not satisfy application admission")
	}
	return precise, jit, start, dataAddress
}

func routeHoist26AssertRun(t *testing.T, precise, jit *Backend, start uint32, mode cpu.Mode, budget uint64) (cpu.Result, cpu.Result) {
	t.Helper()
	want := precise.Run(context.Background(), start, mode, budget)
	got := jit.Run(context.Background(), start, mode, budget)
	if got.Reason != want.Reason || got.Instructions != want.Instructions || got.PC != want.PC ||
		!sameError(got.Err, want.Err) {
		t.Fatalf("public execution differs: precise=%+v jit=%+v", want, got)
	}
	// Inspect actual captures BEFORE a state getter can materialize flags.
	if !reflect.DeepEqual(jit.PCRegisterCaptures(), precise.PCRegisterCaptures()) ||
		!reflect.DeepEqual(jit.PCHistory(), precise.PCHistory()) || !reflect.DeepEqual(jit.pcHits, precise.pcHits) {
		t.Fatal("route changed a public pre-instruction observer")
	}
	wantState, err := precise.SaveContext()
	check(t, err)
	gotState, err := jit.SaveContext()
	check(t, err)
	if !bytes.Equal(gotState, wantState) || len(precise.regions) != len(jit.regions) {
		t.Fatal("route changed architectural/context state")
	}
	for i := range precise.regions {
		if precise.regions[i].address != jit.regions[i].address ||
			precise.regions[i].permissions != jit.regions[i].permissions ||
			!bytes.Equal(precise.regions[i].data, jit.regions[i].data) {
			t.Fatal("route changed a mapped region or partial-write observation")
		}
	}
	return want, got
}

func TestThumbApplicationPrivateRouteEntryTLBMatchesPrecise(t *testing.T) {
	for _, load := range []struct {
		name string
		raw uint16
		want uint32
		dedicated bool
	}{
		{"word", 0x6808, 0x040380fe, false},           // LDRr0,[r1,#0].
		{"byte", 0x7808, 0xfe, false},                // LDRBr0,[r1,#0].
		{"half", 0x8808, 0x80fe, false},              // LDRHr0,[r1,#0].
		{"register-byte", 0x5c88, 0xfe, false},       // LDRBr0,[r1,r2].
		{"register-half", 0x5a88, 0x80fe, false},     // LDRHr0,[r1,r2].
		{"signed-byte", 0x5688, 0xfffffffe, false},   // LDRSBr0,[r1,r2].
		{"signed-half", 0x5e88, 0xffff80fe, false},   // LDRSHr0,[r1,r2].
		{"literal-word", 0x4801, 0x78563412, true},  // LDRr0,[PC,#4] at1ffc, pool2004.
		{"sp-word", 0x9800, 0x040380fe, true},       // LDRr0,[SP,#0].
		{"register-word", 0x5888, 0x040380fe, true}, // LDRr0,[r1,r2].
	} {
		budgets := []uint64{1, 2, 3, 4, 5, 255, 256, 257}
		if load.dedicated {
			budgets = []uint64{2} // Small full-fit positives, not another large sweep.
		}
		for _, nonnilTLB := range []bool{false, true} {
			for _, warm := range []bool{false, true} {
				for _, budget := range budgets {
					t.Run(fmt.Sprintf("%s/tlb%t/warm%t/budget%d", load.name, nonnilTLB, warm, budget), func(t *testing.T) {
						precise, jit, start, dataAddress := routeHoist26Pair(t, load.raw, nonnilTLB, warm)
						_, got := routeHoist26AssertRun(t, precise, jit, start, cpu.ModeThumb, budget)
						if got.Err != nil || got.Reason != cpu.StopBudget || got.Instructions != budget || jit.regs[0] != load.want {
							t.Fatalf("literal load/budget changed: result=%+v", got)
						}
						if nonnilTLB && !jit.tlbHit(dataAddress, cpu.PermissionRead) {
							t.Fatal("application runner bypassed scalar fallback native-TLB bookkeeping")
						}
						if collectThumbMicro {
							micro := jit.ExecutionStatistics().ThumbMicro
							var observed uint64
							if micro != nil {
								for _, count := range micro.Classes {
									observed += count
								}
							}
							if micro == nil || observed != budget {
								t.Fatal("route changed exact original-op profile credit")
							}
						}
					})
				}
			}
		}
	}
}

func TestThumbApplicationPrivateRouteObserverAdmissionMatchesPrecise(t *testing.T) {
	for _, observer := range []string{"history", "hits", "capture"} {
		for _, budget := range []uint64{1, 2, 3, 4, 255, 256, 257} {
			t.Run(fmt.Sprintf("%s/budget%d", observer, budget), func(t *testing.T) {
				precise, jit, start, _ := routeHoist26Pair(t, 0x6808, false, true)
				for _, b := range []*Backend{precise, jit} {
					switch observer {
					case "history":
						check(t, b.SetPCHistoryLimit(32))
					case "hits":
						b.pcHits = make(map[uint32]uint64)
					case "capture":
						check(t, b.SetPCRegisterCapture(0x1000, 32))
					}
				}
				if !jit.tracing() {
					t.Fatal("observer admission negative control was not active")
				}
				routeHoist26AssertRun(t, precise, jit, start, cpu.ModeThumb, budget)
			})
		}
	}
}

func TestThumbApplicationPrivateRoutePublicModeAndCP15Boundary(t *testing.T) {
	for _, system := range []bool{false, true} {
		for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 255, 256, 257} {
			t.Run(fmt.Sprintf("system%t/budget%d", system, budget), func(t *testing.T) {
				precise, jit := New(), NewJIT()
				for _, b := range []*Backend{precise, jit} {
					t.Cleanup(func() { _ = b.Close() })
					if !system {
						for _, address := range []uint32{0x1000, 0x2000, 0x3000, 0x4000} {
							check(t, b.Map(address, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
						}
					}
					thumbEntry := thumbTestCode(0x6808, 0x4730)     // LDRr0,[r1]; BXr6.
					armEntry := armTestCode(0xee012f10, 0xe12fff17) // MCRp15,c1,r2; BXr7.
					thumbReturn := thumbTestCode(0x6823, 0xe7fd)    // LDRr3,[r4]; B3000.
					if !system {
						check(t, b.WriteMemory(0x1000, thumbEntry))
						check(t, b.WriteMemory(0x2000, armEntry))
						check(t, b.WriteMemory(0x3000, thumbReturn))
						check(t, b.WriteMemory(0x4000, []byte{1, 2, 3, 4}))
					}
					for r, value := range map[uint32]uint32{1: 0x4000, 2: 2, 4: 0x4000, 6: 0x2000, 7: 0x3001} {
						check(t, b.WriteRegister(r, value))
					}
					check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSupervisor)))
					if system {
						bus := &testSystemBus{memory: make(map[uint32]byte)}
						bus.writeRaw(0x1000, thumbEntry)
						bus.writeRaw(0x2000, armEntry)
						bus.writeRaw(0x3000, thumbReturn)
						bus.writeRaw(0x4000, []byte{9, 8, 7, 6})
						check(t, b.AttachSystemBus(bus))
						if len(b.regions) != 0 || b.mapped != 0 {
							t.Fatal("system fixture must attach without private mappings")
						}
						// Independent internal fault-negative only: a public Map
						// cannot coexist with this attached bus. Seed a separate
						// synthetic cache entry, not a publicly mapped region, to
						// prove Run ignores stale private bytes in physical mode.
						syntheticData := [16]byte{1, 2, 3, 4}
						syntheticRegion := region{address: 0x4000, size: 16,
							permissions: cpu.PermissionRead | cpu.PermissionWrite, data: syntheticData[:]}
						b.cacheData(&syntheticRegion, 0x4000, cpu.PermissionRead)
						entry := &b.privateDataCache[privateDataCacheIndex(0x4000, cpu.PermissionRead)]
						if value, hit := cachedJITWord(entry, 0x4000); !hit || value != 0x04030201 {
							t.Fatal("synthetic stale-cache negative control is not populated")
						}
					}
				}
				_, got := routeHoist26AssertRun(t, precise, jit, 0x1000, cpu.ModeThumb, budget)
				if system {
					if got.Err != nil || got.Reason != cpu.StopBudget || got.Instructions != budget || jit.regs[0] != 0x06070809 {
						t.Fatal("system interworking used private bytes or changed its exact budget")
					}
					if budget >= 3 && jit.cp15.control != 2 || !jit.physicalAccess {
						t.Fatal("accepted system CP15 did not preserve physical routing")
					}
					if budget >= 5 && jit.regs[3] != 0x06070809 {
						t.Fatal("Thumb return failed to retain physical data routing")
					}
				} else if budget >= 3 {
					if got.Err == nil || got.Reason != cpu.StopFault || got.Instructions != 2 || got.PC != 0x2004 || jit.cp15.control != 0 || jit.physicalAccess {
						t.Fatal("application CP15 was admitted or fault retirement/PC changed")
					}
				} else if got.Err != nil || got.Instructions != budget {
					t.Fatal("Thumb-to-ARM partial budget changed")
				}
			})
		}
	}
}

func routeHoist26SystemContext(t *testing.T, control uint32) []byte {
	t.Helper()
	donor := New()
	t.Cleanup(func() { _ = donor.Close() })
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	bus.writeRaw(0x6000, armTestCode(0xee010f10)) // MCRp15,0,r0,c1,c0,0.
	check(t, donor.AttachSystemBus(bus))
	check(t, donor.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSupervisor)))
	check(t, donor.WriteRegister(cpu.RegisterR0, control))
	result := donor.Run(context.Background(), 0x6000, cpu.ModeARM, 1)
	if result.Err != nil || result.Instructions != 1 || donor.cp15.control != control {
		t.Fatal("public system-CP15 context donor did not execute the literal instruction")
	}
	state, err := donor.SaveContext()
	check(t, err)
	return state
}

func TestThumbApplicationPrivateRouteContextRefresh(t *testing.T) {
	for _, control := range []uint32{1, 1 << 12} { // MMU and I-cache each force physicalAccess.
		for _, nonnilTLB := range []bool{false, true} {
			t.Run(fmt.Sprintf("control%x/tlb%t", control, nonnilTLB), func(t *testing.T) {
				precise, jit, _, _ := routeHoist26Pair(t, 0x6808, nonnilTLB, true)
				state := routeHoist26SystemContext(t, control)
				var privateStates [2][]byte
				for i, b := range []*Backend{precise, jit} {
					var err error
					privateStates[i], err = b.SaveContext()
					check(t, err)
					routeHoist26AssertFailedRestoreAtomic(t, b, privateStates[i], state[:len(state)-1])
				}
				routeHoist26AssertRun(t, precise, jit, 0x1000, cpu.ModeThumb, 2)
				for _, b := range []*Backend{precise, jit} {
					check(t, b.RestoreContext(state))
					check(t, b.WriteRegister(cpu.RegisterR1, 0x4000))
					if !b.physicalAccess || b.cp15.control != control {
						t.Fatal("serialized CP15 restore did not refresh route")
					}
				}
				_, physical := routeHoist26AssertRun(t, precise, jit, 0x1000, cpu.ModeThumb, 2)
				if control == 1 {
					// TTB0 is intentionally unmapped: the first instruction fetch
					// must fault, not execute the warm private code/data mappings.
					if physical.Err == nil || physical.Instructions != 0 {
						t.Fatal("restored MMU route bypassed translation/fetch fault")
					}
				} else if physical.Err != nil || physical.Instructions != 2 || jit.regs[0] != 0x040380fe {
					t.Fatal("restored I-cache-only route changed the mapped program")
				}
				for i, b := range []*Backend{precise, jit} {
					check(t, b.RestoreContext(privateStates[i]))
					if b.physicalAccess || b.cp15.control != 0 {
						t.Fatal("private context restore retained stale physical routing")
					}
				}
				_, restored := routeHoist26AssertRun(t, precise, jit, 0x1000, cpu.ModeThumb, 2)
				if restored.Err != nil || restored.Instructions != 2 || jit.regs[0] != 0x040380fe {
					t.Fatal("new application invocation retained a route from the prior call")
				}
			})
		}
	}
}

// A failed serialized restore must not mutate this same backend. This is not a
// raw-pending-flags equality requirement across the independent New/JIT pair.
type routeHoist26SliceIdentity struct {
	first *byte
	length, capacity int
	nilSlice bool
}

func routeHoist26SliceID(data []byte) routeHoist26SliceIdentity {
	result := routeHoist26SliceIdentity{length: len(data), capacity: cap(data), nilSlice: data == nil}
	if cap(data) > 0 {
		result.first = &data[:1][0]
	}
	return result
}

type routeHoist26CacheEntryView struct {
	address uint32
	perms cpu.Permissions
	slice routeHoist26SliceIdentity
}

func routeHoist26CacheView(entries []dataRegionCache) []routeHoist26CacheEntryView {
	view := make([]routeHoist26CacheEntryView, len(entries))
	for i, entry := range entries {
		view[i] = routeHoist26CacheEntryView{address: entry.address, perms: entry.perms, slice: routeHoist26SliceID(entry.data)}
	}
	return view
}

func routeHoist26AssertFailedRestoreAtomic(t *testing.T, b *Backend, beforeState, invalidState []byte) {
	t.Helper()
	// The preceding SaveContext has already materialized pending flags and
	// incremented its getter counter. All snapshots below start AFTER it.
	beforeStats := b.ExecutionStatistics()
	beforeFlags := b.flags
	beforeMapping, beforeJIT := b.mappingGen, b.jitGen
	beforePhysical, beforeMode := b.physicalAccess, b.mode
	beforePrivate := routeHoist26CacheView(b.privateDataCache[:])
	beforeDirect := routeHoist26CacheView(b.dataCache[:])
	beforeAlt := routeHoist26CacheView(b.directDataCacheAlt[:])
	beforeMiss := b.directDataMissCache
	beforeExecuteAddress, beforeExecute := b.executeAddress, routeHoist26SliceID(b.executeData)
	beforeWindow, beforeWindowTag, beforeWindowSerial := b.instructionWindow, b.instructionWindowTag, b.instructionWindowSerial
	beforeCacheSerial := b.instructionCacheSerial
	beforeMMU, beforeICache, beforeVirtual := b.mmuTLBTable, b.instructionCacheTable, b.virtualData
	if beforeMMU != nil || beforeICache != nil || beforeVirtual != nil {
		t.Fatal("failed-restore fixture must start with private routing and no MMU/I-cache tables")
	}
	// Native TLB has no independent generation field: compare every tag,
	// victim and host pointer, along with mappingGen for MMU/I-cache entries.
	tlbEntries := append([]tlbEntry(nil), b.tlb...)
	tlbLength, tlbCapacity, tlbNil := len(b.tlb), cap(b.tlb), b.tlb == nil
	var tlbFirst *tlbEntry
	if len(b.tlb) > 0 {
		tlbFirst = &b.tlb[0]
	}
	beforeBlock := b.jitBlocks[0x1000]
	type savedRegion struct {
		address, size uint32
		permissions cpu.Permissions
		slice routeHoist26SliceIdentity
		data []byte
	}
	regions := make([]savedRegion, len(b.regions))
	for i, region := range b.regions {
		regions[i] = savedRegion{address: region.address, size: region.size, permissions: region.permissions,
			slice: routeHoist26SliceID(region.data), data: append([]byte(nil), region.data...)}
	}
	if err := b.RestoreContext(invalidState); err == nil {
		t.Fatal("truncated serialized context was accepted")
	}
	if !reflect.DeepEqual(b.ExecutionStatistics(), beforeStats) || b.flags != beforeFlags ||
		b.mappingGen != beforeMapping || b.jitGen != beforeJIT || b.physicalAccess != beforePhysical || b.mode != beforeMode ||
		b.executeAddress != beforeExecuteAddress || routeHoist26SliceID(b.executeData) != beforeExecute ||
		b.instructionWindow != beforeWindow || b.instructionWindowTag != beforeWindowTag ||
		b.instructionWindowSerial != beforeWindowSerial || b.instructionCacheSerial != beforeCacheSerial ||
		b.mmuTLBTable != beforeMMU || b.instructionCacheTable != beforeICache || b.virtualData != beforeVirtual ||
		b.directDataMissCache != beforeMiss || b.jitBlocks[0x1000] != beforeBlock {
		t.Fatal("failed restore changed route/generations/cache windows/flags/block identity/counters")
	}
	if !routeHoist26CacheViewsEqual(routeHoist26CacheView(b.privateDataCache[:]), beforePrivate) ||
		!routeHoist26CacheViewsEqual(routeHoist26CacheView(b.dataCache[:]), beforeDirect) ||
		!routeHoist26CacheViewsEqual(routeHoist26CacheView(b.directDataCacheAlt[:]), beforeAlt) ||
		len(b.tlb) != tlbLength || cap(b.tlb) != tlbCapacity || (b.tlb == nil) != tlbNil ||
		!reflect.DeepEqual(b.tlb, tlbEntries) {
		t.Fatal("failed restore changed a cache tag/backing identity or native TLB entry")
	}
	if len(b.tlb) > 0 && &b.tlb[0] != tlbFirst {
		t.Fatal("failed restore replaced the native TLB backing allocation")
	}
	if len(b.regions) != len(regions) {
		t.Fatal("failed restore changed the mapped region count")
	}
	for i, region := range b.regions {
		before := regions[i]
		if region.address != before.address || region.size != before.size || region.permissions != before.permissions ||
			routeHoist26SliceID(region.data) != before.slice || !bytes.Equal(region.data, before.data) {
			t.Fatal("failed restore changed a region's metadata, backing identity or bytes")
		}
	}
	afterState, err := b.SaveContext()
	check(t, err)
	if !bytes.Equal(afterState, beforeState) {
		t.Fatal("failed restore changed the serialized architectural state")
	}
	// Only this final explicit SaveContext may increment the getter counter.
	beforeStats.SerializedContextSaves++
	if !reflect.DeepEqual(b.ExecutionStatistics(), beforeStats) {
		t.Fatal("failed restore or state observation changed an unexpected counter")
	}
}


// Every field is comparable, so == compares *byte by address rather than
// reflect.DeepEqual's pointed-to byte contents.
func routeHoist26CacheViewsEqual(left, right []routeHoist26CacheEntryView) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestThumbApplicationPrivateRouteCacheBackingIdentity(t *testing.T) {
	left := [16]byte{0x5a}
	right := [16]byte{0x5a}
	if !bytes.Equal(left[:], right[:]) || &left[0] == &right[0] {
		t.Fatal("literal equal-value/distinct-backing fixture is not independent")
	}
	before := routeHoist26CacheView([]dataRegionCache{{address: 0x4000, perms: cpu.PermissionRead, data: left[:]}})
	same := routeHoist26CacheView([]dataRegionCache{{address: 0x4000, perms: cpu.PermissionRead, data: left[:]}})
	replaced := routeHoist26CacheView([]dataRegionCache{{address: 0x4000, perms: cpu.PermissionRead, data: right[:]}})
	if !routeHoist26CacheViewsEqual(before, same) || routeHoist26CacheViewsEqual(before, replaced) {
		t.Fatal("cache snapshot did not compare the actual backing pointer identity")
	}
	changedTag := routeHoist26CacheView([]dataRegionCache{{address: 0x4004, perms: cpu.PermissionRead, data: left[:]}})
	if routeHoist26CacheViewsEqual(before, changedTag) {
		t.Fatal("cache snapshot ignored a changed tag")
	}
}
