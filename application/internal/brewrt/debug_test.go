package brewrt

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestFaultDebugSnapshotPreservesLastHostPointers(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	destination := heapBase + 0x200
	source := stackBase + 0x200
	vtable := heapBase + 0x400
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], vtable)
	if err := runtime.cpu.WriteMemory(destination, pointer[:]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(source, []byte{0x54, 0xcc, 0x44, 0xbe}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(vtable, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR5: destination,
		cpu.RegisterSP: source,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	runtime.lastRunResult = cpu.Result{PC: moduleBase + 8, Err: errors.New("invalid guest address")}
	runtime.lastHostCall = "AEEStdLib slot 9"
	runtime.lastHostArgs = [4]uint32{destination, source}

	snapshot := runtime.DebugSnapshot(8)
	if snapshot.CPU == nil || snapshot.LastResult == nil ||
		len(snapshot.HostTrace.Entries) != 1 ||
		!strings.Contains(snapshot.HostTrace.Entries[0], "slot 9") {
		t.Fatalf("BREW fault snapshot = %+v", snapshot)
	}
	regions := runtime.DebugMemoryRegions(4096)
	want := map[string]bool{"last-host-r0": false, "last-host-r1": false, "r5-vtable": false}
	total := 0
	for _, region := range regions {
		total += len(region.Data)
		if _, ok := want[region.Label]; ok && len(region.Data) != 0 {
			want[region.Label] = true
		}
	}
	if total > 4096 {
		t.Fatalf("BREW debug memory = %d bytes, want at most 4096", total)
	}
	for label, present := range want {
		if !present {
			t.Errorf("BREW debug region %q absent: %+v", label, regions)
		}
	}
}
