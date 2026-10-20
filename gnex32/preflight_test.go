package gnex32

import (
	"errors"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestPreflightAcceptsCoveredCapabilities(t *testing.T) {
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code:    []byte{0x92, 0, 0x5f, 0, 0xff, 0},
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}},
		Media:   []gnex.GNEX32Media{{Flags: 0, Data: []byte("text\x00")}},
	}
	report, err := Preflight(image)
	if err != nil {
		t.Fatal(err)
	}
	if report.SymbolCount != 1 || report.MediaCount != 1 || report.Analysis.InstructionCount != 2 ||
		len(report.Analysis.ServiceCalls) != 1 || report.Analysis.ServiceCalls[0].ID != 0x5f {
		t.Fatalf("preflight report = %+v", report)
	}
}

func TestPreflightReportsOpcodeAndServiceSites(t *testing.T) {
	code := make([]byte, 18)
	putAnalysisWord(code, 0, 0x85)
	putAnalysisAddress(code, 2, 16)
	putAnalysisWord(code, 6, 0x92)
	putAnalysisWord(code, 8, 0xbeef)
	putAnalysisWord(code, 10, 0xff)
	putAnalysisWord(code, 16, 0xdead)
	_, err := Preflight(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: code,
	})
	var capability *CapabilityError
	if !errors.As(err, &capability) || len(capability.UnsupportedServices) != 1 ||
		len(capability.UnknownOpcodes) != 1 || !strings.Contains(err.Error(), "code offset 16") ||
		!strings.Contains(err.Error(), "code offset 6") {
		t.Fatalf("capability error = %#v, %v", capability, err)
	}
}

func TestPreflightRejectsUnsupportedStorageBeforeCode(t *testing.T) {
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: []byte{0xff, 0},
		Media: []gnex.GNEX32Media{{Flags: 0xdead}},
	}
	if _, err := Preflight(image); err == nil || !strings.Contains(err.Error(), "media capability preflight") {
		t.Fatalf("unsupported media preflight = %v", err)
	}
}
