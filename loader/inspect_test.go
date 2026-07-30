package loader

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDATWithMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.dat")
	data := make([]byte, 512)
	copy(data[128:], "ABHS")
	copy(data[400:], "EADS")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := InspectFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != KindDAT {
		t.Fatalf("Kind = %q, want %q", report.Kind, KindDAT)
	}
	if len(report.Markers) != 2 {
		t.Fatalf("len(Markers) = %d, want 2", len(report.Markers))
	}
	if report.Markers[0].Magic != "ABHS" || report.Markers[0].Offset != 128 {
		t.Fatalf("first marker = %#v", report.Markers[0])
	}
	if report.SHA256 == "" {
		t.Fatal("SHA256 is empty")
	}
}

func TestInspectRejectsDirectory(t *testing.T) {
	if _, err := InspectFile(t.TempDir()); err == nil {
		t.Fatal("InspectFile(directory) succeeded")
	}
}

func TestInspectBytesDetectsSupportedKindsAndHash(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want Kind
	}{
		{"module.resource", []byte("ABHS payload"), KindABHS},
		{"image.resource", []byte("EADS payload"), KindEADS},
		{"program.resource", []byte{0x7f, 'E', 'L', 'F'}, KindELF},
		{"application.jar", []byte{'P', 'K', 3, 4}, KindJava},
		{"application.dat", []byte("data"), KindDAT},
		{"firmware.wbin", []byte("data"), KindWBIN},
		{"boot.wbt", []byte("data"), KindWBT},
		{"device.fnt", []byte("data"), KindFont},
		{"firmware.bin", []byte("data"), KindFirmware},
		{"unknown.resource", []byte("data"), KindUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := InspectBytes(test.name, test.data)
			if err != nil {
				t.Fatal(err)
			}
			if report.Kind != test.want {
				t.Fatalf("Kind = %q, want %q", report.Kind, test.want)
			}
		})
	}

	report, err := InspectBytes("hash.resource", []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	const wantSHA256 = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if report.SHA256 != wantSHA256 {
		t.Fatalf("SHA256 = %q, want %q", report.SHA256, wantSHA256)
	}
}

func TestInspectBytesOrdersMarkersByOffset(t *testing.T) {
	data := make([]byte, 64)
	copy(data[8:], "EADS")
	copy(data[40:], "ABHS")
	report, err := InspectBytes("embedded.resource", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Markers) != 2 ||
		report.Markers[0] != (Marker{Magic: "EADS", Offset: 8}) ||
		report.Markers[1] != (Marker{Magic: "ABHS", Offset: 40}) {
		t.Fatalf("Markers = %+v", report.Markers)
	}
	if report.Kind != KindEADS {
		t.Fatalf("Kind = %q, want %q", report.Kind, KindEADS)
	}
}

func TestInspectFindsMarkerAcrossReadBoundary(t *testing.T) {
	data := make([]byte, 1024*1024+4)
	copy(data[1024*1024-2:], "ABHS")
	report, err := InspectBytes("boundary.bin", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Markers) != 1 ||
		report.Markers[0].Offset != 1024*1024-2 {
		t.Fatalf("Markers = %+v", report.Markers)
	}
}

func TestInspectReadsOnlyDeclaredSize(t *testing.T) {
	report, err := Inspect("sample.dat", &countingReaderAt{
		data: []byte("EADS trailing bytes"),
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if report.Size != 4 || len(report.Markers) != 1 ||
		report.Markers[0].Offset != 0 {
		t.Fatalf("Report = %+v", report)
	}
}

func TestInspectReportsReadOffset(t *testing.T) {
	_, err := Inspect("broken.bin", &failingReaderAt{
		data:   make([]byte, 128),
		failAt: 73,
	}, 128)
	var offsetErr *OffsetError
	if !errors.As(err, &offsetErr) {
		t.Fatalf("Inspect error = %v, want OffsetError", err)
	}
	if offsetErr.Offset != 73 || !errors.Is(offsetErr, io.ErrUnexpectedEOF) {
		t.Fatalf("OffsetError = %+v", offsetErr)
	}
}

type countingReaderAt struct {
	data []byte
}

func (r *countingReaderAt) ReadAt(destination []byte, offset int64) (int, error) {
	if offset >= int64(len(r.data)) {
		return 0, io.EOF
	}
	count := copy(destination, r.data[offset:])
	if count != len(destination) {
		return count, io.EOF
	}
	return count, nil
}

type failingReaderAt struct {
	data   []byte
	failAt int64
}

func (r *failingReaderAt) ReadAt(destination []byte, offset int64) (int, error) {
	if offset >= r.failAt {
		return 0, io.EOF
	}
	available := min(int64(len(destination)), r.failAt-offset)
	count := copy(destination[:available], r.data[offset:offset+available])
	if count != len(destination) {
		return count, io.EOF
	}
	return count, nil
}
