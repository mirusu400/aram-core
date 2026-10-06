package brewrt

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// DebugSnapshot exposes the faulting BREW CPU state to a user-submitted debug
// bundle. The runtime has no guest log; the last host trap identifies the call
// that preceded a fault without embedding guest memory in JSON.
func (r *Runtime) DebugSnapshot(maxEntries int) guest.DebugSnapshot {
	limit := guest.NormalizeDebugSnapshotLimit(maxEntries)
	snapshot := guest.DebugSnapshot{
		Runtime:   "brew",
		CPU:       guest.NewDebugCPUSnapshot(r.cpu),
		GuestLog:  guest.NewDebugLogSnapshot(nil, 0, limit),
		HostTrace: guest.NewDebugLogSnapshot(nil, 0, limit),
	}
	if r.lastRunResult.Instructions != 0 || r.lastRunResult.PC != 0 || r.lastRunResult.Err != nil {
		snapshot.LastResult = guest.NewDebugExecutionResult(r.lastRunResult)
	}
	if r.lastHostCall != "" {
		snapshot.HostTrace = guest.NewDebugLogSnapshot([]string{r.lastHostCall}, 0, limit)
	}
	return snapshot
}

// DebugMemoryRegions reads small windows around the failed instruction, stack,
// and the last host call's pointer arguments. The caller limits this to faulted
// machines and includes the bytes only in an explicitly submitted crash bundle.
func (r *Runtime) DebugMemoryRegions(limit int) []guest.DebugMemoryRegion {
	if r == nil || r.cpu == nil {
		return nil
	}
	if limit <= 0 || limit > 64<<10 {
		limit = 64 << 10
	}
	regions := make([]guest.DebugMemoryRegion, 0, 8)
	add := func(label string, address uint32, before uint32, size int) {
		if address == 0 {
			return
		}
		base := address
		if base > before {
			base -= before
		} else {
			base = 0
		}
		region := guest.ReadDebugMemoryRegion(r.cpu, label, base, size)
		if len(region.Data) != 0 {
			regions = append(regions, region)
		}
	}
	add("pc", r.lastRunResult.PC, 128, 512)
	if sp, err := r.cpu.ReadRegister(cpu.RegisterSP); err == nil {
		add("stack", sp, 0, 1024)
	}
	for index := 0; index < 2 && r.lastHostCall != ""; index++ {
		add(fmt.Sprintf("last-host-r%d", index), r.lastHostArgs[index], 64, 512)
	}
	for _, register := range []uint32{cpu.RegisterR4, cpu.RegisterR5} {
		address, err := r.cpu.ReadRegister(register)
		if err != nil {
			continue
		}
		add(fmt.Sprintf("r%d", register), address, 64, 256)
		var pointer [4]byte
		if err := r.cpu.ReadMemory(address, pointer[:]); err == nil {
			add(fmt.Sprintf("r%d-vtable", register), binary.LittleEndian.Uint32(pointer[:]), 0, 128)
		}
	}
	bounded := regions[:0]
	total := 0
	for _, region := range regions {
		if total >= limit {
			break
		}
		if len(region.Data) > limit-total {
			region.Data = region.Data[:limit-total]
		}
		bounded = append(bounded, region)
		total += len(region.Data)
	}
	return bounded
}
