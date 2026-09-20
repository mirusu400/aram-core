package raptor

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
)

// ELF section flags as the loader reads them; the loader keeps them private.
const testSectionAlloc = 0x2

// A section that ends mid-granule is mapped out to the granule, so the last
// page of its globals has a whole region behind it (the native TLB refuses a
// page that is not); the padding reads as zero and stops at the next section.
func TestMapRaptorImagePadsSectionsToTheMappingGranule(t *testing.T) {
	image := raptorloader.Image{Sections: []raptorloader.Section{
		{Name: ".bss", Flags: testSectionAlloc, Address: 0x1000, Size: 0x594},
		{Name: ".data", Flags: testSectionAlloc, Address: 0x3000, Size: 0x100, Data: []byte{1, 2, 3}},
		{Name: ".late", Flags: testSectionAlloc, Address: 0x3180, Size: 0x100},
	}}
	if got := mappedSectionSize(image, image.Sections[0]); got != 0x800 {
		t.Fatalf("padded .bss size = 0x%x, want 0x800", got)
	}
	if got := mappedSectionSize(image, image.Sections[1]); got != 0x180 {
		t.Fatalf(".data padding ran into .late: size = 0x%x, want 0x180", got)
	}
	if got := mappedSectionSize(image, image.Sections[2]); got != 0x280 {
		t.Fatalf("padded .late size = 0x%x, want 0x280", got)
	}

	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := MapRaptorImage(backend, image); err != nil {
		t.Fatal(err)
	}
	var word [4]byte
	if err := backend.ReadMemory(0x1000+0x7fc, word[:]); err != nil {
		t.Fatalf("last padded word of .bss is not mapped: %v", err)
	}
	if word != [4]byte{} {
		t.Fatalf("padding reads %v, want zero", word)
	}
	if err := backend.ReadMemory(0x1000+0x800, word[:1]); err == nil {
		t.Fatal("the byte past the padded .bss page is mapped")
	}
	if err := backend.ReadMemory(0x3180, word[:]); err != nil {
		t.Fatalf(".late is not mapped after a clamped neighbour: %v", err)
	}
	if err := backend.WriteMemory(0x1000+0x7fc, []byte{9, 9, 9, 9}); err != nil {
		t.Fatalf("padding is not writable like the rest of the section: %v", err)
	}
	if got := RequiredMemory(image); got < uint64(0x800+0x180+0x280) {
		t.Fatalf("RequiredMemory = %d does not include the padding", got)
	}
}

func TestRaptorImagePatchChecksOriginalAndSurvivesRestore(t *testing.T) {
	const address = uint32(0x1800)
	const expected = uint32(0xe92dd810)
	const replacement = uint32(0x000d8640)
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, expected)
	image := raptorloader.Image{Sections: []raptorloader.Section{{
		Name: ".text", Flags: testSectionAlloc, Address: address, Size: 4, Data: data,
	}}}
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, MapRaptorImage(backend, image))
	runtime := &Runtime{
		CPU: backend,
		Pkg: raptorloader.Package{Image: image},
		imagePatches: []ImagePatch{{
			Address: address, Expected: expected, Replacement: replacement,
		}},
	}

	check(t, runtime.applyImagePatches())
	var encoded [4]byte
	check(t, backend.ReadMemory(address, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != replacement {
		t.Fatalf("patched word = 0x%08x, want 0x%08x", got, replacement)
	}
	check(t, runtime.RestoreImage())
	check(t, backend.ReadMemory(address, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != replacement {
		t.Fatalf("restored patch = 0x%08x, want 0x%08x", got, replacement)
	}

	binary.LittleEndian.PutUint32(encoded[:], 0x12345678)
	check(t, backend.WriteMemory(address, encoded[:]))
	err := runtime.applyImagePatches()
	if err == nil || !strings.Contains(err.Error(), "expected 0xe92dd810, got 0x12345678") {
		t.Fatalf("unexpected original error = %v", err)
	}
}
