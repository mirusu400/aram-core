package ktf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

const (
	cstringRegionBase = uint32(0x00002000)
	cstringRegionSize = uint32(0x00001000)
)

func newCStringRuntime(t *testing.T) *Runtime {
	t.Helper()
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		cstringRegionBase,
		cstringRegionSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		t.Fatal(err)
	}
	return &Runtime{CPU: backend}
}

// TestReadCStringBlockBoundaries covers the block-read path: strings shorter
// than one block, strings spanning several blocks, and strings whose block
// would run past the end of a mapped region.
func TestReadCStringBlockBoundaries(t *testing.T) {
	regionEnd := cstringRegionBase + cstringRegionSize
	long := strings.Repeat("abcdefgh", 30)

	tests := []struct {
		name    string
		address uint32
		payload []byte
		limit   uint32
		want    string
	}{
		{
			name:    "within one block",
			address: cstringRegionBase,
			payload: []byte("org/kwis/msp/lcdui/Graphics\x00"),
			limit:   1024,
			want:    "org/kwis/msp/lcdui/Graphics",
		},
		{
			name:    "spans several blocks",
			address: cstringRegionBase + 0x100,
			payload: append([]byte(long), 0),
			limit:   1024,
			want:    long,
		},
		{
			name:    "empty string",
			address: cstringRegionBase + 0x400,
			payload: []byte{0},
			limit:   16,
			want:    "",
		},
		{
			// A full block read from here would cross the end of the mapped
			// region, so the reader must narrow the block instead of faulting
			// on a string that terminates before the boundary.
			name:    "terminates just before the region end",
			address: regionEnd - 3,
			payload: []byte("ok\x00"),
			limit:   1024,
			want:    "ok",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newCStringRuntime(t)
			if err := runtime.CPU.WriteMemory(
				test.address,
				test.payload,
			); err != nil {
				t.Fatal(err)
			}
			got, err := runtime.readCString(test.address, test.limit)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("readCString = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReadCStringUnterminatedAtRegionEnd(t *testing.T) {
	runtime := newCStringRuntime(t)
	address := cstringRegionBase + cstringRegionSize - 3
	if err := runtime.CPU.WriteMemory(address, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.readCString(address, 1024); err == nil {
		t.Fatalf("readCString = %q, want a fault past the region end", got)
	}
}

func TestReadCStringUnterminatedWithinLimit(t *testing.T) {
	runtime := newCStringRuntime(t)
	if err := runtime.CPU.WriteMemory(
		cstringRegionBase,
		bytes.Repeat([]byte("x"), 200),
	); err != nil {
		t.Fatal(err)
	}
	_, err := runtime.readCString(cstringRegionBase, 100)
	if err == nil || !strings.Contains(err.Error(), "not terminated") {
		t.Fatalf("readCString error = %v, want a not-terminated report", err)
	}
}

func TestReadCStringNullPointer(t *testing.T) {
	runtime := newCStringRuntime(t)
	if _, err := runtime.readCString(0, 64); err == nil {
		t.Fatal("readCString(0) succeeded, want an error")
	}
}
