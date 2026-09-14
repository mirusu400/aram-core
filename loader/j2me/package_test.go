package j2me

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func testZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(files[n]); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func testClass() []byte {
	var b bytes.Buffer
	u2 := func(v uint16) { _ = binary.Write(&b, binary.BigEndian, v) }
	_ = binary.Write(&b, binary.BigEndian, uint32(0xcafebabe))
	u2(3)
	u2(45)
	u2(5)
	for _, s := range []string{"Game", "java/lang/Object"} {
		b.WriteByte(1)
		u2(uint16(len(s)))
		b.WriteString(s)
		b.WriteByte(7)
		if s == "Game" {
			u2(1)
		} else {
			u2(3)
		}
	}
	for _, v := range []uint16{1, 2, 4, 0, 0, 0, 0} {
		u2(v)
	}
	return b.Bytes()
}

const testManifest = "Manifest-Version: 1.0\r\nMIDlet-Name: Demo\r\nMIDlet-Version: 1.0\r\nMIDlet-Vendor: Test\r\nMIDlet-1: Demo, , Ga\r\n me\r\nMicroEdition-Profile: MIDP-1.0\r\nMicroEdition-Configuration: CLDC-1.0\r\n\r\nName: icon.png\r\nMIDlet-1: Wrong, , Wrong\r\n"

func testJAR(t *testing.T) []byte {
	return testZIP(t, map[string][]byte{"META-INF/MANIFEST.MF": []byte(testManifest), "Game.class": testClass(), "icon.png": []byte("synthetic resource")})
}
func TestInspectStandaloneAndJADPair(t *testing.T) {
	jar := testJAR(t)
	for _, url := range []string{"", "Game.jar", "https://example.invalid/Game.jar"} {
		jad := "MIDlet-1: Demo, , Game\nTest-Override: jad\nMIDlet-Jar-Size: " + fmt.Sprint(len(jar)) + "\n"
		if url != "" {
			jad += "MIDlet-Jar-URL: " + url + "\n"
		}
		pair := testZIP(t, map[string][]byte{"pkg/Game.jar": jar, "pkg/Game.jad": []byte(jad)})
		for _, data := range [][]byte{jar, pair} {
			pkg, err := Inspect(data)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Descriptor.MainClass != "Game" || pkg.Descriptor.Name != "Demo" || len(pkg.Classes) != 1 || string(pkg.Resources["icon.png"]) != "synthetic resource" {
				t.Fatalf("unexpected package %#v", pkg)
			}
			if bytes.Equal(data, pair) && pkg.Descriptor.Raw["Test-Override"] != "jad" {
				t.Fatal("JAD properties not merged")
			}
		}
	}
}
func TestInspectMalformed(t *testing.T) {
	jar := testJAR(t)
	tests := map[string]map[string][]byte{
		"missing jar":          {"Game.jad": []byte("MIDlet-1: Demo, , Game")},
		"multiple jars":        {"Game.jad": []byte("MIDlet-1: Demo, , Game"), "Game.jar": jar, "Other.jar": jar},
		"size":                 {"Game.jad": []byte("MIDlet-Jar-Size: 1\nMIDlet-1: Demo, , Game"), "Game.jar": jar},
		"url traversal":        {"Game.jad": []byte("MIDlet-Jar-URL: ../Game.jar\nMIDlet-1: Demo, , Game"), "Game.jar": jar},
		"encoded traversal":    {"Game.jad": []byte("MIDlet-Jar-URL: %2e%2e/Game.jar\nMIDlet-1: Demo, , Game"), "Game.jar": jar},
		"wrong url":            {"Game.jad": []byte("MIDlet-Jar-URL: https://example.invalid/Other.jar\nMIDlet-1: Demo, , Game"), "Game.jar": jar},
		"identity mismatch":    {"Game.jad": []byte("MIDlet-Name: Other\nMIDlet-1: Demo, , Game"), "Game.jar": jar},
		"malformed class":      {"META-INF/MANIFEST.MF": []byte(testManifest), "Game.class": []byte("bad")},
		"class path mismatch":  {"META-INF/MANIFEST.MF": []byte(testManifest), "Other.class": testClass()},
		"missing main":         {"META-INF/MANIFEST.MF": []byte(testManifest)},
		"duplicate property":   {"META-INF/MANIFEST.MF": []byte("MIDlet-1: Demo, , Game\nMIDlet-1: Other, , Game\n"), "Game.class": testClass()},
		"orphan continuation":  {"META-INF/MANIFEST.MF": []byte(" orphan\nMIDlet-1: Demo, , Game"), "Game.class": testClass()},
		"oversized descriptor": {"META-INF/MANIFEST.MF": bytes.Repeat([]byte{'x'}, MaxDescriptorSize+1)},
		"oversized line":       {"META-INF/MANIFEST.MF": []byte("MIDlet-1: " + strings.Repeat("x", MaxDescriptorLine))},
		"unsafe path":          {"META-INF/MANIFEST.MF": []byte(testManifest), "../Game.class": testClass()},
		"case alias":           {"META-INF/MANIFEST.MF": []byte(testManifest), "Game.class": testClass(), "game.class": testClass()},
	}
	for name, files := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Inspect(testZIP(t, files))
			var format *FormatError
			if !errors.As(err, &format) || format.Offset < 0 {
				t.Fatalf("wanted offset-bearing format error, got %v", err)
			}
		})
	}
}
func TestZIPBudgetsAndCorruption(t *testing.T) {
	data := testZIP(t, map[string][]byte{"file": []byte("test")})
	budget := uint64(3)
	if _, err := readZIP(data, "test", &budget); err == nil {
		t.Fatal("aggregate limit ignored")
	}
	oversized := append([]byte(nil), data...)
	central := bytes.Index(oversized, []byte{'P', 'K', 1, 2})
	binary.LittleEndian.PutUint32(oversized[central+24:], MaxMemberSize+1)
	budget = MaxExpandedSize
	if _, err := readZIP(oversized, "test", &budget); err == nil {
		t.Fatal("member limit ignored")
	}
	corrupt := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(corrupt[central+16:], 0)
	budget = MaxExpandedSize
	if _, err := readZIP(corrupt, "test", &budget); err == nil {
		t.Fatal("checksum ignored")
	}
	if _, err := Inspect(data[:len(data)-10]); err == nil {
		t.Fatal("truncated ZIP accepted")
	}
	files := map[string][]byte{}
	for i := 0; i <= MaxArchiveEntries; i++ {
		files[fmt.Sprint(i)] = nil
	}
	budget = MaxExpandedSize
	if _, err := readZIP(testZIP(t, files), "test", &budget); err == nil {
		t.Fatal("entry limit ignored")
	}
}
func TestUnclaimedArchives(t *testing.T) {
	for _, data := range [][]byte{[]byte("MIDlet-1: Demo, , Game"), testZIP(t, map[string][]byte{"Game.class": testClass()}), testZIP(t, map[string][]byte{"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"), "ordinary.txt": nil})} {
		if _, err := Inspect(data); !errors.Is(err, ErrNotPackage) {
			t.Fatalf("ordinary input claimed: %v", err)
		}
	}
}

