package raptor

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader/raptor"
)

func TestRaptorResourceBytesReturnsPackagedDataAndEmptyMissingEntry(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:    public.CPU,
		Public: public,
		Pkg: raptor.Package{Resources: map[string][]byte{
			"Assets/Logo.bin": {1, 3, 5, 7},
		}},
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}

	for _, test := range []struct {
		name string
		want []byte
	}{
		{`\assets\logo.BIN`, []byte{1, 3, 5, 7}},
		{"missing-logical-entry", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, err := runtime.NewRaptorJavaString(test.name)
			check(t, err)
			check(t, public.CPU.WriteRegister(cpu.RegisterR0, name))
			result, err := runtime.raptorResourceBytes()
			check(t, err)
			if result.Low == 0 {
				t.Fatal("resource byte array is null")
			}
			body, err := public.ReadU32(result.Low + 8)
			check(t, err)
			count, err := public.ReadU32(body)
			check(t, err)
			if count != uint32(len(test.want)) {
				t.Fatalf("array length = %d, want %d", count, len(test.want))
			}
			got := make([]byte, count)
			check(t, public.CPU.ReadMemory(body+4, got))
			if !bytes.Equal(got, test.want) {
				t.Fatalf("array data = %v, want %v", got, test.want)
			}
			java := runtime.Java
			mirror := java.lgtToKTF[result.Low]
			mirrorBody, mirrorCount, element, primitive, ok := java.Host.ArrayShape(mirror)
			if !ok || !primitive || element != 1 || mirrorCount != count {
				t.Fatalf("mirror shape = body %08x count %d element %d primitive %t ok %t",
					mirrorBody, mirrorCount, element, primitive, ok)
			}
			mirrorData := make([]byte, mirrorCount)
			check(t, public.CPU.ReadMemory(mirrorBody, mirrorData))
			if !bytes.Equal(mirrorData, test.want) {
				t.Fatalf("mirror data = %v, want %v", mirrorData, test.want)
			}
		})
	}
}

func TestRaptorResourceBytesRejectsParentTraversal(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		Pkg:             raptor.Package{Resources: map[string][]byte{"secret": {9}}},
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	name, err := runtime.NewRaptorJavaString("../secret")
	check(t, err)
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, name))
	result, err := runtime.raptorResourceBytes()
	check(t, err)
	if result.Low != 0 {
		t.Fatalf("parent traversal returned array 0x%08x", result.Low)
	}
}

func TestInstallRaptorResourceBytesHelperValidatesAndPatchesPrologue(t *testing.T) {
	public := newPublicRuntime(t)
	const address = uint32(0x00032000)
	check(t, public.CPU.Map(address, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	helper := address + 0x338
	check(t, public.CPU.WriteMemory(helper, raptorResourceBytesHelperPrologue))
	runtime := &Runtime{
		CPU:                 public.CPU,
		Public:              public,
		resourceBytesHelper: helper,
		resolvedImports:     make(map[raptorImportKey]uint64),
		importSlotByKey:     make(map[raptorImportKey]uint32),
	}
	check(t, runtime.installResourceBytesHelper())
	patched := make([]byte, 8)
	check(t, public.CPU.ReadMemory(helper, patched))
	if !bytes.Equal(patched[:4], []byte{0x00, 0x4b, 0x18, 0x47}) {
		t.Fatalf("veneer instructions = %x", patched[:4])
	}
	if len(runtime.importSlots) != 1 ||
		runtime.importSlots[0].Module != raptorResourceBytesHostModule {
		t.Fatalf("import slots = %#v", runtime.importSlots)
	}

	check(t, public.CPU.WriteMemory(helper, []byte{0, 0, 0, 0, 0, 0}))
	if err := runtime.installResourceBytesHelper(); err == nil {
		t.Fatal("unexpected prologue was accepted")
	}
}
