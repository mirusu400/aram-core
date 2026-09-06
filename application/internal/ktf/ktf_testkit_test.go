package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// check fails the test when err is non-nil.
func check(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatal(err)
	}
}

// newUnmappedTestRuntime builds a runtime around the minimal two-byte
// `bx lr` client and closes its CPU when the test ends.
func newUnmappedTestRuntime(tb testing.TB) *Runtime {
	tb.Helper()
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	check(tb, err)
	tb.Cleanup(func() { _ = runtime.CPU.Close() })
	return runtime
}

// newTestRuntime is newUnmappedTestRuntime with the image and host mapped.
func newTestRuntime(tb testing.TB) *Runtime {
	tb.Helper()
	runtime := newUnmappedTestRuntime(tb)
	check(tb, runtime.MapImageAndHost())
	return runtime
}

// The helpers below unwrap the runtime accessors tests call most often so a
// fixture reads as one line per allocation instead of four.

func allocWords(tb testing.TB, r *Runtime, count uint32) uint32 {
	tb.Helper()
	address, err := r.AllocateWords(count)
	check(tb, err)
	return address
}

func heapAlloc(tb testing.TB, r *Runtime, size uint32, clearMemory bool) uint32 {
	tb.Helper()
	address, err := r.Heap.Allocate(size, clearMemory)
	check(tb, err)
	return address
}

func newHostObject(tb testing.TB, r *Runtime, className string) uint32 {
	tb.Helper()
	object, err := r.NewHostJavaObject(className)
	check(tb, err)
	return object
}

func readU32(tb testing.TB, r *Runtime, address uint32) uint32 {
	tb.Helper()
	value, err := r.ReadU32(address)
	check(tb, err)
	return value
}

func readWords(tb testing.TB, r *Runtime, address uint32, count int) []uint32 {
	tb.Helper()
	words, err := r.ReadWords(address, count)
	check(tb, err)
	return words
}

func newJavaString(tb testing.TB, r *Runtime, value string) uint32 {
	tb.Helper()
	object, err := r.NewJavaString(value)
	check(tb, err)
	return object
}

func inspectClass(tb testing.TB, r *Runtime, address uint32) JavaClass {
	tb.Helper()
	class, err := r.InspectJavaClass(address)
	check(tb, err)
	return class
}

func ensureClass(tb testing.TB, r *Runtime, name string) uint32 {
	tb.Helper()
	class, err := r.EnsureJavaClass(name)
	check(tb, err)
	return class
}
