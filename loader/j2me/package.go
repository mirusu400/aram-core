// Package j2me validates standalone MIDlet JARs and ZIP distributions containing
// one local JAD/JAR pair. It never accesses the filesystem or downloads JAD URLs.
package j2me

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/mirusu400/aram-core/loader/internal/zipname"
	engine "github.com/mirusu400/aram-core/skvm"
)

const (
	ProfileID         = "j2me-1.0/generic/generic"
	LGTProfileID      = "j2me-1.0/lgt/generic"
	MaxArchiveEntries = 10000
	MaxArchiveSize    = 128 << 20
	MaxMemberSize     = 64 << 20
	MaxExpandedSize   = 128 << 20
	MaxDescriptorSize = 64 << 10
	MaxDescriptorLine = 4096
)

var ErrNotPackage = errors.New("not a J2ME package")

var ErrUnsupportedFeature = errors.New("unsupported J2ME feature")

// UnsupportedFeatureError distinguishes valid but unsupported package features
// from corrupt archive or class data.
type UnsupportedFeatureError struct {
	Path    string
	Offset  int64
	Feature string
}

func (e *UnsupportedFeatureError) Error() string {
	return fmt.Sprintf("J2ME package %q at offset 0x%x: %s is unsupported", e.Path, e.Offset, e.Feature)
}
func (e *UnsupportedFeatureError) Unwrap() error { return ErrUnsupportedFeature }

type FormatError struct {
	Path   string
	Offset int64
	Reason string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("J2ME package %q at offset 0x%x: %s", e.Path, e.Offset, e.Reason)
}
func malformed(name string, offset int64, reason string) error {
	return &FormatError{name, offset, reason}
}

type Descriptor struct {
	Name, Version, Vendor, MainClass, JARURL, Configuration string
	JARSize                                                 uint64
	Profiles                                                []string
	Raw                                                     map[string]string
}
type Package struct {
	Descriptor       Descriptor
	JARName, JADName string
	Classes          map[string][]byte
	Resources        map[string][]byte
}

