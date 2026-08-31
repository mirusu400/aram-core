package system

import (
	"errors"
	"sort"
	"sync"
)

// LCDTransferReportSchema identifies the stable JSON-facing report shape.
const LCDTransferReportSchema = "aram-lcd-transfer-report-v1"

const (
	lcdProbeMaximumPendingWrites = 8
	lcdProbeMaximumPorts         = 16
	lcdProbeMaximumPairs         = 32
)

// ErrLCDTransferProbe reports invalid or repeated observer attachment.
var ErrLCDTransferProbe = errors.New("invalid LCD transfer probe")

// LCDTransferEvidence contains bounded counters derived from a logical DCS
// stream. It deliberately excludes arbitrary command values and pixel data so
// reports can be shared without retaining firmware-originated payloads.
type LCDTransferEvidence struct {
	MatchedCommandWrites  uint64 `json:"matched_command_writes"`
	MatchedDataWrites     uint64 `json:"matched_data_writes"`
	RecognizedDCSCommands uint64 `json:"recognized_dcs_commands"`
	ColumnWindows         uint64 `json:"complete_column_windows"`
	PageWindows           uint64 `json:"complete_page_windows"`
	PixelFormatWrites     uint64 `json:"pixel_format_writes"`
	AddressModeWrites     uint64 `json:"address_mode_writes"`
	MemoryWriteCommands   uint64 `json:"memory_write_commands"`
	PixelDataWrites       uint64 `json:"pixel_data_writes"`
}

// LCDTransferCandidate is one observed physical command/data pair and the
// transfer grammar supported by the logical panel stream behind it. Addresses
// and widths are candidates for BoardProfile.PanelPorts and its transport.
type LCDTransferCandidate struct {
	CommandAddress      uint32              `json:"command_address"`
	DataAddress         uint32              `json:"data_address"`
	CommandWidthBits    uint8               `json:"command_width_bits"`
	DataWidthBits       uint8               `json:"data_width_bits"`
	ParameterPacking    string              `json:"parameter_packing"`
	PixelPacking        string              `json:"pixel_packing"`
	PixelFormat         string              `json:"pixel_format"`
	PixelFormatEvidence string              `json:"pixel_format_evidence"`
	ColorOrder          string              `json:"color_order"`
	Confidence          string              `json:"confidence"`
	Reasons             []string            `json:"reasons"`
	Evidence            LCDTransferEvidence `json:"evidence"`
}

// LCDTransferReport is a review-only inference report. Candidate status means
// that a coherent command/data grammar was observed; it does not register or
// mutate a BoardProfile.
type LCDTransferReport struct {
	Schema                string                 `json:"schema"`
	Status                string                 `json:"status"`
	LogicalWrites         uint64                 `json:"logical_writes"`
	MatchedPhysicalWrites uint64                 `json:"matched_physical_writes"`
	CorrelationFailures   uint64                 `json:"correlation_failures"`
	DroppedEvidence       uint64                 `json:"dropped_evidence"`
	Candidates            []LCDTransferCandidate `json:"candidates"`
	Warnings              []string               `json:"warnings,omitempty"`
}

type lcdProbePort struct {
	address uint32
	width   Width
}

type lcdProbePair struct {
	command lcdProbePort
	data    lcdProbePort
}

type lcdProbePairStats struct {
	commandWrites uint64
	dataWrites    uint64
	grammar       lcdProbeGrammar
}

type lcdProbeGrammar struct {
	currentCommand uint16
	parameterCount uint8
	parameters     [4]uint8
	windowValid    bool

	recognizedCommands uint64
	columnWindows      uint64
	pageWindows        uint64
	pixelFormatWrites  uint64
	addressModeWrites  uint64
	memoryCommands     uint64
	pixelDataWrites    uint64
	parameterWrites    uint64
	wideParameters     uint64

	rgb444Formats  uint64
	rgb565Formats  uint64
	rgb666Formats  uint64
	rgb888Formats  uint64
	unknownFormats uint64
	rgbModes       uint64
	bgrModes       uint64
}

