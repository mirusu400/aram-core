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

func TestInspectAcceptsSGSWithoutManifest(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"orphan.sgs": validSGS(t, "고아"),
	})
	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ManifestKind != ManifestNone || pkg.ManifestName != "" || pkg.Manifest != nil || pkg.Header.Title != "고아" {
		t.Fatalf("unexpected standalone metadata: %+v", pkg)
	}
}

func TestInspectRawStandaloneSGS(t *testing.T) {
	for _, version := range []byte{1, 2} {
		for _, prefix := range []int{0, 32} {
			data := buildSGS(version, prefix, []byte("Synthetic"), []byte{0x55})
			pkg, err := Inspect(data)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Header.FormatVersion != version || pkg.Header.PrefixOffset != prefix || pkg.SGSName != "" || pkg.ManifestKind != ManifestNone {
				t.Fatalf("unexpected raw SGS metadata: %+v", pkg)
			}
			data[len(data)-1] = 0
			if pkg.SGS[len(pkg.SGS)-1] != 0x55 {
				t.Fatal("raw SGS aliases caller buffer")
			}
		}
	}
}

func TestInspectStandaloneRejectsUnprovenShapes(t *testing.T) {
	nonzeroPrefix := buildSGS(1, 32, []byte("Synthetic"), []byte{1})
	nonzeroPrefix[0] = 0x55
	cases := map[string][]byte{
		"header only":       buildSGS(1, 0, []byte("Synthetic"), nil),
		"unobserved prefix": buildSGS(1, 8, []byte("Synthetic"), []byte{1}),
		"nonzero prefix":    nonzeroPrefix,
		"invalid encoding":  buildSGS(1, 0, []byte{0xff}, []byte{1}),
		"embedded NUL":      buildSGS(1, 0, []byte{'a', 0, 'b'}, []byte{1}),
		"control":           buildSGS(1, 0, []byte{'a', '\n', 'b'}, []byte{1}),
		"extension only":    []byte("not SGS"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Inspect(data); !errors.Is(err, ErrNotPackage) {
				t.Fatalf("raw: %v, want ErrNotPackage", err)
			}
			if _, err := Inspect(buildZIP(t, map[string][]byte{"file.sgs": data})); !errors.Is(err, ErrNotPackage) {
				t.Fatalf("ZIP: %v, want ErrNotPackage", err)
			}
		})
	}
}

func TestInspectRejectsPairedAndStandaloneAmbiguity(t *testing.T) {
	archive := buildZIP(t, map[string][]byte{
		"one.sgs": validSGS(t, "One"), "one.mod": []byte("descriptor"),
		"two.sgs": validSGS(t, "Two"),
	})
	var formatErr *FormatError
	if _, err := Inspect(archive); !errors.As(err, &formatErr) {
		t.Fatalf("error = %v, want FormatError", err)
	}
}

func FuzzInspectStandaloneSGS(f *testing.F) {
	f.Add(buildSGS(1, 0, []byte("Synthetic"), []byte{1}))
	f.Add(buildSGS(2, 32, []byte("Synthetic"), []byte{1}))
	f.Add([]byte("unrelated"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if hasZIPMagic(data) {
			return
		}
		pkg, err := Inspect(data)
		if err == nil && (pkg.Header.BodyOffset >= len(pkg.SGS) || pkg.Header.BodyOffset < 12) {
			t.Fatal("recognized standalone has invalid body boundary")
		}
	})
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
