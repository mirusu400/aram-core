package ktf

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestInspectPackage(t *testing.T) {
	jar := makeZIP(t, map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"),
		"client.bin4096":       {0x04, 0xe0, 0x70, 0x47},
		"r/icon.png":           {1, 2, 3},
	})
	archive := makeZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:PD000001\r\nAID:01020304\r\nMClass:GameMain\r\n"),
		"small.icon":   {4, 5, 6},
	})

	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Descriptor.AID != "01020304" ||
		pkg.Descriptor.PID != "PD000001" ||
		pkg.Descriptor.MainClass != "GameMain" {
		t.Fatalf("descriptor = %+v", pkg.Descriptor)
	}
	if pkg.JARName != "01020304.jar" ||
		pkg.ClientName != "client.bin4096" ||
		pkg.BSSSize != 4096 {
		t.Fatalf("binary metadata = jar %q client %q bss %d",
			pkg.JARName, pkg.ClientName, pkg.BSSSize)
	}
	if !bytes.Equal(pkg.Client, []byte{0x04, 0xe0, 0x70, 0x47}) {
		t.Fatalf("client = %x", pkg.Client)
	}
	if _, ok := pkg.Resources["client.bin4096"]; ok {
		t.Fatal("client image leaked into resources")
	}
	if !bytes.Equal(pkg.Resources["r/icon.png"], []byte{1, 2, 3}) {
		t.Fatalf("resource = %x", pkg.Resources["r/icon.png"])
	}
}

func TestInspectWrappedPackage(t *testing.T) {
	jar := makeZIP(t, map[string][]byte{
		"client.bin64": {0x70, 0x47},
	})
	archive := makeZIP(t, map[string][]byte{
		"installer/apps/game/01020304.jar": jar,
		"installer/apps/game/__adf__": []byte(
			"PID:PD000001\nAID:01020304\nMClass:GameMain\n",
		),
		"installer/exe_info": []byte("metadata"),
	})

	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.JARName != "installer/apps/game/01020304.jar" {
		t.Fatalf("JARName = %q", pkg.JARName)
	}
	if pkg.ClientName != "client.bin64" || pkg.BSSSize != 64 {
		t.Fatalf("client = %q, BSS = %d", pkg.ClientName, pkg.BSSSize)
	}
	if _, ok := pkg.Files["installer/exe_info"]; !ok {
		t.Fatal("outer installer metadata was not retained")
	}
}

func TestInspectRejectsNonPackageAndUnsafeNestedMember(t *testing.T) {
	ordinary := makeZIP(t, map[string][]byte{"game.jar": []byte("jar")})
	if _, err := Inspect(ordinary); !errors.Is(err, ErrNotPackage) {
		t.Fatalf("ordinary ZIP error = %v", err)
	}

	jar := makeZIP(t, map[string][]byte{
		"client.bin1": {0x70, 0x47},
		"../escape":   {1},
	})
	archive := makeZIP(t, map[string][]byte{
		"game.jar": jar,
		"__adf__":  []byte("PID:pid\nAID:game\nMClass:Main\n"),
	})
	if _, err := Inspect(archive); err == nil {
		t.Fatal("Inspect accepted an unsafe JAR member")
	}
}

func TestInspectRejectsAmbiguousWrappedPackages(t *testing.T) {
	archive := makeZIP(t, map[string][]byte{
		"first/__adf__":  []byte("AID:first\n"),
		"second/__adf__": []byte("AID:second\n"),
	})
	if _, err := Inspect(archive); err == nil {
		t.Fatal("Inspect accepted multiple package descriptors")
	}
}

func TestInspectAcceptsNullEncryptedOMADCFJar(t *testing.T) {
	jar := makeZIP(t, map[string][]byte{
		"client.bin64": {0x70, 0x47},
		"icon.png":     {1, 2, 3},
	})
	archive := makeZIP(t, map[string][]byte{
		"01020304.jar": makeOMADCF(t, 0, uint64(len(jar)), jar),
		"__adf__":      []byte("PID:pid\nAID:01020304\nMClass:Main\n"),
	})
	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ClientName != "client.bin64" ||
		!bytes.Equal(pkg.Resources["icon.png"], []byte{1, 2, 3}) {
		t.Fatalf("NULL-encrypted DCF package = %+v", pkg)
	}
}

func TestInspectRetainsCompleteResourceWithBadChecksum(t *testing.T) {
	jar := makeZIP(t, map[string][]byte{
		"client.bin64": {0x70, 0x47},
		"icon.png":     {1, 2, 3},
	})
	jar = corruptZIPMemberChecksum(t, jar, "icon.png")
	archive := makeZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:pid\nAID:01020304\nMClass:Main\n"),
	})

	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pkg.Resources["icon.png"], []byte{1, 2, 3}) {
		t.Fatalf("bad-checksum resource = %x", pkg.Resources["icon.png"])
	}
	if len(pkg.Warnings) != 1 ||
		pkg.Warnings[0] !=
			"icon.png: checksum mismatch; retained complete payload" {
		t.Fatalf("bad-checksum warnings = %v", pkg.Warnings)
	}

	jar = makeZIP(t, map[string][]byte{
		"client.bin64": {0x70, 0x47},
	})
	jar = corruptZIPMemberChecksum(t, jar, "client.bin64")
	archive = makeZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:pid\nAID:01020304\nMClass:Main\n"),
	})
	if _, err := Inspect(archive); err == nil {
		t.Fatal("Inspect accepted a client image with a bad checksum")
	}
}

