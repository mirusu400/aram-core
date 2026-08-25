package system

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
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
	referenceDirectory := os.Getenv("ARAM_SCHW830_REFERENCE_DIR")
	if referenceDirectory == "" {
		root := os.Getenv("ARAM_REFERENCE_REPO")
		if root == "" {
			t.Skip("ARAM_REFERENCE_REPO or ARAM_SCHW830_REFERENCE_DIR is not configured")
		}
		referenceDirectory = filepath.Join(root, "SCH-W380_DL21")
	}
	set := openSCHW830ReferenceSet(t, referenceDirectory)
	pkg, err := samsung.Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	progressive, err := samsung.DecodeWBIN(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if dumpPath := os.Getenv("ARAM_DUMP_PROGRESSIVE_ELF"); dumpPath != "" {
		if writeErr := os.WriteFile(dumpPath, progressive.Bytes, 0o600); writeErr != nil {
			t.Fatalf("write decoded progressive ELF: %v", writeErr)
		}
		t.Logf("wrote decoded progressive ELF to %s", dumpPath)
	}
	t.Logf("progressive ELF program headers: %+v", progressive.ELF.ProgramHeaders)
	if needleText := os.Getenv("ARAM_TRACE_PROGRESSIVE_BYTES"); needleText != "" {
		needle, decodeErr := hex.DecodeString(strings.ReplaceAll(needleText, " ", ""))
		if decodeErr != nil || len(needle) == 0 {
			t.Fatalf("invalid ARAM_TRACE_PROGRESSIVE_BYTES %q", needleText)
		}
		matches := 0
		remaining := progressive.Bytes
		base := 0
		for {
			index := bytes.Index(remaining, needle)
			if index < 0 {
				break
			}
			offset := base + index
			addresses := make([]uint32, 0, 1)
			for _, header := range progressive.ELF.ProgramHeaders {
				if header.Type != 1 || uint64(offset) < uint64(header.Offset) ||
					uint64(offset)+uint64(len(needle)) > uint64(header.Offset)+uint64(header.FileSize) {
					continue
				}
				addresses = append(addresses, header.VirtualAddress+uint32(offset-int(header.Offset)))
			}
			matches++
			t.Logf(
				"progressive byte match %d: file+0x%x virtual=%#x",
				matches, offset, addresses,
			)
			base = offset + 1
			remaining = progressive.Bytes[base:]
		}
		t.Logf("progressive byte matches: %d", matches)
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
	referenceDirectory := os.Getenv("ARAM_SCHW830_REFERENCE_DIR")
	if referenceDirectory == "" {
		root := os.Getenv("ARAM_REFERENCE_REPO")
		if root == "" {
			t.Skip("ARAM_REFERENCE_REPO or ARAM_SCHW830_REFERENCE_DIR is not configured")
		}
		referenceDirectory = filepath.Join(root, "SCH-W380_DL21")
	}
	set := openSCHW830ReferenceSet(t, referenceDirectory)
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
	board := SCHW830DL21BoardProfile()
	board.FirmwareBuildID = profile.ID
	if profile.Model == "SCH-W860" {
		board.LegacyTopWritableOffsets = []uint32{
			qualcommLegacyTopIDOffset,
			qualcommLegacyTopIDOffset + 4,
		}
	}
	legacyNANDMarkers := os.Getenv("ARAM_LEGACY_NAND_MARKERS") != ""
	physicalBadBlocks := board.NANDFactoryBadBlocks
	if legacyNANDMarkers {
		// The legacy diagnostic reproduces the former logical-image device
		// directly, before physical bad-block placement was modeled.
		physicalBadBlocks = nil
	}
	flashImage, err := samsung.AssembleFlashWithOptions(set, pkg, samsung.FlashAssemblyOptions{
		FactoryBadBlocks: physicalBadBlocks,
	})
	if err != nil {
		t.Fatal(err)
	}
	flash, err := NewCOWFlashWithCapacityAndSeeds(
		flashImage, board.NANDSize, samsung.EraseBlockSize, flashImage.Identity(),
		board.NANDInitialData,
	)
	if err != nil {
		t.Fatal(err)
	}
	mediaLoadPrefix := os.Getenv("ARAM_LOAD_FACTORY_MEDIA_PREFIX")
	if mediaLoadPrefix != "" {
		flashState, readErr := os.ReadFile(mediaLoadPrefix + ".flash")
		if readErr != nil {
			t.Fatalf("read formatted flash state: %v", readErr)
		}
		if loadErr := flash.LoadState(flashState); loadErr != nil {
			t.Fatalf("load formatted flash state: %v", loadErr)
		}
		t.Logf("loaded formatted flash state from %s.flash", mediaLoadPrefix)
	}
	if os.Getenv("ARAM_LOAD_RUNTIME_PREFIX") == "" {
		applySCHW830BootStatusOverride(t, &board, "ARAM_BOOT_STATUS_048C", 0x048c)
		applySCHW830BootStatusOverride(t, &board, "ARAM_BOOT_STATUS_0C34", 0x0c34)
	}
	backend := interpreter.New()
	if os.Getenv("ARAM_INITIAL_HIGH_VECTORS") != "" {
		seedSCHW830InitialHighVectors(t, backend)
	}
	if historyText := os.Getenv("ARAM_PC_HISTORY"); historyText != "" {
		historyLimit, historyErr := strconv.ParseUint(historyText, 0, 32)
		if historyErr != nil || historyLimit == 0 {
			t.Fatalf("invalid ARAM_PC_HISTORY %q", historyText)
		}
		if err := backend.SetPCHistoryLimit(uint32(historyLimit)); err != nil {
			t.Fatal(err)
		}
	}
	if captureText := os.Getenv("ARAM_CAPTURE_PC_REGISTERS"); captureText != "" {
		parts := strings.Split(captureText, ",")
		if len(parts) != 2 {
			t.Fatalf("invalid ARAM_CAPTURE_PC_REGISTERS %q", captureText)
		}
		address, addressErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
		limit, limitErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 32)
		if addressErr != nil || limitErr != nil || limit == 0 {
			t.Fatalf("invalid ARAM_CAPTURE_PC_REGISTERS %q", captureText)
		}
		if err := backend.SetPCRegisterCapture(uint32(address), uint32(limit)); err != nil {
			t.Fatal(err)
		}
	}
	if historyText := os.Getenv("ARAM_CP15_CONTROL_HISTORY"); historyText != "" {
		historyLimit, historyErr := strconv.ParseUint(historyText, 0, 32)
		if historyErr != nil || historyLimit == 0 {
			t.Fatalf("invalid ARAM_CP15_CONTROL_HISTORY %q", historyText)
		}
		if err := backend.SetCP15ControlHistoryLimit(uint32(historyLimit)); err != nil {
			t.Fatal(err)
		}
	}
	if historyText := os.Getenv("ARAM_CP15_PREFETCH_HISTORY"); historyText != "" {
		historyLimit, historyErr := strconv.ParseUint(historyText, 0, 32)
		if historyErr != nil || historyLimit == 0 {
			t.Fatalf("invalid ARAM_CP15_PREFETCH_HISTORY %q", historyText)
		}
		if err := backend.SetInstructionCachePrefetchHistoryLimit(uint32(historyLimit)); err != nil {
			t.Fatal(err)
		}
	}
	interruptController := NewQualcommInterruptController(nil)
	if board.VectoredInterrupt == nil {
		t.Fatal("SCH-W830 profile has no vectored interrupt controller")
	}
	vectoredInterruptController, err := NewQualcommVectoredInterruptController(
		*board.VectoredInterrupt,
		backend,
	)
	if err != nil {
		t.Fatal(err)
	}
	nandReady := NewStatusSignal()
	var timeTickClock *QualcommTimeTickClockConfig
	if board.TimeTickClock != nil {
		configured := *board.TimeTickClock
		timeTickClock = &configured
	}
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
			UseVectoredController: board.TimeTickClock != nil &&
				board.TimeTickClock.UseVectoredController,
		}
	}
	nandConfig := Qualcomm2K8BitNANDConfig(board.NANDReadID, nandReady)
	nandConfig.Capacity = board.NANDSize
	nandConfig.FactoryBadBlocks = append([]uint32(nil), board.NANDFactoryBadBlocks...)
	if legacyNANDMarkers {
		// Diagnostic reproduction of the pre-OOB model: command 2 sampled a
		// word from main storage at the controller address. Keeping this behind
		// an explicit private-test switch lets traces be compared without making
		// the compatibility accident part of the device model.
		const spareSize = uint32(0x10)
		pageCount := nandConfig.Capacity / uint64(nandConfig.PageSize)
		spare := bytes.Repeat([]byte{0xff}, int(pageCount)*int(spareSize))
		var marker [2]byte
		for page := uint64(0); page < pageCount; page++ {
			if _, readErr := flash.ReadAt(marker[:], int64(page*qualcommNANDCodewordDataSize)); readErr != nil {
				t.Fatal(readErr)
			}
			copy(spare[page*uint64(spareSize):], marker[:])
		}
		nandConfig.SpareSize = spareSize
		nandConfig.Spare = byteStorage{data: spare}
		nandConfig.FactoryBadBlocks = nil
	}
	if nandConfig.PageSize != samsung.PageSize {
		t.Fatal("SCH-W830 NAND profile page size does not match normalized flash")
	}
	nand, err := NewQualcommNAND(flash, nandConfig)
	if err != nil {
		t.Fatal(err)
	}
	if mediaLoadPrefix != "" {
		nandState, readErr := os.ReadFile(mediaLoadPrefix + ".nand")
		if readErr != nil {
			t.Fatalf("read formatted NAND state: %v", readErr)
		}
		if loadErr := nand.LoadState(nandState); loadErr != nil {
			t.Fatalf("load formatted NAND state: %v", loadErr)
		}
		if resetErr := nand.Reset(); resetErr != nil {
			t.Fatalf("reset controller after loading formatted NAND state: %v", resetErr)
		}
		t.Logf("loaded formatted NAND spare state from %s.nand", mediaLoadPrefix)
	}
	bootControl, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5880, ClockModeStatus: board.BootClockModeStatus,
		WritableOffsets:             board.BootControlWritableOffsets,
		HalfwordOffsets:             board.BootControlHalfwordOffsets,
		MixedWidthOffsets:           board.BootControlMixedWidthOffsets,
		ReadOnlyRegisters:           board.BootControlReadOnlyRegisters,
		RegisterResets:              board.BootControlRegisterResets,
		CompletionEvents:            board.BootControlCompletionEvents,
		LegacyUARTControllers:       board.BootControlLegacyUARTControllers,
		SBIControllers:              board.BootControlSBIControllers,
		SBICompletionStatus:         board.BootControlSBICompletionStatus,
		NANDReady:                   nandReady,
		InterruptController:         interruptController,
		VectoredInterruptController: vectoredInterruptController,
		TimeTickClock:               timeTickClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondaryClock, err := NewQualcommSecondaryClockControlWithWritableOffsets(
		board.SecondaryClockWritableOffsets,
	)
	if err != nil {
		t.Fatal(err)
	}
	primaryClock, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		Status:          board.PrimaryClockStatus,
		InputMask:       board.PrimaryClockInputMask,
		WritableOffsets: board.PrimaryClockWritableOffsets,
	})
	if err != nil {
		t.Fatal(err)
	}
	keypad, err := board.AttachKeypad(primaryClock, secondaryClock, interruptController)
	if err != nil {
		t.Fatal(err)
	}
	legacyTop, err := NewQualcommLegacyTopPageWithConfig(QualcommLegacyTopConfig{
		Version:         board.LegacyTopVersion,
		Identification:  board.LegacyTopIdentification,
		WritableOffsets: board.LegacyTopWritableOffsets,
	})
	if err != nil {
		t.Fatal(err)
	}
	clockRegime, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		SleepControllers:            board.ClockRegimeSleepControllers,
		Counters:                    board.ClockRegimeCounters,
		Comparators:                 board.ClockRegimeComparators,
		InterruptController:         interruptController,
		VectoredInterruptController: vectoredInterruptController,
	})
	if err != nil {
		t.Fatal(err)
	}
	busRegisters, err := NewSparseWordRegisters(schw830BusRegisterOffsets())
	if err != nil {
		t.Fatal(err)
	}
	panelController, err := NewDCSPanelController(board.Panel)
	if err != nil {
		t.Fatal(err)
	}
	panel, err := NewParallelPanelInterfaceWithController(panelController)
	if err != nil {
		t.Fatal(err)
	}
	var panelTrace *schw830PanelTrace
	if os.Getenv("ARAM_TRACE_PANEL_COMMANDS") != "" {
		panelTrace = &schw830PanelTrace{}
		panel.SetWriteObserver(panelTrace.Observe)
	}
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
	mdpEngine, err := board.AttachMDP(bus, panelController, bootControl)
	if err != nil {
		t.Fatal(err)
	}
	var (
		traceContextAccesses       []MemoryAccess
		traceContextLowWriteWindow []MemoryAccess
	)
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
		maximumTraceContextAccesses := 4096
		if limitText := os.Getenv("ARAM_TRACE_CONTEXT_LIMIT"); limitText != "" {
			limit, limitErr := strconv.ParseUint(limitText, 0, 16)
			if limitErr != nil || limit == 0 {
				t.Fatalf("invalid ARAM_TRACE_CONTEXT_LIMIT %q", limitText)
			}
			maximumTraceContextAccesses = int(limit)
		}
		if err := bus.SetInstructionMemoryObserver(uint32(address), uint32(size), func(access MemoryAccess) {
			if access.Permission == cpu.PermissionExecute {
				return
			}
			// This observer is opt-in and diagnostic-only. Keep enough accesses to
			// retain complete early-driver registration sequences; a 256-entry
			// tail dropped the first VIC callbacks before the firmware reached its
			// scheduler idle boundary.
			if len(traceContextAccesses) == maximumTraceContextAccesses {
				copy(traceContextAccesses, traceContextAccesses[1:])
				traceContextAccesses = traceContextAccesses[:maximumTraceContextAccesses-1]
			}
			traceContextAccesses = append(traceContextAccesses, access)
			if access.Write && access.Address < 0x20 {
				const lowWriteContextWindow = 64
				start := max(0, len(traceContextAccesses)-lowWriteContextWindow)
				traceContextLowWriteWindow = append(
					traceContextLowWriteWindow[:0],
					traceContextAccesses[start:]...,
				)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	traceAllMMIO := os.Getenv("ARAM_TRACE_ALL_MMIO") != ""
	mmioTrace := newSCHW830MMIOTrace(
		os.Getenv("ARAM_TRACE_SCHW830_MMIO") != "" || traceAllMMIO ||
			os.Getenv("ARAM_TRACE_MMIO_ADDRESSES") != "" ||
			os.Getenv("ARAM_TRACE_MMIO_RANGE") != "",
		traceAllMMIO,
	)
	if rangeText := os.Getenv("ARAM_TRACE_MMIO_RANGE"); rangeText != "" {
		parts := strings.Split(rangeText, ",")
		if len(parts) != 2 {
			t.Fatalf("invalid ARAM_TRACE_MMIO_RANGE %q", rangeText)
		}
		start, startErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
		size, sizeErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 32)
		if startErr != nil || sizeErr != nil || size == 0 || start+size > 1<<32 {
			t.Fatalf("invalid ARAM_TRACE_MMIO_RANGE %q", rangeText)
		}
		mmioTrace.SetFocusRange(uint32(start), uint32(size))
	}
	var panelSourceTrace *schw830PanelSourceTrace
	if os.Getenv("ARAM_TRACE_PANEL_SOURCES") != "" {
		panelSourceTrace = newSCHW830PanelSourceTrace()
	}
	var nandCommandTrace *schw830NANDCommandTrace
	if os.Getenv("ARAM_TRACE_NAND_COMMANDS") != "" {
		nandCommandTrace = newSCHW830NANDCommandTrace(nand, nandReady)
	}
	if mmioTrace != nil || panelSourceTrace != nil || nandCommandTrace != nil {
		bus.SetMMIOObserver(func(access MMIOAccess) {
			if mmioTrace != nil {
				mmioTrace.Observe(access)
			}
			if panelSourceTrace != nil {
				panelSourceTrace.Observe(access)
			}
			if nandCommandTrace != nil {
				nandCommandTrace.Observe(access)
			}
		})
	}
	var (
		traceWrite            *MemoryAccess
		traceWriteHistory     []schw830TraceWriteCapture
		traceExecution        *MemoryAccess
		traceExecutionHistory []schw830TraceWriteCapture
		traceMemoryWrites     []MemoryAccess
		lowVectorWrites       []MemoryAccess
		lowVectorExecutions   []MemoryAccess
	)
	if rangeText := os.Getenv("ARAM_LOG_MEMORY_WRITES"); rangeText != "" {
		if os.Getenv("ARAM_TRACE_WRITE_ADDRESS") != "" ||
			os.Getenv("ARAM_TRACE_READ_ADDRESS") != "" ||
			os.Getenv("ARAM_TRACE_EXEC_ADDRESS") != "" {
			t.Fatal("ARAM_LOG_MEMORY_WRITES cannot be combined with a memory watchpoint")
		}
		parts := strings.Split(rangeText, ",")
		if len(parts) != 2 {
			t.Fatalf("invalid ARAM_LOG_MEMORY_WRITES %q", rangeText)
		}
		address, addressErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
		size, sizeErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 32)
		if addressErr != nil || sizeErr != nil || size == 0 || address+size > 1<<32 {
			t.Fatalf("invalid ARAM_LOG_MEMORY_WRITES %q", rangeText)
		}
		limit := uint64(4096)
		if limitText := os.Getenv("ARAM_LOG_MEMORY_WRITES_LIMIT"); limitText != "" {
			parsed, limitErr := strconv.ParseUint(limitText, 0, 32)
			if limitErr != nil || parsed == 0 {
				t.Fatalf("invalid ARAM_LOG_MEMORY_WRITES_LIMIT %q", limitText)
			}
			limit = parsed
		}
		if err := bus.SetMemoryObserver(uint32(address), uint32(size), func(access MemoryAccess) {
			if access.Write && uint64(len(traceMemoryWrites)) < limit {
				traceMemoryWrites = append(traceMemoryWrites, access)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeAddressText := os.Getenv("ARAM_TRACE_WRITE_ADDRESS")
	readAddressText := os.Getenv("ARAM_TRACE_READ_ADDRESS")
	if writeAddressText != "" && readAddressText != "" ||
		(writeAddressText != "" || readAddressText != "") && os.Getenv("ARAM_TRACE_EXEC_ADDRESS") != "" {
		t.Fatal("ARAM_TRACE_WRITE_ADDRESS, ARAM_TRACE_READ_ADDRESS, and ARAM_TRACE_EXEC_ADDRESS are mutually exclusive")
	}
	traceReads := readAddressText != ""
	addressText := writeAddressText
	if traceReads {
		addressText = readAddressText
	}
	if addressText != "" {
		addressValue, parseErr := strconv.ParseUint(addressText, 0, 32)
		if parseErr != nil {
			t.Fatalf("invalid memory trace address %q", addressText)
		}
		traceRangeSize := uint64(4)
		rangeEnvironment := "ARAM_TRACE_WRITE_RANGE_SIZE"
		if traceReads {
			rangeEnvironment = "ARAM_TRACE_READ_RANGE_SIZE"
		}
		if sizeText := os.Getenv(rangeEnvironment); sizeText != "" {
			size, sizeErr := strconv.ParseUint(sizeText, 0, 32)
			if sizeErr != nil || size == 0 {
				t.Fatalf("invalid %s %q", rangeEnvironment, sizeText)
			}
			traceRangeSize = size
		}
		if addressValue+traceRangeSize > 1<<32 {
			t.Fatalf(
				"memory trace range %#x+%#x exceeds physical address space",
				addressValue,
				traceRangeSize,
			)
		}
		var (
			matchValue        uint32
			matchSet          bool
			matchPC           uint32
			matchPCSet        bool
			matchWidth        Width
			matchWidthSet     bool
			matchPanelCommand uint16
			panelCommandSet   bool
			matchNonZero      bool
			matchingWrites    uint64
			stopCount         uint64 = 1
		)
		matchNonZero = os.Getenv("ARAM_TRACE_WRITE_NONZERO") != ""
		if valueText := os.Getenv("ARAM_TRACE_WRITE_VALUE"); valueText != "" {
			value, valueErr := strconv.ParseUint(valueText, 0, 32)
			if valueErr != nil {
				t.Fatalf("invalid ARAM_TRACE_WRITE_VALUE %q", valueText)
			}
			matchValue, matchSet = uint32(value), true
		}
		if pcText := os.Getenv("ARAM_TRACE_WRITE_PC"); pcText != "" {
			pc, pcErr := strconv.ParseUint(pcText, 0, 32)
			if pcErr != nil {
				t.Fatalf("invalid ARAM_TRACE_WRITE_PC %q", pcText)
			}
			matchPC, matchPCSet = uint32(pc), true
		}
		if widthText := os.Getenv("ARAM_TRACE_WRITE_WIDTH"); widthText != "" {
			width, widthErr := strconv.ParseUint(widthText, 0, 8)
			if widthErr != nil || width != 1 && width != 2 && width != 4 {
				t.Fatalf("invalid ARAM_TRACE_WRITE_WIDTH %q", widthText)
			}
			matchWidth, matchWidthSet = Width(width), true
		}
		if commandText := os.Getenv("ARAM_TRACE_WRITE_PANEL_COMMAND"); commandText != "" {
			command, commandErr := strconv.ParseUint(commandText, 0, 16)
			if commandErr != nil {
				t.Fatalf("invalid ARAM_TRACE_WRITE_PANEL_COMMAND %q", commandText)
			}
			matchPanelCommand, panelCommandSet = uint16(command), true
		}
		countEnvironment := "ARAM_TRACE_WRITE_STOP_COUNT"
		if traceReads {
			countEnvironment = "ARAM_TRACE_READ_STOP_COUNT"
		}
		if countText := os.Getenv(countEnvironment); countText != "" {
			count, countErr := strconv.ParseUint(countText, 0, 64)
			if countErr != nil || count == 0 {
				t.Fatalf("invalid %s %q", countEnvironment, countText)
			}
			stopCount = count
		}
		if err := bus.SetMemoryObserver(uint32(addressValue), uint32(traceRangeSize), func(access MemoryAccess) {
			if traceWrite != nil || !access.Context.Attributed || access.Write == traceReads ||
				matchNonZero && access.Value == 0 ||
				matchSet && access.Value != matchValue ||
				matchPCSet && access.Context.InstructionAddress != matchPC ||
				matchWidthSet && access.Width != matchWidth ||
				panelCommandSet && panel.CurrentCommand() != matchPanelCommand {
				return
			}
			matchingWrites++
			stack := make([]byte, 0x100)
			stackErr := readSCHW830BusMemory(bus, access.Context.StackAddress, stack)
			mmioSnapshot := ""
			if mmioTrace != nil {
				mmioSnapshot = mmioTrace.String()
			}
			traceWriteHistory = appendSCHW830TraceCapture(traceWriteHistory, schw830TraceWriteCapture{
				Match: matchingWrites, Access: access, Stack: stack, StackErr: stackErr,
				NANDAddress: nand.address, NANDNextChunk: nand.nextChunk,
				NANDStatus: nand.status, MMIOSnapshot: mmioSnapshot,
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
		var (
			matchLinkAddress uint32
			matchLinkSet     bool
		)
		if linkText := os.Getenv("ARAM_TRACE_EXEC_LINK_ADDRESS"); linkText != "" {
			link, linkErr := strconv.ParseUint(linkText, 0, 32)
			if linkErr != nil {
				t.Fatalf("invalid ARAM_TRACE_EXEC_LINK_ADDRESS %q", linkText)
			}
			matchLinkAddress, matchLinkSet = uint32(link), true
		}
		var matchingExecutions uint64
		if err := bus.SetMemoryObserver(uint32(addressValue), 2, func(access MemoryAccess) {
			if traceExecution != nil || !access.Context.Attributed || access.Write ||
				access.Permission != cpu.PermissionExecute ||
				matchLinkSet && access.Context.LinkAddress != matchLinkAddress {
				return
			}
			matchingExecutions++
			stack := make([]byte, 0x100)
			stackErr := readSCHW830BusMemory(bus, access.Context.StackAddress, stack)
			traceExecutionHistory = appendSCHW830TraceCapture(traceExecutionHistory, schw830TraceWriteCapture{
				Match: matchingExecutions, Access: access, Stack: stack, StackErr: stackErr,
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
	if os.Getenv("ARAM_TRACE_LOW_VECTOR_WRITES") != "" ||
		os.Getenv("ARAM_TRACE_LOW_VECTOR_ACCESSES") != "" ||
		os.Getenv("ARAM_TRACE_VECTOR_WRITE_BASE") != "" {
		if traceWrite != nil || os.Getenv("ARAM_TRACE_WRITE_ADDRESS") != "" ||
			os.Getenv("ARAM_TRACE_READ_ADDRESS") != "" ||
			os.Getenv("ARAM_TRACE_EXEC_ADDRESS") != "" {
			t.Fatal("vector-write tracing cannot be combined with a memory watchpoint")
		}
		vectorBase := uint32(0)
		if baseText := os.Getenv("ARAM_TRACE_VECTOR_WRITE_BASE"); baseText != "" {
			base, parseErr := strconv.ParseUint(baseText, 0, 32)
			if parseErr != nil || base > uint64(^uint32(0)-0x1f) {
				t.Fatalf("invalid ARAM_TRACE_VECTOR_WRITE_BASE %q", baseText)
			}
			vectorBase = uint32(base)
		}
		if err := bus.SetMemoryObserver(vectorBase, 0x20, func(access MemoryAccess) {
			if access.Permission == cpu.PermissionExecute {
				const maximumLowVectorExecutions = 256
				if len(lowVectorExecutions) == maximumLowVectorExecutions {
					copy(lowVectorExecutions, lowVectorExecutions[1:])
					lowVectorExecutions = lowVectorExecutions[:maximumLowVectorExecutions-1]
				}
				lowVectorExecutions = append(lowVectorExecutions, access)
			}
			if !access.Write {
				return
			}
			const maximumLowVectorWrites = 256
			if len(lowVectorWrites) == maximumLowVectorWrites {
				copy(lowVectorWrites, lowVectorWrites[1:])
				lowVectorWrites = lowVectorWrites[:maximumLowVectorWrites-1]
			}
			lowVectorWrites = append(lowVectorWrites, access)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := board.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	factoryFormatInjected := false
	if err := board.ApplyReadOnlyRegisters(bus); err != nil {
		t.Fatal(err)
	}
	if err := board.ApplyLatchedRegistersWithInterrupts(
		bus,
		interruptController,
		vectoredInterruptController,
	); err != nil {
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
	clockedDevices := bus.ClockedDevices()
	fatalDiagnostic := errors.New("unexpected OEM fatal diagnostic")
	flashInitFailure := errors.New("OEM flash initialization failed")
	traceStop := errors.New("requested firmware trace stop")
	factoryBootstrapReturned := errors.New("guest factory bootstrap returned")
	factoryTaskArgumentInjected := errors.New("guest factory-task argument injected")
	const factoryBootstrapReturnAddress = uint32(0x07fff000)
	var calls []HLECallProfile
	if profile.Model == "SCH-W830" {
		// These are addresses in the exact SCH-W830 OEMSBL/runtime images, not
		// Qualcomm platform calls. Installing them for an adjacent board can
		// collide with unrelated guest code and manufacture a false boundary.
		calls = append(calls,
			HLECallProfile{
				ID: "diagnostic-oem-fatal", Contract: "diagnostic.oem-fatal",
				Address: 0x00107ffc, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
			},
			HLECallProfile{
				ID: "diagnostic-flash-init-failure", Contract: "diagnostic.flash-init-failure",
				Address: 0x000a6ae0, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
			},
		)
	}
	if os.Getenv("ARAM_INJECT_FACTORY_FORMAT") != "" {
		// The TFS4 task selects its factory path from its entry argument: r0 == 0
		// takes the normal mount path while any non-zero value runs the firmware's
		// native BML/STL and removable-volume provisioning sequence. Stop before
		// the entry PUSH, override the argument while the backend is not running,
		// remove this one-shot trap, and resume at the same guest instruction.
		calls = append(calls, HLECallProfile{
			ID:       "diagnostic-factory-task-argument",
			Contract: "diagnostic.factory-task-argument",
			Address:  0x020b1106,
			Mode:     cpu.ModeThumb,
			Return:   HLEReturnLinkRegister,
		})
	}
	if os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") != "" ||
		os.Getenv("ARAM_CALL_GUEST_RFS_FORMAT") != "" ||
		os.Getenv("ARAM_GUEST_FACTORY_MKDIR") != "" ||
		os.Getenv("ARAM_GUEST_FACTORY_TOUCH") != "" ||
		os.Getenv("ARAM_GUEST_READ_FILE") != "" ||
		os.Getenv("ARAM_GUEST_SIGNAL_MAIN") != "" ||
		os.Getenv("ARAM_GUEST_SIGNAL_MDSP") != "" ||
		os.Getenv("ARAM_GUEST_SIGNAL_UIM") != "" ||
		os.Getenv("ARAM_GUEST_SIGNAL_GSDI") != "" ||
		os.Getenv("ARAM_GUEST_SIGNAL_UI") != "" ||
		os.Getenv("ARAM_GUEST_SIGNAL_WCDMA_L1") != "" ||
		os.Getenv("ARAM_CALL_GUEST_ENTER_IDLE_NOSIM") != "" ||
		os.Getenv("ARAM_CALL_GUEST_ENTRY") != "" ||
		os.Getenv("ARAM_SCAN_GUEST_CONFIG_BYTE") != "" {
		calls = append(calls, HLECallProfile{
			ID:       "diagnostic-factory-bootstrap-return",
			Contract: "diagnostic.factory-bootstrap-return",
			Address:  factoryBootstrapReturnAddress,
			Mode:     cpu.ModeThumb,
			Return:   HLEReturnLinkRegister,
		})
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
	if traceStopText := os.Getenv("ARAM_TRACE_STOP_ARM_PC"); traceStopText != "" {
		address, parseErr := strconv.ParseUint(traceStopText, 0, 32)
		if parseErr != nil || address&3 != 0 {
			t.Fatalf("invalid ARAM_TRACE_STOP_ARM_PC %q", traceStopText)
		}
		calls = append(calls, HLECallProfile{
			ID: "diagnostic-arm-trace-stop", Contract: "diagnostic.trace-stop",
			Address: uint32(address), Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		})
	}
	handlers := map[string]HLECallHandler{
		"diagnostic.oem-fatal": HLECallHandlerFunc(func(HLECallContext) error {
			return fatalDiagnostic
		}),
		"diagnostic.flash-init-failure": HLECallHandlerFunc(func(HLECallContext) error {
			return flashInitFailure
		}),
		"diagnostic.trace-stop": HLECallHandlerFunc(func(HLECallContext) error {
			return traceStop
		}),
		"diagnostic.factory-bootstrap-return": HLECallHandlerFunc(func(HLECallContext) error {
			return factoryBootstrapReturned
		}),
		"diagnostic.factory-task-argument": HLECallHandlerFunc(func(call HLECallContext) error {
			if err := call.CPU.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return err
			}
			factoryFormatInjected = true
			return factoryTaskArgumentInjected
		}),
	}
	runner, err := NewHLERunner(bus, backend, calls, handlers)
	if err != nil {
		t.Fatal(err)
	}
	callGuestRFSFormat := func(device string) {
		const (
			// The public wrapper at 0x01a50ad6 collapses every backend failure to
			// -1. Call its immediate backend wrapper so diagnostics retain the
			// original VFS/RFAT error code while using the same four arguments.
			formatEntry   = uint32(0x00ccc32c)
			formatScratch = uint32(0x07ffdc00)
			scratchBytes  = 0xc0
		)
		if os.Getenv("ARAM_GUEST_RFS_INIT") != "" {
			const initializeVFSEntry = uint32(0x00ccc50c)
			var savedRegisters [cpu.RegisterCPSR + 1]uint32
			for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
				value, readErr := backend.ReadRegister(register)
				if readErr != nil {
					t.Fatalf("save register %d before guest VFS initialization: %v", register, readErr)
				}
				savedRegisters[register] = value
			}
			initializeStatus := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb | 1<<7 | 1<<6
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0:   0,
				cpu.RegisterR1:   0,
				cpu.RegisterR2:   0,
				cpu.RegisterR3:   0,
				cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
				cpu.RegisterCPSR: initializeStatus,
			} {
				if writeErr := backend.WriteRegister(register, value); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			initializeResult := runner.Run(
				context.Background(), initializeVFSEntry, cpu.ModeThumb, 200_000_000,
			)
			initializeReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
				if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
					t.Fatalf("restore register %d after guest VFS initialization: %v", register, writeErr)
				}
			}
			t.Logf("guest VFS initialization result: %+v return=0x%08x", initializeResult, initializeReturn)
			if !errors.Is(initializeResult.Err, factoryBootstrapReturned) {
				t.Fatalf("guest VFS initialization did not return through its sentinel: %+v", initializeResult)
			}
			if initializeReturn != 0 {
				t.Fatalf("guest VFS initialization returned failure 0x%08x", initializeReturn)
			}
		}
		if os.Getenv("ARAM_GUEST_RFS_REGISTER_DEVICES") != "" {
			const registerDeviceEntry = uint32(0x0101cdd0)
			var allocatorState [8]byte
			if readErr := backend.ReadMemory(0x043ac2a4, allocatorState[:]); readErr != nil {
				t.Fatalf("read guest VFS device allocator state: %v", readErr)
			}
			var VFSCounts [16]byte
			if readErr := backend.ReadMemory(0x043acb54, VFSCounts[:]); readErr != nil {
				t.Fatalf("read guest VFS limits: %v", readErr)
			}
			t.Logf(
				"guest VFS device allocator before registration: state=%x limits=%x",
				allocatorState,
				VFSCounts,
			)
			for index := uint32(0); index < 4; index++ {
				entry := make([]byte, 0x68)
				entryAddress := uint32(0x043d6b34) + index*0x68
				if readErr := backend.ReadMemory(entryAddress, entry); readErr != nil {
					t.Fatalf("read guest VFS device entry %d: %v", index, readErr)
				}
				name := strings.TrimRight(string(entry[0x28:0x40]), "\x00")
				t.Logf(
					"guest VFS device entry %d: index=%d active=%d opens=%d name=%q config-name=0x%08x",
					index,
					binary.LittleEndian.Uint16(entry[0:]),
					binary.LittleEndian.Uint16(entry[2:]),
					binary.LittleEndian.Uint16(entry[4:]),
					name,
					binary.LittleEndian.Uint32(entry[0x64:]),
				)
			}
			var savedRegisters [cpu.RegisterCPSR + 1]uint32
			for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
				value, readErr := backend.ReadRegister(register)
				if readErr != nil {
					t.Fatalf("save register %d before guest RFS device registration: %v", register, readErr)
				}
				savedRegisters[register] = value
			}
			configBackup := make([]byte, 0x28)
			if readErr := backend.ReadMemory(formatScratch, configBackup); readErr != nil {
				t.Fatalf("preserve guest RFS device config scratch: %v", readErr)
			}
			config := make([]byte, 0x28)
			for offset, value := range map[int]uint32{
				0x00: 0x00517c79,
				0x04: 0x00517bbb,
				0x08: 0x00517b69,
				0x0c: 0x00517eef,
				0x10: 0x00517d45,
				0x14: 0x00517d17,
				0x18: 0x00517ce1,
				0x1c: 0x00517ca5,
				0x20: 0,
				0x24: 0x028ce368,
			} {
				binary.LittleEndian.PutUint32(config[offset:], value)
			}
			if writeErr := backend.WriteMemory(formatScratch, config); writeErr != nil {
				t.Fatalf("write guest RFS device config: %v", writeErr)
			}
			registerStatus := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb | 1<<7 | 1<<6
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0:   formatScratch,
				cpu.RegisterR1:   1,
				cpu.RegisterR2:   1,
				cpu.RegisterR3:   0,
				cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
				cpu.RegisterCPSR: registerStatus,
			} {
				if writeErr := backend.WriteRegister(register, value); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			registerResult := runner.Run(
				context.Background(), registerDeviceEntry, cpu.ModeThumb, 100_000_000,
			)
			registerReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if writeErr := backend.WriteMemory(formatScratch, configBackup); writeErr != nil {
				t.Fatalf("restore guest RFS device config scratch: %v", writeErr)
			}
			for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
				if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
					t.Fatalf("restore register %d after guest RFS device registration: %v", register, writeErr)
				}
			}
			t.Logf("guest RFS device registration result: %+v return=0x%08x", registerResult, registerReturn)
			if !errors.Is(registerResult.Err, factoryBootstrapReturned) {
				t.Fatalf("guest RFS device registration did not return through its sentinel: %+v", registerResult)
			}
		}
		clusterSectors := uint32(4)
		if clusterText := os.Getenv("ARAM_GUEST_RFS_CLUSTER_SECTORS"); clusterText != "" {
			parsed, parseErr := strconv.ParseUint(clusterText, 0, 32)
			if parseErr != nil || parsed == 0 {
				t.Fatalf("invalid ARAM_GUEST_RFS_CLUSTER_SECTORS %q", clusterText)
			}
			clusterSectors = uint32(parsed)
		}
		switch device {
		case "1", "nfa":
			device = "/dev/nfa0"
		case "nfb":
			device = "/dev/nfb0"
		case "nf":
			device = "/dev/nf0"
		}
		if len(device)+1 > 0x40 {
			t.Fatalf("guest RFS device path is too long: %q", device)
		}
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before guest RFS format: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		scratch := make([]byte, scratchBytes)
		if readErr := backend.ReadMemory(formatScratch, scratch); readErr != nil {
			t.Fatalf("preserve guest RFS format scratch: %v", readErr)
		}
		arguments := make([]byte, scratchBytes)
		copy(arguments[0x00:], append([]byte(device), 0))
		copy(arguments[0x40:], []byte("KFATFS\x00"))
		copy(arguments[0x80:], []byte("FAT16\x00"))
		if writeErr := backend.WriteMemory(formatScratch, arguments); writeErr != nil {
			t.Fatalf("write guest RFS format arguments: %v", writeErr)
		}
		formatStatus := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb | 1<<7 | 1<<6
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   formatScratch,
			cpu.RegisterR1:   formatScratch + 0x40,
			cpu.RegisterR2:   formatScratch + 0x80,
			cpu.RegisterR3:   clusterSectors,
			cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
			cpu.RegisterCPSR: formatStatus,
		} {
			if writeErr := backend.WriteRegister(register, value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		formatResult := runner.Run(
			context.Background(), formatEntry, cpu.ModeThumb, 500_000_000,
		)
		formatReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var encoded [4]byte
		if readErr := backend.ReadMemory(0x03fbe5a4, encoded[:]); readErr != nil {
			t.Fatalf("read C library errno after guest RFS format: %v", readErr)
		}
		guestErrno := binary.LittleEndian.Uint32(encoded[:])
		if writeErr := backend.WriteMemory(formatScratch, scratch); writeErr != nil {
			t.Fatalf("restore guest RFS format scratch: %v", writeErr)
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
				t.Fatalf("restore register %d after guest RFS format: %v", register, writeErr)
			}
		}
		dirtyBlocks := flash.DirtyBlocks()
		dirtySummary := "none"
		if len(dirtyBlocks) != 0 {
			dirtySummary = fmt.Sprintf(
				"%d blocks (0x%x..0x%x)", len(dirtyBlocks), dirtyBlocks[0], dirtyBlocks[len(dirtyBlocks)-1],
			)
		}
		t.Logf(
			"guest RFS format(%q, KFATFS, FAT16, cluster=%d) result: %+v return=0x%08x errno=0x%08x dirty=%s",
			device,
			clusterSectors,
			formatResult,
			formatReturn,
			guestErrno,
			dirtySummary,
		)
		if !errors.Is(formatResult.Err, factoryBootstrapReturned) {
			t.Fatalf("guest RFS format did not return through its sentinel: %+v", formatResult)
		}
		if formatReturn != 0 {
			t.Fatalf("guest RFS format(%q) returned failure 0x%08x", device, formatReturn)
		}
	}
	callGuestSignal := func(taskName string, taskTCB, signal uint32) {
		const rexSetSigsEntry = uint32(0x0013ad82)
		readSignalState := func(label string) {
			var state [0x20]byte
			var control [0x38]byte
			if readErr := backend.ReadMemory(taskTCB, state[:]); readErr != nil {
				t.Fatalf("read %s TCB %s guest signal: %v", taskName, label, readErr)
			}
			if readErr := backend.ReadMemory(0x040ea834, control[:]); readErr != nil {
				t.Fatalf("read REX control %s guest signal: %v", label, readErr)
			}
			t.Logf(
				"%s TCB %s guest signal: signals=0x%08x wait=0x%08x priority=0x%08x current=0x%08x",
				taskName, label,
				binary.LittleEndian.Uint32(state[0x0c:]),
				binary.LittleEndian.Uint32(state[0x10:]),
				binary.LittleEndian.Uint32(state[0x14:]),
				binary.LittleEndian.Uint32(control[0x30:]),
			)
		}
		readSignalState("before")
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before guest Main signal: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		status := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   taskTCB,
			cpu.RegisterR1:   signal,
			cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
			cpu.RegisterCPSR: status,
		} {
			if writeErr := backend.WriteRegister(register, value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		signalResult := runner.Run(context.Background(), rexSetSigsEntry, cpu.ModeThumb, 100_000_000)
		signalReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		t.Logf(
			"guest rex_set_sigs(%s, 0x%08x) result: %+v return=0x%08x",
			taskName, signal, signalResult, signalReturn,
		)
		if !errors.Is(signalResult.Err, factoryBootstrapReturned) {
			t.Fatalf("guest %s signal did not return through its sentinel: %+v", taskName, signalResult)
		}
		readSignalState("after")
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
				t.Fatalf("restore register %d after guest Main signal: %v", register, writeErr)
			}
		}
	}
	callGuestEnterIdleNoSIM := func(object uint32) {
		const enterIdleNoSIMEntry = uint32(0x00934438)
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before guest EnterIdle_NoUSIM: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		status := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   object,
			cpu.RegisterR1:   0,
			cpu.RegisterR2:   0,
			cpu.RegisterR3:   0,
			cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
			cpu.RegisterCPSR: status,
		} {
			if writeErr := backend.WriteRegister(register, value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		callResult := runner.Run(
			context.Background(), enterIdleNoSIMEntry, cpu.ModeThumb, 100_000_000,
		)
		callReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		t.Logf(
			"guest CSECIdleApp_EnterIdle_NoUSIM(0x%08x) result: %+v return=0x%08x",
			object, callResult, callReturn,
		)
		if !errors.Is(callResult.Err, factoryBootstrapReturned) {
			t.Fatalf("guest EnterIdle_NoUSIM did not return through its sentinel: %+v", callResult)
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
				t.Fatalf("restore register %d after guest EnterIdle_NoUSIM: %v", register, writeErr)
			}
		}
	}
	diagnosticGuestCallBudget := uint64(100_000_000)
	if budgetText := os.Getenv("ARAM_CALL_GUEST_BUDGET"); budgetText != "" {
		parsed, parseErr := strconv.ParseUint(budgetText, 0, 64)
		if parseErr != nil || parsed == 0 {
			t.Fatalf("invalid ARAM_CALL_GUEST_BUDGET %q", budgetText)
		}
		diagnosticGuestCallBudget = parsed
	}
	var diagnosticExecutionRunner ExecutionRunner = runner
	callGuestDiagnosticWithLog := func(
		entry uint32,
		arguments [4]uint32,
		logCall bool,
	) uint32 {
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before diagnostic guest call: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		mode := cpu.ModeARM
		status := savedRegisters[cpu.RegisterCPSR] &^ cpu.StatusThumb
		address := entry &^ uint32(3)
		if entry&1 != 0 {
			mode = cpu.ModeThumb
			status |= cpu.StatusThumb
			address = entry &^ uint32(1)
		}
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   arguments[0],
			cpu.RegisterR1:   arguments[1],
			cpu.RegisterR2:   arguments[2],
			cpu.RegisterR3:   arguments[3],
			cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
			cpu.RegisterCPSR: status,
		} {
			if writeErr := backend.WriteRegister(register, value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		callResult := diagnosticExecutionRunner.Run(
			context.Background(), address, mode, diagnosticGuestCallBudget,
		)
		callReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if logCall {
			t.Logf(
				"diagnostic guest call 0x%08x(%08x, %08x, %08x, %08x): %+v return=0x%08x",
				entry, arguments[0], arguments[1], arguments[2], arguments[3], callResult, callReturn,
			)
		}
		watchpointStopped := traceWrite != nil && errors.Is(callResult.Err, cpu.ErrStopped)
		if !errors.Is(callResult.Err, factoryBootstrapReturned) && !watchpointStopped {
			t.Fatalf("diagnostic guest call did not return through its sentinel: %+v", callResult)
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
				t.Fatalf("restore register %d after diagnostic guest call: %v", register, writeErr)
			}
		}
		if watchpointStopped {
			logSCHW830TraceWrite(t, backend, traceWriteHistory, callResult.PC)
		}
		return callReturn
	}
	callGuestDiagnostic := func(entry uint32, arguments [4]uint32) uint32 {
		return callGuestDiagnosticWithLog(entry, arguments, true)
	}
	callGuestMkdir := func(directory string) {
		const (
			fileManagerGlobal = uint32(0x038c3bec)
			mkdirVTableOffset = uint32(0x14)
			pathScratch       = uint32(0x07ffe000)
			maximumPathBytes  = 0x80
		)
		path := append([]byte(directory), 0)
		if len(path) > maximumPathBytes {
			t.Fatalf("guest factory directory path is too long: %q", directory)
		}
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before guest IFileMgr MkDir: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		readWord := func(label string, address uint32) uint32 {
			var encoded [4]byte
			if readErr := backend.ReadMemory(address, encoded[:]); readErr != nil {
				t.Fatalf("read guest %s at 0x%08x: %v", label, address, readErr)
			}
			return binary.LittleEndian.Uint32(encoded[:])
		}
		fileManager := readWord("IFileMgr global", fileManagerGlobal)
		if fileManager == 0 {
			t.Fatal("guest IFileMgr global is null at factory directory bootstrap")
		}
		fileManagerVTable := readWord("IFileMgr vtable", fileManager)
		mkdirEntry := readWord("IFileMgr MkDir entry", fileManagerVTable+mkdirVTableOffset)
		if mkdirEntry&1 == 0 {
			t.Fatalf("guest IFileMgr MkDir entry is not Thumb code: 0x%08x", mkdirEntry)
		}
		scratch := make([]byte, maximumPathBytes)
		if readErr := backend.ReadMemory(pathScratch, scratch); readErr != nil {
			t.Fatalf("preserve guest factory path scratch: %v", readErr)
		}
		if writeErr := backend.WriteMemory(pathScratch, path); writeErr != nil {
			t.Fatalf("write guest factory directory path: %v", writeErr)
		}
		// IFileMgr operations can cross into the filesystem task. Preserve the
		// interrupted UI task's exception masks instead of treating this like a
		// low-level, synchronous flash-format helper.
		mkdirStatus := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   fileManager,
			cpu.RegisterR1:   pathScratch,
			cpu.RegisterR2:   0,
			cpu.RegisterR3:   0,
			cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
			cpu.RegisterCPSR: mkdirStatus,
		} {
			if writeErr := backend.WriteRegister(register, value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		var mkdirResultWrite *MemoryAccess
		if resultAddressText := os.Getenv("ARAM_GUEST_MKDIR_RESULT_ADDRESS"); resultAddressText != "" {
			resultAddress, parseErr := strconv.ParseUint(resultAddressText, 0, 32)
			if parseErr != nil || resultAddress > uint64(^uint32(0)) {
				t.Fatalf("invalid ARAM_GUEST_MKDIR_RESULT_ADDRESS %q", resultAddressText)
			}
			var (
				resultValue    uint32
				resultValueSet bool
			)
			if resultValueText := os.Getenv("ARAM_GUEST_MKDIR_RESULT_VALUE"); resultValueText != "" {
				parsed, valueErr := strconv.ParseUint(resultValueText, 0, 8)
				if valueErr != nil {
					t.Fatalf("invalid ARAM_GUEST_MKDIR_RESULT_VALUE %q", resultValueText)
				}
				resultValue, resultValueSet = uint32(parsed), true
			}
			if observerErr := bus.SetMemoryObserver(uint32(resultAddress), 1, func(access MemoryAccess) {
				if mkdirResultWrite != nil || !access.Context.Attributed || !access.Write ||
					access.Width != Width8 || resultValueSet && access.Value != resultValue ||
					!resultValueSet && access.Value == 0 {
					return
				}
				captured := access
				mkdirResultWrite = &captured
				_ = backend.Stop()
			}); observerErr != nil {
				t.Fatalf("watch guest IFileMgr MkDir result: %v", observerErr)
			}
		}
		mkdirRunner := runner
		guestTraceStopConfigured := false
		if traceStopText := os.Getenv("ARAM_GUEST_TRACE_STOP_PC"); traceStopText != "" {
			address, parseErr := strconv.ParseUint(traceStopText, 0, 32)
			if parseErr != nil || address&1 != 0 {
				t.Fatalf("invalid ARAM_GUEST_TRACE_STOP_PC %q", traceStopText)
			}
			traceCalls := []HLECallProfile{
				{
					ID:       "diagnostic-factory-bootstrap-return",
					Contract: "diagnostic.factory-bootstrap-return",
					Address:  factoryBootstrapReturnAddress,
					Mode:     cpu.ModeThumb,
					Return:   HLEReturnLinkRegister,
				},
				{
					ID:       "diagnostic-guest-trace-stop",
					Contract: "diagnostic.trace-stop",
					Address:  uint32(address),
					Mode:     cpu.ModeThumb,
					Return:   HLEReturnLinkRegister,
				},
			}
			traceRunner, runnerErr := NewHLERunner(bus, backend, traceCalls, handlers)
			if runnerErr != nil {
				t.Fatalf("configure guest IFileMgr trace-stop: %v", runnerErr)
			}
			mkdirRunner = traceRunner
			guestTraceStopConfigured = true
		}
		mkdirResult := mkdirRunner.Run(
			context.Background(), mkdirEntry&^1, cpu.ModeThumb, 100_000_000,
		)
		if os.Getenv("ARAM_GUEST_MKDIR_RESULT_ADDRESS") != "" {
			if observerErr := bus.SetMemoryObserver(0, 0, nil); observerErr != nil {
				t.Fatalf("clear guest IFileMgr MkDir result watch: %v", observerErr)
			}
		}
		if guestTraceStopConfigured {
			if _, restoreErr := NewHLERunner(bus, backend, calls, handlers); restoreErr != nil {
				t.Fatalf("restore firmware execution traps after guest IFileMgr trace-stop: %v", restoreErr)
			}
		}
		mkdirReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		fileManagerError := readWord("IFileMgr last error", fileManager+0x0c)
		const guestErrnoAddress = uint32(0x03fbe5a4)
		guestErrno := readWord("C library errno", guestErrnoAddress)
		if mkdirResultWrite != nil {
			t.Logf("guest IFileMgr MkDir result write: %+v", *mkdirResultWrite)
			logSCHW830TraceState(t, backend, mkdirResult.PC)
			logSCHW830PCHistory(t, backend)
			logSCHW830RequestedMemory(t, backend, "guest IFileMgr MkDir result write")
		}
		if errors.Is(mkdirResult.Err, traceStop) {
			logSCHW830TraceState(t, backend, mkdirResult.PC)
			logSCHW830PCHistory(t, backend)
			logSCHW830RequestedMemory(t, backend, "guest IFileMgr MkDir trace-stop")
		}
		if os.Getenv("ARAM_LOG_GUEST_MKDIR_HISTORY") != "" {
			logSCHW830PCHistory(t, backend)
			logSCHW830RequestedMemory(t, backend, "guest IFileMgr MkDir completion")
		}
		if writeErr := backend.WriteMemory(pathScratch, scratch); writeErr != nil {
			t.Fatalf("restore guest factory path scratch: %v", writeErr)
		}
		for register := uint32(0); register < cpu.RegisterCPSR; register++ {
			if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
				t.Fatalf("restore register %d after guest IFileMgr MkDir: %v", register, writeErr)
			}
		}
		if writeErr := backend.WriteRegister(
			cpu.RegisterCPSR,
			savedRegisters[cpu.RegisterCPSR],
		); writeErr != nil {
			t.Fatal(writeErr)
		}
		t.Logf(
			"guest IFileMgr MkDir(%q) result: %+v return=0x%08x file-error=0x%08x errno=0x%08x dirty=%#v",
			directory,
			mkdirResult,
			mkdirReturn,
			fileManagerError,
			guestErrno,
			flash.DirtyBlocks(),
		)
		if !errors.Is(mkdirResult.Err, factoryBootstrapReturned) {
			t.Fatalf("guest IFileMgr MkDir did not return through its sentinel: %+v", mkdirResult)
		}
		if mkdirReturn != 0 {
			t.Fatalf("guest IFileMgr MkDir(%q) returned failure 0x%08x", directory, mkdirReturn)
		}
	}
	callGuestTouch := func(pathname string) {
		const (
			fileManagerGlobal    = uint32(0x038c3bec)
			openFileVTableOffset = uint32(0x08)
			releaseVTableOffset  = uint32(0x04)
			createFlag           = uint32(4)
			pathScratch          = uint32(0x07ffe000)
			maximumPathBytes     = 0x80
		)
		path := append([]byte(pathname), 0)
		if len(path) > maximumPathBytes {
			t.Fatalf("guest factory file path is too long: %q", pathname)
		}
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before guest IFileMgr OpenFile: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		readWord := func(label string, address uint32) uint32 {
			var encoded [4]byte
			if readErr := backend.ReadMemory(address, encoded[:]); readErr != nil {
				t.Fatalf("read guest %s at 0x%08x: %v", label, address, readErr)
			}
			return binary.LittleEndian.Uint32(encoded[:])
		}
		fileManager := readWord("IFileMgr global", fileManagerGlobal)
		if fileManager == 0 {
			t.Fatal("guest IFileMgr global is null at factory file bootstrap")
		}
		fileManagerVTable := readWord("IFileMgr vtable", fileManager)
		openFileEntry := readWord("IFileMgr OpenFile entry", fileManagerVTable+openFileVTableOffset)
		if openFileEntry&1 == 0 {
			t.Fatalf("guest IFileMgr OpenFile entry is not Thumb code: 0x%08x", openFileEntry)
		}
		scratch := make([]byte, maximumPathBytes)
		if readErr := backend.ReadMemory(pathScratch, scratch); readErr != nil {
			t.Fatalf("preserve guest factory path scratch: %v", readErr)
		}
		if writeErr := backend.WriteMemory(pathScratch, path); writeErr != nil {
			t.Fatalf("write guest factory file path: %v", writeErr)
		}
		// IFileMgr operations can cross into the filesystem task. Preserve the
		// interrupted UI task's exception masks so normal REX scheduling remains
		// available while the request is serviced.
		callStatus := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   fileManager,
			cpu.RegisterR1:   pathScratch,
			cpu.RegisterR2:   createFlag,
			cpu.RegisterR3:   0,
			cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
			cpu.RegisterCPSR: callStatus,
		} {
			if writeErr := backend.WriteRegister(register, value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		openResult := runner.Run(
			context.Background(), openFileEntry&^1, cpu.ModeThumb, 100_000_000,
		)
		openedFile, readErr := backend.ReadRegister(cpu.RegisterR0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !errors.Is(openResult.Err, factoryBootstrapReturned) {
			t.Fatalf("guest IFileMgr OpenFile did not return through its sentinel: %+v", openResult)
		}
		fileManagerError := readWord("IFileMgr last error", fileManager+0x0c)
		if openedFile == 0 {
			t.Fatalf(
				"guest IFileMgr OpenFile(%q, CREATE) returned null: file-error=0x%08x",
				pathname, fileManagerError,
			)
		}
		fileVTable := readWord("IFile vtable", openedFile)
		fileEntries := make([]uint32, 16)
		for index := range fileEntries {
			fileEntries[index] = readWord(
				fmt.Sprintf("IFile vtable entry %d", index),
				fileVTable+uint32(index)*4,
			)
		}
		t.Logf(
			"guest writable IFile object=0x%08x vtable=0x%08x entries=%#v",
			openedFile, fileVTable, fileEntries,
		)
		releaseEntry := readWord("IFile Release entry", fileVTable+releaseVTableOffset)
		if releaseEntry&1 == 0 {
			t.Fatalf("guest IFile Release entry is not Thumb code: 0x%08x", releaseEntry)
		}
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   openedFile,
			cpu.RegisterLR:   factoryBootstrapReturnAddress | 1,
			cpu.RegisterCPSR: callStatus,
		} {
			if writeErr := backend.WriteRegister(register, value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		releaseResult := runner.Run(
			context.Background(), releaseEntry&^1, cpu.ModeThumb, 100_000_000,
		)
		if !errors.Is(releaseResult.Err, factoryBootstrapReturned) {
			t.Fatalf("guest IFile Release did not return through its sentinel: %+v", releaseResult)
		}
		if writeErr := backend.WriteMemory(pathScratch, scratch); writeErr != nil {
			t.Fatalf("restore guest factory path scratch: %v", writeErr)
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
				t.Fatalf("restore register %d after guest IFileMgr OpenFile: %v", register, writeErr)
			}
		}
		t.Logf(
			"guest IFileMgr touch(%q) result: open=%+v release=%+v file-error=0x%08x dirty=%#v",
			pathname, openResult, releaseResult, fileManagerError, flash.DirtyBlocks(),
		)
	}
	callGuestReadFile := func(pathname string) {
		const (
			fileManagerGlobal    = uint32(0x038c3bec)
			openFileVTableOffset = uint32(0x08)
			releaseVTableOffset  = uint32(0x04)
			readVTableOffset     = uint32(0x0c)
			readFlag             = uint32(1)
			pathScratch          = uint32(0x07ffe000)
			dataScratch          = uint32(0x07ffc000)
			maximumPathBytes     = 0x80
			maximumReadBytes     = 0x2000
		)
		path := append([]byte(pathname), 0)
		if len(path) > maximumPathBytes {
			t.Fatalf("guest file path is too long: %q", pathname)
		}
		readLimit := uint32(maximumReadBytes)
		if limitText := os.Getenv("ARAM_GUEST_READ_FILE_LIMIT"); limitText != "" {
			parsed, parseErr := strconv.ParseUint(limitText, 0, 32)
			if parseErr != nil || parsed == 0 || parsed > maximumReadBytes {
				t.Fatalf("invalid ARAM_GUEST_READ_FILE_LIMIT %q", limitText)
			}
			readLimit = uint32(parsed)
		}
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before guest file read: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		readWord := func(label string, address uint32) uint32 {
			var encoded [4]byte
			if readErr := backend.ReadMemory(address, encoded[:]); readErr != nil {
				t.Fatalf("read guest %s at 0x%08x: %v", label, address, readErr)
			}
			return binary.LittleEndian.Uint32(encoded[:])
		}
		fileManager := readWord("IFileMgr global", fileManagerGlobal)
		if fileManager == 0 {
			t.Fatal("guest IFileMgr global is null during file read")
		}
		fileManagerVTable := readWord("IFileMgr vtable", fileManager)
		openFileEntry := readWord("IFileMgr OpenFile entry", fileManagerVTable+openFileVTableOffset)
		if openFileEntry&1 == 0 {
			t.Fatalf("guest IFileMgr OpenFile entry is not Thumb code: 0x%08x", openFileEntry)
		}
		pathBefore := make([]byte, maximumPathBytes)
		dataBefore := make([]byte, maximumReadBytes)
		if readErr := backend.ReadMemory(pathScratch, pathBefore); readErr != nil {
			t.Fatalf("preserve guest file path scratch: %v", readErr)
		}
		if readErr := backend.ReadMemory(dataScratch, dataBefore); readErr != nil {
			t.Fatalf("preserve guest file data scratch: %v", readErr)
		}
		if writeErr := backend.WriteMemory(pathScratch, path); writeErr != nil {
			t.Fatalf("write guest file path: %v", writeErr)
		}
		callStatus := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb
		call := func(entry uint32, arguments [4]uint32) (cpu.Result, uint32) {
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: arguments[0], cpu.RegisterR1: arguments[1],
				cpu.RegisterR2: arguments[2], cpu.RegisterR3: arguments[3],
				cpu.RegisterLR: factoryBootstrapReturnAddress | 1, cpu.RegisterCPSR: callStatus,
			} {
				if writeErr := backend.WriteRegister(register, value); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			callResult := runner.Run(context.Background(), entry&^1, cpu.ModeThumb, 100_000_000)
			callReturn, readErr := backend.ReadRegister(cpu.RegisterR0)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !errors.Is(callResult.Err, factoryBootstrapReturned) {
				t.Fatalf("guest file call 0x%08x did not return through its sentinel: %+v", entry, callResult)
			}
			return callResult, callReturn
		}
		openResult, openedFile := call(openFileEntry, [4]uint32{
			fileManager, pathScratch, readFlag, 0,
		})
		fileManagerError := readWord("IFileMgr last error", fileManager+0x0c)
		if openedFile == 0 {
			t.Fatalf(
				"guest IFileMgr OpenFile(%q, READ) returned null: file-error=0x%08x",
				pathname, fileManagerError,
			)
		}
		fileVTable := readWord("IFile vtable", openedFile)
		fileEntries := make([]uint32, 16)
		for index := range fileEntries {
			fileEntries[index] = readWord(
				fmt.Sprintf("IFile vtable entry %d", index),
				fileVTable+uint32(index)*4,
			)
		}
		t.Logf(
			"guest IFile object=0x%08x vtable=0x%08x entries=%#v",
			openedFile, fileVTable, fileEntries,
		)
		readEntry := readWord("IFile Read entry", fileVTable+readVTableOffset)
		releaseEntry := readWord("IFile Release entry", fileVTable+releaseVTableOffset)
		if readEntry&1 == 0 || releaseEntry&1 == 0 {
			t.Fatalf(
				"guest IFile entries are not Thumb code: read=0x%08x release=0x%08x",
				readEntry, releaseEntry,
			)
		}
		readResult, readCount := call(readEntry, [4]uint32{
			openedFile, dataScratch, readLimit, 0,
		})
		if readCount > readLimit {
			t.Fatalf("guest IFile Read(%q) returned invalid length 0x%08x", pathname, readCount)
		}
		contents := make([]byte, readCount)
		if readErr := backend.ReadMemory(dataScratch, contents); readErr != nil {
			t.Fatalf("read guest file result: %v", readErr)
		}
		releaseResult, _ := call(releaseEntry, [4]uint32{openedFile, 0, 0, 0})
		if dumpPath := os.Getenv("ARAM_GUEST_READ_FILE_DUMP"); dumpPath != "" {
			if writeErr := os.WriteFile(dumpPath, contents, 0o600); writeErr != nil {
				t.Fatalf("write guest file dump: %v", writeErr)
			}
		}
		if writeErr := backend.WriteMemory(pathScratch, pathBefore); writeErr != nil {
			t.Fatalf("restore guest file path scratch: %v", writeErr)
		}
		if writeErr := backend.WriteMemory(dataScratch, dataBefore); writeErr != nil {
			t.Fatalf("restore guest file data scratch: %v", writeErr)
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if writeErr := backend.WriteRegister(register, savedRegisters[register]); writeErr != nil {
				t.Fatalf("restore register %d after guest file read: %v", register, writeErr)
			}
		}
		t.Logf(
			"guest IFileMgr read(%q) result: open=%+v read=%+v release=%+v length=%d sha256=%x file-error=0x%08x first=%x",
			pathname, openResult, readResult, releaseResult, len(contents), sha256.Sum256(contents),
			fileManagerError, contents[:min(len(contents), 64)],
		)
	}
	var executionRunner ExecutionRunner = runner
	if len(clockedDevices) != 0 {
		executionRunner, err = NewClockedRunner(
			backend, runner, DefaultClockedRunnerQuantum, clockedDevices...,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Most task-backed guest services need the normal clocked runner so their
	// worker tasks can make progress.  A synchronous DIAG handler is different:
	// when a diagnostic probe temporarily supplies the DIAG REX identity, a
	// clock interrupt would let the scheduler observe a deliberately synthetic
	// current-task/CPU-stack pairing.  Keep an explicit diagnostic-only escape
	// hatch for studying those handlers without changing normal firmware runs.
	if os.Getenv("ARAM_CALL_GUEST_UNCLOCKED") == "" {
		diagnosticExecutionRunner = executionRunner
	}
	qcsblBoundaryBudget := uint64(1_195_629)
	if budgetText := os.Getenv("ARAM_QCSBL_BOUNDARY_BUDGET"); budgetText != "" {
		parsed, parseErr := strconv.ParseUint(budgetText, 0, 64)
		if parseErr != nil || parsed == 0 {
			t.Fatalf("invalid ARAM_QCSBL_BOUNDARY_BUDGET %q", budgetText)
		}
		qcsblBoundaryBudget = parsed
	}
	result := executionRunner.Run(context.Background(), handoff.Entry, handoff.Mode, qcsblBoundaryBudget)
	if traceWrite != nil {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830LowWriteContext(t, traceContextLowWriteWindow)
		logSCHW830PanelSourceTrace(t, panelSourceTrace)
		logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
		logSCHW830InstructionCache(t, backend)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		return
	}
	if traceExecution != nil {
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830PanelSourceTrace(t, panelSourceTrace)
		logSCHW830TraceExecution(t, backend, traceExecutionHistory, result.PC)
		logSCHW830MemoryWordMatches(t, bus)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		return
	}
	if errors.Is(result.Err, traceStop) {
		t.Logf(
			"QCSBL diagnostic trace stop at pc=0x%08x after %d instructions",
			result.PC,
			result.Instructions,
		)
		return
	}
	if qcsblBoundaryBudget != 1_195_629 && errors.Is(result.Err, flashInitFailure) {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830NANDCommandTrace(t, nandCommandTrace)
		t.Logf(
			"adjacent-board diagnostic reached OEM flash initialization failure at pc=0x%08x after %d instructions",
			result.PC,
			result.Instructions,
		)
		return
	}
	if qcsblBoundaryBudget != 1_195_629 && profile.Model != "SCH-W830" {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830NANDCommandTrace(t, nandCommandTrace)
		if result.Err != nil {
			t.Logf(
				"adjacent-board diagnostic stopped at pc=0x%08x after %d instructions: %v",
				result.PC,
				result.Instructions,
				result.Err,
			)
			return
		}
		if result.Reason == cpu.StopBudget && result.Instructions == qcsblBoundaryBudget {
			t.Logf(
				"adjacent-board diagnostic reached the configured budget at pc=0x%08x after %d instructions",
				result.PC,
				result.Instructions,
			)
			return
		}
		t.Fatalf("unexpected adjacent-board diagnostic result: %+v", result)
	}
	if qcsblBoundaryBudget != 1_195_629 {
		t.Fatalf("custom QCSBL budget expired before a configured trace stop: %+v", result)
	}
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != qcsblBoundaryBudget || result.PC != 0x000a07d8 {
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
	firmwareWarmupBudget := uint64(552_000_000)
	if budgetText := os.Getenv("ARAM_FACTORY_CALL_AFTER_INSTRUCTIONS"); budgetText != "" {
		parsed, parseErr := strconv.ParseUint(budgetText, 0, 64)
		if parseErr != nil || parsed == 0 {
			t.Fatalf("invalid ARAM_FACTORY_CALL_AFTER_INSTRUCTIONS %q", budgetText)
		}
		firmwareWarmupBudget = parsed
	}
	runtimeLoadPrefix := os.Getenv("ARAM_LOAD_RUNTIME_PREFIX")
	if runtimeLoadPrefix != "" {
		meta, readErr := os.ReadFile(runtimeLoadPrefix + ".meta")
		if readErr != nil {
			t.Fatalf("read runtime snapshot metadata: %v", readErr)
		}
		if len(meta) != 24 || string(meta[:4]) != "SWRT" ||
			binary.LittleEndian.Uint32(meta[4:8]) != 1 ||
			!cpu.Mode(binary.LittleEndian.Uint32(meta[12:16])).Valid() ||
			binary.LittleEndian.Uint64(meta[16:24]) == 0 {
			t.Fatal("invalid runtime snapshot metadata")
		}
		flashState, readErr := os.ReadFile(runtimeLoadPrefix + ".flash")
		if readErr != nil {
			t.Fatalf("read runtime flash state: %v", readErr)
		}
		if loadErr := flash.LoadState(flashState); loadErr != nil {
			t.Fatalf("load runtime flash state: %v", loadErr)
		}
		busState, readErr := os.ReadFile(runtimeLoadPrefix + ".bus")
		if readErr != nil {
			t.Fatalf("read runtime bus state: %v", readErr)
		}
		if loadErr := bus.LoadStateSubset(busState); loadErr != nil {
			t.Fatalf("load runtime bus state: %v", loadErr)
		}
		cpuState, readErr := os.ReadFile(runtimeLoadPrefix + ".cpu")
		if readErr != nil {
			t.Fatalf("read runtime CPU state: %v", readErr)
		}
		if loadErr := backend.RestoreContext(cpuState); loadErr != nil {
			t.Fatalf("load runtime CPU state: %v", loadErr)
		}
		applySCHW830LiveBootStatusOverride(t, bootControl, "ARAM_BOOT_STATUS_048C", 0x048c)
		applySCHW830LiveBootStatusOverride(t, bootControl, "ARAM_BOOT_STATUS_0C34", 0x0c34)
		if os.Getenv("ARAM_REPLAY_MDP_FRAME") != "" {
			if board.MDP == nil || mdpEngine == nil {
				t.Fatal("runtime MDP replay requested without an MDP profile")
			}
			pointer, readErr := bootControl.Read(board.MDP.ScriptPointerOffset, Width32)
			if readErr != nil {
				t.Fatalf("read runtime MDP script pointer: %v", readErr)
			}
			if queueErr := mdpEngine.QueueScript(pointer); queueErr != nil {
				t.Fatalf("queue runtime MDP script: %v", queueErr)
			}
			if advanceErr := mdpEngine.Advance(0); advanceErr != nil {
				t.Fatalf("replay runtime MDP script: %v", advanceErr)
			}
			t.Logf("replayed runtime MDP script at 0x%08x", pointer)
		}
		result = cpu.Result{
			Reason:       cpu.StopBudget,
			Instructions: binary.LittleEndian.Uint64(meta[16:24]),
			PC:           binary.LittleEndian.Uint32(meta[8:12]),
		}
		t.Logf(
			"loaded runtime snapshot from %s at pc=0x%08x after %d instructions",
			runtimeLoadPrefix,
			result.PC,
			result.Instructions,
		)
	} else {
		result = executionRunner.Run(context.Background(), result.PC, cpu.ModeARM, firmwareWarmupBudget)
	}
	if runtimeLoadPrefix == "" && errors.Is(result.Err, factoryTaskArgumentInjected) {
		callsWithoutFactoryArgument := make([]HLECallProfile, 0, len(calls)-1)
		for _, call := range calls {
			if call.Contract != "diagnostic.factory-task-argument" {
				callsWithoutFactoryArgument = append(callsWithoutFactoryArgument, call)
			}
		}
		postInjectionRunner, runnerErr := NewHLERunner(
			bus, backend, callsWithoutFactoryArgument, handlers,
		)
		if runnerErr != nil {
			t.Fatalf("remove factory-task argument trap: %v", runnerErr)
		}
		executionRunner = postInjectionRunner
		if len(clockedDevices) != 0 {
			executionRunner, err = NewClockedRunner(
				backend, postInjectionRunner, DefaultClockedRunnerQuantum, clockedDevices...,
			)
			if err != nil {
				t.Fatalf("resume after factory-task argument injection: %v", err)
			}
		}
		remainingBudget := firmwareWarmupBudget - result.Instructions
		injectionInstructions := result.Instructions
		result = executionRunner.Run(context.Background(), result.PC, cpu.ModeThumb, remainingBudget)
		result.Instructions += injectionInstructions
	}
	expectedWarmupInstructions := result.Instructions
	saveRuntimeSnapshot := func(runtimeSavePrefix string, snapshotResult cpu.Result) {
		if runtimeSavePrefix == "" ||
			(snapshotResult.Err != nil && !errors.Is(snapshotResult.Err, traceStop)) ||
			(snapshotResult.Err == nil && snapshotResult.Reason != cpu.StopBudget) {
			return
		}
		status, statusErr := backend.ReadRegister(cpu.RegisterCPSR)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		mode := cpu.ModeARM
		if status&cpu.StatusThumb != 0 {
			mode = cpu.ModeThumb
		}
		meta := make([]byte, 24)
		copy(meta, "SWRT")
		binary.LittleEndian.PutUint32(meta[4:8], 1)
		binary.LittleEndian.PutUint32(meta[8:12], snapshotResult.PC)
		binary.LittleEndian.PutUint32(meta[12:16], uint32(mode))
		binary.LittleEndian.PutUint64(meta[16:24], snapshotResult.Instructions)
		cpuState, saveErr := backend.SaveContext()
		if saveErr != nil {
			t.Fatalf("save runtime CPU state: %v", saveErr)
		}
		busState, saveErr := bus.SaveState()
		if saveErr != nil {
			t.Fatalf("save runtime bus state: %v", saveErr)
		}
		flashState, saveErr := flash.SaveState()
		if saveErr != nil {
			t.Fatalf("save runtime flash state: %v", saveErr)
		}
		if saveErr := os.MkdirAll(filepath.Dir(runtimeSavePrefix), 0o755); saveErr != nil {
			t.Fatalf("create runtime snapshot directory: %v", saveErr)
		}
		for suffix, state := range map[string][]byte{
			".meta":  meta,
			".cpu":   cpuState,
			".bus":   busState,
			".flash": flashState,
		} {
			if writeErr := os.WriteFile(runtimeSavePrefix+suffix, state, 0o600); writeErr != nil {
				t.Fatalf("write runtime snapshot %s: %v", suffix, writeErr)
			}
		}
		t.Logf("saved runtime snapshot to %s.{meta,cpu,bus,flash}", runtimeSavePrefix)
	}
	saveMediaSnapshot := func(mediaSavePrefix string, snapshotResult cpu.Result) {
		if mediaSavePrefix == "" {
			return
		}
		if snapshotResult.Err != nil && !errors.Is(snapshotResult.Err, traceStop) {
			t.Fatalf("refuse to save NAND media after execution error: %v", snapshotResult.Err)
		}
		if snapshotResult.Err == nil && snapshotResult.Reason != cpu.StopBudget {
			t.Fatalf("refuse to save NAND media after unexpected stop: %+v", snapshotResult)
		}
		flashState, saveErr := flash.SaveState()
		if saveErr != nil {
			t.Fatalf("save persistent flash state: %v", saveErr)
		}
		nandState, saveErr := nand.SaveState()
		if saveErr != nil {
			t.Fatalf("save persistent NAND spare state: %v", saveErr)
		}
		if saveErr := os.MkdirAll(filepath.Dir(mediaSavePrefix), 0o755); saveErr != nil {
			t.Fatalf("create persistent media directory: %v", saveErr)
		}
		for suffix, state := range map[string][]byte{
			".flash": flashState,
			".nand":  nandState,
		} {
			if writeErr := os.WriteFile(mediaSavePrefix+suffix, state, 0o600); writeErr != nil {
				t.Fatalf("write persistent media %s: %v", suffix, writeErr)
			}
		}
		t.Logf("saved persistent NAND media to %s.{flash,nand}", mediaSavePrefix)
	}
	saveRuntimeSnapshot(os.Getenv("ARAM_SAVE_RUNTIME_PREFIX"), result)
	factoryOperationRequested := os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") != "" ||
		os.Getenv("ARAM_CALL_GUEST_RFS_FORMAT") != "" ||
		os.Getenv("ARAM_GUEST_FACTORY_MKDIR") != "" ||
		os.Getenv("ARAM_GUEST_FACTORY_TOUCH") != "" ||
		os.Getenv("ARAM_GUEST_READ_FILE") != ""
	if traceWrite != nil && !factoryOperationRequested {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
		logSCHW830InstructionCache(t, backend)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		return
	}
	if traceExecution != nil {
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830TraceExecution(t, backend, traceExecutionHistory, result.PC)
		logSCHW830RequestedMemory(t, backend, "trace-execution requested")
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		return
	}
	factoryTraceBoundary := (errors.Is(result.Err, traceStop) || traceWrite != nil) &&
		factoryOperationRequested
	if errors.Is(result.Err, traceStop) && !factoryTraceBoundary {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830PanelSourceTrace(t, panelSourceTrace)
		logSCHW830PCHistory(t, backend)
		logSCHW830PCHitRange(t, backend)
		logSCHW830RequestedMemory(t, backend, "trace-stop requested")
		logSCHW830MemoryWordMatches(t, bus)
		logSCHW830MemoryByteMatches(t, bus)
		logSCHW830PhysicalCode(t, bus)
		logSCHW830TraceState(t, backend, result.PC)
		logSCHW830REXTasks(t, backend)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		if dumpPath := os.Getenv("ARAM_DUMP_EBI_RAM"); dumpPath != "" {
			dumpSCHW830EBIRAM(t, bus, dumpPath)
		}
		return
	}
	if mmioTrace != nil && os.Getenv("ARAM_RESET_MMIO_TRACE_AFTER_WARMUP") != "" {
		mmioTrace.Reset()
		t.Log("reset SCH-W830 MMIO trace after scheduler warmup")
	}
	if nandCommandTrace != nil {
		nandCommandTrace.Reset()
	}
	var warmupRAMPages map[uint32][sha256.Size]byte
	if os.Getenv("ARAM_TRACE_RAM_PAGES") != "" {
		warmupRAMPages = snapshotSCHW830RAMPages(bus)
		t.Logf("snapshotted %d SCH-W830 RAM pages after scheduler warmup", len(warmupRAMPages))
	}
	if device := os.Getenv("ARAM_CALL_GUEST_RFS_FORMAT"); device != "" &&
		os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") == "" &&
		((result.Err == nil && result.Reason == cpu.StopBudget) || factoryTraceBoundary) {
		callGuestRFSFormat(device)
		if mediaSavePrefix := os.Getenv("ARAM_SAVE_FACTORY_MEDIA_PREFIX"); mediaSavePrefix != "" {
			flashState, saveErr := flash.SaveState()
			if saveErr != nil {
				t.Fatalf("save RFS-formatted flash state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".flash", flashState, 0o600); writeErr != nil {
				t.Fatalf("write RFS-formatted flash state: %v", writeErr)
			}
			nandState, saveErr := nand.SaveState()
			if saveErr != nil {
				t.Fatalf("save RFS-formatted NAND state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".nand", nandState, 0o600); writeErr != nil {
				t.Fatalf("write RFS-formatted NAND state: %v", writeErr)
			}
			t.Logf(
				"saved guest RFS-formatted NAND media to %s.flash and %s.nand",
				mediaSavePrefix,
				mediaSavePrefix,
			)
		}
		if os.Getenv("ARAM_STOP_AFTER_GUEST_RFS_FORMAT") != "" {
			return
		}
	}
	if directory := os.Getenv("ARAM_GUEST_FACTORY_MKDIR"); directory != "" && os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") == "" &&
		((result.Err == nil && result.Reason == cpu.StopBudget) || factoryTraceBoundary) {
		callGuestMkdir(directory)
		if mediaSavePrefix := os.Getenv("ARAM_SAVE_FACTORY_MEDIA_PREFIX"); mediaSavePrefix != "" {
			flashState, saveErr := flash.SaveState()
			if saveErr != nil {
				t.Fatalf("save provisioned flash state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".flash", flashState, 0o600); writeErr != nil {
				t.Fatalf("write provisioned flash state: %v", writeErr)
			}
			nandState, saveErr := nand.SaveState()
			if saveErr != nil {
				t.Fatalf("save provisioned NAND state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".nand", nandState, 0o600); writeErr != nil {
				t.Fatalf("write provisioned NAND state: %v", writeErr)
			}
			t.Logf(
				"saved guest-provisioned NAND media to %s.flash and %s.nand",
				mediaSavePrefix,
				mediaSavePrefix,
			)
		}
		if os.Getenv("ARAM_STOP_AFTER_GUEST_FACTORY_MKDIR") != "" {
			return
		}
	}
	if pathList := os.Getenv("ARAM_GUEST_FACTORY_TOUCH"); pathList != "" && os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") == "" &&
		((result.Err == nil && result.Reason == cpu.StopBudget) || factoryTraceBoundary) {
		for _, pathname := range strings.Split(pathList, ";") {
			pathname = strings.TrimSpace(pathname)
			if pathname == "" {
				t.Fatalf("invalid empty path in ARAM_GUEST_FACTORY_TOUCH %q", pathList)
			}
			callGuestTouch(pathname)
		}
		if mediaSavePrefix := os.Getenv("ARAM_SAVE_FACTORY_MEDIA_PREFIX"); mediaSavePrefix != "" {
			flashState, saveErr := flash.SaveState()
			if saveErr != nil {
				t.Fatalf("save touched flash state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".flash", flashState, 0o600); writeErr != nil {
				t.Fatalf("write touched flash state: %v", writeErr)
			}
			nandState, saveErr := nand.SaveState()
			if saveErr != nil {
				t.Fatalf("save touched NAND state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".nand", nandState, 0o600); writeErr != nil {
				t.Fatalf("write touched NAND state: %v", writeErr)
			}
			t.Logf(
				"saved guest-touched NAND media to %s.flash and %s.nand",
				mediaSavePrefix, mediaSavePrefix,
			)
		}
		if os.Getenv("ARAM_STOP_AFTER_GUEST_FACTORY_TOUCH") != "" {
			return
		}
	}
	if pathname := strings.TrimSpace(os.Getenv("ARAM_GUEST_READ_FILE")); pathname != "" &&
		os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") == "" &&
		((result.Err == nil && result.Reason == cpu.StopBudget) || factoryTraceBoundary) {
		callGuestReadFile(pathname)
	}
	if os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") != "" &&
		((result.Err == nil && result.Reason == cpu.StopBudget) || factoryTraceBoundary) {
		factoryFormatName := "combined"
		factoryFormatEntry := uint32(0x020b0e60)
		factoryFollowupEntry := uint32(0)
		switch formatMode := os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT"); formatMode {
		case "1", "all":
		case "provision":
			factoryFormatName = "combined plus TFS4 removable-volume"
			factoryFollowupEntry = 0x020b0fde
		case "bml":
			factoryFormatName = "BML"
			factoryFormatEntry = 0x00517e16
		case "stl":
			factoryFormatName = "STL"
			factoryFormatEntry = 0x00517e9a
		case "tfs", "user":
			// The normal TFS4 task assumes the removable FAT volumes were
			// provisioned by the handset factory.  Download packages do not
			// contain that persistent state, so exercise the firmware's own
			// /dev/nfa0 and /dev/nfb0 initialization routine once the VFS device
			// layer has started.  This is intentionally separate from BREW's
			// Qualcomm EFS namespace.
			factoryFormatName = "TFS4 removable-volume"
			factoryFormatEntry = 0x020b0fde
		default:
			t.Fatalf("invalid ARAM_CALL_GUEST_FACTORY_FORMAT %q", formatMode)
		}
		var savedRegisters [cpu.RegisterCPSR + 1]uint32
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			value, readErr := backend.ReadRegister(register)
			if readErr != nil {
				t.Fatalf("save register %d before guest factory bootstrap: %v", register, readErr)
			}
			savedRegisters[register] = value
		}
		// Preserve the scheduler's current privileged bank and stack, but mask
		// asynchronous exceptions while the synchronous firmware helper runs.
		factoryStatus := savedRegisters[cpu.RegisterCPSR] | cpu.StatusThumb | 1<<7 | 1<<6
		if err := backend.WriteRegister(cpu.RegisterCPSR, factoryStatus); err != nil {
			t.Fatal(err)
		}
		if err := backend.WriteRegister(
			cpu.RegisterLR,
			factoryBootstrapReturnAddress|1,
		); err != nil {
			t.Fatal(err)
		}
		factoryResult := runner.Run(
			context.Background(), factoryFormatEntry, cpu.ModeThumb, 250_000_000,
		)
		factoryReturn, returnErr := backend.ReadRegister(cpu.RegisterR0)
		if returnErr != nil {
			t.Fatal(returnErr)
		}
		t.Logf(
			"guest %s factory bootstrap result: %+v return=0x%08x dirty=%#v",
			factoryFormatName,
			factoryResult,
			factoryReturn,
			flash.DirtyBlocks(),
		)
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830NANDCommandTrace(t, nandCommandTrace)
		logSCHW830RequestedMemory(t, backend, "guest factory bootstrap requested")
		logSCHW830PhysicalCode(t, bus)
		if traceWrite != nil {
			logSCHW830TraceWrite(t, backend, traceWriteHistory, factoryResult.PC)
			return
		}
		if traceExecution != nil {
			logSCHW830TraceExecution(t, backend, traceExecutionHistory, factoryResult.PC)
			return
		}
		if os.Getenv("ARAM_LOG_GUEST_FACTORY_HISTORY") != "" {
			logSCHW830PCHistory(t, backend)
		}
		if errors.Is(factoryResult.Err, traceStop) {
			logSCHW830TraceState(t, backend, factoryResult.PC)
			logSCHW830RequestedMemory(t, backend, "factory trace-stop requested")
			return
		}
		if !errors.Is(factoryResult.Err, factoryBootstrapReturned) {
			t.Fatalf("guest factory bootstrap did not return through its sentinel: %+v", factoryResult)
		}
		if factoryReturn != 0 {
			t.Fatalf("guest factory bootstrap returned failure 0x%08x", factoryReturn)
		}
		if factoryFollowupEntry != 0 {
			if err := backend.WriteRegister(
				cpu.RegisterLR,
				factoryBootstrapReturnAddress|1,
			); err != nil {
				t.Fatal(err)
			}
			followupResult := runner.Run(
				context.Background(), factoryFollowupEntry, cpu.ModeThumb, 250_000_000,
			)
			followupReturn, returnErr := backend.ReadRegister(cpu.RegisterR0)
			if returnErr != nil {
				t.Fatal(returnErr)
			}
			t.Logf(
				"guest TFS4 removable-volume follow-up result: %+v return=0x%08x dirty=%#v",
				followupResult,
				followupReturn,
				flash.DirtyBlocks(),
			)
			if !errors.Is(followupResult.Err, factoryBootstrapReturned) {
				t.Fatalf(
					"guest TFS4 removable-volume follow-up did not return through its sentinel: %+v",
					followupResult,
				)
			}
		}
		if device := os.Getenv("ARAM_CALL_GUEST_RFS_FORMAT"); device != "" {
			callGuestRFSFormat(device)
		}
		if directory := os.Getenv("ARAM_GUEST_FACTORY_MKDIR"); directory != "" {
			callGuestMkdir(directory)
		}
		for register := uint32(0); register < cpu.RegisterCPSR; register++ {
			if err := backend.WriteRegister(register, savedRegisters[register]); err != nil {
				t.Fatalf("restore register %d after guest factory bootstrap: %v", register, err)
			}
		}
		if err := backend.WriteRegister(cpu.RegisterCPSR, savedRegisters[cpu.RegisterCPSR]); err != nil {
			t.Fatal(err)
		}
		if mediaSavePrefix := os.Getenv("ARAM_SAVE_FACTORY_MEDIA_PREFIX"); mediaSavePrefix != "" {
			flashState, saveErr := flash.SaveState()
			if saveErr != nil {
				t.Fatalf("save formatted flash state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".flash", flashState, 0o600); writeErr != nil {
				t.Fatalf("write formatted flash state: %v", writeErr)
			}
			nandState, saveErr := nand.SaveState()
			if saveErr != nil {
				t.Fatalf("save formatted NAND state: %v", saveErr)
			}
			if writeErr := os.WriteFile(mediaSavePrefix+".nand", nandState, 0o600); writeErr != nil {
				t.Fatalf("write formatted NAND state: %v", writeErr)
			}
			t.Logf(
				"saved guest-formatted NAND media to %s.flash and %s.nand",
				mediaSavePrefix,
				mediaSavePrefix,
			)
		}
		if os.Getenv("ARAM_STOP_AFTER_GUEST_FACTORY_FORMAT") != "" {
			return
		}
		if os.Getenv("ARAM_RESUME_AFTER_GUEST_FACTORY_FORMAT") != "" {
			postFactoryTraceStopText := os.Getenv("ARAM_POST_FACTORY_TRACE_STOP_PC")
			postFactoryARMTraceStopText := os.Getenv("ARAM_POST_FACTORY_TRACE_STOP_ARM_PC")
			postFactoryCalls := make([]HLECallProfile, 0, 2)
			if postFactoryTraceStopText != "" {
				address, parseErr := strconv.ParseUint(postFactoryTraceStopText, 0, 32)
				if parseErr != nil || address&1 != 0 {
					t.Fatalf(
						"invalid ARAM_POST_FACTORY_TRACE_STOP_PC %q",
						postFactoryTraceStopText,
					)
				}
				postFactoryCalls = append(postFactoryCalls, HLECallProfile{
					ID:       "diagnostic-post-factory-trace-stop",
					Contract: "diagnostic.trace-stop",
					Address:  uint32(address),
					Mode:     cpu.ModeThumb,
					Return:   HLEReturnLinkRegister,
				})
				t.Logf(
					"armed post-factory Thumb trace stop at 0x%08x",
					uint32(address),
				)
			}
			if postFactoryARMTraceStopText != "" {
				address, parseErr := strconv.ParseUint(postFactoryARMTraceStopText, 0, 32)
				if parseErr != nil || address&3 != 0 {
					t.Fatalf(
						"invalid ARAM_POST_FACTORY_TRACE_STOP_ARM_PC %q",
						postFactoryARMTraceStopText,
					)
				}
				postFactoryCalls = append(postFactoryCalls, HLECallProfile{
					ID:       "diagnostic-post-factory-arm-trace-stop",
					Contract: "diagnostic.trace-stop",
					Address:  uint32(address),
					Mode:     cpu.ModeARM,
					Return:   HLEReturnLinkRegister,
				})
				t.Logf(
					"armed post-factory ARM trace stop at 0x%08x",
					uint32(address),
				)
			}
			if len(postFactoryCalls) == 0 {
				if err := backend.SetExecutionTraps(nil); err != nil {
					t.Fatalf("clear post-factory diagnostic traps: %v", err)
				}
				executionRunner = backend
			} else {
				postFactoryRunner, runnerErr := NewHLERunner(
					bus,
					backend,
					postFactoryCalls,
					handlers,
				)
				if runnerErr != nil {
					t.Fatalf("build post-factory diagnostic runner: %v", runnerErr)
				}
				executionRunner = postFactoryRunner
			}
			if len(clockedDevices) != 0 {
				executionRunner, err = NewClockedRunner(
					backend, executionRunner, DefaultClockedRunnerQuantum, clockedDevices...,
				)
				if err != nil {
					t.Fatalf("build post-factory clocked runner: %v", err)
				}
			}
			expectedWarmupInstructions = result.Instructions
			result.Err = nil
			result.Reason = cpu.StopBudget
			t.Logf(
				"resuming original firmware at 0x%08x after guest factory bootstrap",
				result.PC,
			)
		}
	}
	pcBaseline := backend.PCHits()
	if result.Err == nil && result.Reason == cpu.StopBudget {
		postWarmupRemaining := postWarmupBudget
		var keypadRelease func() error
		keypadPressBudget := uint64(0)
		keypadReleaseBudget := uint64(0)
		var keypadSequence []string
		signalSpacing := uint64(0)
		if os.Getenv("ARAM_INJECT_AT_TRACE_STOP") != "" {
			if os.Getenv("ARAM_TRACE_STOP_PC") == "" && os.Getenv("ARAM_TRACE_STOP_ARM_PC") == "" {
				t.Fatal("ARAM_INJECT_AT_TRACE_STOP requires a trace-stop PC")
			}
			status, statusErr := backend.ReadRegister(cpu.RegisterCPSR)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			mode := cpu.ModeARM
			if status&cpu.StatusThumb != 0 {
				mode = cpu.ModeThumb
			}
			completedInstructions := result.Instructions
			boundaryResult := executionRunner.Run(
				context.Background(), result.PC, mode, postWarmupRemaining,
			)
			if !errors.Is(boundaryResult.Err, traceStop) {
				t.Fatalf("firmware did not reach the requested injection trace stop: %+v", boundaryResult)
			}
			if boundaryResult.Instructions > postWarmupRemaining {
				t.Fatalf(
					"injection trace stop retired %d instructions with only %d remaining",
					boundaryResult.Instructions, postWarmupRemaining,
				)
			}
			postWarmupRemaining -= boundaryResult.Instructions
			boundaryResult.Instructions += completedInstructions
			result = boundaryResult

			callsWithoutTraceStop := make([]HLECallProfile, 0, len(calls))
			for _, call := range calls {
				if call.Contract != "diagnostic.trace-stop" {
					callsWithoutTraceStop = append(callsWithoutTraceStop, call)
				}
			}
			oneShotRunner, runnerErr := NewHLERunner(
				bus, backend, callsWithoutTraceStop, handlers,
			)
			if runnerErr != nil {
				t.Fatalf("remove one-shot injection trace stop: %v", runnerErr)
			}
			runner = oneShotRunner
			executionRunner = runner
			if len(clockedDevices) != 0 {
				executionRunner, err = NewClockedRunner(
					backend, runner, DefaultClockedRunnerQuantum, clockedDevices...,
				)
				if err != nil {
					t.Fatalf("resume after one-shot injection trace stop: %v", err)
				}
			}
			result.Err = nil
			result.Reason = cpu.StopBudget
			t.Logf(
				"reached one-shot injection trace stop at 0x%08x after %d instructions",
				result.PC, result.Instructions,
			)
		}
		if spacingText := os.Getenv("ARAM_GUEST_SIGNAL_SPACING"); spacingText != "" {
			parsed, parseErr := strconv.ParseUint(spacingText, 0, 64)
			if parseErr != nil || parsed == 0 || parsed >= postWarmupBudget {
				t.Fatalf("invalid ARAM_GUEST_SIGNAL_SPACING %q", spacingText)
			}
			signalSpacing = parsed
		}
		runSignalSpacing := func(label string) {
			if signalSpacing == 0 {
				return
			}
			if signalSpacing > postWarmupRemaining {
				t.Fatalf(
					"%s signal spacing %d exceeds remaining post-warmup budget %d",
					label, signalSpacing, postWarmupRemaining,
				)
			}
			status, statusErr := backend.ReadRegister(cpu.RegisterCPSR)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			mode := cpu.ModeARM
			if status&cpu.StatusThumb != 0 {
				mode = cpu.ModeThumb
			}
			completedInstructions := result.Instructions
			result = executionRunner.Run(
				context.Background(), result.PC, mode, signalSpacing,
			)
			result.Instructions += completedInstructions
			postWarmupRemaining -= signalSpacing
			if result.Err != nil || result.Reason != cpu.StopBudget {
				t.Fatalf("firmware stopped during %s signal spacing: %+v", label, result)
			}
		}
		irqEnable0, irq0Err := interruptController.Read(qualcommIRQEnable0Offset, Width32)
		irqEnable1, irq1Err := interruptController.Read(qualcommIRQEnable1Offset, Width32)
		fiqEnable0, fiq0Err := interruptController.Read(qualcommFIQEnable0Offset, Width32)
		fiqEnable1, fiq1Err := interruptController.Read(qualcommFIQEnable1Offset, Width32)
		vicEnable0, vic0Err := vectoredInterruptController.Read(qualcommVICEnable0Offset, Width32)
		vicEnable1, vic1Err := vectoredInterruptController.Read(qualcommVICEnable1Offset, Width32)
		if irq0Err != nil || irq1Err != nil || fiq0Err != nil || fiq1Err != nil {
			t.Fatalf("read interrupt enables: %v %v %v %v", irq0Err, irq1Err, fiq0Err, fiq1Err)
		}
		if vic0Err != nil || vic1Err != nil {
			t.Fatalf("read vectored interrupt enables: %v %v", vic0Err, vic1Err)
		}
		t.Logf(
			"interrupt enables after warmup: legacy IRQ=%08x/%08x FIQ=%08x/%08x VIC=%08x/%08x",
			irqEnable0, irqEnable1, fiqEnable0, fiqEnable1, vicEnable0, vicEnable1,
		)
		if objectText := os.Getenv("ARAM_CALL_GUEST_ENTER_IDLE_NOSIM"); objectText != "" {
			object, parseErr := strconv.ParseUint(objectText, 0, 32)
			if parseErr != nil || object == 0 || object&3 != 0 {
				t.Fatalf("invalid ARAM_CALL_GUEST_ENTER_IDLE_NOSIM %q", objectText)
			}
			callGuestEnterIdleNoSIM(uint32(object))
		}
		if writeText := strings.TrimSpace(os.Getenv("ARAM_DIAGNOSTIC_WRITE_U32")); writeText != "" {
			parts := strings.FieldsFunc(writeText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			if len(parts) == 0 || len(parts)%2 != 0 {
				t.Fatalf("invalid ARAM_DIAGNOSTIC_WRITE_U32 %q", writeText)
			}
			for index := 0; index < len(parts); index += 2 {
				address, addressErr := strconv.ParseUint(strings.TrimSpace(parts[index]), 0, 32)
				value, valueErr := strconv.ParseUint(strings.TrimSpace(parts[index+1]), 0, 32)
				if addressErr != nil || valueErr != nil || address&3 != 0 {
					t.Fatalf("invalid ARAM_DIAGNOSTIC_WRITE_U32 %q", writeText)
				}
				encoded := make([]byte, 4)
				binary.LittleEndian.PutUint32(encoded, uint32(value))
				if writeErr := backend.WriteMemory(uint32(address), encoded); writeErr != nil {
					t.Fatalf("write diagnostic guest u32: %v", writeErr)
				}
				t.Logf("wrote diagnostic guest u32 [0x%08x] = 0x%08x", address, value)
			}
		}
		if entryText := os.Getenv("ARAM_CALL_GUEST_ENTRY"); entryText != "" {
			entry, parseErr := strconv.ParseUint(entryText, 0, 32)
			if parseErr != nil || entry == 0 {
				t.Fatalf("invalid ARAM_CALL_GUEST_ENTRY %q", entryText)
			}
			var arguments [4]uint32
			if argumentText := strings.TrimSpace(os.Getenv("ARAM_CALL_GUEST_ARGS")); argumentText != "" {
				parts := strings.FieldsFunc(argumentText, func(character rune) bool {
					return character == ',' || character == ';'
				})
				if len(parts) > len(arguments) {
					t.Fatalf("too many ARAM_CALL_GUEST_ARGS values in %q", argumentText)
				}
				for index, part := range parts {
					value, argumentErr := strconv.ParseUint(strings.TrimSpace(part), 0, 32)
					if argumentErr != nil {
						t.Fatalf("invalid ARAM_CALL_GUEST_ARGS value %q", part)
					}
					arguments[index] = uint32(value)
				}
			}
			callGuestDiagnostic(uint32(entry), arguments)
		}
		if scanText := strings.TrimSpace(os.Getenv("ARAM_SCAN_GUEST_CONFIG_BYTE")); scanText != "" {
			// Identify the firmware configuration item that owns one live byte
			// without relying on the generated switch-table layout. The getter is
			// read-only: write two different temporary markers to the target and
			// retain only item IDs whose one-byte result follows both markers.
			parts := strings.FieldsFunc(scanText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			if len(parts) != 3 {
				t.Fatalf("invalid ARAM_SCAN_GUEST_CONFIG_BYTE %q", scanText)
			}
			target, targetErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
			start, startErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 16)
			end, endErr := strconv.ParseUint(strings.TrimSpace(parts[2]), 0, 16)
			if targetErr != nil || startErr != nil || endErr != nil || start >= end {
				t.Fatalf("invalid ARAM_SCAN_GUEST_CONFIG_BYTE %q", scanText)
			}
			const (
				configServiceGlobal = uint32(0x03d121e8)
				configGetterEntry   = uint32(0x008efa8d)
				configScratch       = uint32(0x07ffdc00)
			)
			var original [1]byte
			if readErr := backend.ReadMemory(uint32(target), original[:]); readErr != nil {
				t.Fatalf("read guest config scan target: %v", readErr)
			}
			var encodedService [4]byte
			if readErr := backend.ReadMemory(configServiceGlobal, encodedService[:]); readErr != nil {
				t.Fatalf("read guest config service: %v", readErr)
			}
			service := binary.LittleEndian.Uint32(encodedService[:])
			if service == 0 {
				t.Fatal("guest config service is null during byte scan")
			}
			matches := make(map[uint32]uint8)
			for markerIndex, marker := range []byte{0x5a, 0xc3} {
				if writeErr := backend.WriteMemory(uint32(target), []byte{marker}); writeErr != nil {
					t.Fatalf("write guest config scan marker: %v", writeErr)
				}
				for item := uint32(start); item < uint32(end); item++ {
					if writeErr := backend.WriteMemory(configScratch, []byte{0xe7}); writeErr != nil {
						t.Fatalf("initialize guest config scan scratch: %v", writeErr)
					}
					callReturn := callGuestDiagnosticWithLog(
						configGetterEntry,
						[4]uint32{service, item, configScratch, 1},
						false,
					)
					var result [1]byte
					if readErr := backend.ReadMemory(configScratch, result[:]); readErr != nil {
						t.Fatalf("read guest config scan result: %v", readErr)
					}
					if callReturn == 0 && result[0] == marker {
						matches[item] |= 1 << markerIndex
					}
				}
			}
			if writeErr := backend.WriteMemory(uint32(target), original[:]); writeErr != nil {
				t.Fatalf("restore guest config scan target: %v", writeErr)
			}
			var confirmed []uint32
			for item, markerMask := range matches {
				if markerMask == 3 {
					confirmed = append(confirmed, item)
				}
			}
			sort.Slice(confirmed, func(left, right int) bool { return confirmed[left] < confirmed[right] })
			t.Logf(
				"guest config byte [0x%08x] item scan [%#x,%#x): %x",
				uint32(target), uint32(start), uint32(end), confirmed,
			)
		}
		if valueText := os.Getenv("ARAM_DIAGNOSTIC_UIM_CARD_STATE"); valueText != "" {
			value, parseErr := strconv.ParseUint(valueText, 0, 8)
			if parseErr != nil {
				t.Fatalf("invalid ARAM_DIAGNOSTIC_UIM_CARD_STATE %q", valueText)
			}
			if writeErr := backend.WriteMemory(0x04443c24, []byte{byte(value)}); writeErr != nil {
				t.Fatalf("write diagnostic UIM card state: %v", writeErr)
			}
			t.Logf("wrote diagnostic UIM card state 0x%02x", value)
		}
		if valueText := os.Getenv("ARAM_PRIMARY_INPUT_STATUS"); valueText != "" {
			value, parseErr := strconv.ParseUint(valueText, 0, 32)
			if parseErr != nil {
				t.Fatalf("invalid ARAM_PRIMARY_INPUT_STATUS %q", valueText)
			}
			if inputErr := primaryClock.SetInputStatus(uint32(value)); inputErr != nil {
				t.Fatalf("set diagnostic primary input status: %v", inputErr)
			}
			t.Logf("set diagnostic primary input status 0x%08x", value)
		}
		keyID := strings.TrimSpace(os.Getenv("ARAM_KEYPAD_KEY"))
		matrixText := strings.TrimSpace(os.Getenv("ARAM_KEYPAD_MATRIX"))
		sequenceText := strings.TrimSpace(os.Getenv("ARAM_KEYPAD_SEQUENCE"))
		requestedKeypadInputs := 0
		for _, requested := range []bool{keyID != "", matrixText != "", sequenceText != ""} {
			if requested {
				requestedKeypadInputs++
			}
		}
		if requestedKeypadInputs > 1 {
			t.Fatal("ARAM_KEYPAD_KEY, ARAM_KEYPAD_MATRIX, and ARAM_KEYPAD_SEQUENCE are mutually exclusive")
		}
		if requestedKeypadInputs != 0 {
			if keypad == nil {
				t.Fatal("diagnostic keypad input requested without a keypad profile")
			}
			if sequenceText != "" {
				for _, part := range strings.FieldsFunc(sequenceText, func(character rune) bool {
					return character == ',' || character == ';'
				}) {
					part = strings.TrimSpace(part)
					if part == "" {
						t.Fatalf("invalid ARAM_KEYPAD_SEQUENCE %q", sequenceText)
					}
					keypadSequence = append(keypadSequence, part)
				}
				if len(keypadSequence) == 0 {
					t.Fatalf("invalid ARAM_KEYPAD_SEQUENCE %q", sequenceText)
				}
			} else if keyID != "" {
				if inputErr := keypad.SetKey(keyID, true); inputErr != nil {
					t.Fatalf("press diagnostic keypad key %q: %v", keyID, inputErr)
				}
				keypadRelease = func() error { return keypad.SetKey(keyID, false) }
				t.Logf("pressed diagnostic keypad key %q", keyID)
			} else {
				parts := strings.Split(matrixText, ",")
				if len(parts) != 2 {
					t.Fatalf("invalid ARAM_KEYPAD_MATRIX %q", matrixText)
				}
				row, rowErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 8)
				column, columnErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 8)
				if rowErr != nil || columnErr != nil {
					t.Fatalf("invalid ARAM_KEYPAD_MATRIX %q", matrixText)
				}
				if inputErr := keypad.SetMatrixKey(uint8(row), uint8(column), true); inputErr != nil {
					t.Fatalf("press diagnostic matrix key %q: %v", matrixText, inputErr)
				}
				keypadRelease = func() error {
					return keypad.SetMatrixKey(uint8(row), uint8(column), false)
				}
				t.Logf("pressed diagnostic keypad matrix row %d column %d", row, column)
			}
			keypadPressBudget = 2_000_000
			if budgetText := os.Getenv("ARAM_KEYPAD_PRESS_INSTRUCTIONS"); budgetText != "" {
				parsed, parseErr := strconv.ParseUint(budgetText, 0, 64)
				if parseErr != nil || parsed == 0 {
					t.Fatalf("invalid ARAM_KEYPAD_PRESS_INSTRUCTIONS %q", budgetText)
				}
				keypadPressBudget = parsed
			}
			if len(keypadSequence) != 0 {
				keypadReleaseBudget = 2_000_000
				if budgetText := os.Getenv("ARAM_KEYPAD_RELEASE_INSTRUCTIONS"); budgetText != "" {
					parsed, parseErr := strconv.ParseUint(budgetText, 0, 64)
					if parseErr != nil || parsed == 0 {
						t.Fatalf("invalid ARAM_KEYPAD_RELEASE_INSTRUCTIONS %q", budgetText)
					}
					keypadReleaseBudget = parsed
				}
			}
			requiredKeypadBudget := keypadPressBudget
			if len(keypadSequence) != 0 {
				if keypadReleaseBudget > ^uint64(0)-keypadPressBudget {
					t.Fatal("keypad press and release budget overflows uint64")
				}
				perKeyBudget := keypadPressBudget + keypadReleaseBudget
				if uint64(len(keypadSequence)) > ^uint64(0)/perKeyBudget {
					t.Fatal("keypad sequence budget overflows uint64")
				}
				requiredKeypadBudget = uint64(len(keypadSequence)) * perKeyBudget
			}
			if requiredKeypadBudget >= postWarmupRemaining {
				t.Fatalf(
					"keypad input budget %d exceeds post-warmup budget %d",
					requiredKeypadBudget, postWarmupRemaining,
				)
			}
		}
		if valueText := os.Getenv("ARAM_DIAGNOSTIC_MDSP_STATUS"); valueText != "" {
			value, parseErr := strconv.ParseUint(valueText, 0, 16)
			if parseErr != nil {
				t.Fatalf("invalid ARAM_DIAGNOSTIC_MDSP_STATUS %q", valueText)
			}
			var encoded [2]byte
			binary.LittleEndian.PutUint16(encoded[:], uint16(value))
			if writeErr := bus.Write(0x91200000, encoded[:], cpu.PermissionWrite); writeErr != nil {
				t.Fatalf("write diagnostic MDSP shared status: %v", writeErr)
			}
			t.Logf("wrote diagnostic MDSP shared status 0x%04x", value)
		}
		if valueText := os.Getenv("ARAM_DIAGNOSTIC_MDSP_EVENT0"); valueText != "" {
			value, parseErr := strconv.ParseUint(valueText, 0, 16)
			if parseErr != nil || value == 0 {
				t.Fatalf("invalid ARAM_DIAGNOSTIC_MDSP_EVENT0 %q", valueText)
			}
			var encoded [2]byte
			binary.LittleEndian.PutUint16(encoded[:], uint16(value))
			if writeErr := bus.Write(0x912051a4, encoded[:], cpu.PermissionWrite); writeErr != nil {
				t.Fatalf("write diagnostic MDSP event-0 flag: %v", writeErr)
			}
			t.Logf("wrote diagnostic MDSP event-0 flag 0x%04x", value)
		}
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
		if sourceText := os.Getenv("ARAM_INJECT_VIC_SOURCE"); sourceText != "" {
			source, parseErr := strconv.ParseUint(sourceText, 0, 8)
			if parseErr != nil || source >= uint64(vectoredInterruptController.SourceCount()) {
				t.Fatalf("invalid ARAM_INJECT_VIC_SOURCE %q", sourceText)
			}
			if err := vectoredInterruptController.PulseSource(uint8(source)); err != nil {
				t.Fatal(err)
			}
			t.Logf("injected one Qualcomm vectored interrupt source %d pulse", source)
		}
		if signalText := os.Getenv("ARAM_GUEST_SIGNAL_MAIN"); signalText != "" {
			signalParts := strings.FieldsFunc(signalText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			for index, signalPart := range signalParts {
				signal, parseErr := strconv.ParseUint(strings.TrimSpace(signalPart), 0, 32)
				if parseErr != nil || signal == 0 {
					t.Fatalf("invalid ARAM_GUEST_SIGNAL_MAIN %q", signalText)
				}
				callGuestSignal("Main", 0x0435d7c4, uint32(signal))
				if index+1 < len(signalParts) {
					runSignalSpacing("Main")
				}
			}
		}
		if signalText := os.Getenv("ARAM_GUEST_SIGNAL_MDSP"); signalText != "" {
			signalParts := strings.FieldsFunc(signalText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			for index, signalPart := range signalParts {
				signal, parseErr := strconv.ParseUint(strings.TrimSpace(signalPart), 0, 32)
				if parseErr != nil || signal == 0 {
					t.Fatalf("invalid ARAM_GUEST_SIGNAL_MDSP %q", signalText)
				}
				callGuestSignal("MDSP", 0x042c9ac8, uint32(signal))
				if index+1 < len(signalParts) {
					runSignalSpacing("MDSP")
				}
			}
		}
		if signalText := os.Getenv("ARAM_GUEST_SIGNAL_UIM"); signalText != "" {
			signalParts := strings.FieldsFunc(signalText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			for index, signalPart := range signalParts {
				signal, parseErr := strconv.ParseUint(strings.TrimSpace(signalPart), 0, 32)
				if parseErr != nil || signal == 0 {
					t.Fatalf("invalid ARAM_GUEST_SIGNAL_UIM %q", signalText)
				}
				callGuestSignal("UIM", 0x04356110, uint32(signal))
				if index+1 < len(signalParts) {
					runSignalSpacing("UIM")
				}
			}
		}
		if signalText := os.Getenv("ARAM_GUEST_SIGNAL_GSDI"); signalText != "" {
			signalParts := strings.FieldsFunc(signalText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			for index, signalPart := range signalParts {
				signal, parseErr := strconv.ParseUint(strings.TrimSpace(signalPart), 0, 32)
				if parseErr != nil || signal == 0 {
					t.Fatalf("invalid ARAM_GUEST_SIGNAL_GSDI %q", signalText)
				}
				callGuestSignal("GSDI", 0x0435e9a0, uint32(signal))
				if index+1 < len(signalParts) {
					runSignalSpacing("GSDI")
				}
			}
		}
		if signalText := os.Getenv("ARAM_GUEST_SIGNAL_UI"); signalText != "" {
			signalParts := strings.FieldsFunc(signalText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			for index, signalPart := range signalParts {
				signal, parseErr := strconv.ParseUint(strings.TrimSpace(signalPart), 0, 32)
				if parseErr != nil || signal == 0 {
					t.Fatalf("invalid ARAM_GUEST_SIGNAL_UI %q", signalText)
				}
				callGuestSignal("UI", 0x042d9560, uint32(signal))
				if index+1 < len(signalParts) {
					runSignalSpacing("UI")
				}
			}
		}
		if signalText := os.Getenv("ARAM_GUEST_SIGNAL_WCDMA_L1"); signalText != "" {
			signalParts := strings.FieldsFunc(signalText, func(character rune) bool {
				return character == ',' || character == ';'
			})
			for index, signalPart := range signalParts {
				signal, parseErr := strconv.ParseUint(strings.TrimSpace(signalPart), 0, 32)
				if parseErr != nil || signal == 0 {
					t.Fatalf("invalid ARAM_GUEST_SIGNAL_WCDMA_L1 %q", signalText)
				}
				callGuestSignal("WCDMA L1", 0x04361d58, uint32(signal))
				if index+1 < len(signalParts) {
					runSignalSpacing("WCDMA L1")
				}
			}
		}
		status, statusErr := backend.ReadRegister(cpu.RegisterCPSR)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		mode := cpu.ModeARM
		if status&cpu.StatusThumb != 0 {
			mode = cpu.ModeThumb
		}
		for index, sequenceKey := range keypadSequence {
			if inputErr := keypad.SetKey(sequenceKey, true); inputErr != nil {
				t.Fatalf("press diagnostic keypad sequence key %q: %v", sequenceKey, inputErr)
			}
			t.Logf("pressed diagnostic keypad sequence key %d/%d %q", index+1, len(keypadSequence), sequenceKey)
			completedInstructions := result.Instructions
			result = executionRunner.Run(context.Background(), result.PC, mode, keypadPressBudget)
			result.Instructions += completedInstructions
			postWarmupRemaining -= keypadPressBudget
			traceCaptured := traceWrite != nil || traceExecution != nil || errors.Is(result.Err, traceStop)
			if !traceCaptured && (result.Err != nil || result.Reason != cpu.StopBudget) {
				t.Fatalf("firmware stopped while keypad sequence key %q was pressed: %+v", sequenceKey, result)
			}
			if releaseErr := keypad.SetKey(sequenceKey, false); releaseErr != nil {
				t.Fatalf("release diagnostic keypad sequence key %q: %v", sequenceKey, releaseErr)
			}
			if traceCaptured {
				postWarmupRemaining = 0
				break
			}
			status, statusErr = backend.ReadRegister(cpu.RegisterCPSR)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			mode = cpu.ModeARM
			if status&cpu.StatusThumb != 0 {
				mode = cpu.ModeThumb
			}
			completedInstructions = result.Instructions
			result = executionRunner.Run(context.Background(), result.PC, mode, keypadReleaseBudget)
			result.Instructions += completedInstructions
			postWarmupRemaining -= keypadReleaseBudget
			traceCaptured = traceWrite != nil || traceExecution != nil || errors.Is(result.Err, traceStop)
			if !traceCaptured && (result.Err != nil || result.Reason != cpu.StopBudget) {
				t.Fatalf("firmware stopped after keypad sequence key %q was released: %+v", sequenceKey, result)
			}
			if traceCaptured {
				postWarmupRemaining = 0
				break
			}
			status, statusErr = backend.ReadRegister(cpu.RegisterCPSR)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			mode = cpu.ModeARM
			if status&cpu.StatusThumb != 0 {
				mode = cpu.ModeThumb
			}
			t.Logf(
				"released diagnostic keypad sequence key %d/%d %q after %d/%d instructions",
				index+1, len(keypadSequence), sequenceKey, keypadPressBudget, keypadReleaseBudget,
			)
		}
		if keypadRelease != nil {
			completedInstructions := result.Instructions
			result = executionRunner.Run(context.Background(), result.PC, mode, keypadPressBudget)
			result.Instructions += completedInstructions
			postWarmupRemaining -= keypadPressBudget
			traceCaptured := traceWrite != nil || traceExecution != nil || errors.Is(result.Err, traceStop)
			if !traceCaptured && (result.Err != nil || result.Reason != cpu.StopBudget) {
				t.Fatalf("firmware stopped while keypad was pressed: %+v", result)
			}
			if releaseErr := keypadRelease(); releaseErr != nil {
				t.Fatalf("release diagnostic keypad key: %v", releaseErr)
			}
			if traceCaptured {
				postWarmupRemaining = 0
			}
			status, statusErr = backend.ReadRegister(cpu.RegisterCPSR)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			mode = cpu.ModeARM
			if status&cpu.StatusThumb != 0 {
				mode = cpu.ModeThumb
			}
			t.Logf("released diagnostic keypad key after %d instructions", keypadPressBudget)
		}
		if postWarmupRemaining != 0 {
			warmupInstructions := result.Instructions
			result = executionRunner.Run(context.Background(), result.PC, mode, postWarmupRemaining)
			result.Instructions += warmupInstructions
		}
	}
	saveRuntimeSnapshot(os.Getenv("ARAM_SAVE_POST_RUNTIME_PREFIX"), result)
	saveMediaSnapshot(os.Getenv("ARAM_SAVE_POST_MEDIA_PREFIX"), result)
	if traceWrite != nil {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830PanelSourceTrace(t, panelSourceTrace)
		logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
		logSCHW830InstructionCache(t, backend)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		return
	}
	if traceExecution != nil {
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830PanelSourceTrace(t, panelSourceTrace)
		logSCHW830TraceExecution(t, backend, traceExecutionHistory, result.PC)
		logSCHW830RequestedMemory(t, backend, "trace-execution requested")
		logSCHW830MemoryWordMatches(t, bus)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		return
	}
	if errors.Is(result.Err, traceStop) {
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830PanelSourceTrace(t, panelSourceTrace)
		logSCHW830PCHistory(t, backend)
		logSCHW830PCRegisterCaptures(t, backend)
		logSCHW830PCHitRange(t, backend)
		logSCHW830RequestedMemory(t, backend, "trace-stop requested")
		logSCHW830MemoryWordMatches(t, bus)
		logSCHW830MemoryByteMatches(t, bus)
		logSCHW830PhysicalCode(t, bus)
		logSCHW830TraceState(t, backend, result.PC)
		logSCHW830REXTasks(t, backend)
		logSCHW830InstructionCache(t, backend)
		logSCHW830InstructionCachePrefetchHistory(t, backend)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
		if dumpPath := os.Getenv("ARAM_DUMP_EBI_RAM"); dumpPath != "" {
			dumpSCHW830EBIRAM(t, bus, dumpPath)
		}
		return
	}
	t.Logf("original firmware execution result: %+v", result)
	logSCHW830GuestWords(t, backend)
	logSCHW830PhysicalWords(t, bus)
	if os.Getenv("ARAM_INJECT_FACTORY_FORMAT") != "" {
		t.Logf("guest factory-format flag injected: %t", factoryFormatInjected)
	}
	if mediaSavePrefix := os.Getenv("ARAM_SAVE_FACTORY_MEDIA_PREFIX"); mediaSavePrefix != "" &&
		(factoryFormatInjected || os.Getenv("ARAM_SAVE_FACTORY_MEDIA_AFTER_GUEST_CALL") != "") &&
		os.Getenv("ARAM_CALL_GUEST_FACTORY_FORMAT") == "" {
		if !factoryFormatInjected && os.Getenv("ARAM_CALL_GUEST_ENTRY") == "" {
			t.Fatal("ARAM_SAVE_FACTORY_MEDIA_AFTER_GUEST_CALL requires ARAM_CALL_GUEST_ENTRY")
		}
		flashState, saveErr := flash.SaveState()
		if saveErr != nil {
			t.Fatalf("save natively provisioned flash state: %v", saveErr)
		}
		if writeErr := os.WriteFile(mediaSavePrefix+".flash", flashState, 0o600); writeErr != nil {
			t.Fatalf("write natively provisioned flash state: %v", writeErr)
		}
		nandState, saveErr := nand.SaveState()
		if saveErr != nil {
			t.Fatalf("save natively provisioned NAND state: %v", saveErr)
		}
		if writeErr := os.WriteFile(mediaSavePrefix+".nand", nandState, 0o600); writeErr != nil {
			t.Fatalf("write natively provisioned NAND state: %v", writeErr)
		}
		t.Logf(
			"saved guest-provisioned NAND media to %s.flash and %s.nand",
			mediaSavePrefix,
			mediaSavePrefix,
		)
	}
	if os.Getenv("ARAM_LOG_NAND_DIRTY") != "" {
		t.Logf("NAND dirty erase blocks: %#v", flash.DirtyBlocks())
		const tailSize = 8 * samsung.EraseBlockSize
		tail := make([]byte, tailSize)
		tailStart := flash.Size() - int64(len(tail))
		if count, err := flash.ReadAt(tail, tailStart); count != len(tail) || err != nil {
			t.Fatalf("read SCH-W830 NAND reservoir tail: count=%d err=%v", count, err)
		}
		for _, signature := range [][]byte{[]byte("ULOCKPCH"), []byte("LOCKPCHD")} {
			for searchStart := 0; searchStart < len(tail); {
				index := bytes.Index(tail[searchStart:], signature)
				if index < 0 {
					break
				}
				absolute := tailStart + int64(searchStart+index)
				t.Logf("NAND reservoir signature %q at 0x%x", signature, absolute)
				searchStart += index + 1
			}
		}
	}
	if os.Getenv("ARAM_LOG_MMIO") != "" {
		logSCHW830MMIOTrace(t, mmioTrace)
	}
	if os.Getenv("ARAM_LOG_PC_HISTORY") != "" {
		logSCHW830PCHistory(t, backend)
	}
	if warmupRAMPages != nil {
		logSCHW830ChangedRAMPages(t, bus, warmupRAMPages)
	}
	if dumpPath := os.Getenv("ARAM_DUMP_EBI_RAM"); dumpPath != "" {
		dumpSCHW830EBIRAM(t, bus, dumpPath)
	}
	if panelTrace != nil {
		panelTrace.Log(t)
	}
	logSCHW830PanelSourceTrace(t, panelSourceTrace)
	if os.Getenv("ARAM_LOG_CONTEXT_ACCESSES") != "" {
		logSCHW830ContextAccesses(t, traceContextAccesses)
	}
	for index, access := range traceMemoryWrites {
		t.Logf("trace memory write %d: %+v", index+1, access)
	}
	logSCHW830RequestedMemory(t, backend, "requested")
	if os.Getenv("ARAM_LOG_TRACE_WRITES") != "" && len(traceWriteHistory) != 0 {
		logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
	}
	logSCHW830REXTasks(t, backend)
	logSCHW830PCHitRange(t, backend)
	if os.Getenv("ARAM_LOG_INTERRUPT_STATE") != "" {
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
	}
	if result.Err != nil || result.Reason != cpu.StopBudget {
		code := make([]byte, 0x40)
		codeErr := backend.ReadMemory(result.PC-0x20, code)
		registers := make([]uint32, 16)
		for index := range registers {
			registers[index], _ = backend.ReadRegister(uint32(index))
		}
		t.Logf("probe code around PC: %x error=%v registers=%#v", code, codeErr, registers)
		logSCHW830MMIOTrace(t, mmioTrace)
		logSCHW830ContextAccesses(t, traceContextAccesses)
		logSCHW830LowWriteContext(t, traceContextLowWriteWindow)
		if len(traceWriteHistory) != 0 {
			logSCHW830TraceWrite(t, backend, traceWriteHistory, result.PC)
		}
		logSCHW830MemoryWordMatches(t, bus)
		logSCHW830LowVectorAccesses(t, lowVectorWrites, lowVectorExecutions)
		logSCHW830PhysicalCode(t, bus)
		logSCHW830VectorCandidates(t, bus)
		logSCHW830CP15ControlHistory(t, backend)
		logSCHW830InterruptTraceState(
			t, backend, interruptController, vectoredInterruptController, bootControl,
		)
	}
	if hits := backend.PCHits(); hits != nil {
		type pcHit struct {
			address uint32
			count   uint64
		}
		if addressText := os.Getenv("ARAM_TRACE_PC_ADDRESSES"); addressText != "" {
			for _, part := range strings.FieldsFunc(addressText, func(character rune) bool {
				return character == ',' || character == ';'
			}) {
				address, parseErr := strconv.ParseUint(strings.TrimSpace(part), 0, 32)
				if parseErr != nil {
					t.Fatalf("invalid ARAM_TRACE_PC_ADDRESSES %q", addressText)
				}
				count := hits[uint32(address)]
				if baseline := pcBaseline[uint32(address)]; count >= baseline {
					count -= baseline
				}
				t.Logf("post-warmup PC 0x%08x hits: %d", address, count)
			}
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
		hotLimit := 12
		if limitText := os.Getenv("ARAM_PC_HOT_LIMIT"); limitText != "" {
			limit, parseErr := strconv.ParseUint(limitText, 0, 16)
			if parseErr != nil || limit == 0 || limit > 256 {
				t.Fatalf("invalid ARAM_PC_HOT_LIMIT %q", limitText)
			}
			hotLimit = int(limit)
		}
		if len(ranked) > hotLimit {
			ranked = ranked[:hotLimit]
		}
		t.Logf("post-warmup hottest PCs: %#v", ranked)
	}
	logSCHW830PCRegisterCaptures(t, backend)
	wantInstructions := expectedWarmupInstructions + postWarmupBudget
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
	sleepOut, displayOn := panelController.PowerState()
	addressMode, pixelFormat := panelController.FormatState()
	pixelWrites, panelUpdates := panelController.WriteCounts()
	frame := panelController.FrameRGB565()
	frameBytes := make([]byte, len(frame)*2)
	colors := make(map[uint16]struct{})
	for index, pixel := range frame {
		binary.LittleEndian.PutUint16(frameBytes[index*2:], pixel)
		colors[pixel] = struct{}{}
	}
	frameHash := sha256.Sum256(frameBytes)
	if !sleepOut || !displayOn || pixelFormat != dcsPixelFormatRGB565 ||
		pixelWrites == 0 || panelUpdates == 0 || len(colors) < 2 {
		t.Fatalf(
			"decoded panel state = power %t/%t mode=%02x format=%02x pixels=%d updates=%d colors=%d hash=%x",
			sleepOut,
			displayOn,
			addressMode,
			pixelFormat,
			pixelWrites,
			panelUpdates,
			len(colors),
			frameHash,
		)
	}
	if screenshotPath := os.Getenv("ARAM_PANEL_SCREENSHOT"); screenshotPath != "" {
		file, createErr := os.Create(screenshotPath)
		if createErr != nil {
			t.Fatal(createErr)
		}
		encodeErr := png.Encode(file, panelController.FrameRGBA())
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatalf("write panel screenshot: encode=%v close=%v", encodeErr, closeErr)
		}
		t.Logf("wrote decoded panel screenshot to %s", screenshotPath)
	}
	finalStatus, finalStatusErr := backend.ReadRegister(cpu.RegisterCPSR)
	if finalStatusErr != nil {
		t.Fatal(finalStatusErr)
	}
	finalLink, finalLinkErr := backend.ReadRegister(cpu.RegisterLR)
	if finalLinkErr != nil {
		t.Fatal(finalLinkErr)
	}
	if os.Getenv("ARAM_PC_HISTORY") != "" {
		logSCHW830PCHistory(t, backend)
	}
	t.Logf(
		"post-panel boundary: instructions=%d pc=0x%08x cpsr=0x%08x lr=0x%08x err=%v panel=%d/%d command=0x%x data=0x%x decoded=%d/%d colors=%d hash=%x mode=%02x format=%02x watchdog=%d legacy-irq-status=%08x/%08x vic-status=%08x/%08x",
		result.Instructions,
		result.PC,
		finalStatus,
		finalLink,
		result.Err,
		commands,
		data,
		panel.CurrentCommand(),
		panel.LastData(),
		pixelWrites,
		panelUpdates,
		len(colors),
		frameHash,
		addressMode,
		pixelFormat,
		bootControl.WatchdogServices(),
		interruptController.status[0],
		interruptController.status[1],
		vectoredInterruptController.status[0],
		vectoredInterruptController.status[1],
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

func applySCHW830BootStatusOverride(t *testing.T, board *BoardProfile, environment string, offset uint32) {
	t.Helper()
	text := os.Getenv(environment)
	if text == "" {
		return
	}
	value, err := strconv.ParseUint(text, 0, 32)
	if err != nil {
		t.Fatalf("invalid %s %q", environment, text)
	}
	for index := range board.BootControlReadOnlyRegisters {
		if board.BootControlReadOnlyRegisters[index].Offset == offset {
			board.BootControlReadOnlyRegisters[index].Value = uint32(value)
			t.Logf("overrode SCH-W830 boot status 0x%04x with 0x%08x", offset, value)
			return
		}
	}
	t.Fatalf("SCH-W830 profile has no boot status register 0x%04x", offset)
}

func applySCHW830LiveBootStatusOverride(
	t *testing.T,
	boot *QualcommBootControl,
	environment string,
	offset uint32,
) {
	t.Helper()
	text := os.Getenv(environment)
	if text == "" {
		return
	}
	value, err := strconv.ParseUint(text, 0, 32)
	if err != nil {
		t.Fatalf("invalid %s %q", environment, text)
	}
	if _, ok := boot.readOnlyRegisters[offset]; !ok {
		t.Fatalf("SCH-W830 profile has no live boot status register 0x%04x", offset)
	}
	boot.readOnlyRegisters[offset] = uint32(value)
	boot.registers[offset] = uint32(value)
	t.Logf("overrode live SCH-W830 boot status 0x%04x with 0x%08x", offset, value)
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
	Match         uint64
	Access        MemoryAccess
	Stack         []byte
	StackErr      error
	NANDAddress   uint32
	NANDNextChunk uint32
	NANDStatus    uint32
	MMIOSnapshot  string
}

func appendSCHW830TraceCapture(
	history []schw830TraceWriteCapture,
	capture schw830TraceWriteCapture,
) []schw830TraceWriteCapture {
	const maximumCaptures = 16
	if len(history) == maximumCaptures {
		copy(history, history[1:])
		history = history[:maximumCaptures-1]
	}
	return append(history, capture)
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
		t.Logf("trace write match %d: %+v", capture.Match, access)
		logSCHW830Code(
			t,
			backend,
			"trace-write producer",
			access.Context.InstructionAddress,
			0x100,
			0x200,
		)
		t.Logf(
			"trace-write NAND address=0x%08x next-chunk=0x%x status=0x%08x",
			capture.NANDAddress, capture.NANDNextChunk, capture.NANDStatus,
		)
		if capture.MMIOSnapshot != "" {
			t.Logf("trace-write MMIO at match:\n%s", capture.MMIOSnapshot)
		}
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
	if interpreterBackend, ok := backend.(*interpreter.Backend); ok {
		logSCHW830InstructionCachePrefetchHistory(t, interpreterBackend)
	}
	logSCHW830TraceState(t, backend, pc)
}

func logSCHW830TraceExecution(
	t *testing.T,
	backend *interpreter.Backend,
	history []schw830TraceWriteCapture,
	pc uint32,
) {
	t.Helper()
	for _, capture := range history {
		t.Logf("trace execution match %d: %+v", capture.Match, capture.Access)
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

func logSCHW830InterruptTraceState(
	t *testing.T,
	backend *interpreter.Backend,
	legacy *QualcommInterruptController,
	vectored *QualcommVectoredInterruptController,
	boot *QualcommBootControl,
) {
	t.Helper()
	t.Logf(
		"trace interrupt state: legacy enable=%08x/%08x status=%08x/%08x FIQ=%08x/%08x VIC enable=%08x/%08x status=%08x/%08x in-service=%d/%t timetick=%08x match=%08x phase=%d ready=%t configured=%t",
		legacy.irqEnable[0], legacy.irqEnable[1], legacy.status[0], legacy.status[1],
		legacy.fiqEnable[0], legacy.fiqEnable[1],
		vectored.enable[0], vectored.enable[1], vectored.status[0], vectored.status[1],
		vectored.inService, vectored.inServiceValid,
		boot.timeTick, boot.registers[0x54c4], boot.timeTickPhase,
		boot.timeTickMatchReady, boot.timeTickMatchConfigured,
	)

	contextState, contextErr := backend.SaveContext()
	const (
		contextHeaderBytes      = 8
		contextRegisterWords    = 17
		contextBankedWords      = 22
		contextSavedStatusWords = 5
	)
	savedStatusOffset := contextHeaderBytes +
		(contextRegisterWords+contextBankedWords)*4
	cp15Offset := contextHeaderBytes +
		(contextRegisterWords+contextBankedWords+contextSavedStatusWords)*4
	const cp15Words = 7
	if contextErr != nil || len(contextState) < cp15Offset+cp15Words*4 {
		t.Logf("trace interpreter context: length=%d error=%v", len(contextState), contextErr)
		return
	}
	t.Logf(
		"trace interpreter exception state: SPSR_fiq/irq/svc/abt/und=%08x/%08x/%08x/%08x/%08x CP15_control=%08x TTB=%08x DACR=%08x DFSR=%08x IFSR=%08x FAR=%08x PID=%08x",
		binary.LittleEndian.Uint32(contextState[savedStatusOffset:]),
		binary.LittleEndian.Uint32(contextState[savedStatusOffset+4:]),
		binary.LittleEndian.Uint32(contextState[savedStatusOffset+8:]),
		binary.LittleEndian.Uint32(contextState[savedStatusOffset+12:]),
		binary.LittleEndian.Uint32(contextState[savedStatusOffset+16:]),
		binary.LittleEndian.Uint32(contextState[cp15Offset:]),
		binary.LittleEndian.Uint32(contextState[cp15Offset+4:]),
		binary.LittleEndian.Uint32(contextState[cp15Offset+8:]),
		binary.LittleEndian.Uint32(contextState[cp15Offset+12:]),
		binary.LittleEndian.Uint32(contextState[cp15Offset+16:]),
		binary.LittleEndian.Uint32(contextState[cp15Offset+20:]),
		binary.LittleEndian.Uint32(contextState[cp15Offset+24:]),
	)
}

func seedSCHW830InitialHighVectors(t *testing.T, backend *interpreter.Backend) {
	t.Helper()
	state, err := backend.SaveContext()
	const (
		contextHeaderBytes      = 8
		contextRegisterWords    = 17
		contextBankedWords      = 22
		contextSavedStatusWords = 5
		cp15ControlVectorBit    = uint32(1 << 13)
	)
	controlOffset := contextHeaderBytes +
		(contextRegisterWords+contextBankedWords+contextSavedStatusWords)*4
	if err != nil || len(state) < controlOffset+4 {
		t.Fatalf("seed diagnostic high vectors: context length=%d error=%v", len(state), err)
	}
	control := binary.LittleEndian.Uint32(state[controlOffset:]) | cp15ControlVectorBit
	binary.LittleEndian.PutUint32(state[controlOffset:], control)
	if err := backend.RestoreContext(state); err != nil {
		t.Fatalf("seed diagnostic high vectors: %v", err)
	}
}

func logSCHW830RequestedMemory(t *testing.T, backend cpu.Backend, label string) {
	t.Helper()
	if probeText := os.Getenv("ARAM_TRACE_CODE"); probeText != "" {
		for _, address := range parseSCHW830TraceAddresses(t, probeText) {
			logSCHW830Code(t, backend, label, address, 0, 0x100)
		}
	}
}

func logSCHW830GuestWords(t *testing.T, backend cpu.Backend) {
	t.Helper()
	probeText := os.Getenv("ARAM_LOG_GUEST_WORDS")
	if probeText == "" {
		return
	}
	for _, address := range parseSCHW830TraceAddresses(t, probeText) {
		var encoded [4]byte
		if err := backend.ReadMemory(address, encoded[:]); err != nil {
			t.Logf("guest word [0x%08x]: %v", address, err)
			continue
		}
		t.Logf("guest word [0x%08x] = 0x%08x", address, binary.LittleEndian.Uint32(encoded[:]))
	}
}

func snapshotSCHW830RAMPages(bus *Bus) map[uint32][sha256.Size]byte {
	const pageSize = uint32(4096)
	pages := make(map[uint32][sha256.Size]byte)
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, region := range bus.regions {
		if region.kind != regionRAM {
			continue
		}
		for offset := uint32(0); offset < region.size; offset += pageSize {
			end := min(uint64(offset)+uint64(pageSize), uint64(region.size))
			pages[region.address+offset] = sha256.Sum256(region.data[offset:end])
		}
	}
	return pages
}

func dumpSCHW830EBIRAM(t *testing.T, bus *Bus, path string) {
	t.Helper()
	bus.mu.Lock()
	var snapshot []byte
	for _, region := range bus.regions {
		if region.name == "ebi-ram" && region.kind == regionRAM {
			snapshot = append([]byte(nil), region.data...)
			break
		}
	}
	bus.mu.Unlock()
	if snapshot == nil {
		t.Fatal("SCH-W830 ebi-ram region is not mapped")
	}
	if err := os.WriteFile(path, snapshot, 0o600); err != nil {
		t.Fatalf("write SCH-W830 EBI RAM diagnostic: %v", err)
	}
	t.Logf("wrote %d bytes of SCH-W830 EBI RAM diagnostic", len(snapshot))
}

func logSCHW830ChangedRAMPages(
	t *testing.T,
	bus *Bus,
	warmup map[uint32][sha256.Size]byte,
) {
	t.Helper()
	final := snapshotSCHW830RAMPages(bus)
	changed := make([]uint32, 0)
	for address, hash := range final {
		if hash != warmup[address] {
			changed = append(changed, address)
		}
	}
	sort.Slice(changed, func(left, right int) bool { return changed[left] < changed[right] })
	t.Logf("SCH-W830 RAM pages changed after warmup: %d", len(changed))
	const (
		pageSize         = uint32(4096)
		maximumLogRanges = 256
	)
	logged := 0
	for start := 0; start < len(changed); {
		end := start + 1
		for end < len(changed) && changed[end] == changed[end-1]+pageSize {
			end++
		}
		if logged < maximumLogRanges {
			first := changed[start]
			lastEnd := uint64(changed[end-1]) + uint64(pageSize)
			t.Logf(
				"SCH-W830 changed RAM range %d: 0x%08x..0x%08x (%d pages)",
				logged+1, first, lastEnd, end-start,
			)
			logged++
		}
		start = end
	}
	if logged == maximumLogRanges {
		t.Logf("SCH-W830 changed RAM ranges truncated after %d entries", logged)
	}
}

func logSCHW830MemoryWordMatches(t *testing.T, bus *Bus) {
	t.Helper()
	probeText := os.Getenv("ARAM_TRACE_MEMORY_WORDS")
	if probeText == "" {
		return
	}
	words := parseSCHW830TraceAddresses(t, probeText)
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, word := range words {
		var needle [4]byte
		binary.LittleEndian.PutUint32(needle[:], word)
		matches := 0
		logged := 0
		const maximumLoggedMatches = 32
		for _, region := range bus.regions {
			if len(region.data) == 0 {
				continue
			}
			remaining := region.data
			base := 0
			for {
				index := bytes.Index(remaining, needle[:])
				if index < 0 {
					break
				}
				offset := base + index
				matches++
				if logged < maximumLoggedMatches {
					t.Logf(
						"trace memory word %08x match %d: %s+0x%x physical=0x%08x",
						word, matches, region.name, offset,
						uint32(uint64(region.address)+uint64(offset)),
					)
					logged++
				}
				base = offset + 1
				remaining = region.data[base:]
			}
		}
		t.Logf(
			"trace memory word %08x total matches: %d (first %d logged)",
			word, matches, logged,
		)
	}
}

func logSCHW830MemoryByteMatches(t *testing.T, bus *Bus) {
	t.Helper()
	probeText := os.Getenv("ARAM_TRACE_MEMORY_BYTES")
	if probeText == "" {
		return
	}
	needles := strings.Split(probeText, ",")
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, needleText := range needles {
		needle, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(needleText), " ", ""))
		if err != nil || len(needle) == 0 {
			t.Fatalf("invalid ARAM_TRACE_MEMORY_BYTES value %q", needleText)
		}
		matches := 0
		logged := 0
		const maximumLoggedMatches = 32
		for _, region := range bus.regions {
			if len(region.data) == 0 {
				continue
			}
			remaining := region.data
			base := 0
			for {
				index := bytes.Index(remaining, needle)
				if index < 0 {
					break
				}
				offset := base + index
				matches++
				if logged < maximumLoggedMatches {
					t.Logf(
						"trace memory bytes %x match %d: %s+0x%x physical=0x%08x",
						needle, matches, region.name, offset,
						uint32(uint64(region.address)+uint64(offset)),
					)
					logged++
				}
				base = offset + 1
				remaining = region.data[base:]
			}
		}
		t.Logf("trace memory bytes %x total matches: %d (first %d logged)", needle, matches, logged)
	}
}

func logSCHW830LowVectorAccesses(
	t *testing.T,
	writes []MemoryAccess,
	executions []MemoryAccess,
) {
	t.Helper()
	if os.Getenv("ARAM_TRACE_LOW_VECTOR_WRITES") == "" &&
		os.Getenv("ARAM_TRACE_LOW_VECTOR_ACCESSES") == "" &&
		os.Getenv("ARAM_TRACE_VECTOR_WRITE_BASE") == "" {
		return
	}
	for index, access := range writes {
		t.Logf("trace low-vector write %d: %+v", index+1, access)
	}
	t.Logf("trace low-vector writes retained: %d", len(writes))
	for index, access := range executions {
		t.Logf("trace low-vector execution %d: %+v", index+1, access)
	}
	t.Logf("trace low-vector executions retained: %d", len(executions))
}

func logSCHW830PhysicalCode(t *testing.T, bus *Bus) {
	t.Helper()
	probeText := os.Getenv("ARAM_TRACE_PHYSICAL_CODE")
	if probeText == "" {
		return
	}
	for _, address := range parseSCHW830TraceAddresses(t, probeText) {
		data := make([]byte, 0x100)
		err := readSCHW830BusMemory(bus, address, data)
		t.Logf("trace physical code at 0x%08x: %x error=%v", address, data, err)
	}
}

func logSCHW830PhysicalWords(t *testing.T, bus *Bus) {
	t.Helper()
	probeText := os.Getenv("ARAM_TRACE_PHYSICAL_WORDS")
	if probeText == "" {
		return
	}
	for _, address := range parseSCHW830TraceAddresses(t, probeText) {
		var encoded [4]byte
		err := bus.Read(address, encoded[:], cpu.PermissionRead)
		t.Logf(
			"trace physical word at 0x%08x: value=0x%08x error=%v",
			address,
			binary.LittleEndian.Uint32(encoded[:]),
			err,
		)
	}
}

func logSCHW830CP15ControlHistory(t *testing.T, backend *interpreter.Backend) {
	t.Helper()
	if os.Getenv("ARAM_CP15_CONTROL_HISTORY") == "" {
		return
	}
	for index, access := range backend.CP15ControlHistory() {
		t.Logf("trace CP15 control access %d: %+v", index+1, access)
	}
}

func logSCHW830InstructionCache(t *testing.T, backend *interpreter.Backend) {
	t.Helper()
	probeText := os.Getenv("ARAM_TRACE_ICACHE")
	if probeText == "" {
		return
	}
	for _, address := range parseSCHW830TraceAddresses(t, probeText) {
		mva, line, ok := backend.InstructionCacheLine(address)
		t.Logf(
			"trace instruction cache at 0x%08x: MVA=0x%08x present=%t bytes=%x",
			address, mva, ok, line,
		)
	}
}

func logSCHW830InstructionCachePrefetchHistory(t *testing.T, backend *interpreter.Backend) {
	t.Helper()
	for index, access := range backend.InstructionCachePrefetchHistory() {
		t.Logf("trace instruction-cache prefetch %d: %+v", index+1, access)
	}
}

func logSCHW830VectorCandidates(t *testing.T, bus *Bus) {
	t.Helper()
	if os.Getenv("ARAM_TRACE_VECTOR_CANDIDATES") == "" {
		return
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	const maximumCandidates = 64
	candidates := 0
	for _, region := range bus.regions {
		if len(region.data) < 0x20 {
			continue
		}
		for offset := 0; offset+0x20 <= len(region.data); offset += 4 {
			vectorInstructions := 0
			for slot := 0; slot < 8; slot++ {
				instruction := binary.LittleEndian.Uint32(region.data[offset+slot*4:])
				if isSCHW830VectorInstruction(instruction) {
					vectorInstructions++
				}
			}
			if vectorInstructions < 7 {
				continue
			}
			physical := uint32(uint64(region.address) + uint64(offset))
			t.Logf(
				"trace vector candidate %d: %s+0x%x physical=0x%08x instructions=%d bytes=%x",
				candidates+1, region.name, offset, physical, vectorInstructions,
				region.data[offset:offset+0x20],
			)
			candidates++
			if candidates == maximumCandidates {
				t.Logf("trace vector candidate log capped at %d", maximumCandidates)
				return
			}
		}
	}
	t.Logf("trace vector candidates: %d", candidates)
}

func isSCHW830VectorInstruction(instruction uint32) bool {
	// ARM vector slots normally contain an unconditional B/BL or an immediate
	// LDR that writes PC. Accept both common forms without assuming one
	// firmware's veneer layout or handler addresses.
	return instruction>>24 == 0xea || instruction>>24 == 0xeb ||
		instruction>>28 == 0xe && instruction&0x0c10f000 == 0x0410f000
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

func logSCHW830LowWriteContext(t *testing.T, accesses []MemoryAccess) {
	t.Helper()
	for index, access := range accesses {
		t.Logf("trace low-write context %d: %+v", index+1, access)
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
	stackAddress := registers[cpu.RegisterSP]
	stack := make([]byte, 0x100)
	if err := backend.ReadMemory(stackAddress, stack); err != nil {
		t.Logf("trace-stop stack at 0x%08x: %v", stackAddress, err)
	} else {
		t.Logf("trace-stop stack at 0x%08x: %x", stackAddress, stack)
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

func logSCHW830PCRegisterCaptures(t *testing.T, backend *interpreter.Backend) {
	t.Helper()
	for index, capture := range backend.PCRegisterCaptures() {
		t.Logf(
			"PC register capture %d address=0x%08x registers=%#v",
			index+1, capture.Address, capture.Registers,
		)
	}
}

func logSCHW830REXTasks(t *testing.T, backend cpu.Backend) {
	t.Helper()
	if os.Getenv("ARAM_TRACE_REX_TASKS") == "" {
		return
	}

	// Live SCH-W830 code at 0x0013aab8 walks the priority-ordered REX list
	// through TCB +0x24 and uses the sentinel at control +0x48. Keep this
	// firmware-facing layout confined to the private diagnostic.
	const (
		rexControl  = uint32(0x040ea834)
		rexListHead = rexControl + 0x48
		tcbBytes    = 0x80
		maximumTCBs = 256
	)
	var taskFilter map[string]struct{}
	if filterText := os.Getenv("ARAM_TRACE_REX_TASK_FILTER"); filterText != "" {
		taskFilter = make(map[string]struct{})
		for _, name := range strings.FieldsFunc(filterText, func(character rune) bool {
			return character == ',' || character == ';'
		}) {
			if name = strings.TrimSpace(name); name != "" {
				taskFilter[name] = struct{}{}
			}
		}
	}

	var sentinel [0x2c]byte
	if err := backend.ReadMemory(rexListHead, sentinel[:]); err != nil {
		t.Logf("REX task-list sentinel at 0x%08x: %v", rexListHead, err)
		return
	}
	current := binary.LittleEndian.Uint32(sentinel[0x24:0x28])
	seen := map[uint32]int{rexListHead: -1}
	for index := 0; current != rexListHead && current != 0 && index < maximumTCBs; index++ {
		if previous, duplicate := seen[current]; duplicate {
			t.Logf("REX task list repeats 0x%08x at node %d (first node %d)", current, index, previous)
			return
		}
		seen[current] = index

		data := make([]byte, tcbBytes)
		if err := backend.ReadMemory(current, data); err != nil {
			t.Logf("REX task %d at 0x%08x: %v", index, current, err)
			return
		}
		word := func(offset int) uint32 {
			return binary.LittleEndian.Uint32(data[offset : offset+4])
		}
		name := schw830TaskName(data[0x60:0x6c])
		if _, selected := taskFilter[name]; taskFilter != nil && !selected {
			current = word(0x24)
			continue
		}
		var (
			context     [0x200]byte
			contextErr  = backend.ReadMemory(word(0), context[:])
			savedStatus uint32
			savedLink   uint32
			savedPC     uint32
			waitCaller  uint32
			waitParent  uint32
		)
		if contextErr == nil {
			savedStatus = binary.LittleEndian.Uint32(context[0x00:0x04])
			savedLink = binary.LittleEndian.Uint32(context[0x38:0x3c])
			savedPC = binary.LittleEndian.Uint32(context[0x3c:0x40])
			// rex_wait (0x0013af6e) pushes {r4-r6, lr} before the
			// 0x40-byte context frame, leaving its caller at +0x4c.
			if savedPC == 0x0013afaf {
				waitCaller = binary.LittleEndian.Uint32(context[0x4c:0x50])
				if waitCaller == 0x0101d325 {
					waitParent = binary.LittleEndian.Uint32(context[0x5c:0x60])
				} else if waitCaller == 0x020dcebb {
					waitParent = binary.LittleEndian.Uint32(context[0x54:0x58])
				}
			}
		}
		stackLinks := make([]string, 0, 8)
		if contextErr == nil {
			for offset := 0x40; offset+4 <= len(context) && len(stackLinks) < 24; offset += 4 {
				candidate := binary.LittleEndian.Uint32(context[offset : offset+4])
				if candidate&1 != 0 && candidate >= 0x0001_0001 && candidate < 0x0400_0000 {
					stackLinks = append(stackLinks, fmt.Sprintf("+0x%x=0x%08x", offset, candidate))
				}
			}
		}
		t.Logf(
			"REX task %d tcb=0x%08x sp=0x%08x limit=0x%08x slices=%d signals=0x%08x wait=0x%08x priority=0x%08x ready-aux=0x%08x next=0x%08x prev=0x%08x suspended=%d name=%q saved-status=0x%08x saved-lr=0x%08x saved-pc=0x%08x wait-caller=0x%08x wait-parent=0x%08x stack-links=%q context-error=%v",
			index, current, word(0), word(4), word(8), word(0x0c), word(0x10), word(0x14),
			word(0x20), word(0x24), word(0x28), data[0x58], name,
			savedStatus, savedLink, savedPC, waitCaller, waitParent, strings.Join(stackLinks, ","), contextErr,
		)
		current = word(0x24)
	}
	if current == rexListHead {
		t.Logf("REX task list terminates at sentinel after %d nodes", len(seen)-1)
	} else if current == 0 {
		t.Logf("REX task list terminates at nil after %d nodes", len(seen)-1)
	} else {
		t.Logf("REX task list exceeded %d nodes at 0x%08x", maximumTCBs, current)
	}
}

func schw830TaskName(data []byte) string {
	end := bytes.IndexByte(data, 0)
	if end < 0 {
		end = len(data)
	}
	return string(data[:end])
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

type schw830PanelCommandTrace struct {
	command uint16
	data    []uint16
}

type schw830PanelTrace struct {
	commands []schw830PanelCommandTrace
}

func (p *schw830PanelTrace) Observe(write ParallelPanelWrite) {
	if !write.Data {
		p.commands = append(p.commands, schw830PanelCommandTrace{command: write.Command})
		return
	}
	if len(p.commands) == 0 {
		p.commands = append(p.commands, schw830PanelCommandTrace{command: write.Command})
	}
	current := &p.commands[len(p.commands)-1]
	current.data = append(current.data, write.Value)
}

func (p *schw830PanelTrace) Log(t *testing.T) {
	t.Helper()
	type commandStats struct {
		invocations int
		data        int
		minimum     int
		maximum     int
	}
	statistics := make(map[uint16]commandStats)
	for _, command := range p.commands {
		stats := statistics[command.command]
		count := len(command.data)
		if stats.invocations == 0 || count < stats.minimum {
			stats.minimum = count
		}
		stats.invocations++
		stats.data += count
		stats.maximum = max(stats.maximum, count)
		statistics[command.command] = stats
	}
	commands := make([]int, 0, len(statistics))
	for command := range statistics {
		commands = append(commands, int(command))
	}
	sort.Ints(commands)
	for _, command := range commands {
		stats := statistics[uint16(command)]
		t.Logf(
			"panel command 0x%04x: invocations=%d data=%d range=%d..%d",
			command,
			stats.invocations,
			stats.data,
			stats.minimum,
			stats.maximum,
		)
	}
	logged := 0
	for index, command := range p.commands {
		if index >= 64 && command.command != 0x2a && command.command != 0x2b &&
			command.command != 0x2c && len(command.data) < 256 {
			continue
		}
		const maximumLoggedGroups = 256
		if logged == maximumLoggedGroups {
			t.Logf("panel command group log truncated after %d selected groups", logged)
			break
		}
		first := command.data[:min(8, len(command.data))]
		last := command.data[max(0, len(command.data)-4):]
		t.Logf(
			"panel group %d command=0x%04x data=%d first=%#x last=%#x",
			index,
			command.command,
			len(command.data),
			first,
			last,
		)
		logged++
	}
}

type schw830PanelSourceKey struct {
	address uint32
	mode    cpu.Mode
}

type schw830PanelSourceStats struct {
	writes     uint64
	white      uint64
	first      uint64
	last       uint64
	firstValue uint16
	lastValue  uint16
}

type schw830PanelSourceGroup struct {
	key        schw830PanelSourceKey
	first      uint64
	last       uint64
	firstValue uint16
	lastValue  uint16
	uniform    bool
}

type schw830PanelSourceTrace struct {
	command   uint16
	writes    uint64
	discarded uint64
	stats     map[schw830PanelSourceKey]schw830PanelSourceStats
	groups    []schw830PanelSourceGroup
}

func newSCHW830PanelSourceTrace() *schw830PanelSourceTrace {
	return &schw830PanelSourceTrace{stats: make(map[schw830PanelSourceKey]schw830PanelSourceStats)}
}

func (trace *schw830PanelSourceTrace) Observe(access MMIOAccess) {
	if !access.Write || access.Err != nil || access.Width != Width16 {
		return
	}
	switch access.Address {
	case 0x20000000:
		trace.command = uint16(access.Value)
	case 0x20100000:
		if trace.command != dcsWriteMemoryStart && trace.command != dcsWriteMemoryContinue {
			return
		}
		trace.writes++
		value := uint16(access.Value)
		key := schw830PanelSourceKey{
			address: access.Context.InstructionAddress,
			mode:    access.Context.Mode,
		}
		stats := trace.stats[key]
		if stats.writes == 0 {
			stats.first = trace.writes
			stats.firstValue = value
		}
		stats.writes++
		stats.last = trace.writes
		stats.lastValue = value
		if value == 0xffff {
			stats.white++
		}
		trace.stats[key] = stats

		if len(trace.groups) != 0 {
			group := &trace.groups[len(trace.groups)-1]
			if group.key == key {
				group.last = trace.writes
				group.lastValue = value
				group.uniform = group.uniform && group.firstValue == value
				return
			}
		}
		const maximumGroups = 256
		if len(trace.groups) == maximumGroups {
			copy(trace.groups, trace.groups[1:])
			trace.groups = trace.groups[:maximumGroups-1]
			trace.discarded++
		}
		trace.groups = append(trace.groups, schw830PanelSourceGroup{
			key: key, first: trace.writes, last: trace.writes,
			firstValue: value, lastValue: value, uniform: true,
		})
	}
}

func logSCHW830PanelSourceTrace(t *testing.T, trace *schw830PanelSourceTrace) {
	t.Helper()
	if trace == nil {
		return
	}
	keys := make([]schw830PanelSourceKey, 0, len(trace.stats))
	for key := range trace.stats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		return trace.stats[keys[left]].first < trace.stats[keys[right]].first
	})
	t.Logf(
		"panel source trace: writes=%d sources=%d retained-groups=%d discarded-groups=%d",
		trace.writes, len(keys), len(trace.groups), trace.discarded,
	)
	for _, key := range keys {
		stats := trace.stats[key]
		t.Logf(
			"panel source pc=0x%08x/%d writes=%d white=%d ordinal=%d..%d value=0x%04x..0x%04x",
			key.address, key.mode, stats.writes, stats.white, stats.first, stats.last,
			stats.firstValue, stats.lastValue,
		)
	}
	for index, group := range trace.groups {
		t.Logf(
			"panel source group %d pc=0x%08x/%d ordinal=%d..%d value=0x%04x..0x%04x uniform=%t",
			index+1, group.key.address, group.key.mode, group.first, group.last,
			group.firstValue, group.lastValue, group.uniform,
		)
	}
}

const schw830NANDCommandTraceWindow = 64

type schw830NANDCommandCapture struct {
	command   uint32
	address   uint32
	status    uint32
	nextChunk uint32
	ready     uint32
	context   cpu.MemoryAccessContext
}

type schw830NANDCommandStats struct {
	count       uint64
	first       uint32
	last        uint32
	minimum     uint32
	maximum     uint32
	failed      uint64
	initialized bool
}

type schw830NANDCommandTrace struct {
	nand  *QualcommNAND
	ready *StatusSignal
	total uint64
	stats map[uint32]schw830NANDCommandStats
	first []schw830NANDCommandCapture
	last  []schw830NANDCommandCapture
}

func newSCHW830NANDCommandTrace(
	nand *QualcommNAND,
	ready *StatusSignal,
) *schw830NANDCommandTrace {
	return &schw830NANDCommandTrace{
		nand: nand, ready: ready, stats: make(map[uint32]schw830NANDCommandStats),
	}
}

func (trace *schw830NANDCommandTrace) Reset() {
	if trace == nil {
		return
	}
	*trace = *newSCHW830NANDCommandTrace(trace.nand, trace.ready)
}

func (trace *schw830NANDCommandTrace) Observe(access MMIOAccess) {
	if trace == nil || access.Region != "qualcomm-nand" || !access.Write ||
		access.Offset != qualcommNANDCommandOffset || access.Width != Width32 {
		return
	}
	capture := schw830NANDCommandCapture{
		command: access.Value, address: trace.nand.address, status: trace.nand.status,
		nextChunk: trace.nand.nextChunk, ready: trace.ready.Value(), context: access.Context,
	}
	trace.total++
	stats := trace.stats[capture.command]
	stats.count++
	if !stats.initialized {
		stats.first, stats.minimum, stats.maximum = capture.address, capture.address, capture.address
		stats.initialized = true
	}
	stats.last = capture.address
	stats.minimum = min(stats.minimum, capture.address)
	stats.maximum = max(stats.maximum, capture.address)
	if capture.status&(qualcommNANDStatusOperationError|qualcommNANDStatusProgramFailed) != 0 {
		stats.failed++
	}
	trace.stats[capture.command] = stats
	if len(trace.first) < schw830NANDCommandTraceWindow {
		trace.first = append(trace.first, capture)
	}
	if len(trace.last) < schw830NANDCommandTraceWindow {
		trace.last = append(trace.last, capture)
		return
	}
	trace.last[(trace.total-1)%schw830NANDCommandTraceWindow] = capture
}

func logSCHW830NANDCommandTrace(t *testing.T, trace *schw830NANDCommandTrace) {
	if trace == nil {
		return
	}
	t.Helper()
	commands := make([]uint32, 0, len(trace.stats))
	for command := range trace.stats {
		commands = append(commands, command)
	}
	sort.Slice(commands, func(left, right int) bool { return commands[left] < commands[right] })
	t.Logf("SCH-W830 NAND command trace: total=%d distinct=%d", trace.total, len(commands))
	for _, command := range commands {
		stats := trace.stats[command]
		t.Logf(
			"NAND cmd=%d count=%d failed=%d address=%08x..%08x first=%08x last=%08x",
			command, stats.count, stats.failed, stats.minimum, stats.maximum,
			stats.first, stats.last,
		)
	}
	logCapture := func(label string, index int, capture schw830NANDCommandCapture) {
		t.Logf(
			"NAND %s[%d] cmd=%d address=%08x status=%08x next=%x ready=%x pc=%08x/%d",
			label, index, capture.command, capture.address, capture.status, capture.nextChunk,
			capture.ready, capture.context.InstructionAddress, capture.context.Mode,
		)
	}
	for index, capture := range trace.first {
		logCapture("first", index, capture)
	}
	if trace.total <= schw830NANDCommandTraceWindow {
		return
	}
	start := trace.total % schw830NANDCommandTraceWindow
	for index := uint64(0); index < uint64(len(trace.last)); index++ {
		position := (start + index) % uint64(len(trace.last))
		logCapture("last", int(index), trace.last[position])
	}
}

const schw830MMIOTraceWindow = 64

type schw830MMIOTrace struct {
	total         uint64
	first         []MMIOAccess
	last          []MMIOAccess
	counts        map[schw830MMIOTraceKey]uint64
	addressCounts map[schw830MMIOAddressTraceKey]uint64
	all           bool
	addressesOnly bool
	focusStart    uint32
	focusSize     uint32
}

type schw830MMIOTraceKey struct {
	context cpu.MemoryAccessContext
	address uint32
	width   Width
	value   uint32
	write   bool
}

type schw830MMIOAddressTraceKey struct {
	address uint32
	width   Width
	write   bool
}

func newSCHW830MMIOTrace(enabled, all bool) *schw830MMIOTrace {
	if !enabled {
		return nil
	}
	trace := &schw830MMIOTrace{
		all:           all,
		addressesOnly: os.Getenv("ARAM_TRACE_MMIO_ADDRESSES") != "",
	}
	if trace.addressesOnly {
		trace.addressCounts = make(map[schw830MMIOAddressTraceKey]uint64)
		return trace
	}
	if !all {
		trace.counts = make(map[schw830MMIOTraceKey]uint64)
	}
	return trace
}

func (trace *schw830MMIOTrace) Reset() {
	if trace == nil {
		return
	}
	focusStart, focusSize := trace.focusStart, trace.focusSize
	*trace = *newSCHW830MMIOTrace(true, trace.all)
	trace.SetFocusRange(focusStart, focusSize)
}

func (trace *schw830MMIOTrace) SetFocusRange(start, size uint32) {
	if trace == nil {
		return
	}
	trace.focusStart, trace.focusSize = start, size
	if size != 0 && !trace.all && !trace.addressesOnly && trace.counts == nil {
		trace.counts = make(map[schw830MMIOTraceKey]uint64)
	}
}

func (trace *schw830MMIOTrace) Observe(access MMIOAccess) {
	vectoredInterrupt := access.Address >= 0x80000400 && access.Address < 0x80000600
	interrupt := access.Address >= 0x80000900 && access.Address < 0x80000960
	timeBlock := access.Address >= 0x80005400 && access.Address < 0x80005500
	sleepController := access.Address >= 0x90005200 && access.Address < 0x90005300
	focused := trace.focusSize != 0 &&
		uint64(access.Address) >= uint64(trace.focusStart) &&
		uint64(access.Address) < uint64(trace.focusStart)+uint64(trace.focusSize)
	if !trace.all && !trace.addressesOnly {
		if trace.focusSize != 0 {
			if !focused {
				return
			}
		} else if !vectoredInterrupt && !interrupt && !timeBlock && !sleepController {
			return
		}
	}
	trace.total++
	if trace.addressesOnly {
		trace.addressCounts[schw830MMIOAddressTraceKey{
			address: access.Address,
			width:   access.Width,
			write:   access.Write,
		}]++
		return
	}
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
	if trace.addressesOnly {
		fmt.Fprintf(&output, "total=%d distinct-addresses=%d", trace.total, len(trace.addressCounts))
		keys := make([]schw830MMIOAddressTraceKey, 0, len(trace.addressCounts))
		for key := range trace.addressCounts {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(left, right int) bool {
			if keys[left].address != keys[right].address {
				return keys[left].address < keys[right].address
			}
			if keys[left].write != keys[right].write {
				return !keys[left].write
			}
			return keys[left].width < keys[right].width
		})
		for _, key := range keys {
			direction := "R"
			if key.write {
				direction = "W"
			}
			fmt.Fprintf(
				&output,
				"\ncount %-8d %s address=0x%08x width=%d",
				trace.addressCounts[key], direction, key.address, key.width,
			)
		}
		return output.String()
	}
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
			"\ncount %-5d %s pc=0x%08x/%d lr=0x%08x sp=0x%08x address=0x%08x width=%d value=0x%08x",
			trace.counts[key], direction, key.context.InstructionAddress, key.context.Mode,
			key.context.LinkAddress, key.context.StackAddress,
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
		"%s pc=0x%08x/%d lr=0x%08x sp=0x%08x address=0x%08x width=%d value=0x%08x err=%v",
		direction, access.Context.InstructionAddress, access.Context.Mode,
		access.Context.LinkAddress, access.Context.StackAddress,
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