func TestStaleRemoteURLAndUnsupportedRMS(t *testing.T) {
	jar := testJAR(t)
	remote := "https://example.invalid/original-name.jar"
	jad := []byte(fmt.Sprintf("MIDlet-Jar-URL: %s\nMIDlet-Jar-Size: %d\nMIDlet-1: Demo, , Game\n", remote, len(jar)))
	files := map[string][]byte{"local/Game.jad": jad, "local/Game.jar": jar}
	pkg, err := Inspect(testZIP(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Descriptor.JARURL != remote {
		t.Fatal("remote metadata rewritten")
	}
	for _, name := range []string{"local/Other.jar", "elsewhere/Game.jar"} {
		if _, err := Inspect(testZIP(t, map[string][]byte{"local/Game.jad": jad, name: jar})); err == nil {
			t.Fatal("unassociated stale URL pair accepted")
		}
	}
	for _, bad := range []string{
		fmt.Sprintf("MIDlet-Jar-URL: %s\n", remote),
		fmt.Sprintf("MIDlet-Jar-URL: %s\nMIDlet-Jar-Size: 1\n", remote),
		fmt.Sprintf("MIDlet-Jar-URL: %s\nMIDlet-Jar-Size: %d\nMIDlet-Name: Conflicting\n", remote, len(jar)),
		fmt.Sprintf("MIDlet-Jar-URL: ../original-name.jar\nMIDlet-Jar-Size: %d\n", len(jar)),
	} {
		if _, err := Inspect(testZIP(t, map[string][]byte{"local/Game.jad": []byte(bad), "local/Game.jar": jar})); err == nil {
			t.Fatal("unsafe stale URL exception accepted")
		}
	}
	files["saved.idx"] = []byte("synthetic")
	_, err = Inspect(testZIP(t, files))
	var unsupported *UnsupportedFeatureError
	var malformed *FormatError
	if !errors.Is(err, ErrUnsupportedFeature) || !errors.As(err, &unsupported) || errors.As(err, &malformed) {
		t.Fatalf("RMS should be unsupported, not corrupt: %v", err)
	}
}
func FuzzInspect(f *testing.F) {
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte("MIDlet-1: Demo, , Game"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_, _ = Inspect(data)
	})
}

func TestArchivedFilenameAliasRequiresCompleteIdentity(t *testing.T) {
	const remote = "https://example.invalid/original.jar"
	manifest := "Manifest-Version: 1.0\nMIDlet-Name: Demo\nMIDlet-Version: 1.0\nMIDlet-Vendor: Test\nMIDlet-1: Demo, , Game\n"
	jar := testZIP(t, map[string][]byte{"META-INF/MANIFEST.MF": []byte(manifest), "Game.class": testClass()})
	jad := fmt.Sprintf("MIDlet-Jar-URL: %s\nMIDlet-Jar-Size: %d\nMIDlet-Name: Demo\nMIDlet-Version: 1.0\nMIDlet-Vendor: Test\nMIDlet-1: Demo, , Game\n", remote, len(jar))
	pack := func(jadText string, jarBytes []byte) []byte {
		return testZIP(t, map[string][]byte{"metadata/renamed.jad": []byte(jadText), "application/archived.jar": jarBytes})
	}
	pkg, err := Inspect(pack(jad, jar))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Descriptor.JARURL != remote || pkg.JARName != "application/archived.jar" || pkg.Descriptor.MainClass != "Game" {
		t.Fatal("alias association rewrote identity")
	}
	for _, key := range []string{"MIDlet-Name", "MIDlet-Version", "MIDlet-Vendor", "MIDlet-1"} {
		value := map[string]string{"MIDlet-Name": "Demo", "MIDlet-Version": "1.0", "MIDlet-Vendor": "Test", "MIDlet-1": "Demo, , Game"}[key]
		line := key + ": " + value + "\n"
		for _, replacement := range []string{"", key + ": \n", key + ": Different\n"} {
			t.Run(key+replacement, func(t *testing.T) {
				_, err := Inspect(pack(strings.Replace(jad, line, replacement, 1), jar))
				if err == nil {
					t.Fatal("alias accepted missing or mismatched JAD identity")
				}
				changed := testZIP(t, map[string][]byte{"META-INF/MANIFEST.MF": []byte(strings.Replace(manifest, line, replacement, 1)), "Game.class": testClass()})
				changedJAD := strings.Replace(jad, fmt.Sprintf("MIDlet-Jar-Size: %d", len(jar)), fmt.Sprintf("MIDlet-Jar-Size: %d", len(changed)), 1)
				if _, err := Inspect(pack(changedJAD, changed)); err == nil {
					t.Fatal("alias accepted missing or mismatched manifest identity")
				}
			})
		}
	}
	for _, bad := range []string{
		strings.Replace(jad, fmt.Sprintf("MIDlet-Jar-Size: %d\n", len(jar)), "", 1),
		strings.Replace(jad, fmt.Sprintf("MIDlet-Jar-Size: %d", len(jar)), "MIDlet-Jar-Size: 1", 1),
		strings.Replace(jad, remote, "../application/archived.jar", 1),
		strings.Replace(jad, remote, "other.jar", 1),
		strings.Replace(jad, remote, "file:///original.jar", 1),
		strings.Replace(jad, "MIDlet-1: Demo, , Game", "MIDlet-1: Demo, , Other", 1),
		strings.Replace(jad, "MIDlet-1: Demo, , Game", "MIDlet-1: Demo, , ", 1),
	} {
		if _, err := Inspect(pack(bad, jar)); err == nil {
			t.Fatal("unsafe alias association accepted")
		}
	}
	if _, err := Inspect(testZIP(t, map[string][]byte{"renamed.jad": []byte(jad), "first.jar": jar, "second.jar": jar})); err == nil {
		t.Fatal("alias accepted ambiguous JARs")
	}
}
