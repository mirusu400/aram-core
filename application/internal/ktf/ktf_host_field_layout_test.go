package ktf

import "testing"

// defineGuestSubclass builds the KTF class descriptor a compiled title would
// carry for a class extending parent and declaring one instance field of its
// own at offset 0 - the layout every AOT subclass of a host-modelled class
// has, because a host-modelled ancestor reserves no guest field words.
func defineGuestSubclass(
	tb testing.TB,
	r *Runtime,
	name string,
	parent uint32,
	fieldName string,
	fieldDescriptor string,
) uint32 {
	tb.Helper()
	nameAddress, err := r.allocateBytes([]byte(name+"\x00"), true)
	check(tb, err)
	fullName, err := r.allocateBytes(
		append([]byte{0}, []byte(fieldDescriptor+"+"+fieldName)...),
		true,
	)
	check(tb, err)
	class := allocWords(tb, r, 5)
	field := allocWords(tb, r, 4)
	fields := allocWords(tb, r, 2)
	methods := allocWords(tb, r, 1)
	vtable := allocWords(tb, r, 1)
	descriptor := allocWords(tb, r, 9)
	check(tb, r.writeWords(field, []uint32{0x0001, class, fullName, 0}))
	check(tb, r.writeWords(fields, []uint32{field, 0}))
	check(tb, r.writeWords(descriptor, []uint32{
		nameAddress,
		0,
		parent,
		methods,
		0,
		fields,
		4 << 16,
		0x21,
		0,
	}))
	check(tb, r.writeWords(class, []uint32{
		class + 4,
		0,
		descriptor,
		vtable,
		8 << 16,
	}))
	return class
}

// TestKTFHostInstanceFieldKeepsSubclassFieldZero covers issue #156. A field a
// host-modelled class declares as instance state used to be answered as a
// class constant at offset 0, and the guest's own getfield helper indexes the
// receiver's field block with that offset, so the read landed on the concrete
// subclass's first declared field. 무한의 통통 read TextComponent.m_td that way
// and got its own nInputType back - the plain int 3 - then dereferenced it.
func TestKTFHostInstanceFieldKeepsSubclassFieldZero(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	parentAddress := ensureClass(t, runtime, "org/kwis/msp/lwc/TextComponent")
	parent := inspectClass(t, runtime, parentAddress)

	subclassAddress := defineGuestSubclass(
		t,
		runtime,
		"test/GuestTextBox",
		parentAddress,
		"nInputType",
		"I",
	)
	subclass := inspectClass(t, runtime, subclassAddress)
	if subclass.FieldSize != 4 || subclass.Parent != parentAddress {
		t.Fatalf(
			"subclass size = %d parent = 0x%08x",
			subclass.FieldSize,
			subclass.Parent,
		)
	}

	seen := make(map[uint32]string)
	for _, declared := range HostJavaClassSpecs[parent.Name].fields {
		field, err := runtime.ResolveJavaField(
			parent,
			declared.name,
			declared.descriptor,
		)
		check(t, err)
		words := readWords(t, runtime, field, 4)
		if words[0]&0x0008 != 0 {
			t.Fatalf(
				"%s.%s is answered as a class constant, not instance state",
				parent.Name,
				declared.name,
			)
		}
		if words[3] < ktfHostReservedFieldOffset {
			t.Fatalf(
				"%s.%s offset = %d, which a subclass layout can reach",
				parent.Name,
				declared.name,
				words[3],
			)
		}
		if other, clash := seen[words[3]]; clash {
			t.Fatalf(
				"%s.%s shares offset %d with %s",
				parent.Name,
				declared.name,
				words[3],
				other,
			)
		}
		seen[words[3]] = declared.name
	}
	if len(seen) == 0 {
		t.Fatalf("%s declares no instance fields", parent.Name)
	}

	instance, err := runtime.NewJavaInstanceForClass(subclass)
	check(t, err)
	fields := readWords(t, runtime, instance, 2)[0]
	if own := readU32(t, runtime, fields+4); own != 0 {
		t.Fatalf(
			"subclass field at offset 0 = 0x%08x, want the guest's own zero",
			own,
		)
	}
	handler := readU32(
		t,
		runtime,
		fields+4+ktfHostInstanceFieldOffsets[parent.Name+
			".imHandlerLorg/kwis/msp/lcdui/InputMethodHandler;"],
	)
	if handler == 0 {
		t.Fatal("TextComponent.imHandler was not populated on the instance")
	}
	handlerClass := inspectClass(t, runtime, readWords(t, runtime, handler, 2)[1])
	if handlerClass.Name != "org/kwis/msp/lcdui/InputMethodHandler" {
		t.Fatalf("imHandler class = %q", handlerClass.Name)
	}
}
