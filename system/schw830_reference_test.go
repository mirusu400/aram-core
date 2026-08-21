package system

import (
	"context"
	"encoding/binary"
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

func TestSCHW830PrivateReferenceTracesProgressiveCode(t *testing.T) {
	probeText := os.Getenv("ARAM_TRACE_PROGRESSIVE_CODE")
	if probeText == "" {
		t.Skip("ARAM_TRACE_PROGRESSIVE_CODE is not configured")
	}
	root := os.Getenv("ARAM_REFERENCE_REPO")
	if root == "" {
		t.Skip("ARAM_REFERENCE_REPO is not configured")
	}
	set := openSCHW830ReferenceSet(t, filepath.Join(root, "SCH-W380_DL21"))
	pkg, err := samsung.Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	progressive, err := samsung.DecodeWBIN(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range parseSCHW830TraceAddresses(t, probeText) {
		found := false
		for _, header := range progressive.ELF.ProgramHeaders {
			if header.Type != 1 || address < header.VirtualAddress ||
				uint64(address-header.VirtualAddress) >= uint64(header.FileSize) {
				continue
			}
			offset := uint64(header.Offset) + uint64(address-header.VirtualAddress)
			count := min(uint64(0x100), uint64(header.FileSize)-uint64(address-header.VirtualAddress))
			if offset+count > uint64(len(progressive.Bytes)) {
				t.Fatalf("progressive trace range at 0x%08x exceeds decoded bytes", address)
			}
			t.Logf(
				"progressive code at 0x%08x file+0x%x: %x",
				address, offset, progressive.Bytes[offset:offset+count],
			)
			found = true
			break
		}
		if !found {
			t.Logf("progressive code at 0x%08x is not in a file-backed PT_LOAD range", address)
		}
	}
}

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
	if historyText := os.Getenv("ARAM_PC_HISTORY"); historyText != "" {
		historyLimit, historyErr := strconv.ParseUint(historyText, 0, 32)
		if historyErr != nil || historyLimit == 0 {
			t.Fatalf("invalid ARAM_PC_HISTORY %q", historyText)
		}
		if err := backend.SetPCHistoryLimit(uint32(historyLimit)); err != nil {
			t.Fatal(err)
		}
	}
	interruptController := NewQualcommInterruptController(backend)
	nandReady := NewStatusSignal()
	var timeTickClock *QualcommTimeTickClockConfig
	if clockText := os.Getenv("ARAM_CLOCKED_TIMETICK"); clockText != "" {
		parts := strings.Split(clockText, ",")
		if len(parts) != 3 {
			t.Fatalf("invalid ARAM_CLOCKED_TIMETICK %q", clockText)
		}
		instructionRate, instructionErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 64)
		timeTickHz, timeTickErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 64)
		interruptSource, sourceErr := strconv.ParseUint(strings.TrimSpace(parts[2]), 0, 8)
		if instructionErr != nil || timeTickErr != nil || sourceErr != nil || interruptSource >= 64 {
			t.Fatalf("invalid ARAM_CLOCKED_TIMETICK %q", clockText)
		}
		timeTickClock = &QualcommTimeTickClockConfig{
			InstructionsPerSecond: instructionRate,
			TimeTickHz:            timeTickHz,
			InterruptSource:       uint8(interruptSource),
		}
	}
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
		WritableOffsets:     board.BootControlWritableOffsets,
		HalfwordOffsets:     board.BootControlHalfwordOffsets,
		ReadOnlyRegisters:   board.BootControlReadOnlyRegisters,
		SBIControllers:      board.BootControlSBIControllers,
		SBICompletionStatus: board.BootControlSBICompletionStatus,
		NANDReady:           nandReady,
		InterruptController: interruptController,
		TimeTickClock:       timeTickClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondaryClock := NewQualcommSecondaryClockControl()
	primaryClock, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		Status:          board.PrimaryClockStatus,
		WritableOffsets: board.PrimaryClockWritableOffsets,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyTop := NewQualcommLegacyTopPage(QualcommLegacyTopConfig{
		Version:        board.LegacyTopVersion,
		Identification: board.LegacyTopIdentification,
	})
	clockRegime, err := NewQualcommClockRegimeWithSleepControllers(
		board.ClockRegimeSleepControllers,
	)
	if err != nil {
		t.Fatal(err)
	}
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
	var traceContextAccesses []MemoryAccess
	if traceText := os.Getenv("ARAM_TRACE_CONTEXT_ACCESS"); traceText != "" {
		parts := strings.Split(traceText, ",")
		if len(parts) != 2 {
			t.Fatalf("invalid ARAM_TRACE_CONTEXT_ACCESS %q", traceText)
		}
		address, addressErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
		size, sizeErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 32)
		if addressErr != nil || sizeErr != nil || size == 0 || address+size > 1<<32 {
			t.Fatalf("invalid ARAM_TRACE_CONTEXT_ACCESS %q", traceText)
		}
		if err := bus.SetInstructionMemoryObserver(uint32(address), uint32(size), func(access MemoryAccess) {
			if access.Permission == cpu.PermissionExecute {
				return
			}
			const maximumTraceContextAccesses = 256
			if len(traceContextAccesses) == maximumTraceContextAccesses {
				copy(traceContextAccesses, traceContextAccesses[1:])
				traceContextAccesses = traceContextAccesses[:maximumTraceContextAccesses-1]
			}
			traceContextAccesses = append(traceContextAccesses, access)
		}); err != nil {
			t.Fatal(err)
		}
	}
	traceAllMMIO := os.Getenv("ARAM_TRACE_ALL_MMIO") != ""
	mmioTrace := newSCHW830MMIOTrace(
		os.Getenv("ARAM_TRACE_SCHW830_MMIO") != "" || traceAllMMIO,
		traceAllMMIO,
	)
	if mmioTrace != nil {
		bus.SetMMIOObserver(mmioTrace.Observe)
	}
	var (
		traceWrite            *MemoryAccess
		traceWriteHistory     []schw830TraceWriteCapture
		traceExecution        *MemoryAccess
		traceExecutionHistory []schw830TraceWriteCapture
	)
	if os.Getenv("ARAM_TRACE_WRITE_ADDRESS") != "" && os.Getenv("ARAM_TRACE_EXEC_ADDRESS") != "" {
		t.Fatal("ARAM_TRACE_WRITE_ADDRESS and ARAM_TRACE_EXEC_ADDRESS are mutually exclusive")
	}
	if addressText := os.Getenv("ARAM_TRACE_WRITE_ADDRESS"); addressText != "" {
		addressValue, parseErr := strconv.ParseUint(addressText, 0, 32)
		if parseErr != nil || addressValue > uint64(^uint32(0)-3) {
			t.Fatalf("invalid ARAM_TRACE_WRITE_ADDRESS %q", addressText)
		}
		var (
			matchValue     uint32
			matchSet       bool
			matchingWrites uint64
			stopCount      uint64 = 1
		)
		if valueText := os.Getenv("ARAM_TRACE_WRITE_VALUE"); valueText != "" {
			value, valueErr := strconv.ParseUint(valueText, 0, 32)
			if valueErr != nil {
				t.Fatalf("invalid ARAM_TRACE_WRITE_VALUE %q", valueText)
			}
			matchValue, matchSet = uint32(value), true
		}
		if countText := os.Getenv("ARAM_TRACE_WRITE_STOP_COUNT"); countText != "" {
			count, countErr := strconv.ParseUint(countText, 0, 64)
			if countErr != nil || count == 0 {
				t.Fatalf("invalid ARAM_TRACE_WRITE_STOP_COUNT %q", countText)
			}
			stopCount = count
		}
		if err := bus.SetMemoryObserver(uint32(addressValue), 4, func(access MemoryAccess) {
			if traceWrite != nil || !access.Context.Attributed || !access.Write ||
				matchSet && access.Value != matchValue {
				return
			}
			matchingWrites++
			stack := make([]byte, 0x100)
			stackErr := readSCHW830BusMemory(bus, access.Context.StackAddress, stack)
			traceWriteHistory = append(traceWriteHistory, schw830TraceWriteCapture{
				Access: access, Stack: stack, StackErr: stackErr,
			})
			if matchingWrites != stopCount {
				return
			}
			captured := access
			traceWrite = &captured
			_ = backend.Stop()
		}); err != nil {
			t.Fatal(err)
		}
	}
	if addressText := os.Getenv("ARAM_TRACE_EXEC_ADDRESS"); addressText != "" {
		addressValue, parseErr := strconv.ParseUint(addressText, 0, 32)
		if parseErr != nil || addressValue > uint64(^uint32(0)-1) {
			t.Fatalf("invalid ARAM_TRACE_EXEC_ADDRESS %q", addressText)
		}
		stopCount := uint64(1)
		if countText := os.Getenv("ARAM_TRACE_EXEC_STOP_COUNT"); countText != "" {
			count, countErr := strconv.ParseUint(countText, 0, 64)
			if countErr != nil || count == 0 {
				t.Fatalf("invalid ARAM_TRACE_EXEC_STOP_COUNT %q", countText)
			}
			stopCount = count
		}
		var matchingExecutions uint64
		if err := bus.SetMemoryObserver(uint32(addressValue), 2, func(access MemoryAccess) {
			if traceExecution != nil || !access.Context.Attributed || access.Write ||
				access.Permission != cpu.PermissionExecute {
				return
			}
			matchingExecutions++
			stack := make([]byte, 0x100)
			stackErr := readSCHW830BusMemory(bus, access.Context.StackAddress, stack)
			traceExecutionHistory = append(traceExecutionHistory, schw830TraceWriteCapture{
				Access: access, Stack: stack, StackErr: stackErr,
			})
			if matchingExecutions != stopCount {
				return
			}
			captured := access
			traceExecution = &captured
			_ = backend.Stop()
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := board.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	if err := board.ApplyReadOnlyRegisters(bus); err != nil {
		t.Fatal(err)
	}
	if err := board.ApplyLatchedRegisters(bus); err != nil {
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
	traceStop := errors.New("requested firmware trace stop")
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
	if traceStopText := os.Getenv("ARAM_TRACE_STOP_PC"); traceStopText != "" {
		address, parseErr := strconv.ParseUint(traceStopText, 0, 32)
		if parseErr != nil || address&1 != 0 {
			t.Fatalf("invalid ARAM_TRACE_STOP_PC %q", traceStopText)
		}
		calls = append(calls, HLECallProfile{
			ID: "diagnostic-trace-stop", Contract: "diagnostic.trace-stop",
			Address: uint32(address), Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		})
	}
	runner, err := NewHLERunner(bus, backend, calls, map[string]HLECallHandler{
		"diagnostic.oem-fatal": HLECallHandlerFunc(func(HLECallContext) error {
			return fatalDiagnostic
		}),
		"diagnostic.flash-init-failure": HLECallHandlerFunc(func(HLECallContext) error {
			return flashInitFailure
		}),
		"diagnostic.trace-stop": HLECallHandlerFunc(func(HLECallContext) error {
			return traceStop
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var executionRunner ExecutionRunner = runner
	if timeTickClock != nil {
		executionRunner, err = NewClockedRunner(
			backend, runner, DefaultClockedRunnerQuantum, bootControl,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	result := executionRunner.Run(context.Background(), handoff.Entry, handoff.Mode, 1_195_629)
	if traceWrite != nil {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
		return
	}
	if traceExecution != nil {
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830TraceExecution(t, backend, traceExecutionHistory, result.PC)
		return
	}
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 1_195_629 || result.PC != 0x000a07d8 {
		t.Fatalf("unexpected QCSBL OEM callback boundary: %+v", result)
	}

	postWarmupBudget := uint64(10_000_000)
	if budgetText := os.Getenv("ARAM_POST_WARMUP_BUDGET"); budgetText != "" {
		parsed, parseErr := strconv.ParseUint(budgetText, 0, 64)
		if parseErr != nil || parsed == 0 {
			t.Fatalf("invalid ARAM_POST_WARMUP_BUDGET %q", budgetText)
		}
		postWarmupBudget = parsed
	}
	result = executionRunner.Run(context.Background(), result.PC, cpu.ModeARM, 552_000_000)
	if traceWrite != nil {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
		return
	}
	if traceExecution != nil {
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830TraceExecution(t, backend, traceExecutionHistory, result.PC)
		return
	}
	if errors.Is(result.Err, traceStop) {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830PCHistory(t, backend)
		logSCHW830RequestedMemory(t, backend, "trace-stop requested")
		logSCHW830TraceState(t, backend, result.PC)
		return
	}
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
		result = executionRunner.Run(context.Background(), result.PC, mode, postWarmupBudget)
		result.Instructions += warmupInstructions
	}
	if traceWrite != nil {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
		return
	}
	if traceExecution != nil {
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830TraceExecution(t, backend, traceExecutionHistory, result.PC)
		return
	}
	if errors.Is(result.Err, traceStop) {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830PCHistory(t, backend)
		logSCHW830RequestedMemory(t, backend, "trace-stop requested")
		logSCHW830TraceState(t, backend, result.PC)
		return
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
	wantInstructions := 552_000_000 + postWarmupBudget
	if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != wantInstructions {
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
	if probeText := os.Getenv("ARAM_TRACE_CODE"); probeText != "" {
		for _, address := range parseSCHW830TraceAddresses(t, probeText) {
			if address > ^uint32(0)-0x80 {
				t.Fatalf("ARAM_TRACE_CODE address 0x%x exceeds readable range", address)
			}
			code := make([]byte, 0x80)
			if err := backend.ReadMemory(address, code); err != nil {
				t.Logf("trace code at 0x%08x: %v", address, err)
				continue
			}
			t.Logf("trace code at 0x%08x: %x", address, code)
		}
	}
}

func readSCHW830BusMemory(bus *Bus, address uint32, destination []byte) error {
	for offset := 0; offset < len(destination); {
		width := 4
		if remaining := len(destination) - offset; remaining < width {
			width = 1
			if remaining >= 2 {
				width = 2
			}
		}
		if err := bus.Read(address+uint32(offset), destination[offset:offset+width], cpu.PermissionRead); err != nil {
			return err
		}
		offset += width
	}
	return nil
}

type schw830TraceWriteCapture struct {
	Access   MemoryAccess
	Stack    []byte
	StackErr error
}

func logSCHW830TraceWrite(
	t *testing.T,
	backend cpu.Backend,
	history []schw830TraceWriteCapture,
	pc uint32,
) {
	t.Helper()
	for index, capture := range history {
		access := capture.Access
		t.Logf("trace write match %d: %+v", index+1, access)
		if capture.StackErr != nil {
			t.Logf("trace-write stack at 0x%08x: %v", access.Context.StackAddress, capture.StackErr)
			continue
		}
		t.Logf("trace-write stack at 0x%08x: %x", access.Context.StackAddress, capture.Stack)
		// The currently investigated initializer saves {r3-r7, lr}; keeping
		// this as a diagnostic candidate also makes the exact unwind explicit.
		outerLink := binary.LittleEndian.Uint32(capture.Stack[20:24])
		t.Logf("trace-write saved link candidate [sp+0x14]=0x%08x", outerLink)
		logSCHW830Code(t, backend, "trace-write caller", outerLink, 0x40, 0x80)
		if index == 0 {
			for offset := 0x18; offset+4 <= len(capture.Stack); offset += 4 {
				candidate := binary.LittleEndian.Uint32(capture.Stack[offset : offset+4])
				if candidate&1 == 0 || candidate < 0x0001_0000 || candidate >= 0x0400_0000 {
					continue
				}
				t.Logf("trace-write stack link candidate [sp+0x%x]=0x%08x", offset, candidate)
				logSCHW830Code(t, backend, "trace-write stack caller", candidate, 0x40, 0x80)
			}
		}
	}
	logSCHW830RequestedMemory(t, backend, "trace-write requested")
	logSCHW830PCHitRange(t, backend)
	logSCHW830PCHistory(t, backend)
	logSCHW830TraceState(t, backend, pc)
}

func logSCHW830TraceExecution(
	t *testing.T,
	backend *interpreter.Backend,
	history []schw830TraceWriteCapture,
	pc uint32,
) {
	t.Helper()
	for index, capture := range history {
		t.Logf("trace execution match %d: %+v", index+1, capture.Access)
		if capture.StackErr != nil {
			t.Logf(
				"trace-execution stack at 0x%08x: %v",
				capture.Access.Context.StackAddress,
				capture.StackErr,
			)
			continue
		}
		t.Logf(
			"trace-execution stack at 0x%08x: %x",
			capture.Access.Context.StackAddress,
			capture.Stack,
		)
	}
	logSCHW830PCHistory(t, backend)
	logSCHW830RequestedMemory(t, backend, "trace-execution requested")
	logSCHW830TraceState(t, backend, pc)
}

func logSCHW830RequestedMemory(t *testing.T, backend cpu.Backend, label string) {
	t.Helper()
	if probeText := os.Getenv("ARAM_TRACE_CODE"); probeText != "" {
		for _, address := range parseSCHW830TraceAddresses(t, probeText) {
			logSCHW830Code(t, backend, label, address, 0, 0x100)
		}
	}
}

func logSCHW830PCHistory(t *testing.T, backend cpu.Backend) {
	t.Helper()
	tracer, ok := backend.(interface{ PCHistory() []uint32 })
	if !ok {
		return
	}
	history := tracer.PCHistory()
	focusText := os.Getenv("ARAM_PC_HISTORY_FOCUS")
	if focusText == "" {
		logSCHW830PCSlice(t, "trace", history)
		return
	}
	for _, focus := range parseSCHW830TraceAddresses(t, focusText) {
		matches := 0
		for index, address := range history {
			if address != focus {
				continue
			}
			matches++
			start := max(0, index-128)
			end := min(len(history), index+129)
			logSCHW830PCSlice(t, fmt.Sprintf("trace focus 0x%08x match %d", focus, matches), history[start:end])
		}
		t.Logf("trace PC history focus 0x%08x matches: %d", focus, matches)
	}
}

func logSCHW830PCSlice(t *testing.T, label string, pcHistory []uint32) {
	t.Helper()
	for start := 0; start < len(pcHistory); start += 64 {
		end := min(start+64, len(pcHistory))
		t.Logf("%s PC history %04d..%04d: %#x", label, start, end, pcHistory[start:end])
	}
}

func logSCHW830ContextAccesses(t *testing.T, accesses []MemoryAccess) {
	t.Helper()
	for index, access := range accesses {
		t.Logf("trace context access %d: %+v", index+1, access)
	}
}

func logSCHW830MMIOTrace(t *testing.T, trace *schw830MMIOTrace) {
	t.Helper()
	if trace != nil {
		t.Logf("SCH-W830 MMIO trace at diagnostic stop:\n%s", trace.String())
	}
}

func logSCHW830PCHitRange(t *testing.T, backend cpu.Backend) {
	t.Helper()
	rangeText := os.Getenv("ARAM_TRACE_PC_RANGE")
	if rangeText == "" {
		return
	}
	parts := strings.Split(rangeText, ",")
	if len(parts) != 2 {
		t.Fatalf("invalid ARAM_TRACE_PC_RANGE %q", rangeText)
	}
	startValue, startErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
	sizeValue, sizeErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 32)
	if startErr != nil || sizeErr != nil || sizeValue == 0 || startValue+sizeValue > 1<<32 {
		t.Fatalf("invalid ARAM_TRACE_PC_RANGE %q", rangeText)
	}
	tracer, ok := backend.(interface{ PCHits() map[uint32]uint64 })
	if !ok {
		t.Logf("trace PC range unavailable for backend %q", backend.Identity().Name)
		return
	}
	hits := tracer.PCHits()
	if hits == nil {
		t.Log("trace PC range requested but ARAM_PC_TRACE is disabled")
		return
	}
	start := uint32(startValue)
	end := uint64(start) + sizeValue
	addresses := make([]uint32, 0)
	for address, count := range hits {
		if count != 0 && address >= start && uint64(address) < end {
			addresses = append(addresses, address)
		}
	}
	sort.Slice(addresses, func(left, right int) bool { return addresses[left] < addresses[right] })
	for _, address := range addresses {
		t.Logf("trace PC hit 0x%08x: %d", address, hits[address])
	}
}

func logSCHW830Code(
	t *testing.T,
	backend cpu.Backend,
	label string,
	address uint32,
	before uint32,
	size uint32,
) {
	t.Helper()
	base := (address &^ uint32(1)) &^ uint32(0x1f)
	if base < before {
		t.Logf("%s code around 0x%08x underflows physical address space", label, address)
		return
	}
	base -= before
	code := make([]byte, size)
	if err := backend.ReadMemory(base, code); err != nil {
		t.Logf("%s code at 0x%08x: %v", label, base, err)
	} else {
		t.Logf("%s code at 0x%08x: %x", label, base, code)
	}
}

func logSCHW830TraceState(t *testing.T, backend cpu.Backend, pc uint32) {
	t.Helper()
	registers := make([]uint32, cpu.RegisterCPSR+1)
	for index := range registers {
		registers[index], _ = backend.ReadRegister(uint32(index))
	}
	t.Logf("trace stop pc=0x%08x registers=%#v", pc, registers)
	codeAddress := pc &^ uint32(0x3f)
	code := make([]byte, 0x100)
	if err := backend.ReadMemory(codeAddress, code); err != nil {
		t.Logf("trace-stop code at 0x%08x: %v", codeAddress, err)
	} else {
		t.Logf("trace-stop code at 0x%08x: %x", codeAddress, code)
	}
	for _, register := range []uint32{
		cpu.RegisterR0, cpu.RegisterR1, cpu.RegisterR2, cpu.RegisterR3,
		cpu.RegisterR4, cpu.RegisterR5, cpu.RegisterR6, cpu.RegisterR7,
	} {
		address := registers[register] &^ uint32(3)
		data := make([]byte, 0x40)
		if err := backend.ReadMemory(address, data); err == nil {
			t.Logf("trace-stop r%d memory at 0x%08x: %x", register, address, data)
		}
	}
	current := registers[cpu.RegisterR1]
	seen := make(map[uint32]int)
	for index := 0; current != 0 && index < 4096; index++ {
		if previous, duplicate := seen[current]; duplicate {
			t.Logf(
				"trace-stop r1+0x14 list repeats 0x%08x at node %d (first node %d)",
				current, index, previous,
			)
			break
		}
		seen[current] = index
		var node [0x1c]byte
		if err := backend.ReadMemory(current, node[:]); err != nil {
			t.Logf("trace-stop list node %d at 0x%08x: %v", index, current, err)
			break
		}
		if index < 8 {
			t.Logf("trace-stop list node %d at 0x%08x: %x", index, current, node[:])
		}
		current = binary.LittleEndian.Uint32(node[0x14:0x18])
		if current == 0 {
			t.Logf("trace-stop r1+0x14 list terminates after %d nodes", index+1)
		}
	}
}

func parseSCHW830TraceAddresses(t *testing.T, text string) []uint32 {
	t.Helper()
	parts := strings.Split(text, ",")
	addresses := make([]uint32, 0, len(parts))
	for _, addressText := range parts {
		addressValue, err := strconv.ParseUint(strings.TrimSpace(addressText), 0, 32)
		if err != nil {
			t.Fatalf("invalid trace-code address %q", addressText)
		}
		addresses = append(addresses, uint32(addressValue))
	}
	return addresses
}

const schw830MMIOTraceWindow = 64

type schw830MMIOTrace struct {
	total  uint64
	first  []MMIOAccess
	last   []MMIOAccess
	counts map[schw830MMIOTraceKey]uint64
	all    bool
}

type schw830MMIOTraceKey struct {
	context cpu.MemoryAccessContext
	address uint32
	width   Width
	value   uint32
	write   bool
}

func newSCHW830MMIOTrace(enabled, all bool) *schw830MMIOTrace {
	if !enabled {
		return nil
	}
	trace := &schw830MMIOTrace{all: all}
	if !all {
		trace.counts = make(map[schw830MMIOTraceKey]uint64)
	}
	return trace
}

func (trace *schw830MMIOTrace) Observe(access MMIOAccess) {
	interrupt := access.Address >= 0x80000900 && access.Address < 0x80000960
	timeBlock := access.Address >= 0x80005400 && access.Address < 0x80005500
	sleepController := access.Address >= 0x90005200 && access.Address < 0x90005300
	if !trace.all && !interrupt && !timeBlock && !sleepController {
		return
	}
	trace.total++
	if !trace.all {
		trace.counts[schw830MMIOTraceKey{
			context: access.Context, address: access.Address, width: access.Width,
			value: access.Value, write: access.Write,
		}]++
		if len(trace.first) < schw830MMIOTraceWindow {
			trace.first = append(trace.first, access)
		}
	}
	if len(trace.last) < schw830MMIOTraceWindow {
		trace.last = append(trace.last, access)
		return
	}
	trace.last[(trace.total-1)%schw830MMIOTraceWindow] = access
}

func (trace *schw830MMIOTrace) String() string {
	var output strings.Builder
	if trace.all {
		fmt.Fprintf(&output, "total=%d recent=%d", trace.total, len(trace.last))
		trace.formatLast(&output)
		return output.String()
	}
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
	trace.formatLast(&output)
	return output.String()
}

func (trace *schw830MMIOTrace) formatLast(output *strings.Builder) {
	if len(trace.last) == 0 {
		return
	}
	start := 0
	if trace.total > uint64(len(trace.last)) {
		start = int(trace.total % uint64(len(trace.last)))
	}
	for index := range trace.last {
		access := trace.last[(start+index)%len(trace.last)]
		fmt.Fprintf(output, "\nlast  %s", formatSCHW830MMIOAccess(access))
	}
}

func TestSCHW830MMIOTraceAllKeepsOnlyRecentAccesses(t *testing.T) {
	trace := newSCHW830MMIOTrace(true, true)
	for index := uint32(0); index < schw830MMIOTraceWindow+6; index++ {
		trace.Observe(MMIOAccess{Address: 0x80000000 + index})
	}

	if trace.counts != nil || len(trace.first) != 0 {
		t.Fatalf("all-MMIO trace retained summary state: counts=%v first=%d", trace.counts, len(trace.first))
	}
	if len(trace.last) != schw830MMIOTraceWindow {
		t.Fatalf("recent MMIO count = %d, want %d", len(trace.last), schw830MMIOTraceWindow)
	}
	output := trace.String()
	if strings.Contains(output, "distinct=") || strings.Contains(output, "first ") {
		t.Fatalf("all-MMIO trace unexpectedly summarized accesses:\n%s", output)
	}
	first := strings.Index(output, "address=0x80000006")
	last := strings.Index(output, "address=0x80000045")
	if first < 0 || last < first {
		t.Fatalf("recent MMIO trace is not chronological:\n%s", output)
	}
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
