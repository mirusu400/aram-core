package raptor

import (
	"context"
	"errors"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

// A Java exception that escapes a card's keyNotify ends that key event, the
// way a handset's event dispatcher catches what a listener lets escape. Only a
// host-raised exception in a key callback qualifies (issue #575).
func TestRaptorJavaKeyCallbackAbsorbsUncaughtHostException(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	const keyNotify, paint = uint32(0x20001), uint32(0x20101)
	card, err := runtime.InstallJavaCardForTest(keyNotify, paint)
	check(t, err)
	java := runtime.Java
	java.flatVirtual = make([]raptorJavaMethod, 4)

	key, ok := runtime.JavaInputCallback(machinecore.InputEvent{Control: "ok", Pressed: true})
	if !ok || key.Procedure != keyNotify || key.Args[0] != card {
		t.Fatalf("key callback = %+v, %t", key, ok)
	}
	if !runtime.IsJavaInputCallback(key) {
		t.Fatal("JavaInputCallback result is not recognised as a key callback")
	}
	notKey := wipirt.GuestCallback{Procedure: paint, Args: [4]uint32{card, ktfrt.KeyPressed}}
	if runtime.IsJavaInputCallback(notKey) {
		t.Fatal("a callback to another body was taken for keyNotify")
	}

	// elementAt(0) on an empty Vector raises ArrayIndexOutOfBoundsException
	// from the host, with no guest code run past the throw.
	class, err := runtime.ensureRaptorHostClass(java, "java/util/Vector")
	check(t, err)
	vector, err := runtime.NewRaptorJavaObject(class.Holder)
	check(t, err)
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, vector))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, 0))
	_, thrown := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className:  "java/util/Vector",
		Name:       "elementAt",
		descriptor: "(I)Ljava/lang/Object;",
	})
	if name, ok := ktfrt.UnhandledJavaException(thrown); !ok ||
		name != "java/lang/ArrayIndexOutOfBoundsException" {
		t.Fatalf("empty elementAt error = %v", thrown)
	}

	if runtime.AbsorbUncaughtJavaException(notKey, thrown, "paint fault") {
		t.Fatal("an exception outside a key callback was absorbed")
	}
	if runtime.AbsorbUncaughtJavaException(key, errors.New("ARM fetch at 0x00000000"), "cpu fault") {
		t.Fatal("a CPU fault was absorbed as a Java exception")
	}
	if count, _ := runtime.UncaughtJavaExceptions(); count != 0 {
		t.Fatalf("rejected exceptions were counted: %d", count)
	}
	if !runtime.AbsorbUncaughtJavaException(key, thrown, "key fault") {
		t.Fatal("the key callback's uncaught exception was not absorbed")
	}
	count, last := runtime.UncaughtJavaExceptions()
	if count != 1 || last != "key fault" {
		t.Fatalf("uncaught = %d %q, want 1 \"key fault\"", count, last)
	}
	if got := public.Observed["RAPTOR.Java.uncaught.java/lang/ArrayIndexOutOfBoundsException"]; got != 1 {
		t.Fatalf("observed uncaught count = %d, want 1", got)
	}
}