// LCDTransferProbe correlates the panel transport's logical writes with the
// physical MMIO access which caused each write. The zero value is ready for
// use. Attach installs observers explicitly, keeping normal execution free of
// per-access callbacks.
type LCDTransferProbe struct {
	mu sync.Mutex

	attached bool
	pending  []ParallelPanelWrite

	logicalWrites         uint64
	matchedPhysicalWrites uint64
	correlationFailures   uint64
	droppedEvidence       uint64

	commandPorts map[lcdProbePort]struct{}
	pairs        map[lcdProbePair]lcdProbePairStats
	currentPort  lcdProbePort
	haveCommand  bool
	currentData  map[lcdProbePort]struct{}
}

// NewLCDTransferProbe returns an empty diagnostic probe.
func NewLCDTransferProbe() *LCDTransferProbe {
	return &LCDTransferProbe{}
}

// Attach connects the probe to one bus and one logical parallel-panel
// transport. It replaces those objects' optional diagnostic observers and may
// therefore be called only as part of an explicitly requested probe run.
func (p *LCDTransferProbe) Attach(bus *Bus, panel *ParallelPanelInterface) error {
	if p == nil || bus == nil || panel == nil {
		return ErrLCDTransferProbe
	}
	p.mu.Lock()
	if p.attached {
		p.mu.Unlock()
		return ErrLCDTransferProbe
	}
	p.attached = true
	p.mu.Unlock()

	panel.SetWriteObserver(p.ObservePanelWrite)
	bus.SetMMIOObserver(p.ObserveMMIOAccess)
	return nil
}

// ObservePanelWrite records one accepted logical command or data write. It is
// exported for diagnostic harnesses which already own observer composition;
// ordinary callers should use Attach.
func (p *LCDTransferProbe) ObservePanelWrite(write ParallelPanelWrite) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logicalWrites++
	if len(p.pending) == lcdProbeMaximumPendingWrites {
		p.droppedEvidence++
		return
	}
	p.pending = append(p.pending, write)
}

// ObserveMMIOAccess correlates the completed physical write which immediately
// follows a logical panel callback. Unrelated MMIO is ignored when no logical
// write is pending.
func (p *LCDTransferProbe) ObserveMMIOAccess(access MMIOAccess) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.pending) == 0 {
		return
	}
	write := p.pending[0]
	copy(p.pending, p.pending[1:])
	p.pending = p.pending[:len(p.pending)-1]
	if !access.Write || access.Err != nil || access.Value != uint32(write.Value) ||
		(access.Width != Width8 && access.Width != Width16 && access.Width != Width32) {
		p.correlationFailures++
		return
	}
	p.matchedPhysicalWrites++
	port := lcdProbePort{address: access.Address, width: access.Width}
	if !write.Data {
		if p.commandPorts == nil {
			p.commandPorts = make(map[lcdProbePort]struct{})
		}
		if _, found := p.commandPorts[port]; !found && len(p.commandPorts) == lcdProbeMaximumPorts {
			p.droppedEvidence++
			p.haveCommand = false
			return
		}
		p.commandPorts[port] = struct{}{}
		p.currentPort = port
		p.haveCommand = true
		clear(p.currentData)
		return
	}
	if !p.haveCommand {
		p.correlationFailures++
		return
	}
	if p.currentData == nil {
		p.currentData = make(map[lcdProbePort]struct{})
	}
	pair := lcdProbePair{command: p.currentPort, data: port}
	if p.pairs == nil {
		p.pairs = make(map[lcdProbePair]lcdProbePairStats)
	}
	stats, found := p.pairs[pair]
	if !found && len(p.pairs) == lcdProbeMaximumPairs {
		p.droppedEvidence++
		return
	}
	if _, seen := p.currentData[port]; !seen {
		stats.commandWrites++
		stats.grammar.observe(ParallelPanelWrite{Command: write.Command, Value: write.Command})
		if len(p.currentData) < lcdProbeMaximumPorts {
			p.currentData[port] = struct{}{}
		} else {
			p.droppedEvidence++
		}
	}
	stats.dataWrites++
	stats.grammar.observe(write)
	p.pairs[pair] = stats
}