// Inspect requires MIDlet metadata, not just a .jar suffix or a ZIP signature.
// The aggregate expansion budget covers both the outer ZIP and the inner JAR.
func Inspect(data []byte) (Package, error) {
	if len(data) < 4 || !bytes.Equal(data[:4], []byte{'P', 'K', 3, 4}) {
		return Package{}, ErrNotPackage
	}
	if len(data) > MaxArchiveSize {
		return Package{}, malformed("archive", 0, "archive exceeds byte limit")
	}
	budget := uint64(MaxExpandedSize)
	files, err := readZIP(data, "archive", &budget)
	if err != nil {
		return Package{}, err
	}
	var jars, jads []string
	for name := range files {
		switch strings.ToLower(path.Ext(name)) {
		case ".jad":
			jads = append(jads, name)
		case ".jar":
			jars = append(jars, name)
		}
	}
	sort.Strings(jars)
	sort.Strings(jads)
	manifestName := find(files, "META-INF/MANIFEST.MF")
	// A class-bearing archive is a standalone JAR. Nested JAR resources do not
	// turn it into a distribution. A MIDlet declaration is still required.
	standalone := manifestName != ""
	var manifest map[string]string
	if standalone {
		manifest, err = parseProperties(manifestName, files[manifestName], true)
		if err != nil {
			return Package{}, err
		}
		standalone = manifest["MIDlet-1"] != ""
	}
	pkg := Package{JARName: "archive"}
	var externalRMS string
	var requireAliasIdentity bool
	var jad map[string]string
	if !standalone {
		if len(jads) == 0 {
			return Package{}, ErrNotPackage
		}
		if len(jads) != 1 || len(jars) != 1 {
			return Package{}, malformed("archive", 0, "expected exactly one JAD/JAR pair")
		}
		pkg.JADName, pkg.JARName = jads[0], jars[0]
		jad, err = parseProperties(pkg.JADName, files[pkg.JADName], false)
		if err != nil {
			return Package{}, err
		}
		// A remote URL is metadata only. Archival distributions can rename a
		// co-located same-stem pair without updating its original download URL.
		// Permit that only with a declared JAR size (validated below) and an
		// unambiguous local pair. Identity conflicts are checked after merging.
		// Differently named archived pairs additionally require complete matching
		// manifest/JAD identity below. Relative URLs still resolve exactly.
		localPair := strings.TrimSuffix(pkg.JADName, path.Ext(pkg.JADName)) == strings.TrimSuffix(pkg.JARName, path.Ext(pkg.JARName))
		if raw := jad["MIDlet-Jar-URL"]; raw != "" {
			u, e := url.Parse(raw)
			if e != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" || strings.Contains(u.Path, "\\") {
				return Package{}, malformed(pkg.JADName, 0, "invalid MIDlet-Jar-URL")
			}
			if u.IsAbs() {
				staleRemote := localPair && jad["MIDlet-Jar-Size"] != ""
				if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return Package{}, malformed(pkg.JADName, 0, "MIDlet-Jar-URL does not identify packaged JAR")
				}
				requireAliasIdentity = path.Base(u.Path) != path.Base(pkg.JARName) && !staleRemote
			} else {
				clean, ok := zipname.SafeName(u.Path)
				if !ok || u.Host != "" || path.Join(path.Dir(pkg.JADName), clean) != pkg.JARName {
					return Package{}, malformed(pkg.JADName, 0, "MIDlet-Jar-URL does not identify local JAR")
				}
			}
		} else if strings.TrimSuffix(pkg.JADName, path.Ext(pkg.JADName)) != strings.TrimSuffix(pkg.JARName, path.Ext(pkg.JARName)) {
			return Package{}, malformed(pkg.JADName, 0, "JAD/JAR basenames differ without URL")
		}
		jarBytes := files[pkg.JARName]
		if raw, ok := jad["MIDlet-Jar-Size"]; ok {
			size, e := strconv.ParseUint(raw, 10, 64)
			if e != nil || size != uint64(len(jarBytes)) {
				return Package{}, malformed(pkg.JADName, 0, "MIDlet-Jar-Size does not match packaged JAR")
			}
		}
		for name := range files {
			if strings.EqualFold(path.Ext(name), ".db") || strings.EqualFold(path.Ext(name), ".idx") {
				if externalRMS == "" || name < externalRMS {
					externalRMS = name
				}
			}
		}
		files, err = readZIP(jarBytes, pkg.JARName, &budget)
		if err != nil {
			return Package{}, err
		}
		manifestName = find(files, "META-INF/MANIFEST.MF")
		manifest = map[string]string{}
		if manifestName != "" {
			manifest, err = parseProperties(manifestName, files[manifestName], true)
			if err != nil {
				return Package{}, err
			}
		}
		if requireAliasIdentity {
			if jad["MIDlet-Jar-Size"] == "" {
				return Package{}, malformed(pkg.JADName, 0, "archived URL alias requires a matching declared JAR size")
			}
			for _, key := range []string{"MIDlet-Name", "MIDlet-Version", "MIDlet-Vendor"} {
				if jad[key] == "" || manifest[key] == "" || jad[key] != manifest[key] {
					return Package{}, malformed(pkg.JADName, 0, "archived URL alias requires matching nonempty JAD/manifest identity: "+key)
				}
			}
			jadMain := strings.Split(jad["MIDlet-1"], ",")
			manifestMain := strings.Split(manifest["MIDlet-1"], ",")
			if len(jadMain) != 3 || len(manifestMain) != 3 || strings.TrimSpace(jadMain[2]) == "" || strings.TrimSpace(jadMain[2]) != strings.TrimSpace(manifestMain[2]) {
				return Package{}, malformed(pkg.JADName, 0, "archived URL alias requires matching nonempty MIDlet main class")
			}
		}
	}
	properties := make(map[string]string, len(manifest)+len(jad))
	for k, v := range manifest {
		properties[k] = v
	}
	for k, v := range jad {
		if (k == "MIDlet-Name" || k == "MIDlet-Version" || k == "MIDlet-Vendor") && properties[k] != "" && properties[k] != v {
			return Package{}, malformed(pkg.JADName, 0, "JAD/manifest identity mismatch: "+k)
		}
		properties[k] = v
	}
	descriptor, err := descriptor(properties)
	if err != nil {
		return Package{}, err
	}
	pkg.Descriptor = descriptor
	pkg.Classes = map[string][]byte{}
	pkg.Resources = map[string][]byte{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		payload := files[name]
		if strings.HasSuffix(name, ".class") {
			parsed, e := engine.ParseClass(name, payload)
			if e != nil {
				var ce *engine.FormatError
				if errors.As(e, &ce) {
					return Package{}, malformed(name, int64(ce.Offset), ce.Reason)
				}
				return Package{}, malformed(name, 0, e.Error())
			}
			className := strings.TrimSuffix(name, ".class")
			if parsed.Name != className {
				return Package{}, malformed(name, 0, "class path does not match declared name")
			}
			pkg.Classes[className] = payload
		} else {
			pkg.Resources[name] = payload
		}
	}
	main := strings.ReplaceAll(descriptor.MainClass, ".", "/")
	if _, ok := pkg.Classes[main]; !ok {
		return Package{}, malformed(pkg.JARName, 0, "main class is missing: "+descriptor.MainClass)
	}
	if externalRMS != "" {
		return Package{}, &UnsupportedFeatureError{Path: externalRMS, Offset: 0, Feature: "external RMS database import"}
	}
	return pkg, nil
}

