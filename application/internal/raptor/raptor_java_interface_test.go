package raptor

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorJavaLinkPublishesInterfaceMethods(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	block, err := public.Heap.Allocate(0x800, true)
	check(t, err)
	if block == 0 {
		t.Fatal("allocate link fixture")
	}
	className, err := runtime.allocateJavaCString("org/kwis/msf/io/Socket")
	check(t, err)
	methods := []raptorJavaMethod{
		{Name: "close", descriptor: "()V"},
		{Name: "getInputStream", descriptor: "()Ljava/io/InputStream;"},
		{Name: "getOutputStream", descriptor: "()Ljava/io/OutputStream;"},
	}

	const (
		importedClassesOffset        = uint32(0x000)
		interfaceMethodsOffset       = uint32(0x100)
		virtualMethodsOffset         = uint32(0x200)
		staticMethodsOffset          = uint32(0x280)
		interfaceMethodOffsetsOffset = uint32(0x300)
		fieldOffsetsOffset           = uint32(0x400)
		staticFieldOffsetsOffset     = uint32(0x410)
		virtualMethodOffsetsOffset   = uint32(0x500)
		staticMethodOffsetsOffset    = uint32(0x502)
	)
	importedClasses := block + importedClassesOffset
	interfaceMethodDescriptors := block + interfaceMethodsOffset
	virtualMethodDescriptors := block + virtualMethodsOffset
	staticMethodDescriptors := block + staticMethodsOffset
	interfaceMethodOffsets := block + interfaceMethodOffsetsOffset
	fieldOffsets := block + fieldOffsetsOffset
	staticFieldOffsets := block + staticFieldOffsetsOffset
	virtualMethodOffsets := block + virtualMethodOffsetsOffset
	staticMethodOffsets := block + staticMethodOffsetsOffset

	check(t, public.WriteU32(importedClasses, 1))
	record := importedClasses + 4
	check(t, public.WriteU32(record, className))
	// Word +0x10 is the interface range: three entries beginning at zero.
	check(t, public.WriteU32(record+0x10, uint32(len(methods))<<16))
	for index, method := range methods {
		name, allocErr := runtime.allocateJavaCString(method.Name)
		check(t, allocErr)
		descriptor, allocErr := runtime.allocateJavaCString(method.descriptor)
		check(t, allocErr)
		entry := interfaceMethodDescriptors + uint32(index)*8
		check(t, public.WriteU32(entry, name))
		check(t, public.WriteU32(entry+4, descriptor))
	}

	arguments := []uint32{
		importedClasses,
		block + 0x080, // field descriptors (empty)
		block + 0x088, // static field descriptors (empty)
		virtualMethodDescriptors,
		interfaceMethodDescriptors,
		staticMethodDescriptors,
		fieldOffsets,
		staticFieldOffsets,
		virtualMethodOffsets,
		interfaceMethodOffsets,
		staticMethodOffsets,
	}
	for index := 0; index < 4; index++ {
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR0+uint32(index), arguments[index]))
	}
	stack, err := runtime.CPU.ReadRegister(cpu.RegisterSP)
	check(t, err)
	for index := 4; index < len(arguments); index++ {
		check(t, public.WriteU32(stack+uint32(index-4)*4, arguments[index]))
	}
	check(t, runtime.linkRaptorJavaClasses(java))

	class := java.ClassByName["org/kwis/msf/io/Socket"]
	if class == nil || class.vtable == 0 {
		t.Fatal("Socket has no linked Raptor vtable")
	}
	for index, want := range methods {
		encoded := make([]byte, 2)
		check(t, runtime.CPU.ReadMemory(interfaceMethodOffsets+uint32(index)*2, encoded))
		offset := uint32(binary.LittleEndian.Uint16(encoded))
		wantOffset := raptorJavaFlatVirtualBase + uint32(index)*2
		if offset != wantOffset {
			t.Errorf("interface offset[%d] = %d, want %d", index, offset, wantOffset)
		}
		linked := java.flatVirtual[index]
		if linked.className != "org/kwis/msf/io/Socket" ||
			linked.Name != want.Name || linked.descriptor != want.descriptor {
			t.Errorf("flat interface[%d] = %+v, want Socket.%s%s", index, linked, want.Name, want.descriptor)
		}
		procedure, readErr := public.ReadU32(class.vtable + offset*4 + 4)
		check(t, readErr)
		if procedure == 0 || procedure == java.noopStub {
			t.Errorf("Socket.%s%s resolved to 0x%08x", want.Name, want.descriptor, procedure)
		}
	}
}

