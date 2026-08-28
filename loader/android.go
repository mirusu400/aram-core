package loader

import (
	"archive/zip"
	"bytes"
	"path"
	"strings"
)

// androidPackageScanLimit bounds how many archive entries an Android probe
// reads. The marker files sit at the root of an APK, and a wrapper archive
// holds one payload, so a real match is always near the front; the bound keeps
// a hostile archive with a million entries from turning a rejection into a
// long scan.
const androidPackageScanLimit = 4096

// AndroidPackage reports whether a source is an Android application, and
// whether it only wraps one — a .zip holding a single .apk, which is how the
// Android half of a mixed game archive is usually distributed.
//
// This is a rejection aid, not a loader. Android packages share the ZIP
// container with KTF and Raptor WIPI titles, so an APK reaches the WIPI
// loaders, fails every one of them, and used to be reported as
// "no valid ABHS or EADS records" — a true statement that tells the person who
// opened it nothing about what went wrong. The entry name is deliberately not
// reported: archive names here are frequently CP949, and echoing those bytes
// into an error message prints mojibake.
func AndroidPackage(data []byte) (wrapped, android bool) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false, false
	}
	manifest, dex, carrier := false, false, false
	for index, file := range archive.File {
		if index >= androidPackageScanLimit {
			break
		}
		switch path.Base(strings.ReplaceAll(file.Name, "\\", "/")) {
		case "AndroidManifest.xml":
			manifest = true
		case "classes.dex":
			dex = true
		}
		if strings.EqualFold(path.Ext(file.Name), ".apk") {
			carrier = true
		}
	}
	if manifest && dex {
		return false, true
	}
	return carrier, carrier
}
