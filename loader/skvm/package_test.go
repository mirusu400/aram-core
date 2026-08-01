package skvm

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestInspectWrappedPackage(t *testing.T) {
	class := []byte{0xca, 0xfe, 0xba, 0xbe, 0, 3, 0, 45}
	jar := makeZIP(t, map[string][]byte{
		"Game.class": class,
		"image.dat":  {1, 2, 3},
	})
	wrapped := make([]byte, 32+len(jar))
	binary.LittleEndian.PutUint32(wrapped, 32)
	copy(wrapped[32:], jar)
	archive := makeZIP(t, map[string][]byte{
		"12345.msd": []byte(
			"MIDlet-Name: Test\n" +
				"MIDlet-Version: 1.0\n" +
				"MIDlet-Vendor: ARAM\n" +
				"MIDlet-Jar-Size: 999\n" +
				"MicroEdition-Profile: M_Profile-1.0, SKTP-1.0\n" +
				"MicroEdition-Configuration: M_Configuration-1.0\n" +
				"MIDlet-1: Test,,Game\n" +
				"DD-ProgName: 12345\n" +
				"DD-MIME-Type: application/x-wipi-jar\n",
		),
		"12345.jar":       wrapped,
		"12345.mod":       {4, 5},
		"12345.wmr":       {0xad, 0xde, 0xce, 0xfa},
		"rs/game#Data.db": []byte("firstsecond"),
		"rs/game#Data.sb": recordStoreMetadata(
			t,
			"gameData",
			9,
			11,
			[][3]uint32{{1, 0, 5}, {4, 5, 6}},
		),
	})

	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.BaseName != "12345" || pkg.Descriptor.MainClass != "Game" {
		t.Fatalf("unexpected package identity: %#v", pkg)
	}
	if len(pkg.JARHeader) != 32 {
		t.Fatalf("JAR header length = %d, want 32", len(pkg.JARHeader))
	}
	if got := pkg.Classes["Game"]; got.MajorVersion != 45 || got.MinorVersion != 3 {
		t.Fatalf("unexpected class metadata: %#v", got)
	}
	if !bytes.Equal(pkg.Resources["image.dat"], []byte{1, 2, 3}) {
		t.Fatalf("resource = %v", pkg.Resources["image.dat"])
	}
	if len(pkg.RecordStores) != 1 ||
		pkg.RecordStores[0].Name != "gameData" ||
		pkg.RecordStores[0].NextID != 9 ||
		len(pkg.RecordStores[0].Records) != 2 ||
		pkg.RecordStores[0].Records[0].ID != 1 ||
		!bytes.Equal(pkg.RecordStores[0].Records[0].Data, []byte("first")) ||
		pkg.RecordStores[0].Records[1].ID != 4 ||
		!bytes.Equal(pkg.RecordStores[0].Records[1].Data, []byte("second")) {
		t.Fatalf("record stores = %#v", pkg.RecordStores)
	}
}

func TestInspectRejectsOrdinaryJavaArchive(t *testing.T) {
	data := makeZIP(t, map[string][]byte{"Game.class": {0xca, 0xfe, 0xba, 0xbe}})
	if _, err := Inspect(data); !errors.Is(err, ErrNotPackage) {
		t.Fatalf("Inspect error = %v, want ErrNotPackage", err)
	}
}

func TestInspectRequiresMainClass(t *testing.T) {
	jar := makeZIP(t, map[string][]byte{
		"Other.class": {0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 45},
	})
	archive := makeZIP(t, map[string][]byte{
		"1.msd": []byte(
			"MicroEdition-Profile: SKTP-1.0\nMIDlet-1: Test,,Game\n",
		),
		"1.jar": jar,
		"1.mod": {1},
		"1.wmr": {1},
	})
	if _, err := Inspect(archive); err == nil {
		t.Fatal("Inspect unexpectedly accepted a missing main class")
	}
}

func TestParseDescriptorRejectsInvalidMainClass(t *testing.T) {
	_, err := ParseDescriptor([]byte(
		"MicroEdition-Profile: SKTP-1.0\nMIDlet-1: Test,,../Game\n",
	))
	if err == nil {
		t.Fatal("ParseDescriptor unexpectedly accepted an unsafe class name")
	}
}

func TestInspectRejectsUnsafeMember(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("../escape.msd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("MIDlet-1: Test,,Game\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(buffer.Bytes()); err == nil {
		t.Fatal("Inspect unexpectedly accepted an unsafe member")
	}
}

func makeZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, payload := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func recordStoreMetadata(
	t *testing.T,
	name string,
	nextID, databaseSize uint32,
	records [][3]uint32,
) []byte {
	t.Helper()
	var output bytes.Buffer
	for _, value := range []any{
		uint32(2),
		uint16(len(name)),
		[]byte(name),
		nextID,
		uint32(len(records)),
		databaseSize,
		uint64(0x0102030405060708),
	} {
		if err := binary.Write(&output, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range records {
		for _, value := range record {
			if err := binary.Write(&output, binary.BigEndian, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	return output.Bytes()
}
