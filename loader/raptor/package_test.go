package raptor

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestInspectPackage(t *testing.T) {
	module := makeELF(t, "00029420", []string{"kernel", "dlet", "wipic"})
	jar := makeZIP(t, map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"),
		"binary.mod":           module,
		"res/icon.png":         {1, 2, 3},
	})
	archive := makeZIP(t, map[string][]byte{
		"00029420.jar": jar,
		"app_info": []byte(
			"PID:PD122590\r\nAID:00029420\r\nName:YAP\r\n" +
				"Ver:01.00.01\r\nMClass:clet\r\nVdr:vendor\r\n",
		),
		"small.png": {4, 5, 6},
	})

	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Descriptor.AID != "00029420" ||
		pkg.Descriptor.PID != "PD122590" ||
		pkg.Descriptor.MainClass != "clet" {
		t.Fatalf("descriptor = %+v", pkg.Descriptor)
	}
	if pkg.JARName != "00029420.jar" || pkg.ModuleName != "binary.mod" {
		t.Fatalf("package paths = jar %q module %q", pkg.JARName, pkg.ModuleName)
	}
	if pkg.Image.Entry != 0x1000 ||
		pkg.Image.Metadata.Identifier != "00029420" {
		t.Fatalf("image = entry 0x%x metadata %+v", pkg.Image.Entry, pkg.Image.Metadata)
	}
	if got := pkg.Image.Metadata.Dependencies; len(got) != 3 ||
		got[0] != "kernel" || got[1] != "dlet" || got[2] != "wipic" {
		t.Fatalf("dependencies = %q", got)
	}
	if !bytes.Equal(pkg.Resources["res/icon.png"], []byte{1, 2, 3}) {
		t.Fatalf("resource = %x", pkg.Resources["res/icon.png"])
	}
	if _, ok := pkg.Resources["binary.mod"]; ok {
		t.Fatal("module leaked into resources")
	}
	if len(pkg.Image.Relocations) != 2 {
		t.Fatalf("relocations = %d", len(pkg.Image.Relocations))
	}
}

// Modules built with the ARM RVCT toolchain name their regions ER_RO/ER_RW/
// ER_ZI instead of .text/.data/.bss, so the code section has to be recognized
// by its flags rather than by one toolchain's spelling.
func TestInspectAcceptsRVCTRegionNames(t *testing.T) {
	module := makeELF(t, "wild", nil)
	renamed := bytes.Replace(module, []byte(".text\x00"), []byte("ER_RO\x00"), 1)
	if bytes.Equal(module, renamed) {
		t.Fatal("synthetic module does not carry a .text section name")
	}
	image, err := InspectELF("binary.mod", renamed)
	if err != nil {
		t.Fatalf("RVCT-named module rejected: %v", err)
	}
	if _, ok := image.Section(".text"); ok {
		t.Fatal("renamed module still reports a .text section")
	}
	code, ok := image.CodeSection()
	if !ok || code.Name != "ER_RO" || !code.Executable() {
		t.Fatalf("code section = %+v ok=%t", code, ok)
	}
	data, ok := image.DataSection()
	if !ok || data.Name != ".data" {
		t.Fatalf("data section = %+v ok=%t", data, ok)
	}
	zero, ok := image.ZeroSection()
	if !ok || zero.Name != ".bss" {
		t.Fatalf("zero section = %+v ok=%t", zero, ok)
	}
}

// 붕어빵타이쿤3 stores its entry offset with the Thumb bit set while its ELF
// header keeps the aligned address, so the two only agree once the
// interworking bit is taken out of the comparison.
func TestInspectAcceptsThumbFlaggedEntryOffset(t *testing.T) {
	module := makeELF(t, "thumb", nil)
	// makeELF puts .text at 0x1000 with an entry offset of zero, so flagging
	// the offset alone reproduces the mismatch.
	offset := bytes.Index(module, raptorMagic)
	if offset < 0 {
		t.Fatal("synthetic module carries no .raptor section")
	}
	binary.LittleEndian.PutUint32(module[offset+0x0c:offset+0x10], 1)
	image, err := InspectELF("binary.mod", module)
	if err != nil {
		t.Fatalf("Thumb-flagged entry offset rejected: %v", err)
	}
	if image.Entry != 0x1000 {
		t.Fatalf("entry = 0x%08x", image.Entry)
	}
}