func TestRaptorJavaConcreteDataStreamsExposeInheritedSlots(t *testing.T) {
	for className, expected := range map[string]map[uint32]string{
		"java/io/DataInputStream": {
			0x30: "read([B)I",
			0x68: "readShort()S",
		},
		"java/io/DataOutputStream": {
			0x34: "write([BII)V",
			0x38: "flush()V",
		},
	} {
		layout := raptorJavaFixedVirtualMethods[className]
		actual := make(map[uint32]string, len(layout))
		for _, method := range layout {
			actual[method.offset] = method.Name + method.descriptor
		}
		for offset, want := range expected {
			if got := actual[offset]; got != want {
				t.Errorf("%s slot 0x%02x = %q, want %q", className, offset, got, want)
			}
		}
	}
}

func TestRaptorJavaDataInputStreamReadShortVirtualSlot(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	// Build synthetic host vtables large enough to publish the fixed SDK slots.
	java.flatVirtual = make([]raptorJavaMethod, 1)

	sourceClass, err := runtime.ensureRaptorHostClass(java, "java/io/ByteArrayInputStream")
	check(t, err)
	source, err := runtime.NewRaptorJavaObject(sourceClass.Holder)
	check(t, err)
	data, err := runtime.newRaptorJavaArray('B', 6)
	check(t, err)
	body, err := public.ReadU32(data + 8)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(body+4, []byte{0x00, 0x8f, 0x00, 0x79, 0xff, 0x80}))
	writeRaptorJavaTestArguments(t, runtime, source, data)
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className:  "java/io/ByteArrayInputStream",
		Name:       "<init>",
		descriptor: "([B)V",
	})
	check(t, err)

	streamClass, err := runtime.ensureRaptorHostClass(java, "java/io/DataInputStream")
	check(t, err)
	stream, err := runtime.NewRaptorJavaObject(streamClass.Holder)
	check(t, err)
	writeRaptorJavaTestArguments(t, runtime, stream, source)
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className:  "java/io/DataInputStream",
		Name:       "<init>",
		descriptor: "(Ljava/io/InputStream;)V",
	})
	check(t, err)

	var readShort raptorJavaMethod
	var methodID uint32
	found := false
	for id, candidate := range java.hostMethods {
		if candidate.className == "java/io/DataInputStream" &&
			candidate.Name == "readShort" && candidate.descriptor == "()S" {
			readShort, methodID, found = candidate, id, true
			break
		}
	}
	if !found {
		t.Fatal("DataInputStream.readShort()S was not registered")
	}
	procedure, err := public.ReadU32(streamClass.vtable + 0x68)
	check(t, err)
	stub, err := runtime.importStub(raptorImportKey{
		Module: raptorJavaHostModule, Ordinal: methodID,
	})
	check(t, err)
	if want := stub | 1; procedure != want {
		t.Fatalf("DataInputStream vtable+0x68 = 0x%08x, want readShort stub 0x%08x", procedure, want)
	}

	for index, want := range []uint32{0x8f, 0x79, 0xffffff80} {
		writeRaptorJavaTestArguments(t, runtime, stream)
		result, callErr := runtime.callJavaHostMethod(context.Background(), readShort)
		check(t, callErr)
		if result.Low != want {
			t.Fatalf("readShort[%d] = 0x%08x, want 0x%08x", index, result.Low, want)
		}
	}
}
