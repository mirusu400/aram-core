package brew

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"math"
	"strings"
	"testing"
)

func syntheticMIF() []byte {
	data := make([]byte, 64)
	for off, value := range map[int]uint32{0: MIFTag, 4: 0x10001, 8: 32, 12: 8, 16: 40, 20: 1, 24: 48, 28: 16} {
		binary.LittleEndian.PutUint32(data[off:], value)
	}
	return data
}

func archive(t *testing.T, names []string, payloads [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(payloads[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInspectRecognizesOnlyContainerMetadata(t *testing.T) {
	data := archive(t, []string{"metadata.mif", "app/native.MOD", "app/native.SIG", "app/resource.bar"}, [][]byte{syntheticMIF(), []byte("synthetic opaque module, not executable"), []byte("not a verified signature"), []byte("opaque resource")})
	pkg, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Modules) != 1 || len(pkg.MIFs) != 1 || len(pkg.Files) != 4 {
		t.Fatalf("unexpected member counts: %+v", pkg)
	}
	mod := pkg.Modules[0]
	if mod.Name != "app/native.MOD" || mod.Format != FormatOpaqueNative || mod.Size != uint64(len(pkg.Files[mod.Name])) || !mod.SignaturePresent {
		t.Fatalf("module: %+v", mod)
	}
	mif := pkg.MIFs[0]
	if mif.Name != "metadata.mif" || mif.Tag != MIFTag || mif.TableOffset != 32 || mif.TableSize != 8 || mif.IndexOffset != 40 || mif.IndexCount != 1 || mif.DataOffset != 48 || mif.DataSize != 16 {
		t.Fatalf("MIF: %+v", mif)
	}
}

func TestInspectKeepsMultipleModulesUnassociatedAndSorted(t *testing.T) {
	data := archive(t, []string{"z.mif", "other/z.mod", "a.mif", "app/a.mod"}, [][]byte{syntheticMIF(), {1}, syntheticMIF(), {2}})
	pkg, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Modules) != 2 || len(pkg.MIFs) != 2 || pkg.Modules[0].Name != "app/a.mod" || pkg.MIFs[0].Name != "a.mif" || pkg.Modules[0].SignaturePresent {
		t.Fatalf("metadata: %+v", pkg)
	}
}

func TestInspectDeclinesOtherFormats(t *testing.T) {
	cases := map[string][]byte{
		"empty":          nil,
		"raw MOD":        {0, 1, 2, 3},
		"ELF":            []byte("\x7fELFsynthetic"),
		"extension only": archive(t, []string{"a.mif", "a.mod"}, [][]byte{[]byte("not MIF"), {1}}),
		"GNEX":           archive(t, []string{"a.sgs", "a.mod"}, [][]byte{[]byte("SGS placeholder"), {1}}),
		"Java":           archive(t, []string{"META-INF/MANIFEST.MF", "Main.class"}, [][]byte{[]byte("MIDlet-1: Synthetic,,Main"), {0xca, 0xfe, 0xba, 0xbe}}),
		"APK":            archive(t, []string{"AndroidManifest.xml", "classes.dex"}, [][]byte{{1}, {2}}),
		"unknown tag":    archive(t, []string{"a.mif", "a.mod"}, [][]byte{make([]byte, 64), {1}}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Inspect(data); !errors.Is(err, ErrNotPackage) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestInspectRejectsIncompleteInputs(t *testing.T) {
	for name, data := range map[string][]byte{
		"missing MOD":        archive(t, []string{"a.mif"}, [][]byte{syntheticMIF()}),
		"empty MOD":          archive(t, []string{"a.mif", "a.mod"}, [][]byte{syntheticMIF(), nil}),
		"truncated MIF":      archive(t, []string{"a.mif", "a.mod"}, [][]byte{syntheticMIF()[:8], {1}}),
		"unknown second MIF": archive(t, []string{"a.mif", "b.mif", "a.mod"}, [][]byte{syntheticMIF(), make([]byte, 64), {1}}),
	} {
		t.Run(name, func(t *testing.T) {
			var bad *FormatError
			if _, err := Inspect(data); !errors.As(err, &bad) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMIFEnvelopeBounds(t *testing.T) {
	cases := []struct {
		name   string
		field  int
		value  uint32
		offset int64
	}{
		{"table in header", 8, 0, 8}, {"table overflow", 8, math.MaxUint32, 8}, {"table size overflow", 12, math.MaxUint32, 8},
		{"index overlap", 16, 32, 16}, {"index offset overflow", 16, math.MaxUint32, 16}, {"index count overflow", 20, math.MaxUint32, 16},
		{"data overlap", 24, 40, 24}, {"data offset overflow", 24, math.MaxUint32, 24}, {"data size overflow", 28, math.MaxUint32, 24},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mif := syntheticMIF()
			binary.LittleEndian.PutUint32(mif[tc.field:], tc.value)
			_, err := parseMIF("synthetic.mif", mif)
			var bad *FormatError
			if !errors.As(err, &bad) || bad.Offset != tc.offset {
				t.Fatalf("error=%v, expected offset %d", err, tc.offset)
			}
		})
	}
	for n := 0; n < 32; n++ {
		if _, err := parseMIF("truncated.mif", syntheticMIF()[:n]); err == nil {
			t.Fatalf("accepted %d bytes", n)
		}
	}
}

func TestInspectRejectsUnsafeOrDuplicateZIPNames(t *testing.T) {
	for _, name := range []string{"../escape.mod", "/absolute.mod", "C:/absolute.mod", "a/../../escape.mod", "A.MIF"} {
		t.Run(name, func(t *testing.T) {
			data := archive(t, []string{"a.mif", name}, [][]byte{syntheticMIF(), {1}})
			var bad *FormatError
			if _, err := Inspect(data); !errors.As(err, &bad) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func rawArchive(t *testing.T, headers []zip.FileHeader) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := range headers {
		if _, err := w.CreateRaw(&headers[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInspectRejectsZIPLimitsAndSymlinks(t *testing.T) {
	header := zip.FileHeader{Name: "oversized.mod", Method: zip.Store, UncompressedSize64: MaxMemberSize + 1}
	var bad *FormatError
	if _, err := Inspect(rawArchive(t, []zip.FileHeader{header})); !errors.As(err, &bad) || bad.Reason != "member exceeds size limit" {
		t.Fatalf("oversize error=%v", err)
	}
	header = zip.FileHeader{Name: "link.mod"}
	header.SetMode(fs.ModeSymlink | 0777)
	if _, err := Inspect(rawArchive(t, []zip.FileHeader{header})); !errors.As(err, &bad) || bad.Reason != "symbolic link is not allowed" {
		t.Fatalf("symlink error=%v", err)
	}
	headers := make([]zip.FileHeader, MaxArchiveEntries+1)
	for i := range headers {
		headers[i].Name = "empty"
	}
	if _, err := Inspect(rawArchive(t, headers)); !errors.As(err, &bad) || bad.Reason != "too many ZIP entries" {
		t.Fatalf("entry limit error=%v", err)
	}
	headers = []zip.FileHeader{
		{Name: "one.mod", UncompressedSize64: MaxMemberSize},
		{Name: "two.mod", UncompressedSize64: MaxMemberSize},
		{Name: "three.mod", UncompressedSize64: 1},
	}
	if _, err := Inspect(rawArchive(t, headers)); !errors.As(err, &bad) || bad.Reason != "expanded data exceeds limit" {
		t.Fatalf("expanded limit error=%v", err)
	}
}

func TestInspectRejectsZIPIntegrityErrors(t *testing.T) {
	for _, header := range []zip.FileHeader{
		{Name: "bad.mod", Method: zip.Store, CRC32: 1},
		{Name: "bad.mod", Method: zip.Store, UncompressedSize64: 1},
	} {
		var bad *FormatError
		if _, err := Inspect(rawArchive(t, []zip.FileHeader{header})); !errors.As(err, &bad) || !(strings.HasPrefix(bad.Reason, "read member:") || bad.Reason == "uncompressed size mismatch") {
			t.Fatalf("integrity error=%v", err)
		}
	}
}

func FuzzMIFEnvelope(f *testing.F) {
	f.Add(syntheticMIF())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := parseMIF("synthetic.mif", data)
		if err == nil && (m.Tag != MIFTag || uint64(m.DataOffset)+uint64(m.DataSize) > uint64(len(data))) {
			t.Fatal("invalid accepted boundary")
		}
	})
}
