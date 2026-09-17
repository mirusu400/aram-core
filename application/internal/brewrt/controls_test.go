package brewrt

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestLegacyTextControlRoundTripsUTF16State(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt, outputAt := heapBase+0x100, heapBase+0x200
	want := []byte{'A', 0, 'B', 0, 0, 0}
	if err := runtime.cpu.WriteMemory(textAt, want); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{cpu.RegisterR1: textAt, cpu.RegisterLR: returnTrap | 1} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if handled, err := runtime.handleTextControl(12); err != nil || !handled {
		t.Fatalf("SetText handled=%v err=%v", handled, err)
	}
	for register, value := range map[uint32]uint32{cpu.RegisterR1: outputAt, cpu.RegisterR2: 3} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if handled, err := runtime.handleTextControl(13); err != nil || !handled {
		t.Fatalf("GetText handled=%v err=%v", handled, err)
	}
	got := make([]byte, len(want))
	if err := runtime.cpu.ReadMemory(outputAt, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("text = %v, want %v", got, want)
	}
}
