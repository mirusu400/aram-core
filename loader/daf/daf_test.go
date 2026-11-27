package daf_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/loader/daf"
	"github.com/mirusu400/aram-core/loader/daf/daftest"
)

func syntheticSpec() daftest.Spec {
	text := make([]byte, 0x21000)
	put := func(offset, value uint32) { binary.LittleEndian.PutUint32(text[offset:], value) }
	put(0x0004, daf.LinkedDataBase)      // 4-bit distance
	put(0x0008, 0x00000040)              // text address, 4-bit distance
	put(0x001c, daf.LinkedDataBase+8)    // 0x10-based distance
	put(0x0800, 0x00000101)              // 12-bit distance
	put(0x0804, 0xffffff00)              // self-relative word
	put(0x20804, daf.LinkedDataBase+0xc) // 20-bit distance
	data := daftest.Words(daf.ImportPointerMarker, 0x00000800, daf.LinkedDataBase+4)
	return daftest.Spec{
		Text:    text,
		Data:    data,
		BSSSize: 0x10,
		Entries: daf.Entries{Start: 0x40, Pause: 0x44, Resume: 0x48, Destroy: 0x4c, Paint: 0x50, HandleEvent: 0x54},
		TextRelocations: []daftest.Relocation{
			{Offset: 0x0004, Target: daf.TargetData},
			{Offset: 0x0008, Target: daf.TargetText},
			{Offset: 0x001c, Target: daf.TargetData},
			{Offset: 0x0800, Target: daf.TargetText},
			{Offset: 0x0804, Target: daf.TargetSelfRelative},
			{Offset: 0x20804, Target: daf.TargetData},
		},
		DataRelocations: []daftest.Relocation{
			{Offset: 4, Target: daf.TargetText},
			{Offset: 8, Target: daf.TargetData},
		},
		Resources: []daf.Resource{
			{Name: "first.txt", Data: []byte("one")},
			{Name: "second.bin", Data: []byte{1, 2, 3, 4, 5}},
		},
	}
}

func archive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// The executable names its sections, its six lifecycle entries, every word
// that holds a text or data address, and a pack of named resources.
func TestParseReadsSectionsRelocationsAndResources(t *testing.T) {
	spec := syntheticSpec()
	image, err := daf.Parse(daftest.Build(spec))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(image.Text, spec.Text) || !bytes.Equal(image.Data, spec.Data) || image.BSSSize != 0x10 {
		t.Fatalf("sections: text %d data %d bss %#x", len(image.Text), len(image.Data), image.BSSSize)
	}
	if image.Entries != spec.Entries {
		t.Fatalf("entries = %+v, want %+v", image.Entries, spec.Entries)
	}
	if len(image.TextRelocations) != len(spec.TextRelocations) {
		t.Fatalf("text relocations = %d, want %d", len(image.TextRelocations), len(spec.TextRelocations))
	}
	for index, want := range spec.TextRelocations {
		got := image.TextRelocations[index]
		if got.Offset != want.Offset || got.Target != want.Target {
			t.Fatalf("text relocation %d = %+v, want %+v", index, got, want)
		}
	}
	if len(image.DataRelocations) != 2 || image.DataRelocations[1] != (daf.Relocation{Offset: 8, Target: daf.TargetData}) {
		t.Fatalf("data relocations = %+v", image.DataRelocations)
	}
	if len(image.Resources) != 2 || image.Resources[0].Name != "first.txt" ||
		string(image.Resources[0].Data) != "one" || image.Resources[1].Name != "second.bin" ||
		!bytes.Equal(image.Resources[1].Data, []byte{1, 2, 3, 4, 5}) {
		t.Fatalf("resources = %+v", image.Resources)
	}
}