func descriptor(properties map[string]string) (Descriptor, error) {
	d := Descriptor{Raw: properties, Name: properties["MIDlet-Name"], Version: properties["MIDlet-Version"], Vendor: properties["MIDlet-Vendor"], JARURL: properties["MIDlet-Jar-URL"], Configuration: properties["MicroEdition-Configuration"], Profiles: strings.Fields(properties["MicroEdition-Profile"])}
	fields := strings.Split(properties["MIDlet-1"], ",")
	if len(fields) != 3 || strings.TrimSpace(fields[2]) == "" {
		return Descriptor{}, malformed("descriptor", 0, "MIDlet-1 must contain name, icon, main class")
	}
	d.MainClass = strings.TrimSpace(fields[2])
	for _, part := range strings.Split(strings.ReplaceAll(d.MainClass, "/", "."), ".") {
		if part == "" || strings.ContainsAny(part, ";[ \\:\t\r\n") {
			return Descriptor{}, malformed("descriptor", 0, "invalid MIDlet main class name")
		}
	}
	if raw, ok := properties["MIDlet-Jar-Size"]; ok {
		size, e := strconv.ParseUint(raw, 10, 64)
		if e != nil {
			return Descriptor{}, malformed("descriptor", 0, "invalid MIDlet-Jar-Size")
		}
		d.JARSize = size
	}
	return d, nil
}

// parseProperties supports manifest continuation and stops at the main-section
// boundary. JAD properties are case-sensitive, as required by MIDP.
func parseProperties(name string, data []byte, manifest bool) (map[string]string, error) {
	if len(data) > MaxDescriptorSize {
		return nil, malformed(name, 0, "descriptor exceeds byte limit")
	}
	result := map[string]string{}
	previous := ""
	offset := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		at := offset
		offset += len(line) + 1
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) > MaxDescriptorLine || bytes.IndexByte(line, 0) >= 0 {
			return nil, malformed(name, int64(at), "invalid or oversized descriptor line")
		}
		if len(line) == 0 {
			if manifest {
				break
			}
			previous = ""
			continue
		}
		if line[0] == ' ' && manifest {
			if previous == "" {
				return nil, malformed(name, int64(at), "orphan continuation line")
			}
			result[previous] += string(line[1:])
			if len(result[previous]) > MaxDescriptorLine {
				return nil, malformed(name, int64(at), "continued value exceeds limit")
			}
			continue
		}
		key, value, ok := bytes.Cut(line, []byte{':'})
		if !ok || len(key) == 0 || len(key) > 128 || strings.TrimSpace(string(key)) != string(key) {
			return nil, malformed(name, int64(at), "invalid property line")
		}
		k := string(key)
		if _, duplicate := result[k]; duplicate {
			return nil, malformed(name, int64(at), "duplicate property: "+k)
		}
		result[k] = strings.TrimSpace(string(value))
		previous = k
	}
	return result, nil
}
func find(files map[string][]byte, name string) string {
	for n := range files {
		if strings.EqualFold(n, name) {
			return n
		}
	}
	return ""
}
func readZIP(data []byte, label string, budget *uint64) (map[string][]byte, error) {
	if len(data) > MaxArchiveSize {
		return nil, malformed(label, 0, "archive exceeds byte limit")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, malformed(label, 0, "invalid ZIP: "+err.Error())
	}
	if len(zr.File) > MaxArchiveEntries {
		return nil, malformed(label, 0, "archive entry limit exceeded")
	}
	files := map[string][]byte{}
	seen := map[string]bool{}
	for _, f := range zr.File {
		off, _ := f.DataOffset()
		raw := strings.TrimSuffix(f.Name, "/")
		name, ok := zipname.SafeName(raw)
		if !ok {
			return nil, malformed(label, off, "unsafe member name")
		}
		folded := strings.ToLower(name)
		if seen[folded] {
			return nil, malformed(label, off, "duplicate member name: "+name)
		}
		seen[folded] = true
		if f.Mode()&fs.ModeSymlink != 0 || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) {
			return nil, malformed(label, off, "nonregular archive member")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Flags&1 != 0 {
			return nil, malformed(label, off, "encrypted member is unsupported")
		}
		if f.UncompressedSize64 > MaxMemberSize || f.UncompressedSize64 > *budget {
			return nil, malformed(label, off, "expanded member byte limit exceeded")
		}
		r, e := f.Open()
		if e != nil {
			return nil, malformed(label, off, "open member: "+e.Error())
		}
		payload, e := io.ReadAll(io.LimitReader(r, int64(f.UncompressedSize64)+1))
		closeErr := r.Close()
		if e != nil || closeErr != nil || uint64(len(payload)) != f.UncompressedSize64 {
			return nil, malformed(label, off, "invalid member payload or checksum")
		}
		*budget -= uint64(len(payload))
		files[name] = payload
	}
	return files, nil
}
