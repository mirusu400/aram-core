package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFKernelProgramNameAndFastRelayStartup(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktfloader.Package{
		Descriptor: ktfloader.Descriptor{AID: "01042D83"},
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
		Files: map[string][]byte{
			"P/certification": []byte("01068488891\x00"),
		},
	})
	check(t, err)
	defer runtime.CPU.Close()
	if got := runtime.Services.Device.Config().PhoneNumber; got != "01068488891" {
		t.Fatalf("packaged PHONENUMBER = %q", got)
	}
	check(t, runtime.MapImageAndHost())
	knl, err := runtime.lookupInterface("WIPIC_knlInterface")
	check(t, err)
	programNameCallback := readWords(t, runtime, knl+14*4, 1)[0]
	programNameHost, ok := runtime.hostCalls[programNameCallback&^1]
	if !ok {
		t.Fatalf(
			"MC_knlGetProgramName callback 0x%08x is not registered",
			programNameCallback,
		)
	}

	output := heapAlloc(t, runtime, 16, true)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, output))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 16))
	status, err := programNameHost.handler(context.Background(), runtime)
	check(t, err)
	if status != 0 {
		t.Fatalf("MC_knlGetProgramName status = %d", int32(status))
	}
	if got, err := runtime.readCString(output, 16); err != nil {
		t.Fatal(err)
	} else if got != "01042D83" {
		t.Fatalf("MC_knlGetProgramName = %q", got)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 8))
	status, err = programNameHost.handler(context.Background(), runtime)
	check(t, err)
	if status != ktfWIPICErrorShortBuf {
		t.Fatalf("short MC_knlGetProgramName status = %d", int32(status))
	}

	fastRelay, err := runtime.lookupInterface("FastRelay")
	check(t, err)
	again, err := runtime.lookupInterface("FastRelay")
	check(t, err)
	if fastRelay == 0 || again != fastRelay {
		t.Fatalf(
			"FastRelay interface = 0x%08x, repeated 0x%08x",
			fastRelay,
			again,
		)
	}
	if fastRelay != knl+ktfKnlInterfaceSlotCount*4 {
		t.Fatalf(
			"FastRelay interface = 0x%08x, want KNL tail 0x%08x",
			fastRelay,
			knl+ktfKnlInterfaceSlotCount*4,
		)
	}
	callbacks := readWords(t, runtime, fastRelay, ktfFastRelaySlotCount)
	for index, callback := range callbacks {
		host, ok := runtime.hostCalls[callback&^1]
		if !ok {
			t.Fatalf("FastRelay slot %d callback 0x%08x is not registered", index, callback)
		}
		want := uint32(0)
		if index == 7 {
			want = 1
		}
		if value, err := host.handler(context.Background(), runtime); err != nil {
			t.Fatalf("FastRelay slot %d: %v", index, err)
		} else if value != want {
			t.Fatalf("FastRelay slot %d returned %d, want %d", index, value, want)
		}
	}
}

func TestKTFCertificationPhoneNumberRejectsAmbiguousFiles(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("01068488891"),
		[]byte("0201234567\x00"),
		[]byte("01068x88891\x00"),
		[]byte("01068488891\x00\x00"),
	} {
		if got := ktfCertificationPhoneNumber(map[string][]byte{
			"/Certification": data,
		}); got != "" {
			t.Fatalf("certification %q produced PHONENUMBER %q", data, got)
		}
	}
	if got := ktfCertificationPhoneNumber(map[string][]byte{
		"/certification": []byte("01068488891\x00"),
		"/Certification": []byte("01012345678\x00"),
	}); got != "" {
		t.Fatalf("duplicate certification files produced PHONENUMBER %q", got)
	}
}

func TestKTFKernelMissingResourcesReturnNoEntry(t *testing.T) {
	runtime := newTestRuntime(t)
	name, err := runtime.allocateBytes([]byte("/png/missing.sbmp\x00"), true)
	check(t, err)
	size := allocWords(t, runtime, 1)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, name))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, size))
	status, err := ktfGetResourceID(context.Background(), runtime)
	check(t, err)
	if status != ktfWIPICErrorNoEntry {
		t.Fatalf("missing MC_knlGetResourceID status = %d, want -12", int32(status))
	}

	output := heapAlloc(t, runtime, 1, true)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, ^uint32(0)))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, output))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 1))
	status, err = ktfGetResource(context.Background(), runtime)
	check(t, err)
	if status != ktfWIPICErrorNoEntry {
		t.Fatalf("missing MC_knlGetResource status = %d, want -12", int32(status))
	}
}
