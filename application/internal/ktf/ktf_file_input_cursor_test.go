package ktf

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFFileInputStreamSharesCursor(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		name := "directDataInputStream"
		if wrapped {
			name = "wrappedInputStream"
		}
		t.Run(name, func(t *testing.T) {
			r := newTestRuntime(t)
			r.JvmContext = allocWords(t, r, 3+128)
			file := newHostObject(t, r, "org/kwis/msp/io/File")
			r.files[file] = &ktfFile{name: "/header.dat", mode: ktfFileReadWrite}
			r.FileData["/header.dat"] = []byte{0, 0, 0, 2, 0xaa, 0xbb}
			_, setupErr := r.ensureKTFFileService(file)
			check(t, setupErr)
			check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
			method, descriptor := "openDataInputStream", "()Ljava/io/DataInputStream;"
			if wrapped {
				method, descriptor = "openInputStream", "()Ljava/io/InputStream;"
			}
			stream, err := r.handleFileMethod(method, descriptor)
			check(t, err)
			if wrapped {
				wrapper := newHostObject(t, r, "java/io/DataInputStream")
				check(t, r.CPU.WriteRegister(cpu.RegisterR1, wrapper))
				check(t, r.CPU.WriteRegister(cpu.RegisterR2, stream))
				_, err = r.handleInputStreamMethod(context.Background(), "<init>", "(Ljava/io/InputStream;)V")
				check(t, err)
				stream = wrapper
			}
			readStream := func(method, descriptor string) uint32 {
				t.Helper()
				check(t, r.CPU.WriteRegister(cpu.RegisterR1, stream))
				value, err := r.handleInputStreamMethod(context.Background(), method, descriptor)
				check(t, err)
				return value
			}
			if got := readStream("readInt", "()I"); got != 2 {
				t.Fatalf("header = %d", got)
			}
			if r.files[file].position != 4 {
				t.Fatalf("stream header left File cursor at %d, want 4", r.files[file].position)
			}
			array, err := r.newJavaByteArray(make([]byte, 2))
			check(t, err)
			got, err := r.readKTFFile(file, array, 0, 2)
			check(t, err)
			data, err := r.readJavaByteArray(array)
			check(t, err)
			if got != 2 || !bytes.Equal(data, []byte{0xaa, 0xbb}) {
				t.Fatalf("raw payload = %d, %v", got, data)
			}
			if got := readStream("read", "()I"); got != ^uint32(0) {
				t.Fatalf("stream after raw read = %d, want EOF", got)
			}
			check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
			check(t, r.CPU.WriteRegister(cpu.RegisterR2, 4))
			_, err = r.handleFileMethod("seek", "(I)V")
			check(t, err)
			if got := readStream("readUnsignedByte", "()I"); got != 0xaa {
				t.Fatalf("stream after raw seek = %x", got)
			}
			if r.files[file].position != 5 {
				t.Fatalf("cursor after stream read = %d", r.files[file].position)
			}
			// Writes through the file must be visible to an already-open stream.
			_, err = r.writeKTFFile(file, []byte{0xcc})
			check(t, err)
			check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
			check(t, r.CPU.WriteRegister(cpu.RegisterR2, 5))
			_, err = r.handleFileMethod("seek", "(I)V")
			check(t, err)
			if got := readStream("readUnsignedByte", "()I"); got != 0xcc {
				t.Fatalf("stream after write = %x", got)
			}
		})
	}
}

