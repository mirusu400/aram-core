package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/internal/ime"
	shared "github.com/mirusu400/aram-core/runtime"
	"reflect"
	"testing"
)

func TestKTFWIPICInputBuffers(t *testing.T) {
	r := newTestRuntime(t)
	call := func(slot int, args ...uint32) uint32 {
		t.Helper()
		stack := heapAlloc(t, r, 32, true)
		check(t, r.CPU.WriteRegister(cpu.RegisterSP, stack))
		for i, v := range args {
			if i < 4 {
				check(t, r.CPU.WriteRegister(cpu.RegisterR0+uint32(i), v))
			} else {
				check(t, r.writeWords(stack+uint32(i-4)*4, []uint32{v}))
			}
		}
		result, err := ktfWIPICHandler(ktfWIPICMasterInput, slot)(context.Background(), r)
		check(t, err)
		return result
	}
	if got := call(1, 0); got != 1 {
		t.Fatalf("set mode = %d, want 1", got)
	}
	out1 := heapAlloc(t, r, 8, true)
	out2 := heapAlloc(t, r, 8, true)
	size1 := heapAlloc(t, r, 4, true)
	size2 := heapAlloc(t, r, 4, true)
	check(t, r.writeWords(size1, []uint32{8}))
	check(t, r.writeWords(size2, []uint32{8}))
	step := func(key uint32, want1, want2 string) {
		t.Helper()
		if got := call(0, key, 2, out1, size1, out2, size2); got != 1 {
			t.Fatalf("handle %x = %d", key, got)
		}
		for _, v := range []struct {
			p    uint32
			want string
		}{{out1, want1}, {out2, want2}} {
			got, err := r.readCString(v.p, 8)
			check(t, err)
			encoded, err := r.Services.Text.Encode(v.want, shared.EncodingEUCKR)
			check(t, err)
			if got != string(encoded) {
				t.Fatalf("text = %q want %q", got, v.want)
			}
		}
	}
	step('2', "", "a")
	step('2', "", "b")
	var savedBytes bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&savedBytes)))
	step(0x9d, "b", "")
	decoder := guest.StateDecoder{Reader: bytes.NewReader(savedBytes.Bytes())}
	saved, err := ParseState(r, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	step('3', "b", "d")
	step(0x9d, "d", "")
	// Capacity errors must leave both destinations and composition untouched.
	check(t, r.writeWords(size2, []uint32{1}))
	sentinel := bytes.Repeat([]byte{0xa5}, 8)
	check(t, r.CPU.WriteMemory(out1, sentinel))
	check(t, r.CPU.WriteMemory(out2, sentinel))
	if got := call(0, '2', 2, out1, size1, out2, size2); got != 0 {
		t.Fatalf("short buffer = %d", got)
	}
	for _, p := range []uint32{out1, out2} {
		b := make([]byte, 8)
		check(t, r.CPU.ReadMemory(p, b))
		if !bytes.Equal(b, sentinel) {
			t.Fatalf("short buffer mutated output: %x", b)
		}
	}
	check(t, r.writeWords(size2, []uint32{8}))
	step('2', "", "a")
	if got := call(1, 2); got != 1 {
		t.Fatalf("numeric mode = %d", got)
	}
	step('7', "7", "")
	if got := call(2); got != 2 {
		t.Fatalf("current mode = %d", got)
	}
	if got := call(1, 99); got != 0 {
		t.Fatalf("invalid mode = %d", got)
	}
	if got := call(2); got != 2 {
		t.Fatalf("invalid mode changed state = %d", got)
	}
	if got := call(1, 3); got != 1 {
		t.Fatalf("Korean mode = %d", got)
	}
	step('4', "", "\u3131")
	step('1', "", "\uae30")
	step(0x9d, "\uae30", "")
	if readU32(t, r, size1) != 8 || readU32(t, r, size2) != 8 {
		t.Fatal("input capacities mutated")
	}
	if got := call(1, 0); got != 1 {
		t.Fatal("set lower mode")
	}
	step('2', "", "a")
	before := r.wipicInput
	if got := call(0, '3', 1, out1, size1, out2, size2); got != 0 {
		t.Fatalf("non-press = %d", got)
	}
	if r.wipicInput != before {
		t.Fatal("non-press changed composition")
	}
	if got := call(0, '#', 2, out1, size1, out2, size2); got != 0 {
		t.Fatalf("unhandled = %d", got)
	}
	completed, err := r.readCString(out1, 8)
	check(t, err)
	if completed != "a" || r.wipicInput.Pending != 0 {
		t.Fatal("unhandled key did not commit pending text")
	}
}

