package raptor

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// 테일즈위버 루시안칼츠편 builds the device-information request it sends after
// UserAuthentication with module-1 ordinal 1015: sprintf(buffer, format, ...)
// with five %s arguments, two in r2/r3 and three on the stack, and it frames the
// request body with the returned length. Left unresolved, the import returned
// zero and the title sent an empty body (issue #568).
func TestRaptorSprintfImportFormatsStackArguments(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}

	text := func(value string) uint32 {
		address, err := public.Heap.Allocate(uint32(len(value)+1), true)
		check(t, err)
		check(t, public.CPU.WriteMemory(address, append([]byte(value), 0)))
		return address
	}
	buffer, err := public.Heap.Allocate(256, true)
	check(t, err)
	check(t, public.CPU.WriteMemory(buffer, []byte{0xaa, 0xaa, 0xaa, 0xaa}))
	format := text("ext:%s int:%s phone:%s model:%s appid:%s end")
	version := text("1.0.1")
	phone := text("01000000000")
	model := text("IM-S220L")
	appID := text("0002EC43")

	sp, err := public.CPU.ReadRegister(cpu.RegisterSP)
	check(t, err)
	var stack [12]byte
	binary.LittleEndian.PutUint32(stack[0:], phone)
	binary.LittleEndian.PutUint32(stack[4:], model)
	binary.LittleEndian.PutUint32(stack[8:], appID)
	check(t, public.CPU.WriteMemory(sp, stack[:]))
	for register, value := range []uint32{buffer, format, version, version} {
		check(t, public.CPU.WriteRegister(uint32(register), value))
	}
	check(t, public.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))

	check(t, runtime.dispatchImport(context.Background(), raptorImportKey{Module: 1, Ordinal: 1015}))

	want := "ext:1.0.1 int:1.0.1 phone:01000000000 model:IM-S220L appid:0002EC43 end"
	got, err := public.ReadCString(buffer)
	check(t, err)
	if string(got) != want {
		t.Fatalf("formatted %q, want %q", got, want)
	}
	length, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if length != uint32(len(want)) {
		t.Fatalf("sprintf returned %d, want the formatted length %d", length, len(want))
	}
	if public.Stats.UnimplementedCalls != 0 {
		t.Fatalf("sprintf dispatched as unimplemented: %v", public.Unimplemented)
	}
}
