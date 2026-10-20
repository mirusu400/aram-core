package gnex32

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestAnalyzeServicesWalksDirectControlFlow(t *testing.T) {
	code := make([]byte, 30)
	putAnalysisWord(code, 0, 0x85)
	putAnalysisAddress(code, 2, 16)
	putAnalysisWord(code, 6, 0x92)
	putAnalysisWord(code, 8, 0x5f)
	putAnalysisWord(code, 10, 0x92)
	putAnalysisWord(code, 12, 0xdead)
	putAnalysisWord(code, 14, 0xff)
	putAnalysisWord(code, 16, 0x92)
	putAnalysisWord(code, 18, 0x22)
	putAnalysisWord(code, 20, 0x82)
	putAnalysisAddress(code, 22, 28)
	putAnalysisWord(code, 26, 0x86)
	putAnalysisWord(code, 28, 0xbeef)
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: code,
		CodePointers: [11]uint32{160},
	}

	got, err := AnalyzeServices(image)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstructionCount != 8 || !reflect.DeepEqual(got.Roots, []int{0}) {
		t.Fatalf("analysis coverage = %d instructions, roots %v", got.InstructionCount, got.Roots)
	}
	wantCalls := []StaticServiceCall{
		{ID: 0x22, Name: "SetClip", Supported: true, Offsets: []int{16}},
		{ID: 0x5f, Name: "Flush", Supported: true, Offsets: []int{6}},
		{ID: 0xdead, Supported: false, Offsets: []int{10}},
	}
	if !reflect.DeepEqual(got.ServiceCalls, wantCalls) {
		t.Fatalf("service calls = %+v", got.ServiceCalls)
	}
	if !reflect.DeepEqual(got.UnsupportedServices, wantCalls[2:]) {
		t.Fatalf("unsupported services = %+v", got.UnsupportedServices)
	}
	if want := []StaticOpcodeSite{{Opcode: 0xbeef, Offset: 28}}; !reflect.DeepEqual(got.UnknownOpcodes, want) {
		t.Fatalf("unknown opcodes = %+v", got.UnknownOpcodes)
	}
}

func TestAnalyzeServicesDoesNotScanOperandsAsOpcodes(t *testing.T) {
	code := make([]byte, 8)
	putAnalysisWord(code, 0, 0x07)
	putAnalysisWord(code, 2, 0x92)
	putAnalysisWord(code, 4, 0xdead)
	putAnalysisWord(code, 6, 0xff)
	got, err := AnalyzeServices(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: code,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.InstructionCount != 2 || len(got.ServiceCalls) != 0 || len(got.UnknownOpcodes) != 0 {
		t.Fatalf("operand scan leaked into analysis: %+v", got)
	}
}

func TestAnalyzeServicesRejectsTruncationAndOverlappingTargets(t *testing.T) {
	truncated := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: []byte{0x92, 0},
	}
	if _, err := AnalyzeServices(truncated); err == nil {
		t.Fatal("truncated service instruction accepted")
	}

	code := make([]byte, 8)
	putAnalysisWord(code, 0, 0x85)
	putAnalysisAddress(code, 2, 2)
	putAnalysisWord(code, 6, 0x86)
	overlap := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: code}
	if _, err := AnalyzeServices(overlap); err == nil {
		t.Fatal("control flow into an instruction operand accepted")
	}
}

func TestServiceRegistryMatchesRuntimeDispatch(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	defer runtime.Close()
	for value := 0; value <= int(^uint16(0)); value++ {
		id := uint16(value)
		_, declared := ServiceName(id)
		_, err := runtime.service(id, nil)
		var unsupported *UnsupportedServiceError
		dispatched := !errors.As(err, &unsupported)
		if declared != dispatched {
			t.Fatalf("service %#x registry=%v dispatch=%v error=%v", id, declared, dispatched, err)
		}
	}
	ids := SupportedServiceIDs()
	for index := 1; index < len(ids); index++ {
		if ids[index-1] >= ids[index] {
			t.Fatalf("service IDs are not sorted: %v", ids)
		}
	}
}

func putAnalysisWord(code []byte, offset int, value uint16) {
	binary.LittleEndian.PutUint16(code[offset:], value)
}

func putAnalysisAddress(code []byte, offset, target int) {
	address := uint32(target + 0x80)
	putAnalysisWord(code, offset, uint16(address>>16))
	putAnalysisWord(code, offset+2, uint16(address))
}
