package gnex32

import (
	"fmt"

	"github.com/mirusu400/aram-core/loader/gnex"
)

// CapabilityReport records the code and storage surface validated before a
// GNEX32 image is allowed to execute.
type CapabilityReport struct {
	Analysis    StaticServiceAnalysis
	SymbolCount int
	MediaCount  int
}

// CapabilityError reports reachable code that the current VM or service layer
// cannot execute. The full sites remain available for structured diagnostics.
type CapabilityError struct {
	UnsupportedServices []StaticServiceCall
	UnknownOpcodes      []StaticOpcodeSite
}

func (err *CapabilityError) Error() string {
	switch {
	case len(err.UnknownOpcodes) != 0 && len(err.UnsupportedServices) != 0:
		return fmt.Sprintf(
			"%d reachable unknown opcode site(s), first %#x at code offset %d; %d unsupported service ID(s), first %#x at code offset %d",
			len(err.UnknownOpcodes), err.UnknownOpcodes[0].Opcode, err.UnknownOpcodes[0].Offset,
			len(err.UnsupportedServices), err.UnsupportedServices[0].ID, err.UnsupportedServices[0].Offsets[0],
		)
	case len(err.UnknownOpcodes) != 0:
		return fmt.Sprintf("%d reachable unknown opcode site(s), first %#x at code offset %d",
			len(err.UnknownOpcodes), err.UnknownOpcodes[0].Opcode, err.UnknownOpcodes[0].Offset)
	case len(err.UnsupportedServices) != 0:
		return fmt.Sprintf("%d unsupported service ID(s), first %#x at code offset %d",
			len(err.UnsupportedServices), err.UnsupportedServices[0].ID, err.UnsupportedServices[0].Offsets[0])
	default:
		return "GNEX32 capability preflight failed"
	}
}

// Preflight validates the symbol/media storage contract and statically
// reachable opcode/service surface. It performs no guest execution.
func Preflight(image gnex.GNEX32Image) (CapabilityReport, error) {
	result := CapabilityReport{SymbolCount: len(image.Symbols), MediaCount: len(image.Media)}
	if _, err := image.NewSymbolMemory(); err != nil {
		return result, fmt.Errorf("gnex32: symbol capability preflight: %w", err)
	}
	if _, err := image.NewMediaMemory(); err != nil {
		return result, fmt.Errorf("gnex32: media capability preflight: %w", err)
	}
	analysis, err := AnalyzeServices(image)
	if err != nil {
		return result, fmt.Errorf("gnex32: opcode capability preflight: %w", err)
	}
	result.Analysis = analysis
	if len(analysis.UnsupportedServices) != 0 || len(analysis.UnknownOpcodes) != 0 {
		return result, &CapabilityError{
			UnsupportedServices: append([]StaticServiceCall(nil), analysis.UnsupportedServices...),
			UnknownOpcodes:      append([]StaticOpcodeSite(nil), analysis.UnknownOpcodes...),
		}
	}
	return result, nil
}
