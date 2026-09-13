package ktf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"testing"
)

func saved262(t *testing.T, r *Runtime) (*SavedState, []byte) {
	t.Helper()
	var b bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&b)))
	d := guest.StateDecoder{Reader: bytes.NewReader(b.Bytes())}
	s, e := ParseState(r, &d)
	check(t, e)
	check(t, s.ValidateClipBuffers(nil))
	return s, b.Bytes()
}
func TestKTF262RetainedBufferStateRoundtripAndLegacy(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	out, e := r.newJavaByteArray(make([]byte, 4))
	check(t, e)
	call262(t, r, "getData", "([BII)I", c, out, 0, 3)
	in, e := r.newJavaByteArray([]byte{5, 6, 7})
	check(t, e)
	call262(t, r, "putData", "([BII)I", c, in, 0, 3)
	saved, encoded := saved262(t, r)
	check(t, r.writeJavaByteArrayRange(a, 0, []byte{9, 9, 9, 9}))
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	check(t, r.writeJavaByteArrayRange(a, 0, []byte{8, 9, 10}))
	call262(t, r, "getData", "([BII)I", c, out, 0, 4)
	got, e := r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(got, []byte{4, 8, 9, 10}) {
		t.Fatalf("restored ring=%v", got)
	}
	// Schema13 has the exact old metadata bytes, no alias sidecar.
	legacy := append([]byte(nil), encoded[:len(encoded)-16]...)
	binary.LittleEndian.PutUint32(legacy[4:8], 13)
	d := guest.StateDecoder{Reader: bytes.NewReader(legacy)}
	old, e := ParseState(r, &d)
	check(t, e)
	check(t, old.ValidateClipBuffers(nil))
	check(t, RestoreState(r, r.CPU, old, &started))
	check(t, r.writeJavaByteArrayRange(a, 0, []byte{9, 9, 9, 9}))
	call262(t, r, "getData", "([BII)I", c, out, 0, 4)
	got, e = r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(got, []byte{4, 5, 6, 7}) {
		t.Fatalf("legacy detached=%v", got)
	}
}
func TestKTF262SavedAliasValidation(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	for _, kind := range []string{"type", "length", "front", "count", "overflow", "unallocated", "source-count"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := saved262(t, r)
			alias := s.clipBuffers[c]
			clip := s.metadata.Clips[c]
			put := func(addr, v uint32) { binary.LittleEndian.PutUint32(s.heapMemory[addr-guest.HeapBase:], v) }
			fields, e := r.ReadU32(a)
			check(t, e)
			switch kind {
			case "source-count":
				_, err := s.Services.Media.Append(s.owner, s.metadata.ClipServices[c], []byte{5})
				check(t, err)
			case "type":
				put(a+4, 0)
			case "length":
				put(fields+4, 5)
			case "front":
				alias.Front = 4
			case "count":
				clip.Data = append(clip.Data, 0)
			case "overflow":
				alias.Array = 0xfffffffc
			case "unallocated":
				alias.Array = guest.HeapBase + guest.HeapSize - 8
			}
			s.clipBuffers[c] = alias
			s.metadata.Clips[c] = clip
			if e := s.ValidateClipBuffers(nil); e == nil {
				t.Fatal("invalid saved alias accepted")
			}
			// Validation is read-only even when live array bytes differ from saved bytes.
			live, e := r.readJavaByteArray(a)
			check(t, e)
			if !bytes.Equal(live, []byte{1, 2, 3, 4}) {
				t.Fatal("validator mutated live heap")
			}
		})
	}
}
func TestKTF262RetainedBufferSurvivesGC(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, c))
	check(t, r.CPU.WriteRegister(cpu.RegisterR2, 0))
	check(t, r.CPU.WriteRegister(cpu.RegisterR3, 0))
	r.CollectJavaHeapForTest()
	check(t, r.writeJavaByteArrayRange(a, 0, []byte{9, 8, 7, 6}))
	out, e := r.newJavaByteArray(make([]byte, 4))
	check(t, e)
	call262(t, r, "getData", "([BII)I", c, out, 0, 4)
	got, e := r.readJavaByteArray(out)
	check(t, e)
	if !bytes.Equal(got, []byte{9, 8, 7, 6}) {
		t.Fatalf("GC lost alias=%v", got)
	}
}