// Loading moves text and data to the chosen bases, leaves a self-relative
// word alone, and replaces the import pointer marker with the import root.
func TestLoadRelocatesBothSections(t *testing.T) {
	image, err := daf.Parse(daftest.Build(syntheticSpec()))
	if err != nil {
		t.Fatal(err)
	}
	const textBase, dataBase, root = uint32(0x30000000), uint32(0x30040000), uint32(0x01008000)
	loaded, err := image.Load(textBase, dataBase, root)
	if err != nil {
		t.Fatal(err)
	}
	text := func(offset uint32) uint32 { return binary.LittleEndian.Uint32(loaded.Text[offset:]) }
	data := func(offset uint32) uint32 { return binary.LittleEndian.Uint32(loaded.Data[offset:]) }
	for _, check := range []struct {
		name string
		got  uint32
		want uint32
	}{
		{"text data address", text(0x0004), dataBase},
		{"text text address", text(0x0008), textBase + 0x40},
		{"text data address after a 0x10 distance", text(0x001c), dataBase + 8},
		{"text text address after a 12-bit distance", text(0x0800), textBase + 0x101},
		{"self-relative word", text(0x0804), 0xffffff00},
		{"text data address after a 20-bit distance", text(0x20804), dataBase + 0xc},
		{"import pointer", data(0), root},
		{"data text address", data(4), textBase + 0x800},
		{"data data address", data(8), dataBase + 4},
		{"start entry", loaded.Entries.Start, textBase + 0x40},
		{"event entry", loaded.Entries.HandleEvent, textBase + 0x54},
	} {
		if check.got != check.want {
			t.Errorf("%s = %#08x, want %#08x", check.name, check.got, check.want)
		}
	}
	if len(loaded.Data) != 12+0x10 || !bytes.Equal(loaded.Data[12:], make([]byte, 0x10)) {
		t.Fatalf("loaded data is %d bytes; BSS must follow zeroed", len(loaded.Data))
	}
}

func TestParseRejectsMalformedImages(t *testing.T) {
	valid := daftest.Build(syntheticSpec())
	mutate := func(change func([]byte) []byte) []byte { return change(bytes.Clone(valid)) }
	wrongTarget := syntheticSpec()
	wrongTarget.TextRelocations[1].Target = daf.TargetData
	for name, input := range map[string][]byte{
		"truncated header":    valid[:0x40],
		"magic":               mutate(func(b []byte) []byte { b[daf.OuterHeaderSize] = 'x'; return b }),
		"section sizes":       mutate(func(b []byte) []byte { return b[:len(b)-1] }),
		"import marker":       mutate(func(b []byte) []byte { b[daf.OuterHeaderSize+daf.HeaderSize+0x21000] ^= 1; return b }),
		"entry outside text":  mutate(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[daf.OuterHeaderSize+0x38:], 0x21000); return b }),
		"address not in data": daftest.Build(wrongTarget),
	} {
		_, err := daf.Parse(input)
		var formatErr *daf.FormatError
		if !errors.As(err, &formatErr) {
			t.Errorf("%s: error = %v, want a format error", name, err)
		}
	}
}

// A distribution archive is the executable beside its installer record; an
// archive without a ".daf" member is some other platform's package.
func TestInspectRecognizesTheDistributionArchive(t *testing.T) {
	image := daftest.Build(syntheticSpec())
	pkg, err := daf.Inspect(archive(t, map[string][]byte{
		"0000000001.daf": image,
		"0000000001.mod": []byte("installer record"),
		"savedata.dat":   []byte("save"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.BaseName != "0000000001" || pkg.DAFName != "0000000001.daf" || pkg.MODName != "0000000001.mod" ||
		string(pkg.Files["savedata.dat"]) != "save" || len(pkg.Image.Resources) != 2 {
		t.Fatalf("package = %q %q %q", pkg.BaseName, pkg.DAFName, pkg.MODName)
	}

	if _, err := daf.Inspect(archive(t, map[string][]byte{"other.jar": []byte("x")})); !errors.Is(err, daf.ErrNotPackage) {
		t.Fatalf("archive without an executable: %v", err)
	}
	if _, err := daf.Inspect([]byte("not an archive")); !errors.Is(err, daf.ErrNotPackage) {
		t.Fatalf("non-archive: %v", err)
	}
	var formatErr *daf.FormatError
	if _, err := daf.Inspect(archive(t, map[string][]byte{"0000000001.daf": image})); !errors.As(err, &formatErr) {
		t.Fatalf("executable without its installer record: %v", err)
	}
}
