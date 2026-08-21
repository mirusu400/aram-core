package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/firmwareset"
	"github.com/mirusu400/aram-core/loader/samsung"
)

func TestSCHW830PrivateReferenceRunsOriginalFirmwarePastTimeTickSetup(t *testing.T) {
	root := os.Getenv("ARAM_REFERENCE_REPO")
	if root == "" {
		t.Skip("ARAM_REFERENCE_REPO is not configured")
	}
	set := openSCHW830ReferenceSet(t, filepath.Join(root, "SCH-W380_DL21"))
	pkg, err := samsung.Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := samsung.BuiltinRegistry().Match(pkg)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := profile.BootImage("qcsbl")
	if !ok {
		t.Fatal("SCH-W830 profile has no QCSBL image")
	}
	image, err := samsung.ReconstructBootImage(set, pkg, spec)
	if err != nil {
		t.Fatal(err)
	}
	flashImage, err := samsung.AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	flash, err := NewCOWFlash(flashImage, samsung.EraseBlockSize, flashImage.Identity())
	if err != nil {
		t.Fatal(err)
	}
	board := SCHW830DL21BoardProfile()
	backend := interpreter.New()
	interruptController := NewQualcommInterruptController(backend)
	nandReady := NewStatusSignal()
	nandConfig := Qualcomm2K8BitNANDConfig(board.NANDReadID, nandReady)
	if nandConfig.PageSize != samsung.PageSize {
		t.Fatal("SCH-W830 NAND profile page size does not match normalized flash")
	}
	nand, err := NewQualcommNAND(flash, nandConfig)
	if err != nil {
		t.Fatal(err)
	}
	bootControl, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5880, ClockModeStatus: board.BootClockModeStatus,
		NANDReady: nandReady, InterruptController: interruptController,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondaryClock := NewQualcommSecondaryClockControl()
	primaryClock, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		Status: board.PrimaryClockStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyTop := NewQualcommLegacyTopPage(QualcommLegacyTopConfig{
		Version:        board.LegacyTopVersion,
		Identification: board.LegacyTopIdentification,
	})
	clockRegime := NewQualcommClockRegime()
	busRegisters, err := NewSparseWordRegisters(schw830BusRegisterOffsets())
	if err != nil {
		t.Fatal(err)
	}
	panel := NewParallelPanelInterface()
	handoff, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: image.EntryAddress, TableAddress: 0x78001000,
		PageSize: samsung.PageSize, EraseBlockSize: samsung.EraseBlockSize,
		FlashSize: uint64(flash.Size()), BadBlockLimit: 0x14,
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff.Memory = append(handoff.Memory, MemorySeed{
		Address: image.LoadAddress,
		Bytes:   append([]byte(nil), image.Bytes...),
	})

	bus := NewBus()
	mmioTrace := newSCHW830MMIOTrace(os.Getenv("ARAM_TRACE_SCHW830_MMIO") != "")
	if mmioTrace != nil {
		bus.SetMMIOObserver(mmioTrace.Observe)
	}
	if err := board.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	if err := board.ApplyReadOnlyRegisters(bus); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("qualcomm-boot-control", 0x80000000, QualcommBootControlWindowSize, bootControl); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("qualcomm-nand", 0x60000000, QualcommNANDWindowSize, nand); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"qualcomm-primary-clock",
		0x84000000,
		QualcommPrimaryClockWindowSize,
		primaryClock,
	); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"qualcomm-secondary-clock",
		0x84004000,
		QualcommSecondaryClockWindowSize,
		secondaryClock,
	); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"parallel-panel",
		0x20000000,
		ParallelPanelWindowSize,
		panel,
	); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"qualcomm-clock-regime",
		0x90000000,
		QualcommClockRegimeWindowSize,
		clockRegime,
	); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"qualcomm-sparse-bus-registers",
		0x90400000,
		0x1000,
		busRegisters,
	); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"qualcomm-legacy-top-page",
		0xfffff000,
		QualcommLegacyTopWindowSize,
		legacyTop,
	); err != nil {
		t.Fatal(err)
	}
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := handoff.Apply(bus, backend); err != nil {
		t.Fatal(err)
	}
	fatalDiagnostic := errors.New("unexpected OEM fatal diagnostic")
	flashInitFailure := errors.New("OEM flash initialization failed")
	calls := []HLECallProfile{
		{
			ID: "diagnostic-oem-fatal", Contract: "diagnostic.oem-fatal",
			Address: 0x00107ffc, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID: "diagnostic-flash-init-failure", Contract: "diagnostic.flash-init-failure",
			Address: 0x000a6ae0, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	}
	runner, err := NewHLERunner(bus, backend, calls, map[string]HLECallHandler{
		"diagnostic.oem-fatal": HLECallHandlerFunc(func(HLECallContext) error {
			return fatalDiagnostic
		}),
		"diagnostic.flash-init-failure": HLECallHandlerFunc(func(HLECallContext) error {
			return flashInitFailure
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), handoff.Entry, handoff.Mode, 1_195_629)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 1_195_629 || result.PC != 0x000a07d8 {
		t.Fatalf("unexpected QCSBL OEM callback boundary: %+v", result)
	}

	result = runner.Run(context.Background(), result.PC, cpu.ModeARM, 552_000_000)
	pcBaseline := backend.PCHits()
	if result.Err == nil && result.Reason == cpu.StopBudget {
		irqEnable0, irq0Err := interruptController.Read(qualcommIRQEnable0Offset, Width32)
		irqEnable1, irq1Err := interruptController.Read(qualcommIRQEnable1Offset, Width32)
		fiqEnable0, fiq0Err := interruptController.Read(qualcommFIQEnable0Offset, Width32)
		fiqEnable1, fiq1Err := interruptController.Read(qualcommFIQEnable1Offset, Width32)
		if irq0Err != nil || irq1Err != nil || fiq0Err != nil || fiq1Err != nil {
			t.Fatalf("read interrupt enables: %v %v %v %v", irq0Err, irq1Err, fiq0Err, fiq1Err)
		}
		t.Logf(
			"interrupt enables after warmup: IRQ=%08x/%08x FIQ=%08x/%08x",
			irqEnable0, irqEnable1, fiqEnable0, fiqEnable1,
		)
		if sourceText := os.Getenv("ARAM_INJECT_IRQ_SOURCE"); sourceText != "" {
			source, parseErr := strconv.ParseUint(sourceText, 0, 8)
			if parseErr != nil || source >= 64 {
				t.Fatalf("invalid ARAM_INJECT_IRQ_SOURCE %q", sourceText)
			}
			if err := interruptController.PulseSource(uint8(source)); err != nil {
				t.Fatal(err)
			}
			t.Logf("injected one Qualcomm interrupt source %d pulse", source)
		}
		status, statusErr := backend.ReadRegister(cpu.RegisterCPSR)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		mode := cpu.ModeARM
		if status&cpu.StatusThumb != 0 {
			mode = cpu.ModeThumb
		}
		warmupInstructions := result.Instructions
		result = runner.Run(context.Background(), result.PC, mode, 10_000_000)
		result.Instructions += warmupInstructions
	}
	t.Logf("original firmware execution result: %+v", result)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		code := make([]byte, 0x40)
		codeErr := backend.ReadMemory(result.PC-0x20, code)
		registers := make([]uint32, 16)
		for index := range registers {
			registers[index], _ = backend.ReadRegister(uint32(index))
		}
		t.Logf("probe code around PC: %x error=%v registers=%#v", code, codeErr, registers)
	}
	if hits := backend.PCHits(); hits != nil {
		type pcHit struct {
			address uint32
			count   uint64
		}
		ranked := make([]pcHit, 0, len(hits))
		for address, count := range hits {
			if baseline := pcBaseline[address]; count >= baseline {
				count -= baseline
			}
			if count == 0 {
				continue
			}
			ranked = append(ranked, pcHit{address: address, count: count})
		}
		sort.Slice(ranked, func(left, right int) bool { return ranked[left].count > ranked[right].count })
		if len(ranked) > 12 {
			ranked = ranked[:12]
		}
		t.Logf("post-warmup hottest PCs: %#v", ranked)
	}
	if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 562_000_000 {
		t.Fatalf("original firmware did not reach the post-timetick execution budget: %+v", result)
	}
	if invocations := runner.Invocations(); len(invocations) != 0 {
		t.Fatalf("diagnostic HLE was invoked: %+v", invocations)
	}
	commands, data := panel.WriteCounts()
	if commands == 0 || data == 0 {
		t.Fatalf(
			"panel terminal state = %d/%d command %#x data %#x",
			commands, data, panel.CurrentCommand(), panel.LastData(),
		)
	}
	finalStatus, finalStatusErr := backend.ReadRegister(cpu.RegisterCPSR)
	if finalStatusErr != nil {
		t.Fatal(finalStatusErr)
	}
	finalLink, finalLinkErr := backend.ReadRegister(cpu.RegisterLR)
	if finalLinkErr != nil {
		t.Fatal(finalLinkErr)
	}
	t.Logf(
		"post-panel boundary: instructions=%d pc=0x%08x cpsr=0x%08x lr=0x%08x err=%v panel=%d/%d command=0x%x data=0x%x watchdog=%d irq-status=%08x/%08x",
		result.Instructions,
		result.PC,
		finalStatus,
		finalLink,
		result.Err,
		commands,
		data,
		panel.CurrentCommand(),
		panel.LastData(),
		bootControl.WatchdogServices(),
		interruptController.status[0],
		interruptController.status[1],
	)
	if mmioTrace != nil {
		t.Logf("SCH-W830 focused MMIO trace:\n%s", mmioTrace.String())
		for _, probe := range []struct {
			address uint32
			size    int
		}{
			{address: 0x01701db0, size: 0x50},
			{address: 0x017d2920, size: 0x60},
		} {
			code := make([]byte, probe.size)
			if err := backend.ReadMemory(probe.address, code); err != nil {
				t.Logf("trace code at 0x%08x: %v", probe.address, err)
				continue
			}
			t.Logf("trace code at 0x%08x: %x", probe.address, code)
		}
	}
}

const schw830MMIOTraceWindow = 64

type schw830MMIOTrace struct {
	total  uint64
	first  []MMIOAccess
	last   []MMIOAccess
	counts map[schw830MMIOTraceKey]uint64
}

type schw830MMIOTraceKey struct {
	context cpu.MemoryAccessContext
	address uint32
	width   Width
	value   uint32
	write   bool
}

func newSCHW830MMIOTrace(enabled bool) *schw830MMIOTrace {
	if !enabled {
		return nil
	}
	return &schw830MMIOTrace{counts: make(map[schw830MMIOTraceKey]uint64)}
}

func (trace *schw830MMIOTrace) Observe(access MMIOAccess) {
	interrupt := access.Address >= 0x80000900 && access.Address < 0x80000960
	timeBlock := access.Address >= 0x80005400 && access.Address < 0x80005500
	if !interrupt && !timeBlock {
		return
	}
	trace.total++
	trace.counts[schw830MMIOTraceKey{
		context: access.Context, address: access.Address, width: access.Width,
		value: access.Value, write: access.Write,
	}]++
	if len(trace.first) < schw830MMIOTraceWindow {
		trace.first = append(trace.first, access)
	}
	if len(trace.last) < schw830MMIOTraceWindow {
		trace.last = append(trace.last, access)
		return
	}
	trace.last[(trace.total-1)%schw830MMIOTraceWindow] = access
}

func (trace *schw830MMIOTrace) String() string {
	var output strings.Builder
	fmt.Fprintf(
		&output, "total=%d distinct=%d first=%d last=%d",
		trace.total, len(trace.counts), len(trace.first), len(trace.last),
	)
	keys := make([]schw830MMIOTraceKey, 0, len(trace.counts))
	for key := range trace.counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].address != keys[right].address {
			return keys[left].address < keys[right].address
		}
		if keys[left].write != keys[right].write {
			return !keys[left].write
		}
		if keys[left].context.InstructionAddress != keys[right].context.InstructionAddress {
			return keys[left].context.InstructionAddress < keys[right].context.InstructionAddress
		}
		return keys[left].value < keys[right].value
	})
	for _, key := range keys {
		direction := "R"
		if key.write {
			direction = "W"
		}
		fmt.Fprintf(
			&output,
			"\ncount %-5d %s pc=0x%08x/%d address=0x%08x width=%d value=0x%08x",
			trace.counts[key], direction, key.context.InstructionAddress, key.context.Mode,
			key.address, key.width, key.value,
		)
	}
	for _, access := range trace.first {
		fmt.Fprintf(&output, "\nfirst %s", formatSCHW830MMIOAccess(access))
	}
	if trace.total <= schw830MMIOTraceWindow {
		return output.String()
	}
	start := int(trace.total % schw830MMIOTraceWindow)
	for index := range trace.last {
		access := trace.last[(start+index)%len(trace.last)]
		fmt.Fprintf(&output, "\nlast  %s", formatSCHW830MMIOAccess(access))
	}
	return output.String()
}

