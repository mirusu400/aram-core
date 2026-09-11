package raptor

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/internal/ime"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestRaptorInputMethodModes(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public}
	check(t, r.InstallInterfaces())
	count, _, handled, err := r.DispatchPrivateImport(300)
	check(t, err)
	if !handled || count.Low != 4 {
		t.Fatalf("IME mode count = %d, handled=%t, want 4", count.Low, handled)
	}
	table, _, _, err := r.DispatchPrivateImport(301)
	check(t, err)
	for i, want := range []string{"EN/S", "EN/L", "N123", "KO"} {
		p, err := public.ReadU32(table.Low + uint32(i*4))
		check(t, err)
		got, err := public.ReadCString(p)
		check(t, err)
		if string(got) != want {
			t.Fatalf("mode %d = %q, want %q", i, got, want)
		}
	}
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, 3))
	result, _, _, err := r.DispatchPrivateImport(302)
	check(t, err)
	if result.Low != 1 {
		t.Fatalf("set mode = %d, want 1", result.Low)
	}
	current, _, handled, err := r.DispatchPrivateImport(303)
	check(t, err)
	if !handled || current.Low != 3 {
		t.Fatalf("current mode=%d, handled=%t", current.Low, handled)
	}
}

func TestRaptorInputMethodComposesSeparateCommitAndPreeditBuffers(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public}
	check(t, r.InstallInterfaces())
	memory, err := public.Heap.Allocate(128, true)
	check(t, err)
	check(t, r.CPU.WriteRegister(cpu.RegisterSP, memory+96))
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, 3))
	_, _, _, err = r.DispatchPrivateImport(302)
	check(t, err)
	for _, step := range []struct {
		key                int32
		committed, preedit string
	}{
		{'4', "", "ㄱ"}, {'1', "", "기"}, {'2', "", "가"}, {'5', "", "간"}, {'1', "가", "니"}, {-99, "니", ""},
	} {
		check(t, public.WriteU32(memory+64, 16))
		check(t, public.WriteU32(memory+68, 16))
		check(t, public.WriteU32(memory+96, memory+32))
		check(t, public.WriteU32(memory+100, memory+68))
		for reg, value := range []uint32{uint32(step.key), 502, memory, memory + 64} {
			check(t, r.CPU.WriteRegister(uint32(reg), value))
		}
		result, _, handled, err := r.DispatchPrivateImport(304)
		check(t, err)
		if !handled || result.Low != 1 {
			t.Fatalf("key %d: result=%d handled=%t", step.key, result.Low, handled)
		}
		for i, want := range []string{step.committed, step.preedit} {
			size, err := public.ReadU32(memory + 64 + uint32(i*4))
			check(t, err)
			encoded, err := public.Services.Text.Encode(want, shared.EncodingEUCKR)
			check(t, err)
			if size != uint32(len(encoded)) {
				t.Fatalf("key %d buffer %d size=%d want %d", step.key, i, size, len(encoded))
			}
			got := make([]byte, size)
			check(t, r.CPU.ReadMemory(memory+uint32(i*32), got))
			if !bytes.Equal(got, encoded) {
				t.Fatalf("key %d buffer %d = %x, want %x", step.key, i, got, encoded)
			}
		}
	}
}

func TestRaptorInputMethodRejectsShortBufferWithoutAdvancing(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public}
	memory, err := public.Heap.Allocate(128, true)
	check(t, err)
	check(t, r.CPU.WriteRegister(cpu.RegisterSP, memory+96))
	check(t, public.WriteU32(memory+96, memory+32))
	check(t, public.WriteU32(memory+100, memory+68))
	r.inputMethod().automata.SetMode(ime.ModeKorean)
	before := *r.inputMethod()
	for _, capacity := range []uint32{0, 1, ^uint32(0)} {
		check(t, r.CPU.WriteMemory(memory, bytes.Repeat([]byte{0xa5}, 72)))
		check(t, public.WriteU32(memory+64, 16))
		check(t, public.WriteU32(memory+68, capacity))
		for reg, value := range []uint32{'4', 502, memory, memory + 64} {
			check(t, r.CPU.WriteRegister(uint32(reg), value))
		}
		result, _, _, err := r.DispatchPrivateImport(304)
		check(t, err)
		if result.Low != 0 || *r.inputMethod() != before {
			t.Fatalf("capacity %d consumed input: result=%d", capacity, result.Low)
		}
		var output [64]byte
		check(t, r.CPU.ReadMemory(memory, output[:]))
		if !bytes.Equal(output[:], bytes.Repeat([]byte{0xa5}, 64)) {
			t.Fatalf("capacity %d changed output buffers", capacity)
		}
	}
	check(t, public.WriteU32(memory+68, 16))
	result, _, _, err := r.DispatchPrivateImport(304)
	check(t, err)
	if result.Low != 1 || r.inputMethod().preedit != 'ㄱ' {
		t.Fatal("retry did not produce the first consonant")
	}
	// A release and an unsupported key must not rotate the current candidate.
	before = *r.inputMethod()
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, '4'))
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, 503))
	result, _, _, err = r.DispatchPrivateImport(304)
	check(t, err)
	if result.Low != 0 || *r.inputMethod() != before {
		t.Fatal("release changed composition")
	}
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, 0xfffffff0))
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, 502))
	result, _, _, err = r.DispatchPrivateImport(304)
	check(t, err)
	if result.Low != 0 || *r.inputMethod() != before {
		t.Fatal("unhandled key changed composition")
	}
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, 4))
	result, _, _, err = r.DispatchPrivateImport(302)
	check(t, err)
	if result.Low != 0 || *r.inputMethod() != before {
		t.Fatal("invalid mode changed composition")
	}
}

func TestRaptorInputMethodStateRoundTripAndLegacyModes(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public}
	check(t, r.RestoreImage())
	check(t, r.InstallInterfaces())
	r.inputMethod().automata.SetMode(ime.ModeKorean)
	r.inputMethod().automata.Press('4')
	r.inputMethod().preedit = 'ㄱ'
	var saved bytes.Buffer
	w := guest.NewStateWriter(&saved)
	check(t, WriteState(r, r.CPU, w))
	check(t, w.Err)
	d := &guest.StateDecoder{Reader: bytes.NewReader(saved.Bytes())}
	state, err := ParseState(r, d)
	check(t, err)
	r.ime = nil
	check(t, RestoreState(r, r.CPU, state))
	if r.ime.preedit != 'ㄱ' || r.ime.automata.CurrentMode() != ime.ModeKorean {
		t.Fatal("state lost IME mode or preedit")
	}
	for _, schema := range []uint32{2, 3} {
		legacy := append([]byte(nil), saved.Bytes()[:28]...)
		if schema == 2 {
			legacy = legacy[:24]
		}
		binary.LittleEndian.PutUint32(legacy[4:], schema)
		d := &guest.StateDecoder{Reader: bytes.NewReader(legacy)}
		state, err := ParseState(r, d)
		check(t, err)
		// An old public-memory snapshot can carry the former /L,/S table.
		check(t, r.CPU.WriteMemory(IMEModeTable, bytes.Repeat([]byte{0}, 40)))
		check(t, RestoreState(r, r.CPU, state))
		pointer, err := public.ReadU32(IMEModeTable + 12)
		check(t, err)
		mode, err := public.ReadCString(pointer)
		check(t, err)
		if string(mode) != "KO" || r.inputMethod().preedit != 0 {
			t.Fatalf("schema %d did not restore immutable mode table/default IME", schema)
		}
	}
}
