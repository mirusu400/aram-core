package cheat

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"strings"
	"sync"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

const testMemoryBase = uint32(0x1000)

type testMemory struct {
	mu   sync.Mutex
	base uint32
	data []byte
}

func newTestMemory(size int) *testMemory {
	return &testMemory{
		base: testMemoryBase,
		data: make([]byte, size),
	}
}

func (m *testMemory) ReadMemory(address uint32, destination []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offset, err := m.offset(address, len(destination))
	if err != nil {
		return err
	}
	copy(destination, m.data[offset:offset+len(destination)])
	return nil
}

func (m *testMemory) WriteMemory(address uint32, source []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offset, err := m.offset(address, len(source))
	if err != nil {
		return err
	}
	copy(m.data[offset:offset+len(source)], source)
	return nil
}

func (m *testMemory) offset(address uint32, size int) (int, error) {
	start := uint64(address)
	end := start + uint64(size)
	memoryEnd := uint64(m.base) + uint64(len(m.data))
	if start < uint64(m.base) || end > memoryEnd {
		return 0, errors.New("test memory address is out of range")
	}
	return int(start - uint64(m.base)), nil
}

func testOptions(size uint32) Options {
	return Options{
		TargetSHA256: strings.Repeat("ab", 32),
		Regions: []Region{{
			Name:      "ram",
			Start:     testMemoryBase,
			Size:      size,
			Writable:  true,
			Scannable: true,
		}},
		MaxResults: 128,
	}
}

func TestValueRoundTrip(t *testing.T) {
	t.Parallel()
	values := []Value{
		U8(0xfe),
		I8(-2),
		U16(0x1234),
		I16(-1234),
		U32(0x12345678),
		I32(-12345678),
		U64(0x123456789abcdef0),
		I64(-123456789),
		F32(12.5),
		F64(-91.25),
	}
	for _, endian := range []Endian{EndianLittle, EndianBig} {
		for _, value := range values {
			encoded, err := value.Encode(endian)
			if err != nil {
				t.Fatalf("encode %+v: %v", value, err)
			}
			decoded, err := Decode(value.Type, encoded, endian)
			if err != nil {
				t.Fatalf("decode %+v: %v", value, err)
			}
			if decoded != value {
				t.Fatalf("round trip %+v = %+v", value, decoded)
			}
		}
	}
}

