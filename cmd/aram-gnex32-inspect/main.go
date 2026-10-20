// Command aram-gnex32-inspect statically reports reachable GNEX32 services and
// opcodes without executing guest code.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

type report struct {
	Path                  string          `json:"path"`
	Title                 string          `json:"title"`
	FormatVersion         byte            `json:"format_version"`
	CodeBytes             int             `json:"code_bytes"`
	Roots                 []int           `json:"roots"`
	ReachableInstructions int             `json:"reachable_instructions"`
	ServiceCalls          []serviceCall   `json:"service_calls"`
	UnsupportedServices   []serviceCall   `json:"unsupported_services,omitempty"`
	UnknownOpcodes        []opcodeSite    `json:"unknown_opcodes,omitempty"`
	RuntimeServices       []serviceRecord `json:"runtime_services"`
}

type serviceCall struct {
	ID        uint16 `json:"id"`
	HexID     string `json:"hex_id"`
	Name      string `json:"name,omitempty"`
	Supported bool   `json:"supported"`
	Offsets   []int  `json:"offsets"`
}

type serviceRecord struct {
	ID    uint16 `json:"id"`
	HexID string `json:"hex_id"`
	Name  string `json:"name"`
}

type opcodeSite struct {
	Opcode uint16 `json:"opcode"`
	Hex    string `json:"hex"`
	Offset int    `json:"offset"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: aram-gnex32-inspect <package.zip|title.sgs>")
		return 2
	}
	result, err := inspectFile(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "aram-gnex32-inspect:", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "aram-gnex32-inspect:", err)
		return 1
	}
	return 0
}

func inspectFile(name string) (report, error) {
	info, err := os.Stat(name)
	if err != nil {
		return report{}, err
	}
	if !info.Mode().IsRegular() {
		return report{}, fmt.Errorf("%q is not a regular file", name)
	}
	if uint64(info.Size()) > gnex.MaxExpandedSize {
		return report{}, fmt.Errorf("%q exceeds the package size limit", name)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return report{}, err
	}
	pkg, err := gnex.Inspect(data)
	if err != nil {
		if errors.Is(err, gnex.ErrNotPackage) {
			return report{}, fmt.Errorf("%q is not a GNEX package", name)
		}
		return report{}, err
	}
	if pkg.Header.FormatVersion != 4 {
		return report{}, fmt.Errorf("%q is GNEX version %d, not GNEX32 version 4", name, pkg.Header.FormatVersion)
	}
	image, err := gnex.DecodeGNEX32Image(pkg.SGS)
	if err != nil {
		return report{}, err
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		absolute = name
	}
	return inspectImage(absolute, image)
}

func inspectImage(name string, image gnex.GNEX32Image) (report, error) {
	analysis, err := gnex32.AnalyzeServices(image)
	if err != nil {
		return report{}, err
	}
	result := report{
		Path: name, Title: image.Header.Title, FormatVersion: image.Header.FormatVersion,
		CodeBytes: len(image.Code), Roots: analysis.Roots,
		ReachableInstructions: analysis.InstructionCount,
	}
	convertCall := func(call gnex32.StaticServiceCall) serviceCall {
		return serviceCall{
			ID: call.ID, HexID: fmt.Sprintf("0x%04x", call.ID), Name: call.Name,
			Supported: call.Supported, Offsets: call.Offsets,
		}
	}
	for _, call := range analysis.ServiceCalls {
		result.ServiceCalls = append(result.ServiceCalls, convertCall(call))
	}
	for _, call := range analysis.UnsupportedServices {
		result.UnsupportedServices = append(result.UnsupportedServices, convertCall(call))
	}
	for _, site := range analysis.UnknownOpcodes {
		result.UnknownOpcodes = append(result.UnknownOpcodes, opcodeSite{
			Opcode: site.Opcode, Hex: fmt.Sprintf("0x%04x", site.Opcode), Offset: site.Offset,
		})
	}
	for _, id := range gnex32.SupportedServiceIDs() {
		name, _ := gnex32.ServiceName(id)
		result.RuntimeServices = append(result.RuntimeServices, serviceRecord{
			ID: id, HexID: fmt.Sprintf("0x%04x", id), Name: name,
		})
	}
	return result, nil
}
