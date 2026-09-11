package ktf

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// The File specification confirms EOF=-1. Bounds exceptions are defensive
// Java behavior, consistent with readInputStreamInto, not title compatibility.
func TestKTFFileReadSignedBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		offset, count uint32
	}{
		{"issue269", ^uint32(0), 19291},
		{"negativeCount", 0, ^uint32(0)},
		{"negativeOffsetZeroCount", ^uint32(0), 0},
		{"pastEnd", 19291, 0},
		{"overrun", 19289, 2},
		{"overflow", 1, 0x7fffffff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRuntime(t)
			r.JvmContext = allocWords(t, r, 3+128)
			file := newHostObject(t, r, "org/kwis/msp/io/File")
			r.files[file] = &ktfFile{name: "/bounds.dat", mode: ktfFileReadOnly}
			r.FileData["/bounds.dat"] = []byte{1, 2, 3}
			initial := bytes.Repeat([]byte{0x5a}, 19290)
			array, err := r.newJavaByteArray(initial)
			check(t, err)
			stack := guest.DefaultStackBase + 0x100
			check(t, r.CPU.WriteRegister(cpu.RegisterSP, stack))
			check(t, r.WriteU32(stack, tc.count))
			check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
			check(t, r.CPU.WriteRegister(cpu.RegisterR2, array))
			check(t, r.CPU.WriteRegister(cpu.RegisterR3, tc.offset))
			_, err = r.handleFileMethod("read", "([BII)I")
			var exception *ktfUnhandledJavaException
			if !errors.As(err, &exception) || exception.name != "java/lang/IndexOutOfBoundsException" {
				t.Fatalf("read error = %v", err)
			}
			got, err := r.readJavaByteArray(array)
			check(t, err)
			if !bytes.Equal(got, initial) {
				t.Fatal("invalid read changed buffer")
			}
			if r.files[file].position != 0 || r.fileServices[file] != 0 {
				t.Fatal("invalid read touched file state")
			}
		})
	}
}

func TestKTFFileReadEmptyAndEOF(t *testing.T) {
	r := newTestRuntime(t)
	file := newHostObject(t, r, "org/kwis/msp/io/File")
	r.files[file] = &ktfFile{name: "/partial.dat", mode: ktfFileReadOnly}
	r.FileData["/partial.dat"] = []byte{1}
	array, err := r.newJavaByteArray([]byte{0x5a, 0x5a, 0x5a})
	check(t, err)
	got, err := r.readKTFFile(file, array, 3, 0)
	check(t, err)
	if got != 0 || r.files[file].position != 0 || r.fileServices[file] != 0 {
		t.Fatalf("empty read = %d, file = %#v", got, r.files[file])
	}
	data, err := r.readJavaByteArray(array)
	check(t, err)
	if !bytes.Equal(data, []byte{0x5a, 0x5a, 0x5a}) {
		t.Fatalf("empty read changed buffer: %v", data)
	}
	got, err = r.readKTFFile(file, array, 1, 2)
	check(t, err)
	if got != 1 || r.files[file].position != 1 {
		t.Fatalf("partial read = %d, position = %d", got, r.files[file].position)
	}
	got, err = r.readKTFFile(file, array, 0, 3)
	check(t, err)
	if got != ^uint32(0) || r.files[file].position != 1 {
		t.Fatalf("EOF read = %d, position = %d", got, r.files[file].position)
	}
	data, err = r.readJavaByteArray(array)
	check(t, err)
	if !bytes.Equal(data, []byte{0x5a, 1, 0x5a}) {
		t.Fatalf("partial/EOF buffer = %v", data)
	}
}