func formatSCHW830MMIOAccess(access MMIOAccess) string {
	direction := "R"
	if access.Write {
		direction = "W"
	}
	return fmt.Sprintf(
		"%s pc=0x%08x/%d address=0x%08x width=%d value=0x%08x err=%v",
		direction, access.Context.InstructionAddress, access.Context.Mode,
		access.Address, access.Width, access.Value, access.Err,
	)
}

func schw830BusRegisterOffsets() []uint32 {
	offsets := make([]uint32, 0, 128)
	for _, span := range [][2]uint32{{0x240, 0x27c}, {0x280, 0x29c}, {0x2c0, 0x2dc}} {
		for offset := span[0]; offset <= span[1]; offset += 4 {
			offsets = append(offsets, offset)
		}
	}
	offsets = append(offsets,
		0x3a0, 0x3a4, 0x3a8, 0x3ac, 0x3b0, 0x3b4, 0x3b8, 0x3bc,
		0x3c0, 0x3c4, 0x3c8, 0x3cc, 0x3d0,
		0x3e0, 0x3e4, 0x3e8, 0x3ec, 0x3f0,
	)
	for column := uint32(0); column <= 0x200; column += 0x40 {
		offsets = append(offsets, column+0x10)
	}
	for column := uint32(0x400); column <= 0x600; column += 0x40 {
		for _, lane := range []uint32{0, 4, 8, 0x0c, 0x14} {
			offsets = append(offsets, column+lane)
		}
	}
	for column := uint32(0xc00); column <= 0xe00; column += 0x40 {
		offsets = append(offsets, column+0x18, column+0x1c)
	}
	return offsets
}

func openSCHW830ReferenceSet(t *testing.T, directory string) firmwareset.Set {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured reference directory: %v", err)
	}
	var sources []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() || !schReferenceExtension(filepath.Ext(entry.Name())) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	if len(sources) != 4 {
		t.Fatalf("configured reference contains %d SCH download pieces, want 4", len(sources))
	}
	set, err := firmwareset.NewSet(sources)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func schReferenceExtension(extension string) bool {
	switch extension {
	case ".wbt", ".wbin", ".dat", ".fnt":
		return true
	default:
		return false
	}
}