// A writable-and-executable .data must not shadow .text: several shipped
// modules mark both, and picking the wrong one moves the entry point.
func TestCodeSectionPrefersTheEntryRegion(t *testing.T) {
	image := Image{Sections: []Section{
		{Index: 0},
		{
			Index: 1,
			Name:  ".text",
			Type:  sectionProgBits,
			Flags: sectionAlloc | sectionExec,
			Size:  16,
		},
		{
			Index: 2,
			Name:  ".data",
			Type:  sectionProgBits,
			Flags: sectionAlloc | sectionWrite | sectionExec,
			Size:  8,
		},
		{
			Index: 3,
			Name:  ".bss",
			Type:  sectionNoBits,
			Flags: sectionAlloc | sectionWrite,
			Size:  4,
		},
	}}
	if code, ok := image.CodeSection(); !ok || code.Name != ".text" {
		t.Fatalf("code section = %+v ok=%t", code, ok)
	}
	if data, ok := image.DataSection(); !ok || data.Name != ".data" {
		t.Fatalf("data section = %+v ok=%t", data, ok)
	}
	if zero, ok := image.ZeroSection(); !ok || zero.Name != ".bss" {
		t.Fatalf("zero section = %+v ok=%t", zero, ok)
	}
}

func TestInspectWrappedPackageAndMIE(t *testing.T) {
	module := makeELF(t, "game", []string{"kernel"})
	archive := makeZIP(t, map[string][]byte{
		"installer/game/app_info": []byte("AID:game\nMClass:clet\n"),
		"installer/game/game.jar": makeZIP(t, map[string][]byte{
			"module/binary.mie": module,
		}),
	})
	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.JARName != "installer/game/game.jar" ||
		pkg.ModuleName != "module/binary.mie" {
		t.Fatalf("paths = jar %q module %q", pkg.JARName, pkg.ModuleName)
	}
}

func TestInspectRejectsNonPackageAndUnsafeMember(t *testing.T) {
	if _, err := Inspect([]byte("not a ZIP")); !errors.Is(err, ErrNotPackage) {
		t.Fatalf("raw input error = %v", err)
	}
	ordinary := makeZIP(t, map[string][]byte{"game.jar": []byte("jar")})
	if _, err := Inspect(ordinary); !errors.Is(err, ErrNotPackage) {
		t.Fatalf("ordinary ZIP error = %v", err)
	}
	archive := makeZIP(t, map[string][]byte{
		"app_info": []byte("AID:game\n"),
		"game.jar": makeZIP(t, map[string][]byte{
			"binary.mod": makeELF(t, "game", []string{"kernel"}),
			"../escape":  {1},
		}),
	})
	if _, err := Inspect(archive); err == nil {
		t.Fatal("Inspect accepted an unsafe nested member")
	}
}

func TestInspectELFRejectsEntryMismatchAndOverlap(t *testing.T) {
	// A header entry that disagrees with .raptor is a dummy left by some
	// SDKs; the metadata offset wins.
	entryMismatch := makeELF(t, "game", []string{"kernel"})
	binary.LittleEndian.PutUint32(entryMismatch[24:28], 0x1004)
	image, err := InspectELF("binary.mod", entryMismatch)
	if err != nil {
		t.Fatalf("InspectELF rejected a dummy header entry: %v", err)
	}
	if image.Entry != 0x1000 {
		t.Fatalf("InspectELF kept entry 0x%x instead of the .raptor entry", image.Entry)
	}

	overlap := makeELF(t, "game", []string{"kernel"})
	sectionOffset := binary.LittleEndian.Uint32(overlap[32:36])
	dataHeader := sectionOffset + 2*sectionHdrSize
	binary.LittleEndian.PutUint32(overlap[dataHeader+12:dataHeader+16], 0x1000)
	if _, err := InspectELF("binary.mod", overlap); err == nil {
		t.Fatal("InspectELF accepted overlapping allocated sections")
	}
}

