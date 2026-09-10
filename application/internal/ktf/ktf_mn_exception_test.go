package ktf

import (
	"context"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFMNExceptionDispatchResumesGuestCatch(t *testing.T) {
	runtime := newMNTestRuntime(t)
	check(t, runtime.prepareMNContext(ImageBase+0x3000))
	if runtime.exceptionContext+8*4 != runtime.mnContext+mnContextExceptionFrameField {
		t.Fatal("MN exception environment is detached from the guest frame head")
	}
	const (
		object     = ImageBase + 0x100
		descriptor = ImageBase + 0x180
		resume     = ImageBase + 0x800
		frame      = guest.DefaultStackBase + guest.DefaultStackSize - 0x800
		stack      = guest.DefaultStackBase + guest.DefaultStackSize - 0x400
	)
	// A synthetic catch continuation returns its setjmp result through r7.
	// It is deliberately unrelated to any proprietary method body.
	check(t, runtime.CPU.WriteMemory(resume, []byte{0x38, 0x47})) // bx r7
	_, methods := defineMNLayoutClass(t, runtime, object, descriptor, "test/Catch", 0, 0,
		[]mnLayoutMethod{{name: "work", descriptor: "()V", body: resume | 1}})
	entry := allocWords(t, runtime, 4)
	check(t, runtime.writeWords(entry, []uint32{4, 20, 23, 3})) // import 1
	table := allocWords(t, runtime, 1)
	check(t, runtime.WriteU32(table, entry))
	check(t, runtime.WriteU32(methods[0]+8, table))
	check(t, runtime.WriteU32(methods[0]+16, 1))
	catchName, err := runtime.allocateBytes([]byte("java/lang/Exception"), true)
	check(t, err)
	imports := allocWords(t, runtime, 2)
	check(t, runtime.writeWords(imports, []uint32{0, catchName}))
	check(t, runtime.linkMNClassExceptions(object, imports))
	catch := readU32(t, runtime, entry+12)
	if catch == 0 || catch != runtime.JavaClasses["java/lang/Exception"] {
		t.Fatalf("catch import = 0x%08x, want registered Exception class", catch)
	}
	// A second link must leave the resolved catch pointer alone.
	check(t, runtime.linkMNClassExceptions(object, imports))
	functions := readU32(t, runtime, runtime.mnContext+mnContextExceptionFunctionsField)
	check(t, runtime.writeWords(frame, []uint32{
		methods[0], 0, 0, 0, 12, functions,
		stack, resume | 1, 4, 5, 6, ktfReturnSentinel | 1, 8, 9, 0xdead, ImageBase + 0x3000,
	}))
	check(t, runtime.WriteU32(runtime.mnContext+mnContextExceptionFrameField, frame))
	throw := runtime.RegisterHostCall("test.mn.throw", func(_ context.Context, r *Runtime) (uint32, error) {
		return r.raiseJavaException("java/io/IOException", 0)
	})
	_, value, err := runtime.call(context.Background(), throw, nil, 100)
	check(t, err)
	if value != 23 {
		t.Fatalf("guest catch setjmp result = %d, want 23", value)
	}
	if got := readU32(t, runtime, frame+16); got != 23 {
		t.Fatalf("MN bytecode cursor = %d, want handler 23", got)
	}
	detail := readU32(t, runtime, frame+12)
	if detail == 0 {
		t.Fatal("MN frame did not receive the thrown object")
	}
	class := inspectClass(t, runtime, readU32(t, runtime, detail+4))
	if class.Name != "java/io/IOException" {
		t.Fatalf("MN thrown object class = %q", class.Name)
	}
	_, caught, err := runtime.dispatchJavaException("java/io/IOException", detail)
	check(t, err)
	if caught {
		t.Fatal("a throw from the catch handler re-entered its own try region")
	}
}

func TestKTFMNExceptionRestorePreservesSavedRegisters(t *testing.T) {
	runtime := newMNTestRuntime(t)
	contextAddress := allocWords(t, runtime, 10)
	saved := []uint32{0x70001000, ImageBase | 1, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xdead, 0xaa}
	check(t, runtime.writeWords(contextAddress, saved))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, contextAddress))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 23))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR11, 0xbb))
	value, err := ktfMNRestoreException(context.Background(), runtime)
	check(t, err)
	if value != 23 {
		t.Fatalf("restored handler = %d, want 23", value)
	}
	for register, want := range map[uint32]uint32{
		cpu.RegisterSP: saved[0], cpu.RegisterLR: saved[1],
		cpu.RegisterR4: saved[2], cpu.RegisterR5: saved[3], cpu.RegisterR6: saved[4],
		cpu.RegisterR7: saved[5], cpu.RegisterR8: saved[6], cpu.RegisterR9: saved[7],
		cpu.RegisterR10: saved[9], cpu.RegisterR11: 0xbb,
	} {
		got, err := runtime.CPU.ReadRegister(register)
		check(t, err)
		if got != want {
			t.Fatalf("restored register %d = %08x, want %08x", register, got, want)
		}
	}
}

func TestKTFMNCatchLinkRejectsUnresolvedImport(t *testing.T) {
	runtime := newMNTestRuntime(t)
	const object = ImageBase + 0x100
	_, methods := defineMNLayoutClass(t, runtime, object, ImageBase+0x180, "test/Catch", 0, 0,
		[]mnLayoutMethod{{name: "work", descriptor: "()V", body: ImageBase | 1}})
	entry := allocWords(t, runtime, 4)
	check(t, runtime.writeWords(entry, []uint32{0, 10, 10, 3}))
	table := allocWords(t, runtime, 1)
	check(t, runtime.WriteU32(table, entry))
	check(t, runtime.WriteU32(methods[0]+8, table))
	check(t, runtime.WriteU32(methods[0]+16, 1))
	err := runtime.linkMNClassExceptions(object, 0)
	if err == nil || !strings.Contains(err.Error(), "unresolved catch import") {
		t.Fatalf("unresolved catch error = %v", err)
	}
	if got := readU32(t, runtime, entry+12); got != 3 {
		t.Fatalf("unresolved catch mutated to 0x%08x", got)
	}
}
