package raptor

import (
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