func TestInspectELFRejectsRelocationOutsideTarget(t *testing.T) {
	module := makeELF(t, "game", []string{"kernel"})
	sectionOffset := binary.LittleEndian.Uint32(module[32:36])
	relTextHeader := sectionOffset + 5*sectionHdrSize
	relocationOffset := binary.LittleEndian.Uint32(
		module[relTextHeader+16 : relTextHeader+20],
	)
	binary.LittleEndian.PutUint32(
		module[relocationOffset:relocationOffset+4],
		0x2000,
	)
	if _, err := InspectELF("binary.mod", module); err == nil {
		t.Fatal("InspectELF accepted an out-of-range relocation")
	}
}

func FuzzInspect(f *testing.F) {
	f.Add(makeZIP(f, map[string][]byte{
		"app_info": []byte("AID:game\nMClass:clet\n"),
		"game.jar": makeZIP(f, map[string][]byte{
			"binary.mod": makeELF(f, "game", []string{"kernel"}),
		}),
	}))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Inspect(data)
	})
}

type testingTB interface {
	Helper()
	Fatal(...any)
}

func makeZIP(t testingTB, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func makeELF(t testingTB, identifier string, dependencies []string) []byte {
	t.Helper()
	const (
		textAddress = uint32(0x1000)
		dataAddress = uint32(0x2000)
		bssAddress  = uint32(0x3000)
	)
	text := []byte{0x70, 0x47, 0, 0}
	data := []byte{0, 0, 0, 0}
	names := []byte("\x00.text\x00.data\x00.bss\x00.shstrtab\x00.rel.text\x00.rel.data\x00.raptor\x00")
	nameOffset := func(name string) uint32 {
		offset := bytes.Index(names, []byte(name+"\x00"))
		if offset < 0 {
			t.Fatal("missing test section name", name)
		}
		return uint32(offset)
	}

	dependencyText := []byte(bytes.Join(func() [][]byte {
		result := make([][]byte, len(dependencies))
		for index, dependency := range dependencies {
			result[index] = []byte(dependency)
		}
		return result
	}(), []byte{' '}))
	metadataSize := uint32(0x30 + len(identifier) + 1 + len(dependencyText) + 1)
	metadata := make([]byte, metadataSize)
	copy(metadata, raptorMagic)
	binary.LittleEndian.PutUint32(metadata[4:8], 0x20050512)
	binary.LittleEndian.PutUint32(metadata[8:12], metadataSize)
	binary.LittleEndian.PutUint32(metadata[0x0c:0x10], 0)
	binary.LittleEndian.PutUint32(metadata[0x18:0x1c], 0x00010001)
	binary.LittleEndian.PutUint32(metadata[0x1c:0x20], 1)
	binary.LittleEndian.PutUint32(metadata[0x24:0x28], 0x30)
	dependencyOffset := uint32(0x30 + len(identifier) + 1)
	binary.LittleEndian.PutUint32(metadata[0x2c:0x30], dependencyOffset)
	copy(metadata[0x30:], identifier)
	copy(metadata[dependencyOffset:], dependencyText)

	const sectionCount = 8
	output := make([]byte, elfHeaderSize)
	copy(output, []byte{0x7f, 'E', 'L', 'F', elfClass32, elfDataLittle, elfVersion, 0x61})
	binary.LittleEndian.PutUint16(output[16:18], elfTypeExec)
	binary.LittleEndian.PutUint16(output[18:20], elfMachineARM)
	binary.LittleEndian.PutUint32(output[20:24], elfVersion)
	binary.LittleEndian.PutUint32(output[24:28], textAddress)
	binary.LittleEndian.PutUint32(output[36:40], 0x206)
	binary.LittleEndian.PutUint16(output[40:42], elfHeaderSize)
	binary.LittleEndian.PutUint16(output[46:48], sectionHdrSize)
	binary.LittleEndian.PutUint16(output[48:50], sectionCount)
	binary.LittleEndian.PutUint16(output[50:52], 4)

	appendAligned := func(payload []byte, alignment int) uint32 {
		for len(output)%alignment != 0 {
			output = append(output, 0)
		}
		offset := uint32(len(output))
		output = append(output, payload...)
		return offset
	}
	textOffset := appendAligned(text, 4)
	dataOffset := appendAligned(data, 4)
	namesOffset := appendAligned(names, 1)
	relText := make([]byte, 8)
	binary.LittleEndian.PutUint32(relText[0:4], textAddress)
	binary.LittleEndian.PutUint32(relText[4:8], 2)
	relTextOffset := appendAligned(relText, 4)
	relData := make([]byte, 8)
	binary.LittleEndian.PutUint32(relData[0:4], dataAddress)
	binary.LittleEndian.PutUint32(relData[4:8], 1)
	relDataOffset := appendAligned(relData, 4)
	metadataOffset := appendAligned(metadata, 4)
	for len(output)%4 != 0 {
		output = append(output, 0)
	}
	sectionOffset := uint32(len(output))
	output = append(output, make([]byte, sectionCount*sectionHdrSize)...)
	binary.LittleEndian.PutUint32(output[32:36], sectionOffset)

	writeSection := func(
		index int,
		name string,
		kind, flags, address, offset, size, link, info, alignment, entrySize uint32,
	) {
		base := sectionOffset + uint32(index)*sectionHdrSize
		raw := output[base : base+sectionHdrSize]
		binary.LittleEndian.PutUint32(raw[0:4], nameOffset(name))
		binary.LittleEndian.PutUint32(raw[4:8], kind)
		binary.LittleEndian.PutUint32(raw[8:12], flags)
		binary.LittleEndian.PutUint32(raw[12:16], address)
		binary.LittleEndian.PutUint32(raw[16:20], offset)
		binary.LittleEndian.PutUint32(raw[20:24], size)
		binary.LittleEndian.PutUint32(raw[24:28], link)
		binary.LittleEndian.PutUint32(raw[28:32], info)
		binary.LittleEndian.PutUint32(raw[32:36], alignment)
		binary.LittleEndian.PutUint32(raw[36:40], entrySize)
	}
	writeSection(1, ".text", sectionProgBits, sectionAlloc|sectionExec, textAddress, textOffset, uint32(len(text)), 0, 0, 4, 0)
	writeSection(2, ".data", sectionProgBits, sectionAlloc|sectionWrite, dataAddress, dataOffset, uint32(len(data)), 0, 0, 4, 0)
	writeSection(3, ".bss", sectionNoBits, sectionAlloc|sectionWrite, bssAddress, uint32(len(output)), 16, 0, 0, 4, 0)
	writeSection(4, ".shstrtab", sectionString, 0, 0, namesOffset, uint32(len(names)), 0, 0, 1, 0)
	writeSection(5, ".rel.text", sectionREL, 0, 0, relTextOffset, uint32(len(relText)), 12, 1, 4, 8)
	writeSection(6, ".rel.data", sectionREL, 0, 0, relDataOffset, uint32(len(relData)), 12, 2, 4, 8)
	writeSection(7, ".raptor", sectionNull, 0, 0, metadataOffset, uint32(len(metadata)), 0, 0, 4, uint32(len(metadata)))
	return output
}
