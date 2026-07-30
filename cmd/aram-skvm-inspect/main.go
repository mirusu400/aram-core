// Command aram-skvm-inspect reports the structure and Java dependencies of an
// SK Telecom SK-VM package without executing untrusted bytecode.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
	"github.com/mirusu400/aram-core/skvm"
)

type report struct {
	Path          string            `json:"path"`
	BaseName      string            `json:"base_name"`
	Descriptor    descriptorReport  `json:"descriptor"`
	JARName       string            `json:"jar_name"`
	JARHeaderSize int               `json:"jar_header_size"`
	MODName       string            `json:"mod_name"`
	MODSize       int               `json:"mod_size"`
	WMRName       string            `json:"wmr_name"`
	WMRSize       int               `json:"wmr_size"`
	Classes       []classReport     `json:"classes"`
	Resources     []resourceReport  `json:"resources"`
	References    []referenceReport `json:"references"`
}

type descriptorReport struct {
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Vendor        string   `json:"vendor"`
	MainClass     string   `json:"main_class"`
	Profiles      []string `json:"profiles"`
	Configuration string   `json:"configuration"`
	ProgramName   string   `json:"program_name"`
	MIMEType      string   `json:"mime_type"`
}

type classReport struct {
	Name         string         `json:"name"`
	MajorVersion uint16         `json:"major_version"`
	MinorVersion uint16         `json:"minor_version"`
	Super        string         `json:"super"`
	Interfaces   []string       `json:"interfaces,omitempty"`
	Fields       int            `json:"fields"`
	Methods      []methodReport `json:"methods"`
}

type methodReport struct {
	Name       string `json:"name"`
	Descriptor string `json:"descriptor"`
	CodeBytes  int    `json:"code_bytes"`
	Native     bool   `json:"native,omitempty"`
	Abstract   bool   `json:"abstract,omitempty"`
}

type resourceReport struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

type referenceReport struct {
	Kind       skvm.ReferenceKind `json:"kind"`
	Class      string             `json:"class"`
	Name       string             `json:"name"`
	Descriptor string             `json:"descriptor"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: aram-skvm-inspect <package.zip>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "aram-skvm-inspect:", err)
		os.Exit(1)
	}
}

func run(name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", name)
	}
	if uint64(info.Size()) > skloader.MaxExpandedSize {
		return fmt.Errorf("%q exceeds the package size limit", name)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	result, err := inspect(name, data)
	if err != nil {
		if errors.Is(err, skloader.ErrNotPackage) {
			return fmt.Errorf("%q is not an SKVM package", name)
		}
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func inspect(name string, data []byte) (report, error) {
	pkg, err := skloader.Inspect(data)
	if err != nil {
		return report{}, err
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		absolute = name
	}
	result := report{
		Path:     absolute,
		BaseName: pkg.BaseName,
		Descriptor: descriptorReport{
			Name:          pkg.Descriptor.Name,
			Version:       pkg.Descriptor.Version,
			Vendor:        pkg.Descriptor.Vendor,
			MainClass:     pkg.Descriptor.MainClass,
			Profiles:      pkg.Descriptor.Profiles,
			Configuration: pkg.Descriptor.Configuration,
			ProgramName:   pkg.Descriptor.ProgramName,
			MIMEType:      pkg.Descriptor.MIMEType,
		},
		JARName:       pkg.JARName,
		JARHeaderSize: len(pkg.JARHeader),
		MODName:       pkg.MODName,
		MODSize:       len(pkg.Module),
		WMRName:       pkg.WMRName,
		WMRSize:       len(pkg.WMR),
	}

	classNames := make([]string, 0, len(pkg.Classes))
	for name := range pkg.Classes {
		classNames = append(classNames, name)
	}
	sort.Strings(classNames)
	references := make(map[string]referenceReport)
	for _, name := range classNames {
		parsed, parseErr := skvm.ParseClass(name+".class", pkg.Classes[name].Data)
		if parseErr != nil {
			return report{}, parseErr
		}
		class := classReport{
			Name:         parsed.Name,
			MajorVersion: parsed.MajorVersion,
			MinorVersion: parsed.MinorVersion,
			Super:        parsed.SuperName,
			Interfaces:   parsed.Interfaces,
			Fields:       len(parsed.Fields),
		}
		for _, method := range parsed.Methods {
			class.Methods = append(class.Methods, methodReport{
				Name:       method.Name,
				Descriptor: method.Descriptor,
				CodeBytes:  len(method.Code),
				Native:     method.Native(),
				Abstract:   method.Abstract(),
			})
		}
		result.Classes = append(result.Classes, class)
		classReferences, referenceErr := parsed.References()
		if referenceErr != nil {
			return report{}, referenceErr
		}
		for _, reference := range classReferences {
			key := string(reference.Kind) + "\x00" + reference.Class + "\x00" +
				reference.Name + "\x00" + reference.Descriptor
			references[key] = referenceReport(reference)
		}
	}

	resourceNames := make([]string, 0, len(pkg.Resources))
	for name := range pkg.Resources {
		resourceNames = append(resourceNames, name)
	}
	sort.Strings(resourceNames)
	for _, name := range resourceNames {
		result.Resources = append(result.Resources, resourceReport{
			Name: name,
			Size: len(pkg.Resources[name]),
		})
	}
	for _, reference := range references {
		result.References = append(result.References, reference)
	}
	sort.Slice(result.References, func(left, right int) bool {
		a, b := result.References[left], result.References[right]
		if a.Class != b.Class {
			return a.Class < b.Class
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Descriptor != b.Descriptor {
			return a.Descriptor < b.Descriptor
		}
		return a.Kind < b.Kind
	})
	return result, nil
}
