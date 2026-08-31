package systemmachine

import (
	"testing"

	"github.com/mirusu400/aram-core/system"
)

func TestMachinePanelProtocolReportRequiresExplicitProbe(t *testing.T) {
	machine := &Machine{}
	if report, ok := machine.PanelProtocolReport(); ok || report.Schema != "" || report.Status != "" {
		t.Fatalf("disabled panel protocol report = %+v, %t", report, ok)
	}
	machine.panelProbe = system.NewLCDTransferProbe()
	report, ok := machine.PanelProtocolReport()
	if !ok || report.Schema != system.LCDTransferReportSchema || report.Status != "insufficient-evidence" {
		t.Fatalf("enabled panel protocol report = %+v, %t", report, ok)
	}
}
