package ktf

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
)

// A KTF handset maps its boot ROM at address 0, so native code that reads
// through a null pointer gets ROM contents back instead of an abort.
// 질주쾌감 스케쳐 (상위버전) releases a sound handle by scanning three slots
// and loading the handle word through each slot's pointer, including the
// null pointer of a slot it never filled; on the device that read returns the
// exception-vector instruction, matches nothing, and the release is a no-op.
// ARAM left address 0 unmapped and stopped the whole title instead.
func TestKTFLowROMReadsBackNonzeroAndNeverExecutes(t *testing.T) {
	client := []byte{
		0x00, 0x68, // ldr r0, [r0]
		0x70, 0x47, // bx lr
		0x00, 0x47, // bx r0
	}
	runtime, err := NewRuntime(interpreter.New(), ktfloader.Package{
		Descriptor: ktfloader.Descriptor{AID: "01035B5F"},
		ClientName: "client.bin0",
		Client:     client,
	})
	check(t, err)
	defer runtime.CPU.Close()
	check(t, runtime.MapImageAndHost())

	_, word, err := runtime.call(context.Background(), ImageBase|1, []uint32{0}, 100)
	if err != nil {
		t.Fatalf("null read faulted: %v", err)
	}
	if word == 0 {
		t.Fatal("null read returned zero; the handset ROM at address 0 is not blank")
	}
	_, offsetWord, err := runtime.call(context.Background(), ImageBase|1, []uint32{0x38}, 100)
	if err != nil || offsetWord == 0 {
		t.Fatalf("small-offset null read = 0x%08x, %v", offsetWord, err)
	}
	if _, _, err := runtime.call(context.Background(), (ImageBase+4)|1, []uint32{0}, 100); err == nil {
		t.Fatal("a branch to address 0 executed the ROM page")
	}

	// A machine reset restores the page even if a stray store reached it.
	check(t, runtime.CPU.WriteMemory(0, make([]byte, 4)))
	check(t, runtime.ResetMappedMemory())
	var restored [4]byte
	check(t, runtime.CPU.ReadMemory(0, restored[:]))
	if binary.LittleEndian.Uint32(restored[:]) != word {
		t.Fatalf("reset left address 0 = % x", restored)
	}
}