func TestKTFWIPICInputStateCompatibility(t *testing.T) {
	r := newTestRuntime(t)
	var buffer bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&buffer)))
	legacy := append([]byte(nil), buffer.Bytes()[:buffer.Len()-44]...)
	binary.LittleEndian.PutUint32(legacy[4:8], ktfStateSchemaV12)
	decoder := guest.StateDecoder{Reader: bytes.NewReader(legacy)}
	saved, err := ParseState(r, &decoder)
	check(t, err)
	if saved.wipicInput != (ktfWIPICInputState{}) {
		t.Fatal("legacy input is not fresh")
	}
	a := ime.New(ime.ModeENLower)
	a.Press('2')
	r.wipicInput = ktfWIPICInputState{Automata: a.Snapshot(), Pending: 'a', Initialized: true}
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	out1, out2 := heapAlloc(t, r, 8, true), heapAlloc(t, r, 8, true)
	size1, size2 := heapAlloc(t, r, 4, true), heapAlloc(t, r, 4, true)
	check(t, r.writeWords(size1, []uint32{8}))
	check(t, r.writeWords(size2, []uint32{8}))
	stack := heapAlloc(t, r, 8, true)
	check(t, r.CPU.WriteRegister(cpu.RegisterSP, stack))
	check(t, r.writeWords(stack, []uint32{out2, size2}))
	handle := func(key uint32, want1, want2 string) {
		t.Helper()
		for i, v := range []uint32{key, 2, out1, size1} {
			check(t, r.CPU.WriteRegister(cpu.RegisterR0+uint32(i), v))
		}
		got, err := ktfWIPICInputHandle(context.Background(), r)
		check(t, err)
		if got != 1 {
			t.Fatalf("input after legacy restore = %d", got)
		}
		one, err := r.readCString(out1, 8)
		check(t, err)
		two, err := r.readCString(out2, 8)
		check(t, err)
		if one != want1 || two != want2 {
			t.Fatalf("legacy continuation = %q/%q, want %q/%q", one, two, want1, want2)
		}
	}
	handle(0x9d, "", "")
	handle('3', "", "d")
	handle(0x9d, "d", "")
	for _, index := range []int{0, 2, 3, 4, 5, 6, 8, 9, 10} {
		corrupt := append([]byte(nil), buffer.Bytes()...)
		binary.LittleEndian.PutUint32(corrupt[len(corrupt)-44+index*4:], 0x7fffffff)
		decoder = guest.StateDecoder{Reader: bytes.NewReader(corrupt)}
		if _, err := ParseState(r, &decoder); err == nil {
			t.Fatalf("invalid input word %d accepted", index)
		}
	}
}

func TestKTFWIPICInputDotStateContinuation(t *testing.T) {
	for _, presses := range []int{1, 2} {
		r := newTestRuntime(t)
		a := ime.New(ime.ModeKorean)
		var pending rune
		for i := 0; i < presses; i++ {
			ops, _ := a.Press('2')
			if len(ops) != 0 {
				pending = ops[len(ops)-1].Char
			}
		}
		r.wipicInput = ktfWIPICInputState{Automata: a.Snapshot(), Pending: pending, Initialized: true}
		var buffer bytes.Buffer
		check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&buffer)))
		decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
		saved, err := ParseState(r, &decoder)
		check(t, err)
		r.wipicInput = ktfWIPICInputState{}
		started := false
		check(t, RestoreState(r, r.CPU, saved, &started))
		if r.wipicInput.Pending != pending {
			t.Fatal("pending dot was lost")
		}
		restored := r.wipicInput.automata()
		want, wantHandled := a.Press('1')
		got, handled := restored.Press('1')
		if handled != wantHandled || !reflect.DeepEqual(got, want) || restored.Snapshot() != a.Snapshot() {
			t.Fatalf("dot count %d continuation mismatch", presses)
		}
	}
}
