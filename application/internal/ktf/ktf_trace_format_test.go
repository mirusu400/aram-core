package ktf

import (
	"fmt"
	"testing"
)

// TestKTFJavaMethodTraceMatchesFmt pins the hand-rolled java_method_call line
// against the fmt format it replaced. The line is built by hand because a
// Java-heavy title emits it tens of thousands of times a second and fmt boxed
// five arguments and walked the register slice reflectively; the text it
// produces has to stay byte-for-byte what a debug bundle used to contain.
func TestKTFJavaMethodTraceMatchesFmt(t *testing.T) {
	cases := []struct {
		className, name, descriptor string
		link                        uint32
		registers                   []uint32
	}{
		{"java/lang/Object", "<init>", "()V", 0, nil},
		{"java/lang/Object", "hashCode", "()I", 0xffffffff, []uint32{0}},
		{
			"org/kwis/msp/lcdui/Graphics",
			"drawImage",
			"(Lorg/kwis/msp/lcdui/Image;III)V",
			0x0001c3f4,
			[]uint32{
				0, 1, 0x0a, 0xdeadbeef, 0x80000000, 7, 0x10,
				0xfedcba98, 0x12345678, 0, 0xffff, 0x100, 0x0f,
			},
		},
	}
	for _, test := range cases {
		want := fmt.Sprintf(
			"java_method_call:%s.%s%s:lr=0x%08x:%08x",
			test.className,
			test.name,
			test.descriptor,
			test.link,
			test.registers,
		)
		runtime := &Runtime{}
		runtime.traceJavaMethodCall(
			test.className,
			test.name,
			test.descriptor,
			test.link,
			test.registers,
		)
		if len(runtime.HostTrace) != 1 {
			t.Fatalf("recorded %d entries, want 1", len(runtime.HostTrace))
		}
		if got := runtime.HostTrace[0]; got != want {
			t.Errorf("built  %q\nwant   %q", got, want)
		}
	}
}

// TestKTFJavaMethodTraceReusesScratch confirms the formatter does not hand the
// trace a string aliasing the buffer it reuses for the next entry.
func TestKTFJavaMethodTraceReusesScratch(t *testing.T) {
	runtime := &Runtime{}
	runtime.traceJavaMethodCall("a/B", "m", "()V", 1, []uint32{1})
	first := runtime.HostTrace[0]
	runtime.traceJavaMethodCall("c/D", "n", "(I)V", 2, []uint32{2, 3})
	if runtime.HostTrace[0] != first {
		t.Fatalf("first entry became %q, want %q", runtime.HostTrace[0], first)
	}
}
