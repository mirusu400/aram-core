package gnex

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

func buildZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return buf.Bytes()
}

func validSGS(t *testing.T, title string) []byte {
	t.Helper()
	return buildSGS(1, 0, eucKR(t, title), []byte{0x01, 0x02, 0x03})
}

func TestInspectAcceptsMODPair(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"4400410006.mod": []byte("manifest bytes, format not fully decoded"),
		"4400410006.SGS": validSGS(t, "강호동의천생연분"),
	})

	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if pkg.BaseName != "4400410006" {
		t.Errorf("BaseName = %q, want %q", pkg.BaseName, "4400410006")
	}
	if pkg.ManifestKind != ManifestMOD {
		t.Errorf("ManifestKind = %q, want %q", pkg.ManifestKind, ManifestMOD)
	}
	if pkg.Header.Title != "강호동의천생연분" {
		t.Errorf("Header.Title = %q, want %q", pkg.Header.Title, "강호동의천생연분")
	}
	if len(pkg.Files) != 2 {
		t.Errorf("len(Files) = %d, want 2", len(pkg.Files))
	}
}

func TestInspectAcceptsINFPair(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"3407041031.inf": []byte("older descriptor shape, not fully decoded"),
		"3407041031.sgs": validSGS(t, "북벌"),
		"3407041031.res": []byte("icon resource, unrelated to package identity"),
	})

	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if pkg.ManifestKind != ManifestINF {
		t.Errorf("ManifestKind = %q, want %q", pkg.ManifestKind, ManifestINF)
	}
	if pkg.Header.Title != "북벌" {
		t.Errorf("Header.Title = %q, want %q", pkg.Header.Title, "북벌")
	}
	if len(pkg.Files) != 3 {
		t.Errorf("len(Files) = %d, want 3", len(pkg.Files))
	}
}

func TestInspectIsCaseInsensitiveToExtension(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"title.MOD": []byte("manifest"),
		"title.sgs": validSGS(t, "놈"),
	})
	if _, err := Inspect(archive); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}

func TestInspectRejectsNonZIP(t *testing.T) {
	if _, err := Inspect([]byte("not a zip file at all")); !errors.Is(err, ErrNotPackage) {
		t.Errorf("error = %v, want ErrNotPackage", err)
	}
}

func TestInspectRejectsZIPWithoutSGS(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"readme.txt": []byte("nothing GNEX-shaped in here"),
	})
	if _, err := Inspect(archive); !errors.Is(err, ErrNotPackage) {
		t.Errorf("error = %v, want ErrNotPackage", err)
	}
}

func TestInspectRejectsSGSWithoutManifest(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"orphan.sgs": validSGS(t, "고아"),
	})
	if _, err := Inspect(archive); !errors.Is(err, ErrNotPackage) {
		t.Errorf("error = %v, want ErrNotPackage", err)
	}
}

func TestInspectReportsFormatErrorForBadSGSHeader(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"broken.mod": []byte("manifest"),
		"broken.sgs": []byte("not a valid SGS header at all"),
	})
	_, err := Inspect(archive)
	var formatErr *FormatError
	if !errors.As(err, &formatErr) {
		t.Fatalf("error = %v (%T), want *FormatError", err, err)
	}
}

func TestInspectRejectsMultipleCandidates(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"one.mod": []byte("manifest"),
		"one.sgs": validSGS(t, "하나"),
		"two.mod": []byte("manifest"),
		"two.sgs": validSGS(t, "둘"),
	})
	_, err := Inspect(archive)
	var formatErr *FormatError
	if !errors.As(err, &formatErr) {
		t.Fatalf("error = %v (%T), want *FormatError", err, err)
	}
}
