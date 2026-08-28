package loader

import (
	"archive/zip"
	"bytes"
	"testing"
)

func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// Issue #69, 크아비엔비2011: the archive holds one Android APK. It is a ZIP, so
// it reaches the WIPI loaders and fails all of them, and the report the user
// filed carried the last scan's "no valid ABHS or EADS records" as the reason.
func TestAndroidPackageSeparatesAPKsFromWIPIArchives(t *testing.T) {
	for _, test := range []struct {
		name        string
		entries     map[string]string
		wantWrapped bool
		wantAndroid bool
	}{
		{
			name: "apk",
			entries: map[string]string{
				"AndroidManifest.xml": "binary xml",
				"classes.dex":         "dex\n035",
				"res/drawable/a.png":  "png",
			},
			wantAndroid: true,
		},
		{
			name: "archive wrapping an apk",
			entries: map[string]string{
				"크아비엔비2011.apk": "PK payload",
			},
			wantWrapped: true,
			wantAndroid: true,
		},
		{
			name: "ktf wipi archive",
			entries: map[string]string{
				"__adf__":       "PID:PD004904",
				"__class__":     "Clet",
				"0103731F.jar":  "payload",
				"classes.notdx": "unrelated",
			},
		},
		{
			name:    "manifest without dalvik code",
			entries: map[string]string{"AndroidManifest.xml": "binary xml"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapped, android := AndroidPackage(buildZip(t, test.entries))
			if wrapped != test.wantWrapped || android != test.wantAndroid {
				t.Fatalf(
					"AndroidPackage = %v, %v; want %v, %v",
					wrapped,
					android,
					test.wantWrapped,
					test.wantAndroid,
				)
			}
		})
	}
}

func TestAndroidPackageIgnoresNonArchives(t *testing.T) {
	if wrapped, android := AndroidPackage([]byte("EADS not a zip")); wrapped || android {
		t.Fatalf("AndroidPackage on a non-archive = %v, %v", wrapped, android)
	}
}
