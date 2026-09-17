package brewrt

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func buildResourceFile(kind, id uint16, payload []byte) []byte {
	const indexOffset = 0x20
	const tableOffset = 0x28
	const dataOffset = 0x30
	data := make([]byte, dataOffset+len(payload))
	binary.LittleEndian.PutUint16(data[0:], brewResourceMagic)
	binary.LittleEndian.PutUint16(data[6:], 1)
	binary.LittleEndian.PutUint32(data[8:], indexOffset)
	binary.LittleEndian.PutUint32(data[12:], 8)
	binary.LittleEndian.PutUint32(data[16:], tableOffset)
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint32(data[24:], dataOffset)
	binary.LittleEndian.PutUint16(data[indexOffset:], kind)
	binary.LittleEndian.PutUint16(data[indexOffset+2:], id)
	binary.LittleEndian.PutUint16(data[indexOffset+4:], 0)
	binary.LittleEndian.PutUint16(data[indexOffset+6:], 0)
	binary.LittleEndian.PutUint32(data[tableOffset:], dataOffset)
	binary.LittleEndian.PutUint32(data[tableOffset+4:], uint32(len(data)))
	copy(data[dataOffset:], payload)
	return data
}

func TestResourceDataValidatesRangesAndReturnsExactSection(t *testing.T) {
	file := buildResourceFile(0x5000, 7, []byte("payload"))
	got, ok := resourceData(file, 0x5000, 7)
	if !ok || !bytes.Equal(got, []byte("payload")) {
		t.Fatalf("resource data = %q ok=%v", got, ok)
	}
	if _, ok := resourceData(file[:len(file)-1], 0x5000, 7); ok {
		t.Fatal("truncated resource section was accepted")
	}
	if _, ok := resourceData(file, 6, 7); ok {
		t.Fatal("wrong resource type was accepted")
	}
}

func TestLoadShellResourceDataCopiesOwnedGuestBlock(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.files = map[string][]byte{"assets/game.bar": buildResourceFile(6, 42, []byte{1, 2, 3, 4})}
	pathAt := heapBase + 0x100
	if err := runtime.cpu.WriteMemory(pathAt, []byte("game.bar\x00")); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: pathAt,
		cpu.RegisterR2: 42,
		cpu.RegisterR3: 6,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 18*2 + 2)
	if err != nil || !handled {
		t.Fatalf("LoadResData handled=%v err=%v", handled, err)
	}
	pointer, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || pointer < heapBase {
		t.Fatalf("LoadResData pointer=0x%08x err=%v", pointer, err)
	}
	got := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(pointer, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("loaded resource = %v", got)
	}
}
