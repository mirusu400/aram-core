package skvmhost

import (
	"image"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

const (
	crow2ArchiveSHA256  = "0352c951a2f9cb0ed697211f14517231a0b23803f7bda9342c7eb9b32b9e79ee"
	astoniaEP2SKTSHA256 = "08aa799c11b0d97d4f3b03fc1c2ce7fb28136009227ae6442e0d5ca105fe6ede"
)

func skvmRunBudget(source machinecore.Source) uint64 {
	if source.SHA256 == crow2ArchiveSHA256 {
		// The inventory key handler completes after about 12.1 million guest
		// instructions when it draws the shipped title's item metadata.
		return 20_000_000
	}
	return defaultSKVMRunBudget
}

func lookupSKVMTitleCanvas(
	source machinecore.Source,
	pkg skloader.Package,
	inferred image.Point,
) (quirkdb.SKVMCanvas, bool) {
	return quirkdb.LookupSKVMCanvas(
		source.SHA256,
		pkg.Descriptor.MainClass,
		pkg.Descriptor.ProgramName,
		inferred.X,
		inferred.Y,
	)
}

// skvmTitleCanvas answers with the handset canvas an exact shipped package was
// authored for, or the inferred geometry when the package is not recorded.
func skvmTitleCanvas(
	source machinecore.Source,
	pkg skloader.Package,
	inferred image.Point,
) image.Point {
	entry, ok := lookupSKVMTitleCanvas(source, pkg, inferred)
	if !ok || entry.Width <= 0 || entry.Height <= 0 {
		return inferred
	}
	return image.Pt(entry.Width, entry.Height)
}

// applySKVMTitleCompatibility adds only compatibility behavior that is tied to
// an exact shipped package and its expected metadata. It takes the geometry
// inferSKVMFramebufferSize chose rather than the canvas skvmTitleCanvas
// answered with, so an entry that replaces the canvas still matches itself.
func applySKVMTitleCompatibility(
	config *shared.Config,
	source machinecore.Source,
	pkg skloader.Package,
	inferred image.Point,
) {
	if config == nil {
		return
	}
	if source.SHA256 == astoniaEP2SKTSHA256 && pkg.Descriptor.MainClass == "AstoS2" {
		config.Device.Quirks = append(config.Device.Quirks, shared.DeviceQuirk{
			Name: skengine.AstoniaEP2MapEdgeQuirk, Enabled: true,
		})
	}
	entry, ok := lookupSKVMTitleCanvas(source, pkg, inferred)
	if !ok {
		return
	}
	// DeviceConfig requires quirk names in strictly increasing order.
	// A title may need both the canvas and clipping handset contracts.
	if entry.CanvasHeightInset16 {
		config.Device.Quirks = append(config.Device.Quirks, shared.DeviceQuirk{
			Name:    skengine.CanvasHeightInset16Quirk,
			Enabled: true,
		})
	}
	if entry.InclusiveSetClip {
		config.Device.Quirks = append(config.Device.Quirks, shared.DeviceQuirk{
			Name:    skengine.InclusiveSetClipQuirk,
			Enabled: true,
		})
	}
}

// reconcileNoticeErrorText carries this title's own offline notice into the
// array its renderer reads. Its network error branch populates notice[] and
// selects screen 29, but leaves m_strHelpText[] null before DrawNotice runs.
func (m *Machine) reconcileNoticeErrorText() {
	if !quirkdb.SKVMNoticeErrorText(m.source.SHA256) {
		return
	}
	canvas, ok := m.vm.Object(m.vm.CurrentDisplay())
	if !ok || canvas.Class != "MainCanvas" {
		return
	}
	field := func(name, descriptor string) string {
		return "MainCanvas\x00" + name + "\x00" + descriptor
	}
	current, _ := canvas.Fields[field("mainState", "I")].Int()
	next, _ := canvas.Fields[field("m_NetNextState", "I")].Int()
	if current != 29 && next != 29 {
		return
	}
	count, _ := canvas.Fields[field("Notice_No", "I")].Int()
	if count <= 0 {
		return
	}
	textField := field("m_strHelpText", "[Ljava/lang/String;")
	notice, _ := canvas.Fields[field("notice", "[Ljava/lang/String;")].Reference()
	array, ok := m.vm.Object(notice)
	if !ok || array.Array == nil || array.Array.Descriptor != "[Ljava/lang/String;" ||
		len(array.Array.Elements) < int(count) {
		return
	}
	if text, _ := canvas.Fields[textField].Reference(); text == 0 {
		canvas.Fields[textField] = skengine.ReferenceValue(notice)
	}
	dateField := field("notice_date", "Ljava/lang/String;")
	if date, _ := canvas.Fields[dateField].Reference(); date == 0 {
		canvas.Fields[dateField] = skengine.ReferenceValue(m.vm.NewString("--------"))
	}
}