func TestKTFFileInputStreamCursorStateAndBoundaries(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	file := newHostObject(t, r, "org/kwis/msp/io/File")
	r.files[file] = &ktfFile{name: "/cursor.dat", mode: ktfFileReadOnly}
	r.FileData["/cursor.dat"] = []byte{10, 20, 30, 40}
	_, err := r.ensureKTFFileService(file)
	check(t, err)
	open := func() uint32 {
		t.Helper()
		check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
		stream, err := r.handleFileMethod("openInputStream", "()Ljava/io/InputStream;")
		check(t, err)
		return stream
	}
	first, second := open(), open()
	call := func(stream uint32, method, descriptor string, argument uint32) uint32 {
		t.Helper()
		check(t, r.CPU.WriteRegister(cpu.RegisterR1, stream))
		check(t, r.CPU.WriteRegister(cpu.RegisterR2, argument))
		check(t, r.CPU.WriteRegister(cpu.RegisterR3, 0))
		value, err := r.handleInputStreamMethod(context.Background(), method, descriptor)
		check(t, err)
		return value
	}
	seek := func(position uint32) {
		t.Helper()
		check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
		check(t, r.CPU.WriteRegister(cpu.RegisterR2, position))
		_, err := r.handleFileMethod("seek", "(I)V")
		check(t, err)
	}
	if got := call(first, "read", "()I", 0); got != 10 {
		t.Fatalf("first read = %d", got)
	}
	if got := call(second, "available", "()I", 0); got != 3 || r.files[file].position != 1 {
		t.Fatalf("second available = %d, cursor = %d", got, r.files[file].position)
	}
	if got := call(second, "skipBytes", "(I)I", 2); got != 2 || r.files[file].position != 3 {
		t.Fatalf("skip = %d, cursor = %d", got, r.files[file].position)
	}
	if got := call(first, "read", "()I", 0); got != 40 {
		t.Fatalf("first read after second skip = %d", got)
	}
	seek(8)
	for _, method := range []struct{ name, descriptor string }{
		{"available", "()I"}, {"skipBytes", "(I)I"}, {"skip", "(J)J"},
	} {
		if got := call(first, method.name, method.descriptor, 1); got != 0 || r.files[file].position != 8 {
			t.Fatalf("%s beyond EOF = %d, cursor = %d", method.name, got, r.files[file].position)
		}
	}
	seek(1)
	call(first, "mark", "(I)V", 100)
	if got := call(second, "skip", "(J)J", 1); got != 1 {
		t.Fatalf("skip long = %d", got)
	}
	call(first, "reset", "()V", 0)
	if r.files[file].position != 1 {
		t.Fatalf("reset cursor = %d", r.files[file].position)
	}

	var buffer bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&buffer)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(r, &decoder)
	check(t, err)
	call(second, "read", "()I", 0)
	delete(r.fileStreamTargets, first)
	delete(r.fileStreamTargets, second)
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	if !started || r.fileStreamTargets[first] != file || r.fileStreamTargets[second] != file {
		t.Fatal("state restore lost file stream links")
	}
	if got := call(second, "read", "()I", 0); got != 20 || r.files[file].position != 2 {
		t.Fatalf("read after restore = %d, cursor = %d", got, r.files[file].position)
	}
	call(first, "close", "()V", 0)
	if r.files[file].closed || r.fileStreamTargets[first] != 0 || r.fileStreamTargets[second] != file {
		t.Fatal("closing one input stream changed the File or other stream")
	}
	if got := call(second, "read", "()I", 0); got != 30 {
		t.Fatalf("remaining stream after close = %d", got)
	}
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
	_, err = r.handleFileMethod("close", "()V")
	check(t, err)
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, second))
	_, err = r.handleInputStreamMethod(context.Background(), "read", "()I")
	var exception *ktfUnhandledJavaException
	if !errors.As(err, &exception) || exception.name != "java/io/IOException" {
		t.Fatalf("stream read after File.close error = %v", err)
	}
}

func TestKTFFileInputStreamPartialEOF(t *testing.T) {
	for _, tc := range []struct {
		name, descriptor string
		data             []byte
	}{
		{"readInt", "()I", []byte{1, 2}},
		{"readFully", "([B)V", []byte{1, 2}},
		{"readUTF", "()Ljava/lang/String;", []byte{0, 3, 'a'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRuntime(t)
			r.JvmContext = allocWords(t, r, 3+128)
			file := newHostObject(t, r, "org/kwis/msp/io/File")
			r.files[file] = &ktfFile{name: "/partial.dat", mode: ktfFileReadOnly}
			r.FileData["/partial.dat"] = tc.data
			_, err := r.ensureKTFFileService(file)
			check(t, err)
			check(t, r.CPU.WriteRegister(cpu.RegisterR1, file))
			stream, err := r.handleFileMethod("openDataInputStream", "()Ljava/io/DataInputStream;")
			check(t, err)
			array, err := r.newJavaByteArray([]byte{0x5a, 0x5a, 0x5a})
			check(t, err)
			check(t, r.CPU.WriteRegister(cpu.RegisterR1, stream))
			check(t, r.CPU.WriteRegister(cpu.RegisterR2, array))
			_, err = r.handleInputStreamMethod(context.Background(), tc.name, tc.descriptor)
			var exception *ktfUnhandledJavaException
			if !errors.As(err, &exception) || exception.name != "java/io/EOFException" {
				t.Fatalf("partial read error = %v", err)
			}
			if r.files[file].position != uint32(len(tc.data)) {
				t.Fatalf("partial read cursor = %d", r.files[file].position)
			}
			if tc.name == "readFully" {
				data, err := r.readJavaByteArray(array)
				check(t, err)
				if !bytes.Equal(data, []byte{1, 2, 0x5a}) {
					t.Fatalf("partial readFully buffer = %v", data)
				}
			}
			// The storage service cursor must agree with the Java mirrors too.
			data, err := r.readKTFFileBytes(file, 1)
			check(t, err)
			if len(data) != 0 {
				t.Fatalf("raw file after partial EOF returned %v", data)
			}
		})
	}
}
