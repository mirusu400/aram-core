package raptor

import "testing"

// TestRaptorJavaStringBufferVirtualSlots pins the CLDC StringBuffer vtable
// layout. SD한국전쟁 builds every resource name with a StringBuffer and
// zero-pads the index with append('0') at slot 0x5c; while that slot fell
// through to the no-op backstop the pad was dropped, the title asked for
// "snd/0.mmf" instead of "snd/00.mmf", getResourceAsStream answered null and
// the title's own null check threw into a null dereference that faulted the
// machine (issue #125). The String (0x4c), char (0x5c) and int (0x60) call
// sites anchor the append block, and CLDC's declaration order fixes the rest.
func TestRaptorJavaStringBufferVirtualSlots(t *testing.T) {
	layout := raptorJavaFixedVirtualMethods["java/lang/StringBuffer"]
	if len(layout) == 0 {
		t.Fatal("java/lang/StringBuffer has no fixed vtable layout")
	}
	found := map[uint32]string{}
	for _, method := range layout {
		if previous, clash := found[method.offset]; clash {
			t.Fatalf(
				"StringBuffer slot 0x%02x is claimed twice: %q and %q",
				method.offset,
				previous,
				method.Name+method.descriptor,
			)
		}
		found[method.offset] = method.Name + method.descriptor
	}
	for offset, want := range map[uint32]string{
		0x14: "toString()Ljava/lang/String;",
		0x2c: "length()I",
		0x38: "setLength(I)V",
		0x3c: "charAt(I)C",
		0x48: "append(Ljava/lang/Object;)Ljava/lang/StringBuffer;",
		0x4c: "append(Ljava/lang/String;)Ljava/lang/StringBuffer;",
		0x54: "append([CII)Ljava/lang/StringBuffer;",
		0x58: "append(Z)Ljava/lang/StringBuffer;",
		0x5c: "append(C)Ljava/lang/StringBuffer;",
		0x60: "append(I)Ljava/lang/StringBuffer;",
		0x64: "append(J)Ljava/lang/StringBuffer;",
	} {
		if got := found[offset]; got != want {
			t.Errorf("StringBuffer slot 0x%02x = %q, want %q", offset, got, want)
		}
	}
}
