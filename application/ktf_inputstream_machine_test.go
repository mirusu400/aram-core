package application

import (
	"bytes"
	"context"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
)

func TestKTFDataInputStreamReadFullyUsesApplicationSourceThroughMachine(t *testing.T) {
	t.Run("application virtual source", func(t *testing.T) {
		machine := newKTFInputStreamMachine(t)
		runtime := machine.ktf
		source := newSyntheticKTFApplicationInputStream(
			t,
			runtime,
			(ktfrt.ImageBase+0x30)|1,
		)
		wrapper, err := runtime.NewHostJavaObject("java/io/DataInputStream")
		check(t, err)
		output, err := runtime.NewJavaArray("[B", 3, 1)
		check(t, err)

		check(t, runtime.QueueJavaVirtual(
			wrapper,
			"<init>",
			"(Ljava/io/InputStream;)V",
			source,
		))
		check(t, runtime.QueueJavaVirtual(wrapper, "readFully", "([B)V", output))

		if err := machine.Start(context.Background()); err != nil {
			t.Fatalf("public Machine.Start delegated readFully: %v", err)
		}
		if machine.State() == machinecore.StateFaulted {
			t.Fatalf("public Machine faulted after delegated readFully: %+v", machine.LastResult())
		}
		snapshot := machine.DebugSnapshot(DefaultDebugSnapshotEntries)
		if snapshot.KTF == nil {
			t.Fatal("public Machine debug snapshot has no KTF state")
		}
		if snapshot.KTF.FirstJavaThrow != "" || len(snapshot.KTF.IsolatedTaskFaults) != 0 {
			t.Fatalf(
				"delegated readFully threw through public Machine: first=%q isolated=%+v",
				snapshot.KTF.FirstJavaThrow,
				snapshot.KTF.IsolatedTaskFaults,
			)
		}
		if got := readKTFTestByteArray(t, runtime, output); !bytes.Equal(
			got,
			[]byte{0x7f, 0x7f, 0x7f},
		) {
			t.Fatalf("delegated readFully bytes = %v", got)
		}
	})

	t.Run("host buffer control", func(t *testing.T) {
		machine := newKTFInputStreamMachine(t)
		runtime := machine.ktf
		input, err := runtime.NewJavaArray("[B", 3, 1)
		check(t, err)
		writeKTFTestByteArray(t, runtime, input, []byte{0x11, 0x22, 0x33})
		source, err := runtime.NewHostJavaObject("java/io/ByteArrayInputStream")
		check(t, err)
		wrapper, err := runtime.NewHostJavaObject("java/io/DataInputStream")
		check(t, err)
		output, err := runtime.NewJavaArray("[B", 3, 1)
		check(t, err)

		check(t, runtime.QueueJavaVirtual(source, "<init>", "([B)V", input))
		check(t, runtime.QueueJavaVirtual(
			wrapper,
			"<init>",
			"(Ljava/io/InputStream;)V",
			source,
		))
		check(t, runtime.QueueJavaVirtual(wrapper, "readFully", "([B)V", output))

		if err := machine.Start(context.Background()); err != nil {
			t.Fatalf("public Machine.Start host-buffer readFully: %v", err)
		}
		if machine.State() == machinecore.StateFaulted {
			t.Fatalf("public Machine faulted after host-buffer readFully: %+v", machine.LastResult())
		}
		if got := readKTFTestByteArray(t, runtime, output); !bytes.Equal(
			got,
			[]byte{0x11, 0x22, 0x33},
		) {
			t.Fatalf("host-buffer readFully bytes = %v", got)
		}
	})
}

func newKTFInputStreamMachine(t *testing.T) *Machine {
	t.Helper()
	client := syntheticKTFClient()
	copy(client[0x30:], []byte{
		0x7f, 0x20, // movs r0, #0x7f
		0x70, 0x47, // bx lr
	})
	jar := testZIP(t, map[string][]byte{"client.bin4096": client})
	archive := testZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:PD000001\nAID:01020304\nMClass:GameMain\n"),
	})
	created, err := NewFactory().Create(
		context.Background(),
		machinecore.Source{
			Name:     "inputstream.zip",
			ReaderAt: bytes.NewReader(archive),
			Size:     int64(len(archive)),
		},
	)
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })

	// The package is only a synthetic Java-task fixture. Factory.Create has
	// already exercised the real KTF bootstrap/initialization path; mark its
	// nonexistent GameMain as started so public Machine.Start runs the queued
	// Java tasks below instead of trying to load a class the fixture does not
	// carry.
	machine.ktfStarted = true
	return machine
}

func newSyntheticKTFApplicationInputStream(
	t *testing.T,
	runtime *ktfrt.Runtime,
	readBody uint32,
) uint32 {
	t.Helper()
	parent, err := runtime.EnsureJavaClass("java/io/InputStream")
	check(t, err)
	classAddress, err := runtime.AllocateWords(5)
	check(t, err)
	descriptor, err := runtime.AllocateWords(9)
	check(t, err)
	method, err := runtime.AllocateWords(7)
	check(t, err)
	methodList, err := runtime.AllocateWords(2)
	check(t, err)
	vtable, err := runtime.AllocateWords(2)
	check(t, err)
	name := allocateKTFTestBytes(t, runtime, []byte("test/ApplicationInputStream\x00"))
	fullName := allocateKTFTestBytes(t, runtime, append(
		[]byte{0},
		[]byte("()I+read\x00")...,
	))

	writeKTFTestWords(t, runtime, method, []uint32{
		readBody,
		classAddress,
		0,
		fullName,
		0,
		uint32(0x0001) << 16,
		0,
	})
	writeKTFTestWords(t, runtime, methodList, []uint32{method, 0})
	writeKTFTestWords(t, runtime, vtable, []uint32{method, 0})
	writeKTFTestWords(t, runtime, descriptor, []uint32{
		name,
		0,
		parent,
		methodList,
		0,
		0,
		1,
		0x21,
		0,
	})
	writeKTFTestWords(t, runtime, classAddress, []uint32{
		classAddress + 4,
		0,
		descriptor,
		vtable,
		8 << 16,
	})
	class, err := runtime.InspectJavaClass(classAddress)
	check(t, err)
	source, err := runtime.NewJavaInstanceForClass(class)
	check(t, err)
	return source
}

func allocateKTFTestBytes(t *testing.T, runtime *ktfrt.Runtime, data []byte) uint32 {
	t.Helper()
	words := uint32((len(data) + 3) / 4)
	if words == 0 {
		words = 1
	}
	address, err := runtime.AllocateWords(words)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(address, data))
	return address
}

func writeKTFTestWords(
	t *testing.T,
	runtime *ktfrt.Runtime,
	address uint32,
	words []uint32,
) {
	t.Helper()
	for index, word := range words {
		check(t, runtime.WriteU32(address+uint32(index)*4, word))
	}
}

func writeKTFTestByteArray(
	t *testing.T,
	runtime *ktfrt.Runtime,
	array uint32,
	data []byte,
) {
	t.Helper()
	fields, err := runtime.ReadU32(array)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(fields+8, data))
}

func readKTFTestByteArray(
	t *testing.T,
	runtime *ktfrt.Runtime,
	array uint32,
) []byte {
	t.Helper()
	fields, err := runtime.ReadU32(array)
	check(t, err)
	length, err := runtime.ReadU32(fields + 4)
	check(t, err)
	data := make([]byte, length)
	check(t, runtime.CPU.ReadMemory(fields+8, data))
	return data
}