func TestEngineExactAndFollowupScans(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(16)
	engine, err := New(memory, testOptions(16))
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range []uint32{10, 20, 10, 30} {
		if err := engine.Write(testMemoryBase+uint32(index*4), U32(value), nil); err != nil {
			t.Fatal(err)
		}
	}
	target := U32(10)
	matches, err := engine.Scan(ScanRequest{
		Type:       TypeUint32,
		Comparison: CompareEqual,
		Value:      &target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 ||
		matches[0].Address != testMemoryBase ||
		matches[1].Address != testMemoryBase+8 {
		t.Fatalf("exact matches = %+v", matches)
	}
	if err := engine.Write(testMemoryBase, U32(15), nil); err != nil {
		t.Fatal(err)
	}
	if err := engine.Write(testMemoryBase+8, U32(5), nil); err != nil {
		t.Fatal(err)
	}
	matches, err = engine.NextScan(NextScanRequest{
		Comparison: CompareDecreased,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 ||
		matches[0].Address != testMemoryBase+8 ||
		matches[0].Value != U32(5) {
		t.Fatalf("decreased matches = %+v", matches)
	}
}

func TestEngineUnknownAndChangedScan(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(4)
	engine, err := New(memory, testOptions(4))
	if err != nil {
		t.Fatal(err)
	}
	matches, err := engine.Scan(ScanRequest{
		Type:       TypeUint8,
		Comparison: CompareUnknown,
		Alignment:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 4 {
		t.Fatalf("unknown matches = %d, want 4", len(matches))
	}
	if err := engine.Write(testMemoryBase+2, U8(1), nil); err != nil {
		t.Fatal(err)
	}
	matches, err = engine.NextScan(NextScanRequest{
		Comparison: CompareChanged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Address != testMemoryBase+2 {
		t.Fatalf("changed matches = %+v", matches)
	}
}

func TestEngineExpectedWriteAndRegionPermissions(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(8)
	options := testOptions(8)
	options.Regions = append(options.Regions, Region{
		Name:      "rom",
		Start:     testMemoryBase + 8,
		Size:      4,
		Writable:  false,
		Scannable: false,
	})
	memory.data = make([]byte, 12)
	engine, err := New(memory, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Write(testMemoryBase, U32(7), nil); err != nil {
		t.Fatal(err)
	}
	wrong := U32(8)
	if err := engine.Write(testMemoryBase, U32(9), &wrong); !errors.Is(err, ErrUnexpectedOriginal) {
		t.Fatalf("wrong expected error = %v", err)
	}
	got, err := engine.Read(testMemoryBase, TypeUint32)
	if err != nil {
		t.Fatal(err)
	}
	if got != U32(7) {
		t.Fatalf("value after rejected write = %+v", got)
	}
	if err := engine.Write(testMemoryBase+8, U32(1), nil); !errors.Is(err, ErrReadOnlyRegion) {
		t.Fatalf("read-only write error = %v", err)
	}
}

func TestCodeCaptureFreezeAndRestore(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(8)
	engine, err := New(memory, testOptions(8))
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WriteBytes(testMemoryBase+3, []byte{7}, nil); err != nil {
		t.Fatal(err)
	}
	state, err := engine.AddCode(Code{
		ID:               "lives",
		Description:      "unlimited lives",
		Address:          testMemoryBase + 3,
		Value:            []byte{99},
		Freeze:           true,
		RestoreOnDisable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(state.Code.Expected, []byte{7}) ||
		state.Code.TargetSHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("captured code = %+v", state)
	}
	if err := engine.EnableCode("lives"); err != nil {
		t.Fatal(err)
	}
	if err := memory.WriteMemory(testMemoryBase+3, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ApplyFrozen(); err != nil {
		t.Fatal(err)
	}
	got, err := engine.ReadBytes(testMemoryBase+3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{99}) {
		t.Fatalf("frozen value = %v", got)
	}
	if err := engine.DisableCode("lives"); err != nil {
		t.Fatal(err)
	}
	got, err = engine.ReadBytes(testMemoryBase+3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{7}) {
		t.Fatalf("restored value = %v", got)
	}
}

func TestCodeRejectsWrongTargetAndChangedOriginal(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(4)
	engine, err := New(memory, testOptions(4))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.AddCode(Code{
		ID:           "wrong-title",
		TargetSHA256: strings.Repeat("cd", 32),
		Address:      testMemoryBase,
		Value:        []byte{1},
	})
	if !errors.Is(err, ErrWrongTarget) {
		t.Fatalf("wrong-target error = %v", err)
	}
	_, err = engine.AddCode(Code{
		ID:       "guarded",
		Address:  testMemoryBase,
		Value:    []byte{1},
		Expected: []byte{2},
	})
	if !errors.Is(err, ErrUnexpectedOriginal) {
		t.Fatalf("unexpected-original error = %v", err)
	}
}

type mutatingMachine struct {
	memory *testMemory
	state  machinecore.State
}

func (m *mutatingMachine) Load(context.Context, machinecore.Source) error {
	m.state = machinecore.StateReady
	return nil
}

func (m *mutatingMachine) State() machinecore.State {
	return m.state
}

func (m *mutatingMachine) Start(context.Context) error {
	m.state = machinecore.StatePaused
	return m.memory.WriteMemory(testMemoryBase, []byte{0})
}

func (m *mutatingMachine) Pause() error {
	m.state = machinecore.StatePaused
	return nil
}

func (m *mutatingMachine) Resume() error {
	return m.memory.WriteMemory(testMemoryBase, []byte{0})
}

func (m *mutatingMachine) Stop() error {
	m.state = machinecore.StateStopped
	return nil
}

func (m *mutatingMachine) Reset(context.Context) error {
	m.state = machinecore.StateReady
	return m.memory.WriteMemory(testMemoryBase, []byte{0, 0})
}

func (m *mutatingMachine) StepFrame(context.Context) error {
	return m.memory.WriteMemory(testMemoryBase, []byte{0})
}

func (m *mutatingMachine) QueueInput(machinecore.InputEvent) error {
	return nil
}

func (m *mutatingMachine) Framebuffer() image.Image {
	return nil
}

func (m *mutatingMachine) DrainAudio() machinecore.AudioChunk {
	return machinecore.AudioChunk{}
}

func (m *mutatingMachine) SaveState(io.Writer) error {
	return nil
}

func (m *mutatingMachine) LoadState(io.Reader) error {
	return m.memory.WriteMemory(testMemoryBase, []byte{0, 0})
}

func (m *mutatingMachine) Close() error {
	return nil
}

func TestWrappedMachineEnforcesFrameAndLifecycleCodes(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(4)
	target := &mutatingMachine{
		memory: memory,
		state:  machinecore.StateReady,
	}
	wrapped, err := Wrap(target, memory, testOptions(4))
	if err != nil {
		t.Fatal(err)
	}
	engine := wrapped.Cheats()
	if _, err := engine.AddCode(Code{
		ID:      "freeze",
		Address: testMemoryBase,
		Value:   []byte{9},
		Freeze:  true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddCode(Code{
		ID:      "persistent",
		Address: testMemoryBase + 1,
		Value:   []byte{8},
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.EnableCode("freeze"); err != nil {
		t.Fatal(err)
	}
	if err := engine.EnableCode("persistent"); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := engine.ReadBytes(testMemoryBase, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{9, 8}) {
		t.Fatalf("after step = %v", got)
	}
	if err := wrapped.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err = engine.ReadBytes(testMemoryBase, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{9, 8}) {
		t.Fatalf("after reset = %v", got)
	}
	if err := wrapped.LoadState(bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
	got, err = engine.ReadBytes(testMemoryBase, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{9, 8}) {
		t.Fatalf("after load state = %v", got)
	}
}