func TestKTF262RetainedBufferHostRegistryRelease(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	fields, e := r.ReadU32(a)
	check(t, e)
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, 0))
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, c))
	check(t, r.CPU.WriteRegister(cpu.RegisterR2, 0))
	check(t, r.CPU.WriteRegister(cpu.RegisterR3, 0))
	r.CollectJavaHeapForTest()
	if r.Heap.Root().Allocations[a] == 0 || r.Heap.Root().Allocations[fields] == 0 {
		t.Fatal("GC freed retained wrapper/payload")
	}
	check(t, r.DestroyJavaMedia(r.Services.Events))
	// Internal GC ownership evidence only: production recycling retains clips.
	// Public Reset registry replacement is tested at the application boundary.
	delete(r.clips, c)
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, 0))
	if n := r.CollectJavaHeapForTest(); n == 0 {
		t.Fatal("GC release exercised no collection")
	}
	if r.Heap.Root().Allocations[a] != 0 || r.Heap.Root().Allocations[fields] != 0 {
		t.Fatal("unreachable clip pinned retained wrapper/payload")
	}
}
func TestKTF262SavedExternalAliasRequiresContiguousBytes(t *testing.T) {
	r := newTestRuntime(t)
	c, a := clip262(t, r, []byte{1, 2, 3, 4})
	s, _ := saved262(t, r)
	const base = uint32(0x30000000)
	block := make([]byte, 20)
	class, e := r.ReadU32(a + 4)
	check(t, e)
	binary.LittleEndian.PutUint32(block, base+8)
	binary.LittleEndian.PutUint32(block[4:], class)
	fields, e := r.ReadU32(a)
	check(t, e)
	check(t, r.CPU.ReadMemory(fields, block[8:]))
	s.clipBuffers[c] = ktfClipBufferSnapshot{Array: base}
	s.incrementalHeaps = []ktfIncrementalHeapSnapshot{{Base: base, Size: 20, Allocations: []heapBlockSnapshot{{Address: base, Size: 8}, {Address: base + 8, Size: 12}}}}
	s.metadata.IncrementalMemory = []ktfIncrementalMemoryRegionSnapshot{{Base: base, Size: 20}}
	check(t, validateKTFIncrementalMemory(s.metadata.IncrementalMemory, s.incrementalHeaps))
	for _, missingMiddle := range []bool{false, true} {
		err := s.ValidateClipBuffers(func(addr uint32, size uint64) ([]byte, error) {
			end := uint64(addr) + size
			if uint64(addr) < uint64(base) || end > uint64(base)+uint64(len(block)) || (missingMiddle && uint64(addr) < uint64(base)+18 && end > uint64(base)+17) {
				return nil, fmt.Errorf("missing candidate span")
			}
			return block[addr-base : uint64(addr-base)+size], nil
		})
		if missingMiddle && err == nil {
			t.Fatal("missing middle accepted")
		}
		if !missingMiddle {
			check(t, err)
		}
	}
	s.metadata.IncrementalMemory = append(s.metadata.IncrementalMemory, ktfIncrementalMemoryRegionSnapshot{Base: base + 4, Size: 8})
	s.incrementalHeaps = append(s.incrementalHeaps, ktfIncrementalHeapSnapshot{Base: base + 4, Size: 8})
	if err := validateKTFIncrementalMemory(s.metadata.IncrementalMemory, s.incrementalHeaps); err == nil {
		t.Fatal("overlapping incremental regions accepted")
	}
}
