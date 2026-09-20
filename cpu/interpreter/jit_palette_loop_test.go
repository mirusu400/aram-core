package interpreter

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestClassifyThumbPaletteLoop(t *testing.T) {
	words := thumbPaletteTestWords()
	code := make([]byte, len(words)*2)
	for index, word := range words {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	backend := NewJIT()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(0x1000, uint32(len(code)), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	block := backend.translateThumbBlock(0x1000)
	if block == nil || block.paletteLoop == nil {
		t.Fatalf("palette loop was not classified: block=%+v", block)
	}
}

func TestThumbPaletteLoopMatchesPreciseInterpreter(t *testing.T) {
	for _, budget := range []uint64{1, 3, 11, 12, 16, 17, 28, 29, 33, 45, 46} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise := newThumbPaletteLoopBackend(t, New())
			translated := newThumbPaletteLoopBackend(t, NewJIT())
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			got := translated.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			if got.Reason != want.Reason || got.Instructions != want.Instructions ||
				got.PC != want.PC || !sameError(got.Err, want.Err) {
				t.Fatalf("budget %d result = %+v, want %+v", budget, got, want)
			}
			for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
				gotValue, gotErr := translated.ReadRegister(register)
				wantValue, wantErr := precise.ReadRegister(register)
				if gotErr != nil || wantErr != nil || gotValue != wantValue {
					t.Fatalf(
						"budget %d register %d = 0x%08x (%v), want 0x%08x (%v)",
						budget, register, gotValue, gotErr, wantValue, wantErr,
					)
				}
			}
			gotMemory := make([]byte, 0x100)
			wantMemory := make([]byte, len(gotMemory))
			if err := translated.ReadMemory(0x4000, gotMemory); err != nil {
				t.Fatal(err)
			}
			if err := precise.ReadMemory(0x4000, wantMemory); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotMemory, wantMemory) {
				t.Fatalf("budget %d destination differs", budget)
			}
			if budget == 46 {
				statistics := translated.ExecutionStatistics()
				if statistics.AcceleratedLoopIterations != 3 ||
					statistics.AcceleratedLoopInstructions != 46 {
					t.Fatalf("palette acceleration statistics = %+v", statistics)
				}
			}
		})
	}
}

func newThumbPaletteLoopBackend(t *testing.T, backend *Backend) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = backend.Close() })
	words := thumbPaletteTestWords()
	code := make([]byte, len(words)*2)
	for index, word := range words {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	for _, mapping := range []struct {
		address uint32
		size    uint32
		perms   cpu.Permissions
	}{
		{0x1000, uint32(len(code)), cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
		{0x2000, 0x100, cpu.PermissionRead | cpu.PermissionWrite},
		{0x3000, 0x400, cpu.PermissionRead | cpu.PermissionWrite},
		{0x4000, 0x100, cpu.PermissionRead | cpu.PermissionWrite},
		{0x5000, 0x200, cpu.PermissionRead | cpu.PermissionWrite},
	} {
		if err := backend.Map(mapping.address, mapping.size, mapping.perms); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x2000, []byte{1, 0, 2}); err != nil {
		t.Fatal(err)
	}
	palette := make([]byte, 16)
	binary.LittleEndian.PutUint16(palette[12:], 0x1234)
	binary.LittleEndian.PutUint16(palette[14:], 0xabcd)
	if err := backend.WriteMemory(0x3000, palette); err != nil {
		t.Fatal(err)
	}
	destination := make([]byte, 6)
	binary.LittleEndian.PutUint16(destination[2:], 0x55aa)
	if err := backend.WriteMemory(0x4000, destination); err != nil {
		t.Fatal(err)
	}
	stack := make([]byte, 0x84)
	binary.LittleEndian.PutUint32(stack[0x20:], 1)
	binary.LittleEndian.PutUint32(stack[0x80:], 0x3000)
	if err := backend.WriteMemory(0x5000, stack); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: 0,
		cpu.RegisterR1: 0x2000,
		cpu.RegisterR2: 0x4000,
		cpu.RegisterR4: 0,
		cpu.RegisterR9: 3,
		cpu.RegisterSP: 0x5000,
	} {
		if err := backend.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	return backend
}

func thumbPaletteTestWords() []uint16 {
	// Synthetic register allocation and stack/table offsets deliberately differ
	// from any observed title while exercising the same generic loop shape.
	return []uint16{
		0x780c, 0x2c00, 0xd004, 0x9d20, 0x0064, 0x1964,
		0x8964, 0x8014, 0x1c44, 0x0424, 0x9e08, 0x1420,
		0x0c24, 0x3202, 0x1989, 0x454c, 0xdbee,
	}
}

func sameError(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Error() == right.Error()
}
