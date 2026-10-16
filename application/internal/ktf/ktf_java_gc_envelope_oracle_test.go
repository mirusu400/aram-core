package ktf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// Literal old scanning plus the collector's original mark envelope guard. The
// callback sequence, including duplicates and interior addresses, is exact.
func literalHeapEnvelopeScanRoot(buffer []byte, lower, upper uint32) []uint32 {
	var result []uint32
	mark := func(address uint32) {
		if address < lower || address >= upper {
			return
		}
		result = append(result, address)
	}
	for offset := 0; offset+4 <= len(buffer); offset += 4 {
		mark(binary.LittleEndian.Uint32(buffer[offset:]))
	}
	return result
}

func TestKTFHeapEnvelopeScanLiteralOracle(t *testing.T) {
	random := rand.New(rand.NewSource(0x4a745))
	for _, bounds := range [][2]uint32{{0, 0}, {0, 1}, {0, 0xffffffff}, {0x10000000, 0x12000000}, {0x80000000, 0xffffffff}, {0xfffffff0, 0}, {19, 23}, {23, 19}} {
		t.Run(fmt.Sprintf("%08x-%08x", bounds[0], bounds[1]), func(t *testing.T) {
			for length := 0; length <= 257; length++ {
				buffer := make([]byte, length)
				_, _ = random.Read(buffer)
				values := []uint32{0, 1, 0xffffffff, bounds[0] - 1, bounds[0], bounds[0] + 1, bounds[1] - 1, bounds[1], bounds[1] + 1, bounds[0]}
				for index, value := range values {
					if index*4+4 <= len(buffer) {
						binary.LittleEndian.PutUint32(buffer[index*4:], value)
					}
				}
				for prefix := 0; prefix < min(4, len(buffer)+1); prefix++ {
					window := buffer[prefix:]
					var got []uint32
					scanWordsInHeapEnvelope(window, func(value uint32) { got = append(got, value) }, bounds[0], bounds[1])
					want := literalHeapEnvelopeScanRoot(window, bounds[0], bounds[1])
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("length=%d prefix=%d: got=%v want=%v", length, prefix, got, want)
					}
				}
			}
		})
	}
}

func TestKTFHeapEnvelopePreservesPublicScanCallbacks(t *testing.T) {
	values := []uint32{0, 1, 0x10000000, 0x10000001, 0xffffffff, 0}
	buffer := make([]byte, len(values)*4+3)
	for index, value := range values {
		binary.LittleEndian.PutUint32(buffer[index*4:], value)
	}
	var got []uint32
	ScanWords(buffer, func(value uint32) { got = append(got, value) })
	if !reflect.DeepEqual(got, values) {
		t.Fatalf("public callback sequence changed: %v", got)
	}
}

type gcEnvelopeReadCall struct {
	Address uint32
	Size    int
}
type gcEnvelopeReadSpy struct {
	cpu.Backend
	Calls  []gcEnvelopeReadCall
	FailAt int
}

func (b *gcEnvelopeReadSpy) ReadMemory(address uint32, destination []byte) error {
	b.Calls = append(b.Calls, gcEnvelopeReadCall{address, len(destination)})
	clear(destination)
	if b.FailAt == len(b.Calls) {
		return errors.New("synthetic root read failure")
	}
	for index, value := range []uint32{0, 0x10000000, 0x10000003, 0x10000008, 0x1000000f, 0x10000010, 0xffffffff} {
		if index*4+4 <= len(destination) {
			binary.LittleEndian.PutUint32(destination[index*4:], value)
		}
	}
	return nil
}

// Freeze the old region/chunk/read/break order independently of the candidate.
func literalHeapRegionScanRoot(r *Runtime, mark func(uint32)) {
	regions := []ktfIncrementalMemoryRegion{{base: ImageBase, size: r.ImageSz}, {base: guest.DefaultStackBase, size: guest.DefaultStackSize}, {base: LowWorkRAMBase, size: LowWorkRAMSize}}
	regions = append(regions, r.incrementalMemory...)
	buffer := make([]byte, ktfGCScanLimit)
	for _, region := range regions {
		if region.size == 0 {
			continue
		}
		for offset := uint32(0); offset < region.size; {
			count := min(uint32(len(buffer)), region.size-offset)
			window := buffer[:count]
			if err := r.CPU.ReadMemory(region.base+offset, window); err != nil {
				break
			}
			for word := 0; word+4 <= len(window); word += 4 {
				mark(binary.LittleEndian.Uint32(window[word:]))
			}
			offset += count
		}
	}
}

func TestKTFHeapEnvelopeRegionReadsAndFaultOrder(t *testing.T) {
	for failAt := 0; failAt < 12; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			a, b := &gcEnvelopeReadSpy{FailAt: failAt}, &gcEnvelopeReadSpy{FailAt: failAt}
			makeRuntime := func(backend cpu.Backend) *Runtime {
				return &Runtime{CPU: backend, ImageSz: ktfGCScanLimit + 7, incrementalMemory: []ktfIncrementalMemoryRegion{{base: 0x50000000, size: 0}, {base: 0xfffffff0, size: ktfGCScanLimit + 3}, {base: 0x51000000, size: 31}}}
			}
			var got, want []uint32
			mark := func(address uint32) {
				if address >= 0x10000000 && address < 0x10000010 {
					want = append(want, address)
				}
			}
			literalHeapRegionScanRoot(makeRuntime(a), mark)
			makeRuntime(b).markRegionRoots(func(address uint32) { got = append(got, address) }, 0x10000000, 0x10000010)
			if !reflect.DeepEqual(a.Calls, b.Calls) || !reflect.DeepEqual(got, want) {
				t.Fatal("root read/chunk/fault or callback order differs")
			}
		})
	}
}