func TestInspectReportsEncryptedOMADCFJarAsProtected(t *testing.T) {
	const contentID = "00WIPI00000000000001020304"
	encrypted := make([]byte, 16+32)
	archive := makeZIP(t, map[string][]byte{
		"01020304.jar": makeOMADCF(t, 2, 32, encrypted),
		"__adf__":      []byte("PID:pid\nAID:01020304\nMClass:Main\n"),
	})
	_, err := Inspect(archive)
	if !errors.Is(err, ErrProtectedContent) {
		t.Fatalf("encrypted DCF error = %v", err)
	}
	var protected *ProtectedContentError
	if !errors.As(err, &protected) ||
		protected.Path != "01020304.jar" ||
		protected.ContentID != contentID ||
		protected.Algorithm != 2 {
		t.Fatalf("protected DCF error = %#v", protected)
	}
}

func TestParseDescriptorReadsDisplaySize(t *testing.T) {
	descriptor, err := ParseDescriptor([]byte(
		"PID:PD005263\r\nAID:01038900\r\nMClass:Maple\r\nDisplaySize:176*220\r\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.DisplayWidth != 176 || descriptor.DisplayHeight != 220 {
		t.Fatalf(
			"display size = %dx%d",
			descriptor.DisplayWidth,
			descriptor.DisplayHeight,
		)
	}
}

// The field is optional, and a descriptor that spells it oddly still has to
// load; the caller falls back to its own default when it reads back as absent.
func TestParseDescriptorIgnoresUnusableDisplaySize(t *testing.T) {
	for _, value := range []string{
		"",
		"176",
		"176*",
		"176*0",
		"0*220",
		"-176*220",
		"wide*tall",
		"99999*99999",
	} {
		descriptor, err := ParseDescriptor([]byte(
			"AID:01020304\nMClass:Main\nDisplaySize:" + value + "\n",
		))
		if err != nil {
			t.Fatalf("descriptor %q: %v", value, err)
		}
		if descriptor.DisplayWidth != 0 || descriptor.DisplayHeight != 0 {
			t.Fatalf(
				"display size for %q = %dx%d, want absent",
				value,
				descriptor.DisplayWidth,
				descriptor.DisplayHeight,
			)
		}
	}
}

func TestParseBSSSizeRejectsMissingAndExcessiveValues(t *testing.T) {
	for _, name := range []string{
		"client.bin",
		"client.bin-1",
		"client.binABC",
		"client.bin4294967295",
		"other.bin1",
	} {
		if _, err := ParseBSSSize(name); err == nil {
			t.Fatalf("ParseBSSSize(%q) succeeded", name)
		}
	}
}

func FuzzInspect(f *testing.F) {
	f.Add(makeZIP(f, map[string][]byte{
		"game.jar": makeZIP(f, map[string][]byte{
			"client.bin4": {0x70, 0x47},
		}),
		"__adf__": []byte("PID:pid\nAID:game\nMClass:Main\n"),
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

func corruptZIPMemberChecksum(
	t testingTB,
	archive []byte,
	name string,
) []byte {
	t.Helper()
	output := bytes.Clone(archive)
	for offset := 0; offset+46 <= len(output); {
		index := bytes.Index(output[offset:], []byte("PK\x01\x02"))
		if index < 0 {
			break
		}
		offset += index
		nameLength := int(binary.LittleEndian.Uint16(output[offset+28:]))
		extraLength := int(binary.LittleEndian.Uint16(output[offset+30:]))
		commentLength := int(binary.LittleEndian.Uint16(output[offset+32:]))
		end := offset + 46 + nameLength + extraLength + commentLength
		if end > len(output) {
			t.Fatal("ZIP central directory entry is truncated")
		}
		if string(output[offset+46:offset+46+nameLength]) == name {
			checksum := binary.LittleEndian.Uint32(output[offset+16:])
			binary.LittleEndian.PutUint32(output[offset+16:], checksum^0xffffffff)
			return output
		}
		offset = end
	}
	t.Fatal("ZIP member not found:", name)
	return nil
}

func makeOMADCF(
	t testingTB,
	algorithm uint16,
	plaintextBytes uint64,
	object []byte,
) []byte {
	t.Helper()
	const contentID = "00WIPI00000000000001020304"
	var common bytes.Buffer
	common.Write(make([]byte, 4))
	_ = binary.Write(&common, binary.BigEndian, algorithm)
	_ = binary.Write(&common, binary.BigEndian, uint16(0))
	_ = binary.Write(&common, binary.BigEndian, plaintextBytes)
	_ = binary.Write(&common, binary.BigEndian, uint16(len(contentID)))
	_ = binary.Write(&common, binary.BigEndian, uint16(0))
	_ = binary.Write(&common, binary.BigEndian, uint16(0))
	common.WriteString(contentID)
	ohdr := makeOMADCFBox("ohdr", common.Bytes())

	contentType := []byte("application/java-archive")
	var headers bytes.Buffer
	headers.Write(make([]byte, 4))
	headers.WriteByte(byte(len(contentType)))
	headers.Write(contentType)
	headers.Write(ohdr)
	odhe := makeOMADCFBox("odhe", headers.Bytes())

	var content bytes.Buffer
	content.Write(make([]byte, 4))
	_ = binary.Write(&content, binary.BigEndian, uint64(len(object)))
	content.Write(object)
	odda := makeOMADCFBox("odda", content.Bytes())

	var container bytes.Buffer
	container.Write(make([]byte, 4))
	container.Write(odhe)
	container.Write(odda)
	odrm := makeOMADCFBox("odrm", container.Bytes())

	output := append([]byte("odcf\x00\x02\x00\x00"), odrm...)
	return output
}

func makeOMADCFBox(kind string, payload []byte) []byte {
	output := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(output, uint32(8+len(payload)))
	copy(output[4:], kind)
	return append(output, payload...)
}
