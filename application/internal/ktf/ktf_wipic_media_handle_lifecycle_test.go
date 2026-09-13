package ktf

import (
	"bytes"
	"context"
	"maps"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// This checks memory persistence only. Full media clip/service registry
// persistence has a pre-existing gap and is not established by this test.
func TestKTFWIPICMediaIndirectMemoryAllocationRoundtrip(t *testing.T) {
	r := newTestRuntime(t)
	handle := newKTFTestMediaClip(t, r, 4096)
	allocation := r.wipicMemory[handle]
	var buffer bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&buffer)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(r, &decoder)
	check(t, err)
	r.releaseWIPICMemory(handle)
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	if got := r.wipicMemory[handle]; got != allocation {
		t.Fatalf("restored allocation = %+v, want %+v", got, allocation)
	}
	head, err := r.ReadU32(handle)
	check(t, err)
	size, err := r.ReadU32(handle + 4)
	check(t, err)
	if head != allocation.base || allocation.data != head+8 || size != 96 || allocation.size != 96 {
		t.Fatal("restore lost indirect memory header")
	}
	for address, want := range map[uint32]uint32{handle: 8, allocation.base: 104} {
		if got := r.Heap.Root().Allocations[address]; got != want {
			t.Fatalf("restored heap allocation %08x = %d, want %d", address, got, want)
		}
	}
	r.releaseWIPICMemory(handle)
	for _, address := range []uint32{handle, allocation.base} {
		if _, ok := r.Heap.Root().Allocations[address]; ok {
			t.Fatalf("release of restored memory leaked %08x", address)
		}
	}
	if _, ok := r.wipicMemory[handle]; ok {
		t.Fatal("release retained restored memory ID")
	}
}

func TestKTFWIPICMediaHandleFailedCreateCleanup(t *testing.T) {
	r := newTestRuntime(t)
	mediaType, err := r.allocateBytes([]byte("Yamaha_MA3"), true)
	check(t, err)
	// Fill the service pool without allocating guest handles so CreateClip fails
	// only after the guest's indirect allocation has succeeded.
	for range int(r.Services.Config.Limits.Media.MaxClips) {
		_, err := r.Services.Media.CreateClip(r.ServiceOwner, "audio/x-smaf", 1)
		check(t, err)
	}
	setKTFWIPICCallArguments(t, r, []uint32{mediaType, 4096, 0})
	before := maps.Clone(r.Heap.Root().Allocations)
	memoryBefore := maps.Clone(r.wipicMemory)
	servicesBefore, err := r.Services.MarshalBinary()
	check(t, err)
	for range 3 {
		handle, err := ktfWIPICMediaCreate(context.Background(), r)
		if err == nil || handle != 0 {
			t.Fatalf("pool-full create = %08x, %v", handle, err)
		}
		if !reflect.DeepEqual(before, r.Heap.Root().Allocations) || !reflect.DeepEqual(memoryBefore, r.wipicMemory) {
			t.Fatal("failed create leaked or changed guest allocations")
		}
		if len(r.wipicMediaClips) != 0 || len(r.wipicMediaServices) != 0 {
			t.Fatal("failed create registered a clip")
		}
	}
	servicesAfter, err := r.Services.MarshalBinary()
	check(t, err)
	if !bytes.Equal(servicesBefore, servicesAfter) {
		t.Fatal("failed create changed shared services")
	}
}

func TestKTFWIPICMediaHandleExhaustedHeapDoesNotRegister(t *testing.T) {
	r := newTestRuntime(t)
	mediaType, err := r.allocateBytes([]byte("Yamaha_MA3"), true)
	check(t, err)
	setKTFWIPICCallArguments(t, r, []uint32{mediaType, 4096, 0})
	heap := r.Heap.Root()
	free := heap.Free
	heap.Free = nil
	defer func() { heap.Free = free }()
	before := maps.Clone(heap.Allocations)
	servicesBefore, err := r.Services.MarshalBinary()
	check(t, err)
	handle, err := ktfWIPICMediaCreate(context.Background(), r)
	check(t, err)
	if handle != 0 {
		t.Fatalf("exhausted create returned %08x", handle)
	}
	if len(r.wipicMediaClips) != 0 || len(r.wipicMediaServices) != 0 || len(r.wipicMemory) != 0 {
		t.Fatal("exhausted create registered a clip or memory ID")
	}
	if !reflect.DeepEqual(before, heap.Allocations) {
		t.Fatal("exhausted create changed allocations")
	}
	servicesAfter, err := r.Services.MarshalBinary()
	check(t, err)
	if !bytes.Equal(servicesBefore, servicesAfter) {
		t.Fatal("exhausted create leaked a shared service")
	}
}

func TestKTFWIPICMediaHandleLegacyDirectDestroy(t *testing.T) {
	r := newTestRuntime(t)
	handle, err := r.AllocateWords(24)
	check(t, err)
	serviceID, err := r.Services.Media.CreateClip(r.ServiceOwner, "audio/x-smaf", 4096)
	check(t, err)
	r.wipicMediaClips[handle] = &ktfWIPICMediaClip{mediaType: "audio/x-smaf", capacity: 4096, volume: 100}
	r.wipicMediaServices[handle] = serviceID
	setKTFWIPICCallArguments(t, r, []uint32{handle})
	result, err := ktfWIPICMediaDestroy(context.Background(), r)
	check(t, err)
	if result != 0 {
		t.Fatalf("legacy destroy = %08x", result)
	}
	if _, ok := r.Heap.Root().Allocations[handle]; ok {
		t.Fatal("legacy destroy leaked direct allocation")
	}
	if len(r.wipicMediaClips) != 0 || len(r.wipicMediaServices) != 0 {
		t.Fatal("legacy destroy retained clip registry")
	}
	if _, err := r.Services.Media.Info(r.ServiceOwner, serviceID); err == nil {
		t.Fatal("legacy destroy retained shared service")
	}
}