// Report returns a detached, privacy-safe summary of all writes observed since
// construction. It is safe to call while a probe-enabled machine is running.
func (p *LCDTransferProbe) Report() LCDTransferReport {
	report := LCDTransferReport{
		Schema:     LCDTransferReportSchema,
		Status:     "insufficient-evidence",
		Candidates: make([]LCDTransferCandidate, 0),
	}
	if p == nil {
		report.Warnings = []string{"nil-probe"}
		return report
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	report.LogicalWrites = p.logicalWrites
	report.MatchedPhysicalWrites = p.matchedPhysicalWrites
	report.CorrelationFailures = p.correlationFailures
	report.DroppedEvidence = p.droppedEvidence
	for pair, stats := range p.pairs {
		candidate := p.candidate(pair, stats)
		report.Candidates = append(report.Candidates, candidate)
	}
	sort.Slice(report.Candidates, func(left, right int) bool {
		leftCandidate := report.Candidates[left]
		rightCandidate := report.Candidates[right]
		if rank := confidenceRank(leftCandidate.Confidence) - confidenceRank(rightCandidate.Confidence); rank != 0 {
			return rank > 0
		}
		if leftCandidate.Evidence.MatchedDataWrites != rightCandidate.Evidence.MatchedDataWrites {
			return leftCandidate.Evidence.MatchedDataWrites > rightCandidate.Evidence.MatchedDataWrites
		}
		if leftCandidate.CommandAddress != rightCandidate.CommandAddress {
			return leftCandidate.CommandAddress < rightCandidate.CommandAddress
		}
		if leftCandidate.DataAddress != rightCandidate.DataAddress {
			return leftCandidate.DataAddress < rightCandidate.DataAddress
		}
		if leftCandidate.CommandWidthBits != rightCandidate.CommandWidthBits {
			return leftCandidate.CommandWidthBits < rightCandidate.CommandWidthBits
		}
		return leftCandidate.DataWidthBits < rightCandidate.DataWidthBits
	})
	if len(report.Candidates) > 8 {
		report.Candidates = report.Candidates[:8]
		report.DroppedEvidence++
	}
	if len(report.Candidates) != 0 {
		best := report.Candidates[0]
		if best.Confidence != "low" {
			report.Status = "candidate"
		}
		if len(report.Candidates) > 1 {
			second := report.Candidates[1]
			if second.Confidence == best.Confidence &&
				second.Evidence.MatchedDataWrites == best.Evidence.MatchedDataWrites {
				report.Status = "ambiguous"
			}
		}
	}
	if len(p.pending) != 0 {
		report.Warnings = append(report.Warnings, "logical-physical-correlation-pending")
	}
	if report.CorrelationFailures != 0 {
		report.Warnings = append(report.Warnings, "logical-physical-correlation-failures")
	}
	if report.DroppedEvidence != 0 {
		report.Warnings = append(report.Warnings, "bounded-evidence-limit-reached")
	}
	if len(report.Candidates) == 0 {
		report.Warnings = append(report.Warnings, "no-command-data-pair")
	} else if report.Candidates[0].Confidence == "low" {
		report.Warnings = append(report.Warnings, "protocol-grammar-not-proven")
	}
	return report
}

func (p *LCDTransferProbe) candidate(pair lcdProbePair, stats lcdProbePairStats) LCDTransferCandidate {
	grammar := stats.grammar
	format, formatEvidence := grammar.pixelFormat(pair.data.width)
	parameterPacking := grammar.parameterPacking(pair.data.width)
	pixelPacking := grammar.pixelPacking(pair.data.width, format)
	confidence := grammar.confidence(pair, stats, format, formatEvidence, p.correlationFailures, p.droppedEvidence)
	reasons := make([]string, 0, 5)
	if pair.command.address != pair.data.address {
		reasons = append(reasons, "observed-distinct-command-data-addresses")
	}
	if pair.command.width == pair.data.width {
		reasons = append(reasons, "consistent-command-data-width")
	}
	if grammar.columnWindows != 0 && grammar.pageWindows != 0 {
		reasons = append(reasons, "complete-dcs-address-window")
	}
	if formatEvidence == "dcs-pixel-format-command" {
		reasons = append(reasons, "explicit-dcs-pixel-format")
	}
	if grammar.memoryCommands != 0 && grammar.pixelDataWrites != 0 {
		reasons = append(reasons, "dcs-memory-write-payload")
	}
	return LCDTransferCandidate{
		CommandAddress:      pair.command.address,
		DataAddress:         pair.data.address,
		CommandWidthBits:    uint8(pair.command.width) * 8,
		DataWidthBits:       uint8(pair.data.width) * 8,
		ParameterPacking:    parameterPacking,
		PixelPacking:        pixelPacking,
		PixelFormat:         format,
		PixelFormatEvidence: formatEvidence,
		ColorOrder:          grammar.colorOrder(),
		Confidence:          confidence,
		Reasons:             reasons,
		Evidence: LCDTransferEvidence{
			MatchedCommandWrites:  stats.commandWrites,
			MatchedDataWrites:     stats.dataWrites,
			RecognizedDCSCommands: grammar.recognizedCommands,
			ColumnWindows:         grammar.columnWindows,
			PageWindows:           grammar.pageWindows,
			PixelFormatWrites:     grammar.pixelFormatWrites,
			AddressModeWrites:     grammar.addressModeWrites,
			MemoryWriteCommands:   grammar.memoryCommands,
			PixelDataWrites:       grammar.pixelDataWrites,
		},
	}
}

func (g *lcdProbeGrammar) observe(write ParallelPanelWrite) {
	if !write.Data {
		g.currentCommand = write.Command
		g.parameterCount = 0
		g.parameters = [4]uint8{}
		g.windowValid = true
		switch write.Command {
		case dcsExitSleepMode, dcsEnterSleepMode, dcsSetDisplayOff, dcsSetDisplayOn,
			dcsSetColumnAddress, dcsSetPageAddress, dcsWriteMemoryStart,
			dcsSetAddressMode, dcsSetPixelFormat, dcsWriteMemoryContinue:
			g.recognizedCommands++
		}
		if write.Command == dcsWriteMemoryStart || write.Command == dcsWriteMemoryContinue {
			g.memoryCommands++
		}
		return
	}

	switch g.currentCommand {
	case dcsSetColumnAddress, dcsSetPageAddress:
		g.parameterWrites++
		g.parameterCount++
		if write.Value > 0xff || g.parameterCount > 4 {
			g.wideParameters++
			g.windowValid = false
		} else {
			g.parameters[g.parameterCount-1] = uint8(write.Value)
		}
		if g.parameterCount == 4 && g.windowValid {
			start := uint16(g.parameters[0])<<8 | uint16(g.parameters[1])
			end := uint16(g.parameters[2])<<8 | uint16(g.parameters[3])
			if start <= end {
				if g.currentCommand == dcsSetColumnAddress {
					g.columnWindows++
				} else {
					g.pageWindows++
				}
			}
		}
	case dcsSetPixelFormat:
		g.parameterWrites++
		g.pixelFormatWrites++
		if write.Value > 0xff {
			g.wideParameters++
			g.unknownFormats++
			return
		}
		switch uint8(write.Value) & 0x07 {
		case 3:
			g.rgb444Formats++
		case 5:
			g.rgb565Formats++
		case 6:
			g.rgb666Formats++
		case 7:
			g.rgb888Formats++
		default:
			g.unknownFormats++
		}
	case dcsSetAddressMode:
		g.parameterWrites++
		g.addressModeWrites++
		if write.Value > 0xff {
			g.wideParameters++
			return
		}
		if uint8(write.Value)&dcsAddressModeBGR != 0 {
			g.bgrModes++
		} else {
			g.rgbModes++
		}
	case dcsWriteMemoryStart, dcsWriteMemoryContinue:
		g.pixelDataWrites++
	}
}

func (g lcdProbeGrammar) pixelFormat(width Width) (string, string) {
	formats := []struct {
		name  string
		count uint64
	}{
		{"rgb444", g.rgb444Formats},
		{"rgb565", g.rgb565Formats},
		{"rgb666", g.rgb666Formats},
		{"rgb888", g.rgb888Formats},
		{"unknown", g.unknownFormats},
	}
	sort.Slice(formats, func(left, right int) bool { return formats[left].count > formats[right].count })
	if formats[0].count != 0 && (formats[1].count == 0 || formats[0].count > formats[1].count) {
		if formats[0].name == "unknown" {
			return "unknown", "unrecognized-dcs-pixel-format"
		}
		return formats[0].name, "dcs-pixel-format-command"
	}
	if g.pixelFormatWrites != 0 {
		return "unknown", "ambiguous-dcs-pixel-format"
	}
	if g.pixelFormatWrites == 0 && width == Width16 && g.pixelDataWrites != 0 {
		return "rgb565", "16-bit-memory-write-inference"
	}
	return "unknown", "insufficient-evidence"
}

func (g lcdProbeGrammar) parameterPacking(width Width) string {
	if g.parameterWrites == 0 {
		return "not-observed"
	}
	switch width {
	case Width8:
		return "one-byte-per-write"
	case Width16:
		if g.wideParameters == 0 {
			return "low-byte-per-halfword"
		}
		return "mixed-or-unknown-halfword-lanes"
	case Width32:
		return "unresolved-word-lanes"
	default:
		return "unknown"
	}
}

func (g lcdProbeGrammar) pixelPacking(width Width, format string) string {
	if g.pixelDataWrites == 0 {
		return "not-observed"
	}
	switch width {
	case Width8:
		if format == "rgb565" {
			return "rgb565-byte-order-unresolved"
		}
		return "one-byte-per-write"
	case Width16:
		if format == "rgb565" {
			return "one-rgb565-pixel-per-halfword"
		}
		return "one-16-bit-value-per-write"
	case Width32:
		if format == "rgb565" {
			return "rgb565-halfword-order-unresolved"
		}
		return "one-32-bit-value-per-write"
	default:
		return "unknown"
	}
}

func (g lcdProbeGrammar) colorOrder() string {
	switch {
	case g.rgbModes != 0 && g.bgrModes != 0:
		return "mixed"
	case g.bgrModes != 0:
		return "bgr"
	case g.rgbModes != 0:
		return "rgb"
	default:
		return "unresolved"
	}
}

func (g lcdProbeGrammar) confidence(
	pair lcdProbePair,
	stats lcdProbePairStats,
	format string,
	formatEvidence string,
	correlationFailures uint64,
	droppedEvidence uint64,
) string {
	if pair.command.address == pair.data.address || stats.commandWrites == 0 || stats.dataWrites == 0 {
		return "low"
	}
	completeWindow := g.columnWindows != 0 && g.pageWindows != 0
	memoryPayload := g.memoryCommands != 0 && g.pixelDataWrites != 0
	if pair.command.width == Width16 && pair.data.width == Width16 &&
		completeWindow && memoryPayload && g.pixelDataWrites >= 4 && format == "rgb565" &&
		formatEvidence == "dcs-pixel-format-command" && g.rgb565Formats == g.pixelFormatWrites &&
		g.wideParameters == 0 &&
		correlationFailures == 0 && droppedEvidence == 0 {
		return "high"
	}
	if memoryPayload && (completeWindow || formatEvidence == "dcs-pixel-format-command") {
		return "medium"
	}
	return "low"
}

func confidenceRank(confidence string) int {
	switch confidence {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}
