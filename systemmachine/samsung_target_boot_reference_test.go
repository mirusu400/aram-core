package systemmachine

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/firmwareset"
	"github.com/mirusu400/aram-core/loader/samsung"
	"github.com/mirusu400/aram-core/system"
)

const samsungTargetBootDefaultBudget = uint64(600_000_000)
const samsungW350TargetBootDefaultBudget = uint64(2_000_000_000)

func TestSamsungTargetBootPrivateReferences(t *testing.T) {
	configured := os.Getenv("ARAM_SAMSUNG_RAW_REFERENCE_DIRS")
	if configured == "" {
		t.Skip("ARAM_SAMSUNG_RAW_REFERENCE_DIRS is not configured")
	}
	var configuredBudget uint64
	if text := os.Getenv("ARAM_SAMSUNG_RAW_RUN_BUDGET"); text != "" {
		parsed, err := strconv.ParseUint(text, 0, 64)
		if err != nil || parsed == 0 {
			t.Fatal("ARAM_SAMSUNG_RAW_RUN_BUDGET is invalid")
		}
		configuredBudget = parsed
	}
	for index, directory := range filepath.SplitList(configured) {
		directory := directory
		if strings.TrimSpace(directory) == "" {
			continue
		}
		t.Run(fmt.Sprintf("reference-%d", index), func(t *testing.T) {
			set := openSamsungSCHReferenceSet(t, directory)
			pkg, err := samsung.Inspect(set)
			check(t, err)
			firmwareProfile, err := samsung.BuiltinRegistry().Match(pkg)
			check(t, err)
			if os.Getenv("ARAM_SAMSUNG_RAW_LOG_PROGRESSIVE_ELF") != "" {
				image, decodeErr := samsung.DecodeWBIN(set, pkg)
				check(t, decodeErr)
				t.Logf("%s progressive ELF entry=%#08x flags=%#x headers=%+v", firmwareProfile.Model, image.ELF.Entry, image.ELF.Flags, image.ELF.ProgramHeaders)
				for _, marker := range [][]byte{
					[]byte("tsc2007.c"),
					[]byte("[TSC 2007] gpio_int_pen_handler"),
					[]byte("/mf/flash/PhoneLock.dmf"),
				} {
					if offset := bytes.Index(image.Bytes, marker); offset >= 0 {
						mapped := uint32(0)
						for _, header := range image.ELF.ProgramHeaders {
							if uint64(offset) >= uint64(header.Offset) && uint64(offset) < uint64(header.Offset)+uint64(header.FileSize) {
								mapped = header.PhysicalAddress + uint32(offset) - header.Offset
								break
							}
						}
						t.Logf("%s decoded marker %q offset=%#08x mapped=%#08x", firmwareProfile.Model, marker, offset, mapped)
					}
				}
			}
			budget := configuredBudget
			if budget == 0 {
				budget = samsungTargetBootDefaultBudget
				if firmwareProfile.ID == samsung.SCHW350CK06ProfileID {
					budget = samsungW350TargetBootDefaultBudget
				}
			}
			backendMode := CPUBackendJIT
			switch text := os.Getenv("ARAM_SAMSUNG_RAW_BACKEND_MODE"); text {
			case "", string(CPUBackendJIT):
			case string(CPUBackendPrecise):
				backendMode = CPUBackendPrecise
			case string(CPUBackendJITLoops):
				backendMode = CPUBackendJITLoops
			default:
				t.Fatalf("ARAM_SAMSUNG_RAW_BACKEND_MODE is invalid: %q", text)
			}
			options := Options{BackendMode: backendMode}
			if prefix := os.Getenv("ARAM_SAMSUNG_RAW_LOAD_MEDIA_PREFIX"); prefix != "" {
				flash, readErr := os.ReadFile(prefix + ".flash")
				check(t, readErr)
				nand, readErr := os.ReadFile(prefix + ".nand")
				check(t, readErr)
				options.Media = &MediaState{
					FirmwareBuildID: firmwareProfile.ID,
					Flash:           flash,
					NAND:            nand,
				}
				if secondary, readErr := os.ReadFile(prefix + ".secondary"); readErr == nil {
					options.Media.SecondaryFlash = secondary
				} else if !os.IsNotExist(readErr) {
					check(t, readErr)
				}
				if spare, readErr := os.ReadFile(prefix + ".onenand-spare"); readErr == nil {
					options.Media.OneNANDSpare = spare
				} else if !os.IsNotExist(readErr) {
					check(t, readErr)
				}
			}
			var machine *Machine
			if override := os.Getenv("ARAM_SAMSUNG_RAW_SBI_READ_OVERRIDE"); override != "" {
				if firmwareProfile.ID != samsung.SPHW4200DC17ProfileID {
					t.Fatal("SBI read override requested for another firmware")
				}
				parts := strings.Split(override, ",")
				if len(parts) != 3 {
					t.Fatalf("ARAM_SAMSUNG_RAW_SBI_READ_OVERRIDE is invalid: %q", override)
				}
				values := make([]uint64, len(parts))
				for index, part := range parts {
					values[index], err = strconv.ParseUint(strings.TrimSpace(part), 0, 32)
					if err != nil {
						t.Fatalf("ARAM_SAMSUNG_RAW_SBI_READ_OVERRIDE is invalid: %q", override)
					}
				}
				if values[0] > uint64(^uint32(0)) || values[1] > uint64(^uint8(0)) || values[2] > uint64(^uint8(0)) {
					t.Fatalf("ARAM_SAMSUNG_RAW_SBI_READ_OVERRIDE is out of range: %q", override)
				}
				board := system.SPHW4200DC17BoardProfile()
				board.BootControlSBIReadResponses = append(
					board.BootControlSBIReadResponses,
					system.QualcommSBIReadResponse{
						Controller: uint32(values[0]), Address: uint8(values[1]), Value: uint8(values[2]),
					},
				)
				machine, err = newSamsungQualcommMachine(set, pkg, firmwareProfile, board, bootBoundary{}, options)
			} else {
				machine, err = New(set, options)
			}
			check(t, err)
			t.Cleanup(func() { _ = machine.Close() })
			if os.Getenv("ARAM_SAMSUNG_RAW_W350_MARK_PROVISIONED") != "" {
				if firmwareProfile.ID != samsung.SCHW350CK06ProfileID {
					t.Fatal("W350 provisioned marker requested for another firmware")
				}
				const blockOffset = int64(0x019a0000)
				block := make([]byte, samsung.EraseBlockSize)
				read, readErr := machine.flash.ReadAt(block, blockOffset)
				if readErr != nil || read != len(block) {
					t.Fatalf("read W350 manufacturing block = %d, %v", read, readErr)
				}
				block[4] = 1
				check(t, machine.flash.EraseBlock(uint32(blockOffset/samsung.EraseBlockSize)))
				check(t, machine.flash.ProgramAt(block, blockOffset))
			}
			if os.Getenv("ARAM_SAMSUNG_RAW_W340_PRESET_PROVISIONED") != "" {
				if firmwareProfile.ID != samsung.SCHW340DC18ProfileID {
					t.Fatal("W340 provisioned state requested for another firmware")
				}
				for _, patch := range []struct {
					offset int64
					block  uint32
					data   []byte
				}{
					{offset: 0x08800000, block: 0x440, data: bytes.Repeat([]byte{0xff}, 12)},
					{offset: 0x0ffe0000, block: 0x7ff, data: make([]byte, 4)},
				} {
					block := make([]byte, samsung.EraseBlockSize)
					if read, readErr := machine.flash.ReadAt(block, patch.offset); readErr != nil || read != len(block) {
						t.Fatalf("read W340 provisioned block %#x = %d, %v", patch.offset, read, readErr)
					}
					copy(block, patch.data)
					check(t, machine.flash.EraseBlock(patch.block))
					check(t, machine.flash.ProgramAt(block, patch.offset))
				}
			}
			var pcHistory interface{ PCHistory() []uint32 }
			logPCHistory := os.Getenv("ARAM_SAMSUNG_RAW_LOG_PC_HISTORY") != ""
			if os.Getenv("ARAM_SAMSUNG_RAW_TRACE_PANEL") != "" || logPCHistory {
				history, ok := machine.backend.(interface {
					SetPCHistoryLimit(uint32) error
					PCHistory() []uint32
				})
				if !ok {
					t.Fatal("selected backend has no PC history support")
				}
				check(t, history.SetPCHistoryLimit(4096))
				pcHistory = history
			}
			if identity := machine.Identity(); identity.Model != firmwareProfile.Model ||
				identity.FirmwareBuildID != firmwareProfile.ID {
				t.Fatalf("selected machine identity = %+v", identity)
			}
			var watchedMemory []system.MemoryAccess
			var watchedAddress *uint32
			watchStop := os.Getenv("ARAM_SAMSUNG_RAW_WATCH_STOP_ON_WRITE") != ""
			watchStopOnAccess := os.Getenv("ARAM_SAMSUNG_RAW_WATCH_STOP_ON_ACCESS") != ""
			watchStopOnRead := os.Getenv("ARAM_SAMSUNG_RAW_WATCH_STOP_ON_READ") != ""
			var watchStopValue *uint32
			if text := os.Getenv("ARAM_SAMSUNG_RAW_WATCH_STOP_VALUE"); text != "" {
				value, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_WATCH_STOP_VALUE is invalid: %q", text)
				}
				selected := uint32(value)
				watchStopValue = &selected
			}
			var watchAfter uint64
			var installMemoryWatch func()
			if text := os.Getenv("ARAM_SAMSUNG_RAW_WATCH_ADDRESS"); text != "" {
				address, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_WATCH_ADDRESS is invalid: %q", text)
				}
				selectedAddress := uint32(address)
				watchedAddress = &selectedAddress
				installMemoryWatch = func() {
					watchSize := uint32(4)
					if firmwareProfile.ID == samsung.SCHW340DC18ProfileID &&
						(selectedAddress == 0x03ecfb90 || selectedAddress == 0x03f43d50) {
						watchSize = 8
					}
					check(t, machine.bus.SetMemoryObserver(selectedAddress, watchSize, func(access system.MemoryAccess) {
						watchedMemory = append(watchedMemory, access)
						if len(watchedMemory) > 64 {
							copy(watchedMemory, watchedMemory[len(watchedMemory)-64:])
							watchedMemory = watchedMemory[:64]
						}
						matchesStopValue := watchStopValue == nil || access.Value == *watchStopValue
						if matchesStopValue && (watchStopOnAccess || watchStop && access.Write || watchStopOnRead && !access.Write) {
							_ = machine.backend.Stop()
						}
					}))
				}
				if afterText := os.Getenv("ARAM_SAMSUNG_RAW_WATCH_AFTER"); afterText != "" {
					watchAfter, parseErr = strconv.ParseUint(afterText, 0, 64)
					if parseErr != nil || watchAfter == 0 || watchAfter >= budget {
						t.Fatalf("ARAM_SAMSUNG_RAW_WATCH_AFTER is invalid: %q", afterText)
					}
				} else {
					installMemoryWatch()
				}
			}
			if text := os.Getenv("ARAM_SAMSUNG_RAW_WATCH_INSTRUCTION"); text != "" {
				instruction, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil || installMemoryWatch == nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_WATCH_INSTRUCTION is invalid: %q", text)
				}
				installPhysicalWatch := installMemoryWatch
				installMemoryWatch = func() {
					installPhysicalWatch()
					check(t, machine.bus.SetInstructionMemoryObserver(uint32(instruction), 2, func(access system.MemoryAccess) {
						watchedMemory = append(watchedMemory, access)
						if len(watchedMemory) > 64 {
							copy(watchedMemory, watchedMemory[len(watchedMemory)-64:])
							watchedMemory = watchedMemory[:64]
						}
					}))
				}
			}

			initialImageID := "qcsbl"
			if firmwareProfile.DirectResetImage != "" {
				initialImageID = firmwareProfile.DirectResetImage
			}
			initialSpec, ok := firmwareProfile.BootImage(initialImageID)
			if !ok {
				t.Fatalf("profile has no initial boot image %q", initialImageID)
			}
			wantEntry := initialSpec.LoadAddress + initialSpec.EntryOffset
			if position := machine.Position(); position.PC != wantEntry ||
				position.Mode != cpu.ModeARM || position.Instructions != 0 {
				t.Fatalf("initial boot position = %+v, want PC %#x ARM", position, wantEntry)
			}
			if firmwareProfile.ID == samsung.SCHW320DC18ProfileID {
				assertPrivateW320ResetPBLState(t, machine)
			}

			var mgpControlWrites, mgpInterfaceWrites uint64
			var indexedReads, indexedWrites uint64
			type panelCallsite struct {
				pc, link, stack uint32
				count           uint64
				last            uint64
			}
			panelTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_PANEL") != ""
			panelWriteTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_PANEL_WRITES") != ""
			type panelWriteRun struct {
				command    uint16
				dataWrites uint64
				lastData   uint16
			}
			var panelWriteRuns []panelWriteRun
			mmioTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_MMIO") != ""
			clockTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_CLOCK") != ""
			bootEventTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_BOOT_EVENTS") != ""
			nandCommandTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_NAND_COMMANDS") != ""
			nandCommandDetails := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_NAND_COMMAND_DETAILS") != ""
			if nandCommandDetails {
				nandCommandTrace = true
			}
			type mmioSummary struct {
				reads, writes uint64
				last          system.MMIOAccess
			}
			type adspAccessKey struct {
				region string
				offset uint32
				write  bool
			}
			type adspAccessSummary struct {
				count       uint64
				first, last system.MMIOAccess
			}
			adspTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_ADSP") != ""
			adspAccesses := make(map[adspAccessKey]*adspAccessSummary)
			var recentADSPAccesses []system.MMIOAccess
			mdpTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_MDP") != ""
			mdpAccesses := make(map[adspAccessKey]*adspAccessSummary)
			var recentMDPAccesses []system.MMIOAccess
			var pcMMIOTraceStart, pcMMIOTraceEnd uint32
			pcMMIOAccesses := make(map[adspAccessKey]*adspAccessSummary)
			var recentPCMMIOAccesses []system.MMIOAccess
			if value := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_PC_MMIO_RANGE"); value != "" {
				parts := strings.Split(value, ",")
				if len(parts) != 2 {
					t.Fatalf("ARAM_SAMSUNG_RAW_TRACE_PC_MMIO_RANGE is invalid: %q", value)
				}
				start, startErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
				end, endErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 32)
				if startErr != nil || endErr != nil || start >= end {
					t.Fatalf("ARAM_SAMSUNG_RAW_TRACE_PC_MMIO_RANGE is invalid: %q", value)
				}
				pcMMIOTraceStart, pcMMIOTraceEnd = uint32(start), uint32(end)
			}
			type sbiCommandKey struct {
				controller uint32
				command    uint32
			}
			type sbiCommandSummary struct {
				count       uint64
				first, last system.MMIOAccess
			}
			sbiTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_SBI") != ""
			sbiCommands := make(map[sbiCommandKey]*sbiCommandSummary)
			uartTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_UART") != ""
			uartBytes := make(map[uint32][]byte)
			var uartFirstAccesses []system.MMIOAccess
			var uartAccesses []system.MMIOAccess
			sdccTrace := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_SDCC") != ""
			var recentSDCCAccesses []system.MMIOAccess
			type nandCommandSummary struct {
				count                          uint64
				firstAddress, lastAddress      uint32
				minimumAddress, maximumAddress uint32
				first, last                    system.MMIOAccess
			}
			mmioSummaries := make(map[string]*mmioSummary)
			var recentClockAccesses []system.MMIOAccess
			oneNANDCommandCounts := make(map[uint32]uint64)
			oneNANDStatusCounts := make(map[uint32]uint64)
			type oneNANDCommandDetail struct {
				command, startBuffer, status, interrupt uint32
				addresses                               [8]uint16
				access                                  system.MMIOAccess
				main, spare                             [32]byte
			}
			var oneNANDAddresses [8]uint16
			var oneNANDStartBuffer uint16
			var oneNANDRecent []oneNANDCommandDetail
			var oneNANDRecentNext int
			var oneNANDMutations []oneNANDCommandDetail
			nandSummaries := make(map[uint32]*mmioSummary)
			nandCommandSummaries := make(map[uint32]*nandCommandSummary)
			nandReadMiB := make(map[uint32]uint64)
			nandSparseReads := make(map[uint32]uint64)
			var nandAddress uint32
			type nandCommandDetail struct {
				command, address uint32
				access           system.MMIOAccess
				buffer           [0x210]byte
			}
			var nandCommandDetailLog []nandCommandDetail
			var nandWatchAddress uint32
			if text := os.Getenv("ARAM_SAMSUNG_RAW_NAND_WATCH_ADDRESS"); text != "" {
				address, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_NAND_WATCH_ADDRESS is invalid: %q", text)
				}
				nandWatchAddress = uint32(address)
				nandCommandTrace = true
			}
			type nandWatchCapture struct {
				access system.MMIOAccess
				hit    uint64
				stack  []byte
				buffer [0x210]byte
				err    error
			}
			var nandWatchHits uint64
			var nandWatchCaptures []nandWatchCapture
			var panelStopAfter uint64
			if text := os.Getenv("ARAM_SAMSUNG_RAW_PANEL_STOP_AFTER"); text != "" {
				panelStopAfter, err = strconv.ParseUint(text, 0, 64)
				if err != nil || panelStopAfter == 0 {
					t.Fatalf("ARAM_SAMSUNG_RAW_PANEL_STOP_AFTER is invalid: %q", text)
				}
				panelTrace = true
			}
			panelCallsites := make(map[[3]uint32]*panelCallsite)
			var panelAccesses uint64
			if firmwareProfile.ID == samsung.SCHW320DC18ProfileID ||
				firmwareProfile.ID == samsung.SCHW350CK06ProfileID || panelTrace || mmioTrace || clockTrace || bootEventTrace || nandCommandTrace || sbiTrace || uartTrace || sdccTrace || adspTrace || mdpTrace || pcMMIOTraceEnd != 0 {
				machine.bus.SetMMIOObserver(func(access system.MMIOAccess) {
					if mdpTrace && access.Region == "qualcomm-boot-control" &&
						(access.Offset >= 0x0d00 && access.Offset < 0x1000 ||
							access.Context.Attributed && access.Context.InstructionAddress >= 0x02000000 && access.Context.InstructionAddress < 0x02200000) {
						key := adspAccessKey{region: access.Region, offset: access.Offset, write: access.Write}
						summary := mdpAccesses[key]
						if summary == nil {
							summary = &adspAccessSummary{first: access}
							mdpAccesses[key] = summary
						}
						summary.count++
						summary.last = access
						recentMDPAccesses = append(recentMDPAccesses, access)
						if len(recentMDPAccesses) > 512 {
							copy(recentMDPAccesses, recentMDPAccesses[len(recentMDPAccesses)-256:])
							recentMDPAccesses = recentMDPAccesses[:256]
						}
					}
					if sdccTrace && access.Region == "qualcomm-boot-control" &&
						(access.Offset >= 0x0c00 && access.Offset < 0x0c50 ||
							access.Offset >= 0x4110 && access.Offset < 0x4140) {
						recentSDCCAccesses = append(recentSDCCAccesses, access)
						if len(recentSDCCAccesses) > 1024 {
							copy(recentSDCCAccesses, recentSDCCAccesses[len(recentSDCCAccesses)-512:])
							recentSDCCAccesses = recentSDCCAccesses[:512]
						}
					}
					if clockTrace && (access.Region == "qualcomm-primary-clock" || access.Region == "qualcomm-secondary-clock") {
						recentClockAccesses = append(recentClockAccesses, access)
						if len(recentClockAccesses) > 512 {
							copy(recentClockAccesses, recentClockAccesses[len(recentClockAccesses)-512:])
							recentClockAccesses = recentClockAccesses[:512]
						}
					}
					if pcMMIOTraceEnd != 0 && access.Context.Attributed &&
						access.Context.InstructionAddress >= pcMMIOTraceStart && access.Context.InstructionAddress < pcMMIOTraceEnd {
						key := adspAccessKey{region: access.Region, offset: access.Offset, write: access.Write}
						summary := pcMMIOAccesses[key]
						if summary == nil {
							summary = &adspAccessSummary{first: access}
							pcMMIOAccesses[key] = summary
						}
						summary.count++
						summary.last = access
						recentPCMMIOAccesses = append(recentPCMMIOAccesses, access)
						if len(recentPCMMIOAccesses) > 256 {
							copy(recentPCMMIOAccesses, recentPCMMIOAccesses[len(recentPCMMIOAccesses)-256:])
							recentPCMMIOAccesses = recentPCMMIOAccesses[:256]
						}
					}
					if adspTrace {
						selected := access.Region == "external-32bit-control" ||
							access.Region == "external-16bit-bank-0" && access.Offset >= 0x2100 && access.Offset < 0x2200 ||
							access.Region == "external-16bit-bank-1" && access.Offset >= 0x0b00 && access.Offset < 0x0c00 ||
							access.Region == "external-16bit-bank-1" && (access.Offset == 0x4d1e || access.Offset == 0x51a4)
						if selected {
							key := adspAccessKey{region: access.Region, offset: access.Offset, write: access.Write}
							summary := adspAccesses[key]
							if summary == nil {
								summary = &adspAccessSummary{first: access}
								adspAccesses[key] = summary
							}
							summary.count++
							summary.last = access
							recentADSPAccesses = append(recentADSPAccesses, access)
							if len(recentADSPAccesses) > 256 {
								copy(recentADSPAccesses, recentADSPAccesses[len(recentADSPAccesses)-256:])
								recentADSPAccesses = recentADSPAccesses[:256]
							}
						}
					}
					if uartTrace && access.Region == "qualcomm-boot-control" {
						for _, base := range [...]uint32{0x4000, 0x4100, 0x4200} {
							if access.Offset >= base && access.Offset < base+0x3c {
								if len(uartFirstAccesses) < 256 {
									uartFirstAccesses = append(uartFirstAccesses, access)
								}
								uartAccesses = append(uartAccesses, access)
								if len(uartAccesses) > 512 {
									copy(uartAccesses, uartAccesses[len(uartAccesses)-512:])
									uartAccesses = uartAccesses[:512]
								}
							}
							if access.Write && access.Offset == base+0x0c {
								uartBytes[base] = append(uartBytes[base], byte(access.Value))
							}
						}
					}
					if sbiTrace && access.Region == "qualcomm-boot-control" && access.Write {
						for _, controller := range [...]uint32{0x5000, 0x5100, 0x5200} {
							if access.Offset != controller+0x08 {
								continue
							}
							key := sbiCommandKey{controller: controller, command: access.Value}
							summary := sbiCommands[key]
							if summary == nil {
								summary = &sbiCommandSummary{first: access}
								sbiCommands[key] = summary
							}
							summary.count++
							summary.last = access
						}
					}
					if bootEventTrace && firmwareProfile.ID == samsung.SPHW4200DC17ProfileID &&
						access.Region == "qualcomm-boot-control" && access.Write {
						switch access.Offset {
						case 0x0430, 0x0434, 0x04a4, 0x04a8, 0x54c4:
							t.Logf("%s boot event pc=%#08x offset=%#x value=%#08x", firmwareProfile.Model, access.Context.InstructionAddress, access.Offset, access.Value)
						}
					}
					if panelWriteTrace && access.Write {
						switch access.Region {
						case "parallel-panel-command":
							panelWriteRuns = append(panelWriteRuns, panelWriteRun{command: uint16(access.Value)})
							if len(panelWriteRuns) > 1024 {
								copy(panelWriteRuns, panelWriteRuns[len(panelWriteRuns)-1024:])
								panelWriteRuns = panelWriteRuns[:1024]
							}
						case "parallel-panel-data":
							if len(panelWriteRuns) == 0 {
								panelWriteRuns = append(panelWriteRuns, panelWriteRun{})
							}
							run := &panelWriteRuns[len(panelWriteRuns)-1]
							run.dataWrites++
							run.lastData = uint16(access.Value)
						}
					}
					if nandCommandTrace && access.Region == "qualcomm-nand" && access.Write {
						switch access.Offset {
						case 0x0300:
							nandAddress = access.Value
						case 0x0304:
							if nandCommandDetails && (access.Value == 3 || access.Value == 4) {
								detail := nandCommandDetail{command: access.Value, address: nandAddress, access: access}
								if access.Value == 3 {
									for offset := range detail.buffer {
										value, readErr := machine.nand.Read(uint32(offset), system.Width8)
										if readErr != nil {
											t.Fatalf("read NAND diagnostic buffer at %#x: %v", offset, readErr)
										}
										detail.buffer[offset] = byte(value)
									}
								}
								nandCommandDetailLog = append(nandCommandDetailLog, detail)
							}
							commandSummary := nandCommandSummaries[access.Value]
							if commandSummary == nil {
								commandSummary = &nandCommandSummary{
									firstAddress:   nandAddress,
									minimumAddress: nandAddress,
									maximumAddress: nandAddress,
									first:          access,
								}
								nandCommandSummaries[access.Value] = commandSummary
							}
							commandSummary.count++
							if access.Value == 1 {
								nandReadMiB[nandAddress>>19]++
								if nandAddress >= 0x00580000 {
									nandSparseReads[nandAddress]++
								}
							}
							commandSummary.lastAddress = nandAddress
							commandSummary.minimumAddress = min(commandSummary.minimumAddress, nandAddress)
							commandSummary.maximumAddress = max(commandSummary.maximumAddress, nandAddress)
							commandSummary.last = access
							if access.Value == 1 && nandWatchAddress != 0 && nandAddress == nandWatchAddress {
								nandWatchHits++
								if nandWatchHits == 1 || nandWatchHits&(nandWatchHits-1) == 0 {
									stack := make([]byte, 512)
									stackAddress := access.Context.StackAddress &^ 3
									stackErr := machine.bus.ReadMemory(stackAddress, stack, cpu.PermissionRead)
									capture := nandWatchCapture{access: access, hit: nandWatchHits, stack: stack, err: stackErr}
									for offset := range capture.buffer {
										value, readErr := machine.nand.Read(uint32(offset), system.Width8)
										if readErr != nil {
											capture.err = readErr
											break
										}
										capture.buffer[offset] = byte(value)
									}
									nandWatchCaptures = append(nandWatchCaptures, capture)
								}
							}
						}
					}
					if mmioTrace {
						if access.Region == "samsung-onenand" {
							if access.Write && access.Offset >= 0x1e200 && access.Offset <= 0x1e20e {
								oneNANDAddresses[(access.Offset-0x1e200)/2] = uint16(access.Value)
							}
							if access.Write && access.Offset == 0x1e400 {
								oneNANDStartBuffer = uint16(access.Value)
							}
							if access.Write && access.Offset == 0x1e440 {
								oneNANDCommandCounts[access.Value]++
								switch access.Value {
								case 0x0000, 0x0013, 0x001a, 0x0080, 0x0094:
									detail := oneNANDCommandDetail{
										command: access.Value, startBuffer: uint32(oneNANDStartBuffer),
										addresses: oneNANDAddresses, access: access,
									}
									if firmwareProfile.ID == samsung.SPHW4200DC17ProfileID ||
										firmwareProfile.ID == samsung.SCHW320DC18ProfileID {
										var register [2]byte
										if readErr := machine.bus.Read(0x4001e480, register[:], cpu.PermissionRead); readErr == nil {
											detail.status = uint32(binary.LittleEndian.Uint16(register[:]))
										}
										if readErr := machine.bus.Read(0x4001e482, register[:], cpu.PermissionRead); readErr == nil {
											detail.interrupt = uint32(binary.LittleEndian.Uint16(register[:]))
										}
										buffer := uint32(oneNANDStartBuffer>>8) & 0xf
										mainBase := uint32(0x400)
										spareBase := uint32(0x10020)
										if buffer&4 != 0 {
											mainBase += 0x800
											spareBase += 0x40
										}
										mainBase += (buffer & 3) * 0x200
										spareBase += (buffer & 3) * 0x10
										for offset := uint32(0); offset < 32; offset += 4 {
											_ = machine.bus.Read(0x40000000+mainBase+offset, detail.main[offset:offset+4], cpu.PermissionRead)
											_ = machine.bus.Read(0x40000000+spareBase+offset, detail.spare[offset:offset+4], cpu.PermissionRead)
										}
									}
									if len(oneNANDRecent) < 256 {
										oneNANDRecent = append(oneNANDRecent, detail)
									} else {
										oneNANDRecent[oneNANDRecentNext] = detail
										oneNANDRecentNext = (oneNANDRecentNext + 1) % len(oneNANDRecent)
									}
									if access.Value == 0x001a || access.Value == 0x0080 || access.Value == 0x0094 {
										oneNANDMutations = append(oneNANDMutations, detail)
									}
								}
							}
							if !access.Write && access.Offset == 0x1e480 {
								oneNANDStatusCounts[access.Value]++
							}
						}
						summaryName := access.Region
						if firmwareProfile.ID == samsung.SPHW4200DC17ProfileID ||
							firmwareProfile.ID == samsung.SCHW320DC18ProfileID ||
							firmwareProfile.ID == samsung.SCHW340DC18ProfileID {
							switch {
							case access.Region == "qualcomm-clock-regime":
								summaryName = fmt.Sprintf("%s[%#x]", access.Region, access.Offset)
							case access.Region == "qualcomm-boot-control":
								summaryName = fmt.Sprintf("%s[%#x]", access.Region, access.Offset)
							case access.Region == "samsung-onenand" && access.Offset >= 0x1e400:
								summaryName = fmt.Sprintf("%s[%#x]", access.Region, access.Offset)
							}
						}
						summary := mmioSummaries[summaryName]
						if summary == nil {
							summary = &mmioSummary{}
							mmioSummaries[summaryName] = summary
						}
						if access.Write {
							summary.writes++
						} else {
							summary.reads++
						}
						summary.last = access
						if access.Region == "qualcomm-nand" {
							nandSummary := nandSummaries[access.Address]
							if nandSummary == nil {
								nandSummary = &mmioSummary{}
								nandSummaries[access.Address] = nandSummary
							}
							if access.Write {
								nandSummary.writes++
							} else {
								nandSummary.reads++
							}
							nandSummary.last = access
						}
					}
					if panelTrace && strings.HasPrefix(access.Region, "parallel-panel") &&
						access.Write && access.Context.Attributed {
						panelAccesses++
						key := [3]uint32{
							access.Context.InstructionAddress,
							access.Context.LinkAddress,
							access.Context.StackAddress,
						}
						callsite := panelCallsites[key]
						if callsite == nil {
							callsite = &panelCallsite{pc: key[0], link: key[1], stack: key[2]}
							panelCallsites[key] = callsite
						}
						callsite.count++
						callsite.last = panelAccesses
						if panelAccesses == panelStopAfter {
							_ = machine.backend.Stop()
						}
					}
					switch access.Region {
					case "samsung-mgp-registers":
						if access.Write {
							mgpControlWrites++
						}
					case "samsung-mgp-interface-registers":
						if access.Write {
							mgpInterfaceWrites++
						}
					case "w350-indexed-external-registers-command", "w350-indexed-external-registers-data":
						if access.Write {
							indexedWrites++
						} else {
							indexedReads++
						}
					}
				})
			}

			var oemsblEntry uint32
			var oemsblMode cpu.Mode
			expectOEMSBLTrap := firmwareProfile.ID == samsung.SCHW850CF11ProfileID ||
				firmwareProfile.ID == samsung.SCHW210CK12ProfileID ||
				firmwareProfile.ID == samsung.SCHW240CL28ProfileID ||
				firmwareProfile.ID == samsung.SCHW270CL28ProfileID ||
				firmwareProfile.ID == samsung.SCHW290CK10ProfileID ||
				firmwareProfile.ID == samsung.SCHW300DA04ProfileID ||
				firmwareProfile.ID == samsung.SCHW330CK06ProfileID ||
				firmwareProfile.ID == samsung.SCHW390CK11ProfileID ||
				firmwareProfile.ID == samsung.SCHW420CD16ProfileID ||
				firmwareProfile.ID == samsung.SCHW460CC26ProfileID
			expectOEMSBLTrap = expectOEMSBLTrap || firmwareProfile.ID == samsung.SPHW4200DC17ProfileID
			if os.Getenv("ARAM_SAMSUNG_RAW_CONTINUE_OEMSBL") != "" {
				expectOEMSBLTrap = false
			}
			var directResetEntry uint32
			var directResetMode cpu.Mode
			expectDirectResetTrap := false
			switch firmwareProfile.ID {
			case samsung.SCHW450CK10ProfileID:
				directResetEntry, directResetMode = 0xffff4000, cpu.ModeARM
				expectDirectResetTrap = true
			case samsung.SCHW599BE30ProfileID:
				directResetEntry, directResetMode = 0x00000000, cpu.ModeARM
				expectDirectResetTrap = true
			}
			if firmwareProfile.ID == samsung.SCHW850CF11ProfileID {
				oemsblEntry, oemsblMode = privateW850OEMSBLTrap(t, set, pkg, firmwareProfile)
			} else if expectOEMSBLTrap {
				oemsblEntry, oemsblMode = privateProfileBootImageTrap(t, set, pkg, firmwareProfile, "oemsbl")
			}
			if expectOEMSBLTrap || expectDirectResetTrap {
				traps, ok := machine.backend.(cpu.ExecutionTrapBackend)
				if !ok {
					t.Fatalf("%s backend has no execution-trap support", firmwareProfile.Model)
				}
				trapEntry, trapMode := oemsblEntry, oemsblMode
				if expectDirectResetTrap {
					trapEntry, trapMode = directResetEntry, directResetMode
				}
				executionTraps := []cpu.ExecutionTrap{{
					Address: trapEntry,
					Mode:    trapMode,
				}}
				if firmwareProfile.ID == samsung.SCHW210CK12ProfileID ||
					firmwareProfile.ID == samsung.SCHW270CL28ProfileID {
					board := system.SCHW270CL28BoardProfile()
					if firmwareProfile.ID == samsung.SCHW210CK12ProfileID {
						board = system.SCHW210CK12BoardProfile()
					}
					for _, call := range board.HLECalls {
						executionTraps = append(executionTraps, cpu.ExecutionTrap{
							Address: call.Address,
							Mode:    call.Mode,
						})
					}
					executionTraps = append(executionTraps, cpu.ExecutionTrap{
						Address: 0x00080000,
						Mode:    cpu.ModeARM,
					})
				}
				check(t, traps.SetExecutionTraps(executionTraps))
			}
			heapDiagnosticPatch := os.Getenv("ARAM_SAMSUNG_RAW_W340_HEAP_DIAGNOSTIC_PATCH") != ""
			if heapDiagnosticPatch {
				if firmwareProfile.ID != samsung.SCHW340DC18ProfileID {
					t.Fatal("W340 heap diagnostic patch requested for another firmware")
				}
				traps, ok := machine.backend.(cpu.ExecutionTrapBackend)
				if !ok {
					t.Fatal("selected backend has no execution-trap support")
				}
				check(t, traps.SetExecutionTraps([]cpu.ExecutionTrap{{Address: 0x00000954, Mode: cpu.ModeThumb}}))
			}
			var diagnosticTrap *cpu.ExecutionTrap
			var diagnosticExecutionTraps []cpu.ExecutionTrap
			var diagnosticTrapAfter uint64
			if text := os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP"); text != "" {
				address, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_EXECUTION_TRAP is invalid: %q", text)
				}
				mode := cpu.ModeARM
				if strings.EqualFold(os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_MODE"), "thumb") {
					mode = cpu.ModeThumb
				}
				diagnosticTrap = &cpu.ExecutionTrap{Address: uint32(address), Mode: mode}
				traps, ok := machine.backend.(cpu.ExecutionTrapBackend)
				if !ok {
					t.Fatal("selected backend has no execution-trap support")
				}
				diagnosticExecutionTraps = []cpu.ExecutionTrap{*diagnosticTrap}
				if boardProfile, ok := samsungTargetDiagnosticBoardProfile(firmwareProfile.ID); ok {
					for _, call := range boardProfile.HLECalls {
						diagnosticExecutionTraps = append(diagnosticExecutionTraps, cpu.ExecutionTrap{
							Address: call.Address,
							Mode:    call.Mode,
						})
					}
				}
				if afterText := os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_AFTER"); afterText != "" {
					diagnosticTrapAfter, parseErr = strconv.ParseUint(afterText, 0, 64)
					if parseErr != nil || diagnosticTrapAfter == 0 || diagnosticTrapAfter >= budget {
						t.Fatalf("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_AFTER is invalid: %q", afterText)
					}
					if os.Getenv("ARAM_SAMSUNG_RAW_RUN_MGP_INTERLEAVED") != "" {
						check(t, traps.SetExecutionTraps(diagnosticExecutionTraps[1:]))
					}
				} else {
					check(t, traps.SetExecutionTraps(diagnosticExecutionTraps))
				}
			}

			var initialPrimaryLowLine *uint8
			var initialPrimaryReleaseAfter uint64
			if text := os.Getenv("ARAM_SAMSUNG_RAW_INITIAL_PRIMARY_LOW_LINE"); text != "" {
				line, parseErr := strconv.ParseUint(text, 0, 8)
				if parseErr != nil || line >= 32 || machine.primaryClock == nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_INITIAL_PRIMARY_LOW_LINE is invalid: %q", text)
				}
				afterText := os.Getenv("ARAM_SAMSUNG_RAW_INITIAL_PRIMARY_RELEASE_AFTER")
				initialPrimaryReleaseAfter, parseErr = strconv.ParseUint(afterText, 0, 64)
				if parseErr != nil || initialPrimaryReleaseAfter == 0 || initialPrimaryReleaseAfter >= budget {
					t.Fatalf("ARAM_SAMSUNG_RAW_INITIAL_PRIMARY_RELEASE_AFTER is invalid: %q", afterText)
				}
				selected := uint8(line)
				initialPrimaryLowLine = &selected
				check(t, machine.primaryClock.SetInputLine(selected, false))
			}
			var diagnosticMemoryPatchAddress *uint32
			var diagnosticMemoryPatchValue byte
			var diagnosticMemoryPatchAfter uint64
			if text := os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_PATCH_ADDRESS"); text != "" {
				address, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_MEMORY_PATCH_ADDRESS is invalid: %q", text)
				}
				valueText := os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_PATCH_VALUE")
				value, parseErr := strconv.ParseUint(valueText, 0, 8)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_MEMORY_PATCH_VALUE is invalid: %q", valueText)
				}
				afterText := os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_PATCH_AFTER")
				diagnosticMemoryPatchAfter, parseErr = strconv.ParseUint(afterText, 0, 64)
				if parseErr != nil || diagnosticMemoryPatchAfter == 0 || diagnosticMemoryPatchAfter >= budget {
					t.Fatalf("ARAM_SAMSUNG_RAW_MEMORY_PATCH_AFTER is invalid: %q", afterText)
				}
				selected := uint32(address)
				diagnosticMemoryPatchAddress = &selected
				diagnosticMemoryPatchValue = byte(value)
			}

			var result cpu.Result
			if initialPrimaryLowLine != nil {
				prefix := machine.Run(context.Background(), initialPrimaryReleaseAfter)
				if prefix.Err != nil || prefix.Reason != cpu.StopBudget || prefix.Instructions != initialPrimaryReleaseAfter {
					t.Fatalf("%s primary-input pulse prefix = %+v", firmwareProfile.Model, prefix)
				}
				check(t, machine.primaryClock.SetInputLine(*initialPrimaryLowLine, true))
				result = machine.Run(context.Background(), budget-initialPrimaryReleaseAfter)
				result.Instructions += initialPrimaryReleaseAfter
			} else if diagnosticTrapAfter != 0 && os.Getenv("ARAM_SAMSUNG_RAW_RUN_MGP_INTERLEAVED") == "" &&
				os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_R0_ZERO") == "" &&
				os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_R0") == "" &&
				os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_CURRENT_TCB") == "" {
				traps := machine.backend.(cpu.ExecutionTrapBackend)
				// Keep board HLE gates active while delaying only the requested
				// diagnostic trap. Clearing every trap here bypasses boot contracts
				// and sends the prefix down a different firmware path.
				check(t, traps.SetExecutionTraps(diagnosticExecutionTraps[1:]))
				prefix := machine.Run(context.Background(), diagnosticTrapAfter)
				if prefix.Err != nil || prefix.Reason != cpu.StopBudget || prefix.Instructions != diagnosticTrapAfter {
					t.Fatalf("%s diagnostic trap prefix = %+v", firmwareProfile.Model, prefix)
				}
				check(t, traps.SetExecutionTraps(diagnosticExecutionTraps))
				result = machine.Run(context.Background(), budget-diagnosticTrapAfter)
				result.Instructions += diagnosticTrapAfter
			} else if diagnosticMemoryPatchAddress != nil {
				prefix := machine.Run(context.Background(), diagnosticMemoryPatchAfter)
				if prefix.Err != nil || prefix.Reason != cpu.StopBudget || prefix.Instructions != diagnosticMemoryPatchAfter {
					t.Fatalf("%s memory-patch prefix = %+v", firmwareProfile.Model, prefix)
				}
				check(t, machine.backend.WriteMemory(*diagnosticMemoryPatchAddress, []byte{diagnosticMemoryPatchValue}))
				result = machine.Run(context.Background(), budget-diagnosticMemoryPatchAfter)
				result.Instructions += diagnosticMemoryPatchAfter
			} else if watchAfter != 0 {
				prefix := machine.Run(context.Background(), watchAfter)
				if prefix.Err != nil || prefix.Reason != cpu.StopBudget || prefix.Instructions != watchAfter {
					t.Fatalf("%s memory-watch prefix = %+v", firmwareProfile.Model, prefix)
				}
				installMemoryWatch()
				result = machine.Run(context.Background(), budget-watchAfter)
				result.Instructions += watchAfter
			} else if diagnosticTrap != nil && (os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_R0_ZERO") != "" ||
				os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_R0") != "" ||
				os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_CURRENT_TCB") != "") {
				traps := machine.backend.(cpu.ExecutionTrapBackend)
				var expectedR0 *uint32
				if text := os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_R0"); text != "" {
					value, parseErr := strconv.ParseUint(text, 0, 32)
					if parseErr != nil {
						t.Fatalf("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_R0 is invalid: %q", text)
					}
					selected := uint32(value)
					expectedR0 = &selected
				} else if os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_R0_ZERO") != "" {
					selected := uint32(0)
					expectedR0 = &selected
				}
				var expectedCurrentTCB *uint32
				if text := os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_CURRENT_TCB"); text != "" {
					value, parseErr := strconv.ParseUint(text, 0, 32)
					if parseErr != nil {
						t.Fatalf("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_CURRENT_TCB is invalid: %q", text)
					}
					selected := uint32(value)
					expectedCurrentTCB = &selected
				}
				retired := uint64(0)
				if diagnosticTrapAfter != 0 {
					// Keep board HLE gates active while delaying only the requested
					// diagnostic trap, then apply the register/TCB filters below.
					check(t, traps.SetExecutionTraps(diagnosticExecutionTraps[1:]))
					prefix := machine.Run(context.Background(), diagnosticTrapAfter)
					if prefix.Err != nil || prefix.Reason != cpu.StopBudget || prefix.Instructions != diagnosticTrapAfter {
						t.Fatalf("%s diagnostic conditional trap prefix = %+v", firmwareProfile.Model, prefix)
					}
					retired = diagnosticTrapAfter
					check(t, traps.SetExecutionTraps(diagnosticExecutionTraps))
				}
				for retired < budget {
					part := machine.Run(context.Background(), budget-retired)
					retired += part.Instructions
					result = part
					result.Instructions = retired
					if part.Err != nil || part.Reason != cpu.StopExecutionTrap || part.PC != diagnosticTrap.Address {
						break
					}
					matches := true
					if expectedR0 != nil {
						r0, registerErr := machine.backend.ReadRegister(cpu.RegisterR0)
						check(t, registerErr)
						matches = r0 == *expectedR0
					}
					if matches && expectedCurrentTCB != nil {
						encoded := make([]byte, 4)
						check(t, machine.backend.ReadMemory(0x098c7044, encoded))
						matches = binary.LittleEndian.Uint32(encoded) == *expectedCurrentTCB
					}
					if matches {
						break
					}
					check(t, traps.SetExecutionTraps(nil))
					step := machine.Run(context.Background(), 1)
					retired += step.Instructions
					if step.Err != nil || step.Reason != cpu.StopBudget || step.Instructions != 1 {
						t.Fatalf("%s diagnostic conditional trap step = %+v", firmwareProfile.Model, step)
					}
					check(t, traps.SetExecutionTraps(diagnosticExecutionTraps))
				}
			} else if text := os.Getenv("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_HITS"); text != "" {
				if diagnosticTrap == nil {
					t.Fatal("repeated execution-trap hits require ARAM_SAMSUNG_RAW_EXECUTION_TRAP")
				}
				hits, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil || hits == 0 {
					t.Fatalf("ARAM_SAMSUNG_RAW_EXECUTION_TRAP_HITS is invalid: %q", text)
				}
				traps := machine.backend.(cpu.ExecutionTrapBackend)
				var retired uint64
				for hit := uint64(0); hit < hits && retired < budget; hit++ {
					part := machine.Run(context.Background(), budget-retired)
					retired += part.Instructions
					result = part
					result.Instructions = retired
					registers := make([]uint32, 17)
					for index := range registers {
						registers[index], _ = machine.backend.ReadRegister(uint32(index))
					}
					t.Logf("%s diagnostic trap hit=%d result=%+v registers=%#x", firmwareProfile.Model, hit, result, registers)
					argument := make([]byte, 32)
					if err := machine.backend.ReadMemory(registers[cpu.RegisterR0], argument); err == nil {
						t.Logf("%s diagnostic trap hit=%d r0-memory=% x", firmwareProfile.Model, hit, argument)
					}
					if part.Err != nil || part.Reason != cpu.StopExecutionTrap || part.PC != diagnosticTrap.Address {
						break
					}
					if mmioTrace && hit+1 < hits {
						clear(mmioSummaries)
					}
					check(t, traps.SetExecutionTraps(nil))
					step := machine.Run(context.Background(), 1)
					retired += step.Instructions
					if step.Err != nil || step.Reason != cpu.StopBudget || step.Instructions != 1 {
						t.Fatalf("%s diagnostic trap step = %+v", firmwareProfile.Model, step)
					}
					check(t, traps.SetExecutionTraps(diagnosticExecutionTraps))
				}
			} else if os.Getenv("ARAM_SAMSUNG_RAW_RUN_MGP_INTERLEAVED") != "" {
				if firmwareProfile.ID != samsung.SCHW320DC18ProfileID &&
					firmwareProfile.ID != samsung.SCHW340DC18ProfileID {
					t.Fatal("interleaved MGP diagnostic requested for a firmware without a profiled companion image")
				}
				mgp := interpreter.New()
				t.Cleanup(func() { _ = mgp.Close() })
				mgpBus := newSamsungMGPDiagnosticBus(machine.bus, machine.vectoredIRQs)
				check(t, mgp.AttachSystemBus(mgpBus))
				check(t, mgp.WriteRegister(cpu.RegisterCPSR, 0x000000d3))
				mgpPC := uint32(0x00008000)
				mgpMode := cpu.ModeARM
				mgpStarted, releaseAsserted := false, false
				var readyAfter uint64
				if text := os.Getenv("ARAM_SAMSUNG_RAW_W340_READY_AFTER"); text != "" {
					var parseErr error
					readyAfter, parseErr = strconv.ParseUint(text, 0, 64)
					if parseErr != nil || readyAfter == 0 || readyAfter >= budget {
						t.Fatalf("ARAM_SAMSUNG_RAW_W340_READY_AFTER is invalid: %q", text)
					}
				}
				var readyInjections uint64
				var lsmCallbackAfter uint64
				if text := os.Getenv("ARAM_SAMSUNG_RAW_W340_LSM_CALLBACK_TABLE_AFTER"); text != "" {
					var parseErr error
					lsmCallbackAfter, parseErr = strconv.ParseUint(text, 0, 64)
					if parseErr != nil || lsmCallbackAfter >= budget {
						t.Fatalf("ARAM_SAMSUNG_RAW_W340_LSM_CALLBACK_TABLE_AFTER is invalid: %q", text)
					}
				}
				lsmCallbackInjected := false
				mgpTrace := make([]string, 0, 64)
				diagnosticTrapInstalled := diagnosticTrapAfter == 0
				var captureBackend *interpreter.Backend
				var captureAddress uint64
				var captureAfter uint64
				captureInstalled := false
				if text := os.Getenv("ARAM_SAMSUNG_RAW_CAPTURE_PC"); text != "" {
					var parseErr error
					captureAddress, parseErr = strconv.ParseUint(text, 0, 32)
					if parseErr != nil {
						t.Fatalf("ARAM_SAMSUNG_RAW_CAPTURE_PC is invalid: %q", text)
					}
					captureAfter, parseErr = strconv.ParseUint(os.Getenv("ARAM_SAMSUNG_RAW_CAPTURE_AFTER"), 0, 64)
					if parseErr != nil || captureAfter >= budget {
						t.Fatalf("ARAM_SAMSUNG_RAW_CAPTURE_AFTER is invalid")
					}
					var ok bool
					captureBackend, ok = machine.backend.(*interpreter.Backend)
					if !ok {
						t.Fatal("selected backend has no PC register capture support")
					}
				}
				for retired := uint64(0); retired < budget; {
					if lsmCallbackAfter != 0 && !lsmCallbackInjected && retired >= lsmCallbackAfter {
						var encoded [4]byte
						binary.LittleEndian.PutUint32(encoded[:], 0x0115d828)
						check(t, machine.backend.WriteMemory(0x02455dd4, encoded[:]))
						lsmCallbackInjected = true
					}
					stepLimit := uint64(4096)
					if firmwareProfile.ID == samsung.SCHW340DC18ProfileID {
						stepLimit = 256
					}
					step := min(stepLimit, budget-retired)
					if diagnosticTrapAfter != 0 && retired < diagnosticTrapAfter && retired+step > diagnosticTrapAfter {
						step = diagnosticTrapAfter - retired
					}
					if !diagnosticTrapInstalled && retired >= diagnosticTrapAfter {
						traps := machine.backend.(cpu.ExecutionTrapBackend)
						check(t, traps.SetExecutionTraps(diagnosticExecutionTraps))
						diagnosticTrapInstalled = true
					}
					if captureBackend != nil && !captureInstalled && retired >= captureAfter {
						check(t, captureBackend.SetPCRegisterCapture(uint32(captureAddress), 64))
						captureInstalled = true
					}
					if readyAfter != 0 && retired < readyAfter && retired+step > readyAfter {
						step = readyAfter - retired
					}
					part := machine.Run(context.Background(), step)
					retired += part.Instructions
					result = part
					result.Instructions = retired
					if part.Err != nil || part.Reason != cpu.StopBudget || part.Instructions != step {
						break
					}
					if readyAfter != 0 && retired >= readyAfter &&
						samsungW340MainAwaitingStartupAck(machine) && samsungW340CPUInSupervisorTask(machine) {
						if readyInjections < 5 {
							traceSamsungW340StartupAckWait(t, machine, readyInjections+1)
						}
						check(t, wakeSamsungW340MainStartupAck(machine))
						readyInjections++
					}
					var release [2]byte
					check(t, machine.bus.Read(0x9011f1ac, release[:], cpu.PermissionRead))
					releaseValue := binary.LittleEndian.Uint16(release[:])
					var ready [1]byte
					check(t, machine.bus.Read(0x9010a9e0, ready[:], cpu.PermissionRead))
					var imageVector [4]byte
					check(t, machine.bus.Read(0x90108000, imageVector[:], cpu.PermissionRead))
					releaseAsserted = releaseAsserted || releaseValue != 0 || ready[0] != 0 ||
						binary.LittleEndian.Uint32(imageVector[:]) == 0xea000012
					if !mgpStarted && releaseAsserted && releaseValue == 0 {
						mgpStarted = true
					}
					if !mgpStarted {
						continue
					}
					mgpStep := step
					if os.Getenv("ARAM_SAMSUNG_RAW_TRACE_MGP") != "" {
						mgpStep = 1
					}
					mgpResult := cpu.Result{}
					if os.Getenv("ARAM_SAMSUNG_RAW_MGP_TIMER_IRQ") != "" && mgpStep > 1 {
						status, statusErr := mgp.ReadRegister(cpu.RegisterCPSR)
						check(t, statusErr)
						if status&0x80 == 0 {
							check(t, mgpBus.setTimerInterrupt(true))
							check(t, mgp.SetInterruptLine(cpu.InterruptIRQ, true))
							entry := mgp.Run(context.Background(), mgpPC, mgpMode, 1)
							check(t, mgp.SetInterruptLine(cpu.InterruptIRQ, false))
							if entry.Err != nil || entry.Reason != cpu.StopBudget || entry.Instructions != 1 {
								t.Fatalf("%s interleaved MGP timer entry = %+v", firmwareProfile.Model, entry)
							}
							mgpPC = entry.PC
							status, statusErr = mgp.ReadRegister(cpu.RegisterCPSR)
							check(t, statusErr)
							mgpMode = cpu.ModeARM
							if status&cpu.StatusThumb != 0 {
								mgpMode = cpu.ModeThumb
							}
							mgpResult = mgp.Run(context.Background(), mgpPC, mgpMode, mgpStep-1)
							mgpResult.Instructions++
							check(t, mgpBus.setTimerInterrupt(false))
						} else {
							mgpResult = mgp.Run(context.Background(), mgpPC, mgpMode, mgpStep)
						}
					} else {
						mgpResult = mgp.Run(context.Background(), mgpPC, mgpMode, mgpStep)
					}
					if os.Getenv("ARAM_SAMSUNG_RAW_TRACE_MGP") != "" {
						registers := make([]uint32, 17)
						for index := range registers {
							registers[index], _ = mgp.ReadRegister(uint32(index))
						}
						mgpTrace = append(mgpTrace, fmt.Sprintf("%08x -> %+v regs=%#x", mgpPC, mgpResult, registers))
					}
					if mgpResult.Err != nil || mgpResult.Reason != cpu.StopBudget || mgpResult.Instructions != mgpStep {
						t.Fatalf("%s interleaved MGP run = %+v trace=%s", firmwareProfile.Model, mgpResult, strings.Join(mgpTrace, "\n"))
					}
					mgpPC = mgpResult.PC
					status, statusErr := mgp.ReadRegister(cpu.RegisterCPSR)
					check(t, statusErr)
					mgpMode = cpu.ModeARM
					if status&cpu.StatusThumb != 0 {
						mgpMode = cpu.ModeThumb
					}
				}
				mgpPixels, mgpUpdates := machine.panel.WriteCounts()
				mgpRegisters := make([]uint32, 17)
				for index := range mgpRegisters {
					mgpRegisters[index], _ = mgp.ReadRegister(uint32(index))
				}
				mgpCodeAddress := (mgpPC - min(mgpPC, 64)) &^ 3
				mgpCode := make([]byte, 256)
				_ = mgp.ReadMemory(mgpCodeAddress, mgpCode)
				t.Logf("%s interleaved MGP started=%t pc=%#08x mode=%d registers=%#x code[%#08x]=%x panel=%d/%d frame=%s ready-injections=%d accesses=%s", firmwareProfile.Model, mgpStarted, mgpPC, mgpMode, mgpRegisters, mgpCodeAddress, mgpCode, mgpPixels, mgpUpdates, machine.FrameSHA256(), readyInjections, mgpBus.accessSummary())
				if captureBackend != nil {
					for index, capture := range captureBackend.PCRegisterCaptures() {
						t.Logf("%s PC capture %d address=%#08x registers=%#x", firmwareProfile.Model, index, capture.Address, capture.Registers)
					}
				}
			} else if text := os.Getenv("ARAM_SAMSUNG_RAW_W340_READY_AFTER"); text != "" {
				if firmwareProfile.ID != samsung.SCHW340DC18ProfileID {
					t.Fatal("W340 readiness diagnostic requested for another firmware")
				}
				after, parseErr := strconv.ParseUint(text, 0, 64)
				if parseErr != nil || after == 0 || after >= budget {
					t.Fatalf("ARAM_SAMSUNG_RAW_W340_READY_AFTER is invalid: %q", text)
				}
				prefix := machine.Run(context.Background(), after)
				if prefix.Err != nil || prefix.Reason != cpu.StopBudget || prefix.Instructions != after {
					t.Fatalf("%s readiness diagnostic prefix = %+v", firmwareProfile.Model, prefix)
				}
				registers := make([]uint32, 17)
				for index := range registers {
					registers[index], _ = machine.backend.ReadRegister(uint32(index))
				}
				resumeMode := cpu.ModeARM
				resumeLink := registers[cpu.RegisterPC]
				if registers[cpu.RegisterCPSR]&cpu.StatusThumb != 0 {
					resumeMode = cpu.ModeThumb
					resumeLink |= 1
				}
				traps, ok := machine.backend.(cpu.ExecutionTrapBackend)
				if !ok {
					t.Fatal("selected backend has no execution-trap support")
				}
				check(t, traps.SetExecutionTraps([]cpu.ExecutionTrap{{
					Address: registers[cpu.RegisterPC],
					Mode:    resumeMode,
				}}))
				check(t, machine.backend.WriteRegister(cpu.RegisterR0, 0x03f4af48))
				check(t, machine.backend.WriteRegister(cpu.RegisterR1, 0x6))
				check(t, machine.backend.WriteRegister(cpu.RegisterLR, resumeLink))
				check(t, machine.backend.WriteRegister(cpu.RegisterCPSR, registers[cpu.RegisterCPSR]|cpu.StatusThumb))
				call := machine.runner.Run(context.Background(), 0x00139d0c, cpu.ModeThumb, budget-after)
				machine.instructions += call.Instructions
				machine.pc = call.PC
				if status, statusErr := machine.backend.ReadRegister(cpu.RegisterCPSR); statusErr == nil {
					machine.mode = cpu.ModeARM
					if status&cpu.StatusThumb != 0 {
						machine.mode = cpu.ModeThumb
					}
				}
				retired := after + call.Instructions
				if call.Err != nil {
					t.Fatalf("%s readiness diagnostic callback = %+v", firmwareProfile.Model, call)
				}
				check(t, traps.SetExecutionTraps(nil))
				if call.Reason == cpu.StopExecutionTrap && call.PC == registers[cpu.RegisterPC] {
					for index, value := range registers {
						check(t, machine.backend.WriteRegister(uint32(index), value))
					}
					result = machine.Run(context.Background(), budget-retired)
					result.Instructions += retired
				} else if call.Reason == cpu.StopBudget && call.Instructions == budget-after {
					result = call
					result.Instructions += after
				} else {
					t.Fatalf("%s readiness diagnostic callback = %+v", firmwareProfile.Model, call)
				}
			} else if text := os.Getenv("ARAM_SAMSUNG_RAW_RUN_SLICE"); text != "" {
				slice, parseErr := strconv.ParseUint(text, 0, 64)
				if parseErr != nil || slice == 0 {
					t.Fatalf("ARAM_SAMSUNG_RAW_RUN_SLICE is invalid: %q", text)
				}
				pcCounts := make(map[uint32]uint64)
				for retired := uint64(0); retired < budget; {
					step := min(slice, budget-retired)
					part := machine.Run(context.Background(), step)
					retired += part.Instructions
					pcCounts[part.PC]++
					result = part
					result.Instructions = retired
					if part.Err != nil || part.Reason != cpu.StopBudget || part.Instructions != step {
						break
					}
				}
				type pcCount struct {
					pc    uint32
					count uint64
				}
				counts := make([]pcCount, 0, len(pcCounts))
				for pc, count := range pcCounts {
					counts = append(counts, pcCount{pc: pc, count: count})
				}
				sort.Slice(counts, func(i, j int) bool {
					if counts[i].count != counts[j].count {
						return counts[i].count > counts[j].count
					}
					return counts[i].pc < counts[j].pc
				})
				if len(counts) > 64 {
					counts = counts[:64]
				}
				t.Logf("%s sliced PC samples=%+v", firmwareProfile.Model, counts)
			} else {
				result = machine.Run(context.Background(), budget)
			}
			if heapDiagnosticPatch {
				if result.Err != nil || result.Reason != cpu.StopExecutionTrap || result.PC != 0x00000954 {
					t.Fatalf("W340 heap diagnostic trap = %+v", result)
				}
				value := make([]byte, 4)
				check(t, machine.backend.ReadMemory(0x00000a04, value))
				if got := binary.LittleEndian.Uint32(value); got != 0x02480260 {
					t.Fatalf("W340 heap-limit diagnostic preimage = %#08x", got)
				}
				binary.LittleEndian.PutUint32(value, 0x04480260)
				check(t, machine.backend.WriteMemory(0x00000a04, value))
				traps := machine.backend.(cpu.ExecutionTrapBackend)
				check(t, traps.SetExecutionTraps([]cpu.ExecutionTrap{{Address: 0x00000976, Mode: cpu.ModeThumb}}))
				retired := result.Instructions
				compare := machine.Run(context.Background(), budget-retired)
				retired += compare.Instructions
				if compare.Err != nil || compare.Reason != cpu.StopExecutionTrap || compare.PC != 0x00000976 {
					t.Fatalf("W340 heap-limit comparison trap = %+v", compare)
				}
				r0, _ := machine.backend.ReadRegister(cpu.RegisterR0)
				r7, _ := machine.backend.ReadRegister(cpu.RegisterR7)
				t.Logf("W340 heap-limit comparison values: end=%#08x limit=%#08x", r0, r7)
				check(t, traps.SetExecutionTraps(nil))
				tail := machine.Run(context.Background(), budget-retired)
				tail.Instructions += retired
				result = tail
			}
			if watchedAddress != nil {
				var final [4]byte
				readErr := machine.bus.ReadMemory(*watchedAddress, final[:], cpu.PermissionRead)
				t.Logf("%s watched memory final address=%#08x value=%#08x err=%v", firmwareProfile.Model, *watchedAddress, binary.LittleEndian.Uint32(final[:]), readErr)
			}
			if len(watchedMemory) != 0 && (watchStop || watchStopOnAccess || watchStopOnRead) {
				registers := make([]uint32, 17)
				for index := range registers {
					registers[index], _ = machine.backend.ReadRegister(uint32(index))
				}
				t.Logf("%s watched stop registers=%#x", firmwareProfile.Model, registers)
			}
			for _, access := range watchedMemory {
				t.Logf(
					"%s watched memory pc=%#08x lr=%#08x sp=%#08x address=%#08x width=%d value=%#08x write=%t",
					firmwareProfile.Model,
					access.Context.InstructionAddress, access.Context.LinkAddress, access.Context.StackAddress,
					access.Address, access.Width, access.Value, access.Write,
				)
			}
			if len(watchedMemory) != 0 {
				stackAddress := watchedMemory[len(watchedMemory)-1].Context.StackAddress &^ 3
				stack := make([]byte, 1024)
				if stackErr := machine.backend.ReadMemory(stackAddress, stack); stackErr == nil {
					var candidates []string
					for offset := 0; offset+4 <= len(stack); offset += 4 {
						value := binary.LittleEndian.Uint32(stack[offset:])
						address := value &^ 1
						if address >= 0x00080000 && address < 0x03000000 {
							candidates = append(candidates, fmt.Sprintf("+%03x=%#08x", offset, value))
						}
					}
					t.Logf("%s watched stack %#08x code=%s", firmwareProfile.Model, stackAddress, strings.Join(candidates, ","))
				} else {
					t.Logf("%s watched stack %#08x read: %v", firmwareProfile.Model, stackAddress, stackErr)
				}
			}
			if diagnosticTrap != nil {
				registers := make([]uint32, 17)
				for index := range registers {
					registers[index], _ = machine.backend.ReadRegister(uint32(index))
				}
				if firmwareProfile.ID == samsung.SPHW4200DC17ProfileID ||
					firmwareProfile.ID == samsung.SCHW320DC18ProfileID {
					const bootControlAddress = uint32(0x80000000)
					readWord := func(offset uint32) (uint32, error) {
						data := make([]byte, 4)
						if err := machine.bus.Read(
							bootControlAddress+offset,
							data,
							cpu.PermissionRead,
						); err != nil {
							return 0, err
						}
						return binary.LittleEndian.Uint32(data), nil
					}
					var values []string
					for _, offset := range []uint32{
						0x0914, 0x0918, 0x0954, 0x0958,
						0x048c, 0x4108, 0x4110, 0x4114, 0x4138,
						0x0c0c, 0x0c34, 0x0c3c, 0x0c40,
						0x0430, 0x0434, 0x0474, 0x0478, 0x049c, 0x04a0, 0x04a8,
						0x5408, 0x54c0, 0x54c4,
					} {
						value, readErr := readWord(offset)
						values = append(values, fmt.Sprintf("%#04x=%#08x/%v", offset, value, readErr))
					}
					t.Logf("%s diagnostic UIM state %s", firmwareProfile.Model, strings.Join(values, " "))
				}
				stackAddress := registers[cpu.RegisterSP] &^ 3
				stack := make([]byte, 1024)
				if err := machine.backend.ReadMemory(stackAddress, stack); err != nil {
					t.Logf("%s diagnostic stack %#08x read: %v", firmwareProfile.Model, stackAddress, err)
				} else {
					var candidates []string
					for offset := 0; offset+4 <= len(stack); offset += 4 {
						value := binary.LittleEndian.Uint32(stack[offset:])
						address := value &^ 1
						if address >= 0x00080000 && address < 0x06000000 {
							candidates = append(candidates, fmt.Sprintf("+%03x=%#08x", offset, value))
						}
					}
					t.Logf("%s diagnostic stack %#08x code=%s", firmwareProfile.Model, stackAddress, strings.Join(candidates, ","))
				}
				if result.Err != nil || result.Reason != cpu.StopExecutionTrap || result.PC != diagnosticTrap.Address {
					t.Fatalf("%s diagnostic trap result = %+v registers=%#x", firmwareProfile.Model, result, registers)
				}
				if diagnosticTrap.Address >= 32 {
					code := make([]byte, 96)
					address := (diagnosticTrap.Address - 32) &^ 3
					if err := machine.backend.ReadMemory(address, code); err == nil {
						t.Logf("%s diagnostic trap code at %#08x = %x", firmwareProfile.Model, address, code)
					}
				}
				for _, register := range []uint32{cpu.RegisterR0, cpu.RegisterR1, cpu.RegisterR2, cpu.RegisterR3, cpu.RegisterR4, cpu.RegisterR5, cpu.RegisterR6, cpu.RegisterR7} {
					address := registers[register] &^ 3
					memory := make([]byte, 128)
					if err := machine.backend.ReadMemory(address, memory); err == nil {
						t.Logf("%s diagnostic r%d memory %#08x=%x", firmwareProfile.Model, register, address, memory)
					}
				}
				if dumpDirectory := os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_DUMP_DIR"); dumpDirectory != "" {
					check(t, os.MkdirAll(dumpDirectory, 0o755))
					pages := []uint32{diagnosticTrap.Address &^ 0xffff, registers[cpu.RegisterLR] &^ 0xffff}
					for _, text := range strings.Split(os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_DUMP_PAGES"), ",") {
						if strings.TrimSpace(text) == "" {
							continue
						}
						address, parseErr := strconv.ParseUint(strings.TrimSpace(text), 0, 32)
						if parseErr != nil {
							t.Fatalf("invalid memory dump page %q", text)
						}
						pages = append(pages, uint32(address)&^0xffff)
					}
					for _, page := range pages {
						address, size := page, uint32(0x10000)
						if page == 0x90100000 {
							address, size = 0x90108000, 0x8000
						}
						memory := make([]byte, size)
						if readErr := machine.backend.ReadMemory(page, memory); readErr != nil {
							check(t, machine.bus.ReadMemory(address, memory, cpu.PermissionRead))
						}
						path := filepath.Join(dumpDirectory, fmt.Sprintf("%08x.bin", address))
						check(t, os.WriteFile(path, memory, 0o600))
						t.Logf("saved %s diagnostic memory page %#08x to %s", firmwareProfile.Model, address, path)
					}
				}
				link := registers[cpu.RegisterLR]
				if link >= 32 {
					code := make([]byte, 96)
					address := (link - 32) &^ 3
					if err := machine.backend.ReadMemory(address, code); err == nil {
						t.Logf("%s diagnostic link code at %#08x = %x", firmwareProfile.Model, address, code)
					}
				}
				if pcHistory != nil {
					t.Logf("%s diagnostic PC history=%#x", firmwareProfile.Model, pcHistory.PCHistory())
				}
				if state, stateErr := machine.backend.SaveContext(); stateErr == nil && len(state) >= 212 {
					const abortBankOffset = 8 + (17+18)*4
					const abortSPSROffset = 8 + (17+22+3)*4
					const cp15Offset = 8 + (17+22+5)*4
					t.Logf(
						"%s diagnostic abort bank sp=%#08x lr=%#08x spsr=%#08x",
						firmwareProfile.Model,
						binary.LittleEndian.Uint32(state[abortBankOffset:]),
						binary.LittleEndian.Uint32(state[abortBankOffset+4:]),
						binary.LittleEndian.Uint32(state[abortSPSROffset:]),
					)
					control := binary.LittleEndian.Uint32(state[cp15Offset:])
					table := binary.LittleEndian.Uint32(state[cp15Offset+4:])
					domain := binary.LittleEndian.Uint32(state[cp15Offset+8:])
					dataStatus := binary.LittleEndian.Uint32(state[cp15Offset+12:])
					faultAddress := binary.LittleEndian.Uint32(state[cp15Offset+20:])
					descriptorAddress := table&0xffffc000 | (faultAddress>>20)*4
					var descriptor [4]byte
					descriptorErr := machine.bus.ReadMemory(descriptorAddress, descriptor[:], cpu.PermissionRead)
					t.Logf(
						"%s diagnostic CP15 control=%#08x ttbr=%#08x domain=%#08x dfsr=%#x far=%#08x descriptor[%#08x]=%#08x err=%v",
						firmwareProfile.Model, control, table, domain, dataStatus, faultAddress,
						descriptorAddress, binary.LittleEndian.Uint32(descriptor[:]), descriptorErr,
					)
					for _, virtual := range []uint32{0, 0x02000000, 0x03a00000, 0x03c00000, 0x03f00000, 0x04000000, 0x08000000, 0x08100000, 0x0bf00000} {
						address := table&0xffffc000 | (virtual>>20)*4
						var entry [4]byte
						entryErr := machine.bus.ReadMemory(address, entry[:], cpu.PermissionRead)
						t.Logf("%s diagnostic MMU virtual=%#08x descriptor[%#08x]=%#08x err=%v", firmwareProfile.Model, virtual, address, binary.LittleEndian.Uint32(entry[:]), entryErr)
					}
				}
				t.Logf("%s diagnostic trap result = %+v registers=%#x", firmwareProfile.Model, result, registers)
				if mmioTrace {
					names := make([]string, 0, len(mmioSummaries))
					for name := range mmioSummaries {
						names = append(names, name)
					}
					sort.Strings(names)
					for _, name := range names {
						summary := mmioSummaries[name]
						t.Logf(
							"%s diagnostic MMIO %s reads=%d writes=%d last-pc=%#08x address=%#08x value=%#08x write=%t",
							firmwareProfile.Model, name, summary.reads, summary.writes,
							summary.last.Context.InstructionAddress, summary.last.Address,
							summary.last.Value, summary.last.Write,
						)
					}
					for value, count := range oneNANDCommandCounts {
						t.Logf("%s OneNAND command=%#04x count=%d", firmwareProfile.Model, value, count)
					}
					for index, detail := range oneNANDMutations {
						t.Logf(
							"%s OneNAND mutation=%d command=%#04x pc=%#08x sa=%#04x start=%#04x status=%#04x interrupt=%#04x main=%x spare=%x",
							firmwareProfile.Model, index, detail.command, detail.access.Context.InstructionAddress,
							detail.addresses, detail.startBuffer, detail.status, detail.interrupt, detail.main, detail.spare,
						)
					}
					for index := range oneNANDRecent {
						detail := oneNANDRecent[(oneNANDRecentNext+index)%len(oneNANDRecent)]
						t.Logf(
							"%s OneNAND recent=%d command=%#04x pc=%#08x sa=%#04x start=%#04x status=%#04x interrupt=%#04x main=%x spare=%x",
							firmwareProfile.Model, index, detail.command, detail.access.Context.InstructionAddress,
							detail.addresses, detail.startBuffer, detail.status, detail.interrupt, detail.main, detail.spare,
						)
					}
				}
				if nandCommandTrace {
					commands := make([]uint32, 0, len(nandCommandSummaries))
					for command := range nandCommandSummaries {
						commands = append(commands, command)
					}
					sort.Slice(commands, func(i, j int) bool { return commands[i] < commands[j] })
					for _, command := range commands {
						summary := nandCommandSummaries[command]
						t.Logf(
							"%s diagnostic NAND command=%d count=%d address=%#08x..%#08x first=%#08x last=%#08x first-pc=%#08x last-pc=%#08x last-lr=%#08x last-sp=%#08x",
							firmwareProfile.Model, command, summary.count,
							summary.minimumAddress, summary.maximumAddress,
							summary.firstAddress, summary.lastAddress,
							summary.first.Context.InstructionAddress,
							summary.last.Context.InstructionAddress,
							summary.last.Context.LinkAddress,
							summary.last.Context.StackAddress,
						)
					}
					buckets := make([]uint32, 0, len(nandReadMiB))
					for bucket := range nandReadMiB {
						buckets = append(buckets, bucket)
					}
					sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
					for _, bucket := range buckets {
						t.Logf("%s diagnostic NAND reads flash=%#08x..%#08x count=%d", firmwareProfile.Model, bucket<<20, (bucket+1)<<20, nandReadMiB[bucket])
					}
					addresses := make([]uint32, 0, len(nandSparseReads))
					for address := range nandSparseReads {
						addresses = append(addresses, address)
					}
					sort.Slice(addresses, func(i, j int) bool { return addresses[i] < addresses[j] })
					for _, address := range addresses {
						t.Logf("%s diagnostic NAND sparse read controller=%#08x flash=%#08x count=%d", firmwareProfile.Model, address, address<<2, nandSparseReads[address])
					}
				}
				if pcMMIOTraceEnd != 0 {
					for index, access := range recentPCMMIOAccesses {
						t.Logf(
							"%s diagnostic PC-MMIO recent=%d region=%s offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
							firmwareProfile.Model, index, access.Region, access.Offset, access.Width*8,
							access.Write, access.Value, access.Context.InstructionAddress,
							access.Context.LinkAddress, access.Context.StackAddress,
						)
					}
				}
				dumpSamsungTargetMemoryRange(t, machine, firmwareProfile)
				return
			}
			if text := os.Getenv("ARAM_SAMSUNG_RAW_RUN_MGP"); text != "" {
				if firmwareProfile.ID != samsung.SCHW320DC18ProfileID &&
					firmwareProfile.ID != samsung.SCHW340DC18ProfileID {
					t.Fatal("MGP diagnostic requested for a firmware without a profiled companion image")
				}
				mgpBudget, parseErr := strconv.ParseUint(text, 0, 64)
				if parseErr != nil || mgpBudget == 0 {
					t.Fatalf("ARAM_SAMSUNG_RAW_RUN_MGP is invalid: %q", text)
				}
				mgp := interpreter.New()
				t.Cleanup(func() { _ = mgp.Close() })
				mgpBus := newSamsungMGPDiagnosticBus(machine.bus, machine.vectoredIRQs)
				check(t, mgp.AttachSystemBus(mgpBus))
				check(t, mgp.WriteRegister(cpu.RegisterCPSR, 0x000000d3))
				mgpResult := mgp.Run(context.Background(), 0x00008000, cpu.ModeARM, mgpBudget)
				registers := make([]uint32, 17)
				for index := range registers {
					registers[index], _ = mgp.ReadRegister(uint32(index))
				}
				mgpPixels, mgpUpdates := machine.panel.WriteCounts()
				t.Logf("%s MGP diagnostic result=%+v registers=%#x panel=%d/%d frame=%s", firmwareProfile.Model, mgpResult, registers, mgpPixels, mgpUpdates, machine.FrameSHA256())
			}
			if expectOEMSBLTrap {
				if result.Err != nil || result.Reason != cpu.StopExecutionTrap || result.PC != oemsblEntry {
					registers := make([]uint32, 17)
					for index := range registers {
						registers[index], _ = machine.backend.ReadRegister(uint32(index))
					}
					t.Fatalf(
						"%s QCSBL-to-OEMSBL boot result = %+v registers=%#x",
						firmwareProfile.Model, result, registers,
					)
				}
			} else if expectDirectResetTrap {
				if result.Err != nil || result.Reason != cpu.StopExecutionTrap ||
					result.PC != directResetEntry {
					registers := make([]uint32, 17)
					for index := range registers {
						registers[index], _ = machine.backend.ReadRegister(uint32(index))
					}
					t.Fatalf(
						"%s direct-reset boot result = %+v registers=%#x",
						firmwareProfile.Model,
						result,
						registers,
					)
				}
			} else if panelStopAfter != 0 || watchStop {
				if result.Reason != cpu.StopRequested {
					t.Fatalf("%s panel-stop run = %+v", firmwareProfile.Model, result)
				}
			} else if result.Err != nil || result.Reason != cpu.StopBudget ||
				result.Instructions != budget {
				registers := make([]uint32, 17)
				for index := range registers {
					registers[index], _ = machine.backend.ReadRegister(uint32(index))
				}
				code := make([]byte, 64)
				codeAddress := result.PC &^ 0x1f
				if err := machine.backend.ReadMemory(codeAddress, code); err == nil {
					t.Logf("%s fault code at %#08x = %x", firmwareProfile.Model, codeAddress, code)
				}
				if pcHistory != nil {
					t.Logf("%s fault PC history=%#x", firmwareProfile.Model, pcHistory.PCHistory())
				}
				if firmwareProfile.ID == samsung.SCHW340DC18ProfileID &&
					os.Getenv("ARAM_SAMSUNG_RAW_TRACE_REX") != "" {
					traceSamsungCurrentRexTasks(t, machine, firmwareProfile.Model)
				}
				for key, summary := range pcMMIOAccesses {
					if key.region == "qualcomm-boot-control" &&
						(key.offset == 0x0e04 || key.offset == 0x0e08) {
						t.Logf(
							"%s fault MDP-MMIO offset=%#x write=%t count=%d first-value=%#08x first-pc=%#08x last-value=%#08x last-pc=%#08x",
							firmwareProfile.Model, key.offset, key.write, summary.count,
							summary.first.Value, summary.first.Context.InstructionAddress,
							summary.last.Value, summary.last.Context.InstructionAddress,
						)
					}
				}
				faultRecentStart := len(recentPCMMIOAccesses) - 32
				if faultRecentStart < 0 {
					faultRecentStart = 0
				}
				for index, access := range recentPCMMIOAccesses[faultRecentStart:] {
					t.Logf(
						"%s fault PC-MMIO recent=%d region=%s offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
						firmwareProfile.Model, faultRecentStart+index, access.Region, access.Offset, access.Width*8,
						access.Write, access.Value, access.Context.InstructionAddress,
						access.Context.LinkAddress, access.Context.StackAddress,
					)
				}
				dumpSamsungTargetMemoryRange(t, machine, firmwareProfile)
				t.Fatalf("%s initial boot run = %+v registers=%#x", firmwareProfile.Model, result, registers)
			}
			if sequence := strings.TrimSpace(os.Getenv("ARAM_SAMSUNG_RAW_KEY_SEQUENCE")); sequence != "" {
				keyBudget := uint64(5_000_000)
				if text := os.Getenv("ARAM_SAMSUNG_RAW_KEY_BUDGET"); text != "" {
					parsed, parseErr := strconv.ParseUint(text, 0, 64)
					if parseErr != nil || parsed == 0 {
						t.Fatalf("ARAM_SAMSUNG_RAW_KEY_BUDGET is invalid: %q", text)
					}
					keyBudget = parsed
				}
				for _, id := range strings.Split(sequence, ",") {
					id = strings.TrimSpace(id)
					if id == "" {
						t.Fatalf("ARAM_SAMSUNG_RAW_KEY_SEQUENCE is invalid: %q", sequence)
					}
					setKey := func(pressed bool) error {
						if strings.HasPrefix(id, "matrix-") {
							parts := strings.Split(strings.TrimPrefix(id, "matrix-"), "-")
							if len(parts) != 2 || machine.keypad == nil {
								return fmt.Errorf("invalid diagnostic matrix key %q", id)
							}
							row, rowErr := strconv.ParseUint(parts[0], 0, 8)
							column, columnErr := strconv.ParseUint(parts[1], 0, 8)
							if rowErr != nil || columnErr != nil {
								return fmt.Errorf("invalid diagnostic matrix key %q", id)
							}
							return machine.keypad.SetMatrixKey(uint8(row), uint8(column), pressed)
						}
						return machine.SetKey(id, pressed)
					}
					check(t, setKey(true))
					pressed := machine.Run(context.Background(), keyBudget)
					if pressed.Err != nil || pressed.Reason != cpu.StopBudget || pressed.Instructions != keyBudget {
						t.Fatalf("%s key %q press run = %+v", firmwareProfile.Model, id, pressed)
					}
					check(t, setKey(false))
					released := machine.Run(context.Background(), keyBudget)
					if released.Err != nil || released.Reason != cpu.StopBudget || released.Instructions != keyBudget {
						t.Fatalf("%s key %q release run = %+v", firmwareProfile.Model, id, released)
					}
					t.Logf("%s key %q accepted: frame=%s", firmwareProfile.Model, id, machine.FrameSHA256())
				}
			}
			if sequence := strings.TrimSpace(os.Getenv("ARAM_SAMSUNG_RAW_TOUCH_SEQUENCE")); sequence != "" {
				touchBudget := uint64(5_000_000)
				if text := os.Getenv("ARAM_SAMSUNG_RAW_TOUCH_BUDGET"); text != "" {
					parsed, parseErr := strconv.ParseUint(text, 0, 64)
					if parseErr != nil || parsed == 0 {
						t.Fatalf("ARAM_SAMSUNG_RAW_TOUCH_BUDGET is invalid: %q", text)
					}
					touchBudget = parsed
				}
				traceTouch := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_TOUCH") != ""
				logTouchState := func(stage string) {
					if !traceTouch || machine.touchscreen == nil || machine.primaryClock == nil {
						return
					}
					readPrimary := func(offset uint32) uint32 {
						value, readErr := machine.primaryClock.Read(offset, system.Width32)
						if readErr != nil {
							t.Logf("%s touch trace %s primary[%#x] error: %v", firmwareProfile.Model, stage, offset, readErr)
						}
						return value
					}
					activity := machine.touchscreen.Activity()
					var enabled, pending [2]uint32
					var inService uint8
					var inServiceValid bool
					if machine.vectoredIRQs != nil {
						enabled = machine.vectoredIRQs.EnabledSourceBanks()
						pending = machine.vectoredIRQs.PendingStatusBanks()
						inService, inServiceValid = machine.vectoredIRQs.InServiceSource()
					}
					t.Logf("%s touch trace %s: gpio clear=%#x enable=%#x detect=%#x polarity=%#x status=%#x; vic enable=%#x/%#x pending=%#x/%#x service=%d/%t; activity=%+v",
						firmwareProfile.Model, stage,
						readPrimary(0x0594), readPrimary(0x05a8), readPrimary(0x05bc), readPrimary(0x05d0), readPrimary(0x05e4),
						enabled[0], enabled[1], pending[0], pending[1], inService, inServiceValid, activity)
				}
				logTouchState("before")
				for _, coordinate := range strings.Split(sequence, ",") {
					parts := strings.Split(strings.TrimSpace(coordinate), ":")
					if len(parts) != 2 {
						t.Fatalf("ARAM_SAMSUNG_RAW_TOUCH_SEQUENCE is invalid: %q", sequence)
					}
					x, xErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 0, 32)
					y, yErr := strconv.ParseInt(strings.TrimSpace(parts[1]), 0, 32)
					if xErr != nil || yErr != nil {
						t.Fatalf("ARAM_SAMSUNG_RAW_TOUCH_SEQUENCE is invalid: %q", sequence)
					}
					check(t, machine.SetTouch(int(x), int(y), true))
					pressed := machine.Run(context.Background(), touchBudget)
					if pressed.Err != nil || pressed.Reason != cpu.StopBudget || pressed.Instructions != touchBudget {
						t.Fatalf("%s touch (%d,%d) press run = %+v", firmwareProfile.Model, x, y, pressed)
					}
					logTouchState(fmt.Sprintf("pressed-%d-%d", x, y))
					check(t, machine.SetTouch(int(x), int(y), false))
					released := machine.Run(context.Background(), touchBudget)
					if released.Err != nil || released.Reason != cpu.StopBudget || released.Instructions != touchBudget {
						t.Fatalf("%s touch (%d,%d) release run = %+v", firmwareProfile.Model, x, y, released)
					}
					logTouchState(fmt.Sprintf("released-%d-%d", x, y))
					t.Logf("%s touch (%d,%d) accepted: frame=%s", firmwareProfile.Model, x, y, machine.FrameSHA256())
				}
			}

			pixels, updates := machine.panel.WriteCounts()
			if logPCHistory && pcHistory != nil {
				t.Logf("%s PC history=%#x", firmwareProfile.Model, pcHistory.PCHistory())
			}
			frameHash := machine.FrameSHA256()
			t.Logf(
				"%s initial run accepted: instructions=%d pc=%#08x panel=%d/%d frame=%s",
				firmwareProfile.Model, result.Instructions, result.PC,
				pixels, updates, frameHash,
			)
			dumpSamsungTargetMemoryRange(t, machine, firmwareProfile)
			if os.Getenv("ARAM_SAMSUNG_RAW_TRACE_IRQ") != "" && machine.vectoredIRQs != nil {
				enabled := machine.vectoredIRQs.EnabledSourceBanks()
				pending := machine.vectoredIRQs.PendingStatusBanks()
				inService, valid := machine.vectoredIRQs.InServiceSource()
				t.Logf(
					"%s IRQ diagnostic enabled=%#08x/%#08x pending=%#08x/%#08x service=%d/%t",
					firmwareProfile.Model, enabled[0], enabled[1], pending[0], pending[1], inService, valid,
				)
				state, stateErr := machine.vectoredIRQs.SaveState()
				if stateErr == nil {
					t.Logf("%s IRQ diagnostic state=%x", firmwareProfile.Model, state)
				}
				var uartStatus [4]byte
				if err := machine.bus.Read(0x80004108, uartStatus[:], cpu.PermissionRead); err == nil {
					t.Logf("%s IRQ diagnostic UIM status=%#08x", firmwareProfile.Model, binary.LittleEndian.Uint32(uartStatus[:]))
				}
			}
			if firmwareProfile.ID == samsung.SPHW4200DC17ProfileID &&
				os.Getenv("ARAM_SAMSUNG_RAW_TRACE_REX") != "" {
				traceSPHW4200RexTasks(t, machine)
			}
			if firmwareProfile.ID == samsung.SCHW340DC18ProfileID &&
				os.Getenv("ARAM_SAMSUNG_RAW_TRACE_REX") != "" {
				traceSamsungCurrentRexTasks(t, machine, firmwareProfile.Model)
			}
			// Keep SBI diagnostics visible when a target-specific display
			// assertion below fails during early peripheral discovery.
			if sbiTrace {
				keys := make([]sbiCommandKey, 0, len(sbiCommands))
				for key := range sbiCommands {
					keys = append(keys, key)
				}
				sort.Slice(keys, func(i, j int) bool {
					if keys[i].controller != keys[j].controller {
						return keys[i].controller < keys[j].controller
					}
					return keys[i].command < keys[j].command
				})
				for _, key := range keys {
					summary := sbiCommands[key]
					t.Logf(
						"%s SBI controller=%#x command=%#08x read=%t address=%#02x data=%#04x count=%d first-pc=%#08x first-lr=%#08x last-pc=%#08x last-lr=%#08x last-sp=%#08x",
						firmwareProfile.Model, key.controller, key.command, key.command>>24 == 1,
						uint8(key.command>>16), uint16(key.command), summary.count,
						summary.first.Context.InstructionAddress, summary.first.Context.LinkAddress,
						summary.last.Context.InstructionAddress, summary.last.Context.LinkAddress,
						summary.last.Context.StackAddress,
					)
				}
			}
			// Keep UART diagnostics visible even when a target-specific boot
			// assertion below fails before the normal end-of-run trace block.
			if uartTrace {
				for index, access := range uartFirstAccesses {
					t.Logf(
						"%s UART first=%d offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
						firmwareProfile.Model, index, access.Offset, access.Width*8,
						access.Write, access.Value, access.Context.InstructionAddress,
						access.Context.LinkAddress, access.Context.StackAddress,
					)
				}
				for _, base := range [...]uint32{0x4000, 0x4100, 0x4200} {
					data := uartBytes[base]
					if len(data) > 4096 {
						data = data[len(data)-4096:]
					}
					t.Logf("%s UART %#x bytes=%d tail=%q", firmwareProfile.Model, base, len(uartBytes[base]), data)
				}
			}
			if firmwareProfile.ID == samsung.SCHW350CK06ProfileID {
				manufacturing := make([]byte, 256)
				read, readErr := machine.flash.ReadAt(manufacturing, 0x019a0000)
				t.Logf("%s manufacturing block read=%d err=%v data=%x", firmwareProfile.Model, read, readErr, manufacturing)
			}
			if prefix := os.Getenv("ARAM_SAMSUNG_RAW_SAVE_MEDIA_PREFIX"); prefix != "" {
				media, saveErr := machine.SaveMedia()
				check(t, saveErr)
				check(t, os.WriteFile(prefix+".flash", media.Flash, 0o600))
				check(t, os.WriteFile(prefix+".nand", media.NAND, 0o600))
				if len(media.SecondaryFlash) != 0 {
					check(t, os.WriteFile(prefix+".secondary", media.SecondaryFlash, 0o600))
				}
				if len(media.OneNANDSpare) != 0 {
					check(t, os.WriteFile(prefix+".onenand-spare", media.OneNANDSpare, 0o600))
				}
			}
			// Preserve the framebuffer from an incomplete target run as diagnostic
			// evidence. Target-specific activity checks below may intentionally fail
			// while a newly modeled peripheral is still blocking later UI startup.
			writeSamsungTargetFrame(t, machine, firmwareProfile, "initial")
			switch firmwareProfile.ID {
			case samsung.SCHW320DC18ProfileID:
				ready := []byte{0}
				check(t, machine.backend.ReadMemory(0x9010a9e0, ready))
				if ready[0] != 1 || mgpControlWrites == 0 || mgpInterfaceWrites == 0 {
					t.Fatalf(
						"W320 MGP/LCD companion handoff = ready %#x control %d interface %d",
						ready[0], mgpControlWrites, mgpInterfaceWrites,
					)
				}
			case samsung.SCHW340DC18ProfileID, samsung.SCHW410CL10ProfileID:
				if (pixels == 0 || updates == 0) && os.Getenv("ARAM_SAMSUNG_RAW_ALLOW_INCOMPLETE_INITIAL") == "" {
					var history []uint32
					if pcHistory != nil {
						history = pcHistory.PCHistory()
					}
					t.Fatalf(
						"%s LCD writes = %d/%d result=%+v history=%#x",
						firmwareProfile.Model, pixels, updates, result, history,
					)
				}
			case samsung.SCHW350CK06ProfileID:
				manufacturingComplete := os.Getenv("ARAM_SAMSUNG_RAW_W350_MARK_PROVISIONED") != ""
				if pixels == 0 || updates == 0 || !manufacturingComplete && (indexedReads == 0 || indexedWrites == 0) {
					t.Fatalf(
						"W350 display/external-register activity = panel %d/%d indexed %d/%d",
						pixels, updates, indexedReads, indexedWrites,
					)
				}
			}
			dumpSamsungTargetMemoryRange(t, machine, firmwareProfile)
			if uartTrace {
				for _, base := range [...]uint32{0x4000, 0x4100, 0x4200} {
					data := uartBytes[base]
					if len(data) > 4096 {
						data = data[len(data)-4096:]
					}
					t.Logf("%s UART %#x bytes=%d tail=%q", firmwareProfile.Model, base, len(uartBytes[base]), data)
				}
				for index, access := range uartAccesses {
					t.Logf(
						"%s UART recent=%d offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
						firmwareProfile.Model, index, access.Offset, access.Width*8,
						access.Write, access.Value, access.Context.InstructionAddress,
						access.Context.LinkAddress, access.Context.StackAddress,
					)
				}
			}
			if adspTrace {
				keys := make([]adspAccessKey, 0, len(adspAccesses))
				for key := range adspAccesses {
					keys = append(keys, key)
				}
				sort.Slice(keys, func(i, j int) bool {
					if keys[i].region != keys[j].region {
						return keys[i].region < keys[j].region
					}
					if keys[i].offset != keys[j].offset {
						return keys[i].offset < keys[j].offset
					}
					return !keys[i].write && keys[j].write
				})
				for _, key := range keys {
					summary := adspAccesses[key]
					t.Logf(
						"%s ADSP region=%s offset=%#x write=%t count=%d first-value=%#08x first-pc=%#08x first-lr=%#08x last-value=%#08x last-pc=%#08x last-lr=%#08x",
						firmwareProfile.Model, key.region, key.offset, key.write, summary.count,
						summary.first.Value, summary.first.Context.InstructionAddress, summary.first.Context.LinkAddress,
						summary.last.Value, summary.last.Context.InstructionAddress, summary.last.Context.LinkAddress,
					)
				}
				for index, access := range recentADSPAccesses {
					t.Logf(
						"%s ADSP recent=%d region=%s offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
						firmwareProfile.Model, index, access.Region, access.Offset, access.Width*8,
						access.Write, access.Value, access.Context.InstructionAddress,
						access.Context.LinkAddress, access.Context.StackAddress,
					)
				}
			}
			if pcMMIOTraceEnd != 0 {
				if firmwareProfile.ID == samsung.SPHW4200DC17ProfileID ||
					firmwareProfile.ID == samsung.SCHW320DC18ProfileID ||
					firmwareProfile.ID == samsung.SCHW340DC18ProfileID {
					const bootControlAddress = uint32(0x80000000)
					readWord := func(offset uint32) (uint32, error) {
						data := make([]byte, 4)
						if err := machine.bus.Read(
							bootControlAddress+offset,
							data,
							cpu.PermissionRead,
						); err != nil {
							return 0, err
						}
						return binary.LittleEndian.Uint32(data), nil
					}
					irqEnable, irqEnableErr := readWord(0x0918)
					irqStatus, irqStatusErr := readWord(0x0958)
					uartMask, uartMaskErr := readWord(0x4114)
					uartStatus, uartStatusErr := readWord(0x4110)
					cpsr, cpsrErr := machine.backend.ReadRegister(cpu.RegisterCPSR)
					t.Logf(
						"%s UIM IRQ diagnostic enable=%#08x/%v status=%#08x/%v mask=%#08x/%v uart-status=%#08x/%v cpsr=%#08x/%v",
						firmwareProfile.Model,
						irqEnable, irqEnableErr,
						irqStatus, irqStatusErr,
						uartMask, uartMaskErr,
						uartStatus, uartStatusErr,
						cpsr, cpsrErr,
					)
				}
				keys := make([]adspAccessKey, 0, len(pcMMIOAccesses))
				for key := range pcMMIOAccesses {
					keys = append(keys, key)
				}
				sort.Slice(keys, func(i, j int) bool {
					if keys[i].region != keys[j].region {
						return keys[i].region < keys[j].region
					}
					if keys[i].offset != keys[j].offset {
						return keys[i].offset < keys[j].offset
					}
					return !keys[i].write && keys[j].write
				})
				for _, key := range keys {
					summary := pcMMIOAccesses[key]
					t.Logf(
						"%s PC-MMIO region=%s offset=%#x write=%t count=%d first-value=%#08x first-pc=%#08x first-lr=%#08x last-value=%#08x last-pc=%#08x last-lr=%#08x",
						firmwareProfile.Model, key.region, key.offset, key.write, summary.count,
						summary.first.Value, summary.first.Context.InstructionAddress, summary.first.Context.LinkAddress,
						summary.last.Value, summary.last.Context.InstructionAddress, summary.last.Context.LinkAddress,
					)
				}
				for index, access := range recentPCMMIOAccesses {
					t.Logf(
						"%s PC-MMIO recent=%d region=%s offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
						firmwareProfile.Model, index, access.Region, access.Offset, access.Width*8,
						access.Write, access.Value, access.Context.InstructionAddress,
						access.Context.LinkAddress, access.Context.StackAddress,
					)
				}
			}
			if clockTrace {
				for index, access := range recentClockAccesses {
					t.Logf(
						"%s clock recent=%d region=%s offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
						firmwareProfile.Model, index, access.Region, access.Offset, access.Width*8,
						access.Write, access.Value, access.Context.InstructionAddress,
						access.Context.LinkAddress, access.Context.StackAddress,
					)
				}
			}
			if mmioTrace {
				names := make([]string, 0, len(mmioSummaries))
				for name := range mmioSummaries {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					summary := mmioSummaries[name]
					t.Logf(
						"%s MMIO %s reads=%d writes=%d last-pc=%#08x address=%#08x value=%#08x write=%t",
						firmwareProfile.Model, name, summary.reads, summary.writes,
						summary.last.Context.InstructionAddress, summary.last.Address,
						summary.last.Value, summary.last.Write,
					)
				}
				for value, count := range oneNANDCommandCounts {
					t.Logf("%s OneNAND command=%#04x count=%d", firmwareProfile.Model, value, count)
				}
				for value, count := range oneNANDStatusCounts {
					t.Logf("%s OneNAND controller-status=%#04x count=%d", firmwareProfile.Model, value, count)
				}
				for index, detail := range oneNANDMutations {
					t.Logf(
						"%s OneNAND mutation=%d command=%#04x pc=%#08x sa=%#04x start=%#04x status=%#04x interrupt=%#04x main=%x spare=%x",
						firmwareProfile.Model, index, detail.command, detail.access.Context.InstructionAddress,
						detail.addresses, detail.startBuffer, detail.status, detail.interrupt, detail.main, detail.spare,
					)
				}
				for index := range oneNANDRecent {
					detail := oneNANDRecent[(oneNANDRecentNext+index)%len(oneNANDRecent)]
					t.Logf(
						"%s OneNAND recent=%d command=%#04x pc=%#08x sa=%#04x start=%#04x status=%#04x interrupt=%#04x main=%x spare=%x",
						firmwareProfile.Model, index, detail.command, detail.access.Context.InstructionAddress,
						detail.addresses, detail.startBuffer, detail.status, detail.interrupt, detail.main, detail.spare,
					)
				}
				nandAddresses := make([]uint32, 0, len(nandSummaries))
				for address := range nandSummaries {
					nandAddresses = append(nandAddresses, address)
				}
				sort.Slice(nandAddresses, func(i, j int) bool { return nandAddresses[i] < nandAddresses[j] })
				for _, address := range nandAddresses {
					summary := nandSummaries[address]
					t.Logf(
						"%s NAND address=%#08x reads=%d writes=%d last-pc=%#08x value=%#08x write=%t",
						firmwareProfile.Model, address, summary.reads, summary.writes,
						summary.last.Context.InstructionAddress, summary.last.Value, summary.last.Write,
					)
				}
			}
			if nandCommandTrace {
				for index, detail := range nandCommandDetailLog {
					t.Logf(
						"%s NAND detail=%d command=%d address=%#08x pc=%#08x data=%x spare=%x",
						firmwareProfile.Model, index, detail.command, detail.address,
						detail.access.Context.InstructionAddress,
						detail.buffer[:32], detail.buffer[0x200:],
					)
				}
				commands := make([]uint32, 0, len(nandCommandSummaries))
				for command := range nandCommandSummaries {
					commands = append(commands, command)
				}
				sort.Slice(commands, func(i, j int) bool { return commands[i] < commands[j] })
				for _, command := range commands {
					summary := nandCommandSummaries[command]
					t.Logf(
						"%s NAND command=%d count=%d address=%#08x..%#08x first=%#08x last=%#08x first-pc=%#08x last-pc=%#08x last-lr=%#08x last-sp=%#08x",
						firmwareProfile.Model, command, summary.count,
						summary.minimumAddress, summary.maximumAddress,
						summary.firstAddress, summary.lastAddress,
						summary.first.Context.InstructionAddress,
						summary.last.Context.InstructionAddress,
						summary.last.Context.LinkAddress,
						summary.last.Context.StackAddress,
					)
				}
				for _, capture := range nandWatchCaptures {
					if capture.err != nil {
						t.Logf("%s NAND watch hit=%d stack read: %v", firmwareProfile.Model, capture.hit, capture.err)
						continue
					}
					var candidates []string
					for offset := 0; offset+4 <= len(capture.stack); offset += 4 {
						value := binary.LittleEndian.Uint32(capture.stack[offset : offset+4])
						address := value &^ 1
						if address >= 0x00080000 && address < 0x06000000 {
							candidates = append(candidates, fmt.Sprintf("+%03x=%#08x", offset, value))
						}
					}
					t.Logf(
						"%s NAND watch hit=%d pc=%#08x lr=%#08x sp=%#08x data=%x spare=%x stack-code=%s",
						firmwareProfile.Model, capture.hit,
						capture.access.Context.InstructionAddress,
						capture.access.Context.LinkAddress,
						capture.access.Context.StackAddress,
						capture.buffer[:32], capture.buffer[0x200:],
						strings.Join(candidates, ","),
					)
				}
			}
			if panelTrace {
				registers := make([]uint32, 17)
				for index := range registers {
					registers[index], _ = machine.backend.ReadRegister(uint32(index))
				}
				t.Logf("%s panel trace result=%+v registers=%#x", firmwareProfile.Model, result, registers)
				interruptOffsets := []uint32{
					0x0430, 0x0434, 0x0474, 0x0478, 0x04a8,
					0x0410, 0x0484, 0x0414, 0x0488, 0x0418, 0x048c,
					0x041c, 0x0490, 0x0420, 0x0494, 0x0424, 0x0498,
				}
				interruptState := make(map[uint32]uint32, len(interruptOffsets))
				for _, offset := range interruptOffsets {
					var encoded [4]byte
					if err := machine.bus.Read(
						0x80000000+offset, encoded[:], cpu.PermissionRead,
					); err == nil {
						interruptState[offset] = binary.LittleEndian.Uint32(encoded[:])
					}
				}
				t.Logf("%s final vectored interrupt state=%#x", firmwareProfile.Model, interruptState)
				if stack := registers[cpu.RegisterSP]; stack >= 64 {
					memory := make([]byte, 256)
					address := stack - 64
					if err := machine.backend.ReadMemory(address, memory); err != nil {
						t.Logf("%s final stack %#08x read: %v", firmwareProfile.Model, stack, err)
					} else {
						for offset := 0; offset+4 <= len(memory); offset += 4 {
							value := binary.LittleEndian.Uint32(memory[offset:])
							relative := int64(address) + int64(offset) - int64(stack)
							t.Logf(
								"%s final stack %#08x[%+#x] = %#08x",
								firmwareProfile.Model, stack, relative, value,
							)
						}
					}
				}
				var historyAddresses []uint32
				if pcHistory != nil {
					historyAddresses = pcHistory.PCHistory()
					t.Logf("%s panel PC history=%#x", firmwareProfile.Model, historyAddresses)
				}
				if dumpDirectory := os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_DUMP_DIR"); dumpDirectory != "" {
					check(t, os.MkdirAll(dumpDirectory, 0o755))
					pages := make(map[uint32]bool)
					addresses := append(append([]uint32(nil), registers...), historyAddresses...)
					for _, address := range addresses {
						page := (address &^ 1) &^ 0xffff
						if pages[page] || page < 0x00080000 || page >= 0x06000000 {
							continue
						}
						pages[page] = true
						memory := make([]byte, 0x10000)
						if err := machine.backend.ReadMemory(page, memory); err != nil {
							t.Logf("%s memory page %#08x read: %v", firmwareProfile.Model, page, err)
							continue
						}
						path := filepath.Join(dumpDirectory, fmt.Sprintf("%08x.bin", page))
						check(t, os.WriteFile(path, memory, 0o600))
						t.Logf("saved %s memory page %#08x to %s", firmwareProfile.Model, page, path)
					}
					for _, text := range strings.Split(os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_DUMP_PAGES"), ",") {
						if strings.TrimSpace(text) == "" {
							continue
						}
						address, parseErr := strconv.ParseUint(strings.TrimSpace(text), 0, 32)
						if parseErr != nil {
							t.Fatalf("invalid memory dump page %q", text)
						}
						page := uint32(address) &^ 0xffff
						if pages[page] {
							continue
						}
						pages[page] = true
						memory := make([]byte, 0x10000)
						if err := machine.backend.ReadMemory(page, memory); err != nil {
							t.Logf("%s memory page %#08x read: %v", firmwareProfile.Model, page, err)
							continue
						}
						path := filepath.Join(dumpDirectory, fmt.Sprintf("%08x.bin", page))
						check(t, os.WriteFile(path, memory, 0o600))
						t.Logf("saved %s memory page %#08x to %s", firmwareProfile.Model, page, path)
					}
				}
				callsites := make([]*panelCallsite, 0, len(panelCallsites))
				for _, callsite := range panelCallsites {
					callsites = append(callsites, callsite)
				}
				sort.Slice(callsites, func(i, j int) bool { return callsites[i].last < callsites[j].last })
				if len(callsites) > 24 {
					callsites = callsites[len(callsites)-24:]
				}
				for _, callsite := range callsites {
					t.Logf(
						"%s panel callsite pc=%#08x lr=%#08x sp=%#08x count=%d last=%d/%d",
						firmwareProfile.Model, callsite.pc, callsite.link, callsite.stack,
						callsite.count, callsite.last, panelAccesses,
					)
				}
				loggedStacks := make(map[uint32]bool)
				for index := len(callsites) - 1; index >= 0 && len(loggedStacks) < 4; index-- {
					stack := callsites[index].stack
					if loggedStacks[stack] || stack < 64 {
						continue
					}
					loggedStacks[stack] = true
					memory := make([]byte, 256)
					address := stack - 64
					if err := machine.backend.ReadMemory(address, memory); err != nil {
						t.Logf("%s panel stack %#08x read: %v", firmwareProfile.Model, stack, err)
						continue
					}
					for offset := 0; offset+4 <= len(memory); offset += 4 {
						value := binary.LittleEndian.Uint32(memory[offset:])
						codeAddress := value &^ 1
						relative := int64(address) + int64(offset) - int64(stack)
						if stack >= 0xf0000000 && relative >= 0 && relative <= 0x40 ||
							codeAddress >= 0x00080000 && codeAddress < 0x06000000 {
							t.Logf(
								"%s panel stack %#08x[%+#x] = %#08x",
								firmwareProfile.Model, stack, relative, value,
							)
						}
					}
				}
			}
			if panelWriteTrace {
				for index, run := range panelWriteRuns {
					t.Logf(
						"%s panel write run=%d command=%#04x data=%d last=%#04x",
						firmwareProfile.Model, index, run.command, run.dataWrites, run.lastData,
					)
				}
			}
			t.Logf(
				"%s %s initial boot accepted: instructions=%d pc=%#08x panel=%d/%d frame=%s",
				firmwareProfile.Model, firmwareProfile.Build, result.Instructions, result.PC, pixels, updates,
				frameHash,
			)
			if nandCommandDetails && firmwareProfile.ID == samsung.SCHW340DC18ProfileID {
				data := make([]byte, 64)
				read, readErr := machine.flash.ReadAt(data, 0x0ffe0000)
				t.Logf("%s RF backup media after initial read=%d err=%v data=%x", firmwareProfile.Model, read, readErr, data)
			}

			if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_BOOT_BUDGET"); text != "" {
				budget, parseErr := strconv.ParseUint(text, 0, 64)
				if parseErr != nil || budget == 0 {
					t.Fatalf("ARAM_SAMSUNG_RAW_COLD_BOOT_BUDGET is invalid: %q", text)
				}
				cycles := uint64(1)
				if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_BOOT_CYCLES"); text != "" {
					cycles, parseErr = strconv.ParseUint(text, 0, 32)
					if parseErr != nil || cycles == 0 {
						t.Fatalf("ARAM_SAMSUNG_RAW_COLD_BOOT_CYCLES is invalid: %q", text)
					}
				}
				for cycle := uint64(1); cycle <= cycles; cycle++ {
					watchCaptureStart := len(nandWatchCaptures)
					coldNANDDetailStart := len(nandCommandDetailLog)
					nandWatchHits = 0
					if text := os.Getenv("ARAM_SAMSUNG_RAW_W340_RF_HEADER"); text != "" && cycle == 1 {
						if firmwareProfile.ID != samsung.SCHW340DC18ProfileID {
							t.Fatal("W340 RF header diagnostic requested for another firmware")
						}
						header, parseErr := strconv.ParseUint(text, 0, 32)
						if parseErr != nil {
							t.Fatalf("ARAM_SAMSUNG_RAW_W340_RF_HEADER is invalid: %q", text)
						}
						const blockOffset = int64(0x0ffe0000)
						block := make([]byte, samsung.EraseBlockSize)
						if read, readErr := machine.flash.ReadAt(block, blockOffset); readErr != nil || read != len(block) {
							t.Fatalf("read W340 RF diagnostic block = %d, %v", read, readErr)
						}
						binary.LittleEndian.PutUint32(block, uint32(header))
						check(t, machine.flash.EraseBlock(0x7ff))
						check(t, machine.flash.ProgramAt(block, blockOffset))
					}
					if os.Getenv("ARAM_SAMSUNG_RAW_W340_CLEAR_DOWNLOAD_MARKER") != "" && cycle == 1 {
						if firmwareProfile.ID != samsung.SCHW340DC18ProfileID {
							t.Fatal("W340 download-marker diagnostic requested for another firmware")
						}
						const blockOffset = int64(0x08800000)
						block := make([]byte, samsung.EraseBlockSize)
						if read, readErr := machine.flash.ReadAt(block, blockOffset); readErr != nil || read != len(block) {
							t.Fatalf("read W340 download-marker diagnostic block = %d, %v", read, readErr)
						}
						for index := range 12 {
							block[index] = 0xff
						}
						check(t, machine.flash.EraseBlock(0x440))
						check(t, machine.flash.ProgramAt(block, blockOffset))
					}
					check(t, machine.PowerCycle())
					if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_PRIMARY_LOW_LINE"); text != "" {
						line, lineErr := strconv.ParseUint(text, 0, 8)
						if lineErr != nil || line >= 32 || machine.primaryClock == nil {
							t.Fatalf("ARAM_SAMSUNG_RAW_COLD_PRIMARY_LOW_LINE is invalid: %q", text)
						}
						check(t, machine.primaryClock.SetInputLine(uint8(line), false))
					}
					var coldDiagnosticTrap *cpu.ExecutionTrap
					if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP"); text != "" {
						address, parseErr := strconv.ParseUint(text, 0, 32)
						if parseErr != nil {
							t.Fatalf("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP is invalid: %q", text)
						}
						mode := cpu.ModeARM
						if strings.EqualFold(os.Getenv("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP_MODE"), "thumb") {
							mode = cpu.ModeThumb
						}
						coldDiagnosticTrap = &cpu.ExecutionTrap{Address: uint32(address), Mode: mode}
						executionTraps := []cpu.ExecutionTrap{*coldDiagnosticTrap}
						if boardProfile, ok := samsungTargetDiagnosticBoardProfile(firmwareProfile.ID); ok {
							for _, call := range boardProfile.HLECalls {
								executionTraps = append(executionTraps, cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode})
							}
						}
						traps, ok := machine.backend.(cpu.ExecutionTrapBackend)
						if !ok {
							t.Fatal("selected backend has no execution-trap support")
						}
						check(t, traps.SetExecutionTraps(executionTraps))
					}
					coldResult := cpu.Result{}
					if os.Getenv("ARAM_SAMSUNG_RAW_RUN_MGP_COLD") != "" {
						coldResult = runSamsungMGPInterleavedDiagnostic(t, machine, budget)
					} else {
						coldResult = machine.Run(context.Background(), budget)
					}
					if coldDiagnosticTrap != nil {
						registers := make([]uint32, 17)
						for index := range registers {
							registers[index], _ = machine.backend.ReadRegister(uint32(index))
						}
						memory := make([]byte, 256)
						for _, address := range []uint32{coldResult.PC, registers[cpu.RegisterLR]} {
							codeAddress := (address - min(address, 64)) &^ 3
							if readErr := machine.backend.ReadMemory(codeAddress, memory); readErr == nil {
								t.Logf("%s cold boot %d diagnostic code %#08x=%x", firmwareProfile.Model, cycle, codeAddress, memory)
							}
						}
						for _, address := range []uint32{
							registers[cpu.RegisterR0], registers[cpu.RegisterR1], registers[cpu.RegisterR2], registers[cpu.RegisterR3],
							registers[cpu.RegisterR4], registers[cpu.RegisterR5], registers[cpu.RegisterR6], registers[cpu.RegisterR7], registers[cpu.RegisterSP],
						} {
							memoryAddress := (address - min(address, 64)) &^ 3
							if readErr := machine.backend.ReadMemory(memoryAddress, memory); readErr == nil {
								t.Logf("%s cold boot %d diagnostic memory %#08x=%x", firmwareProfile.Model, cycle, memoryAddress, memory)
							}
						}
						if pcHistory != nil {
							t.Logf("%s cold boot %d diagnostic PC history=%#x", firmwareProfile.Model, cycle, pcHistory.PCHistory())
						}
						if state, stateErr := machine.backend.SaveContext(); stateErr == nil && len(state) >= 212 {
							const spsrOffset = 8 + (17+22)*4
							const cp15Offset = 8 + (17+22+5)*4
							t.Logf(
								"%s cold boot %d diagnostic SPSR fiq/irq/svc/abt/und=%#08x/%#08x/%#08x/%#08x/%#08x",
								firmwareProfile.Model, cycle,
								binary.LittleEndian.Uint32(state[spsrOffset:]),
								binary.LittleEndian.Uint32(state[spsrOffset+4:]),
								binary.LittleEndian.Uint32(state[spsrOffset+8:]),
								binary.LittleEndian.Uint32(state[spsrOffset+12:]),
								binary.LittleEndian.Uint32(state[spsrOffset+16:]),
							)
							t.Logf(
								"%s cold boot %d diagnostic CP15 control=%#08x ttbr=%#08x domain=%#08x dfsr=%#x far=%#08x",
								firmwareProfile.Model, cycle,
								binary.LittleEndian.Uint32(state[cp15Offset:]),
								binary.LittleEndian.Uint32(state[cp15Offset+4:]),
								binary.LittleEndian.Uint32(state[cp15Offset+8:]),
								binary.LittleEndian.Uint32(state[cp15Offset+12:]),
								binary.LittleEndian.Uint32(state[cp15Offset+20:]),
							)
							t.Logf(
								"%s cold boot %d diagnostic FCSE PID=%#08x",
								firmwareProfile.Model,
								cycle,
								binary.LittleEndian.Uint32(state[cp15Offset+24:]),
							)
						}
						t.Logf("%s cold boot %d diagnostic trap result=%+v registers=%#x", firmwareProfile.Model, cycle, coldResult, registers)
						if firmwareProfile.ID == samsung.SCHW340DC18ProfileID &&
							os.Getenv("ARAM_SAMSUNG_RAW_TRACE_REX") != "" {
							traceSamsungCurrentRexTasks(t, machine, firmwareProfile.Model)
						}
						dumpSamsungTargetMemoryRange(t, machine, firmwareProfile)
						return
					}
					if coldResult.Err != nil || coldResult.Reason != cpu.StopBudget ||
						coldResult.Instructions != budget {
						registers := make([]uint32, 17)
						for index := range registers {
							registers[index], _ = machine.backend.ReadRegister(uint32(index))
						}
						if state, stateErr := machine.backend.SaveContext(); stateErr == nil && len(state) >= 184 {
							t.Logf(
								"%s cold boot %d CPU SPSR fiq=%#08x irq=%#08x svc=%#08x abort=%#08x undef=%#08x",
								firmwareProfile.Model, cycle,
								binary.LittleEndian.Uint32(state[164:]),
								binary.LittleEndian.Uint32(state[168:]),
								binary.LittleEndian.Uint32(state[172:]),
								binary.LittleEndian.Uint32(state[176:]),
								binary.LittleEndian.Uint32(state[180:]),
							)
						}
						stack := make([]byte, 128)
						if stackErr := machine.backend.ReadMemory(registers[cpu.RegisterSP], stack); stackErr == nil {
							t.Logf("%s cold boot %d fault stack %#08x=%x", firmwareProfile.Model, cycle, registers[cpu.RegisterSP], stack)
						}
						if pcHistory != nil {
							history := pcHistory.PCHistory()
							if len(history) > 2048 {
								history = history[len(history)-2048:]
							}
							t.Logf("%s cold boot %d fault PC history=%#x", firmwareProfile.Model, cycle, history)
						}
						if firmwareProfile.ID == samsung.SCHW340DC18ProfileID {
							traceSamsungCurrentRexTasks(t, machine, firmwareProfile.Model)
						}
						t.Fatalf("%s cold boot %d run = %+v registers=%#x", firmwareProfile.Model, cycle, coldResult, registers)
					}
					if firmwareProfile.ID == samsung.SCHW340DC18ProfileID &&
						os.Getenv("ARAM_SAMSUNG_RAW_W340_UI_SIGNAL_SWEEP") != "" {
						const sweepBudget = uint64(20_000_000)
						baseline, snapshotErr := machine.SaveSnapshot()
						check(t, snapshotErr)
						for bit := uint32(1); bit != 0; bit <<= 1 {
							if bit&0x000ce5f7 == 0 {
								continue
							}
							check(t, machine.LoadSnapshot(baseline))
							beforePixels, beforeUpdates := machine.panel.WriteCounts()
							beforeFrame := machine.FrameSHA256()
							callSamsungW340RexSetSignals(t, machine, 0x03ecfb84, bit)
							probe := machine.Run(context.Background(), sweepBudget)
							var tcb [0x20]byte
							check(t, machine.backend.ReadMemory(0x03ecfb84, tcb[:]))
							afterPixels, afterUpdates := machine.panel.WriteCounts()
							t.Logf(
								"%s UI signal sweep mask=%#08x result=%+v pending=%#08x wait=%#08x slices=%d panel=%d/%d->%d/%d frame=%s->%s",
								firmwareProfile.Model, bit, probe,
								binary.LittleEndian.Uint32(tcb[0x0c:]), binary.LittleEndian.Uint32(tcb[0x10:]),
								binary.LittleEndian.Uint32(tcb[0x18:]),
								beforePixels, beforeUpdates, afterPixels, afterUpdates,
								beforeFrame, machine.FrameSHA256(),
							)
						}
						check(t, machine.LoadSnapshot(baseline))
					}
					if os.Getenv("ARAM_SAMSUNG_RAW_REPLAY_MDP") != "" {
						if machine.mdp == nil {
							t.Fatal("cold MDP replay requested without an MDP engine")
						}
						var encoded [4]byte
						check(t, machine.bus.Read(0x80000e08, encoded[:], cpu.PermissionRead))
						pointer := binary.LittleEndian.Uint32(encoded[:])
						before := machine.FrameSHA256()
						check(t, machine.mdp.QueueScript(pointer))
						check(t, machine.mdp.Advance(0))
						t.Logf(
							"%s cold replayed MDP script at %#08x frame=%s->%s",
							firmwareProfile.Model, pointer, before, machine.FrameSHA256(),
						)
					}
					coldPixels, coldUpdates := machine.panel.WriteCounts()
					if mdpTrace {
						for _, offset := range []uint32{0x0e00, 0x0e04, 0x0e08, 0x0e0c, 0x0e24, 0x0e28} {
							var encoded [4]byte
							readErr := machine.bus.Read(0x80000000+offset, encoded[:], cpu.PermissionRead)
							t.Logf(
								"%s cold MDP register offset=%#x value=%#08x err=%v",
								firmwareProfile.Model, offset, binary.LittleEndian.Uint32(encoded[:]), readErr,
							)
						}
						keys := make([]adspAccessKey, 0, len(mdpAccesses))
						for key := range mdpAccesses {
							keys = append(keys, key)
						}
						sort.Slice(keys, func(i, j int) bool {
							if keys[i].offset != keys[j].offset {
								return keys[i].offset < keys[j].offset
							}
							return !keys[i].write && keys[j].write
						})
						for _, key := range keys {
							summary := mdpAccesses[key]
							t.Logf(
								"%s cold MDP-MMIO offset=%#x write=%t count=%d first-value=%#08x first-pc=%#08x first-lr=%#08x last-value=%#08x last-pc=%#08x last-lr=%#08x",
								firmwareProfile.Model, key.offset, key.write, summary.count,
								summary.first.Value, summary.first.Context.InstructionAddress, summary.first.Context.LinkAddress,
								summary.last.Value, summary.last.Context.InstructionAddress, summary.last.Context.LinkAddress,
							)
						}
						start := len(recentMDPAccesses) - 128
						if start < 0 {
							start = 0
						}
						for index, access := range recentMDPAccesses[start:] {
							t.Logf(
								"%s cold MDP recent=%d offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
								firmwareProfile.Model, start+index, access.Offset, access.Width*8,
								access.Write, access.Value, access.Context.InstructionAddress,
								access.Context.LinkAddress, access.Context.StackAddress,
							)
						}
					}
					if firmwareProfile.ID == samsung.SCHW340DC18ProfileID &&
						os.Getenv("ARAM_SAMSUNG_RAW_TRACE_REX") != "" {
						registers := make([]uint32, 17)
						for index := range registers {
							registers[index], _ = machine.backend.ReadRegister(uint32(index))
						}
						t.Logf("%s cold CPU registers=%#x", firmwareProfile.Model, registers)
						traceSamsungW340StartupAckWait(t, machine, cycle)
						traceSamsungCurrentRexTasks(t, machine, firmwareProfile.Model)
						for _, access := range watchedMemory {
							t.Logf(
								"%s cold watched memory pc=%#08x lr=%#08x sp=%#08x address=%#08x width=%d value=%#08x write=%t",
								firmwareProfile.Model, access.Context.InstructionAddress,
								access.Context.LinkAddress, access.Context.StackAddress,
								access.Address, access.Width, access.Value, access.Write,
							)
						}
						pending := machine.vectoredIRQs.PendingStatusBanks()
						enabled := machine.vectoredIRQs.EnabledSourceBanks()
						inService, inServiceValid := machine.vectoredIRQs.InServiceSource()
						vicState, _ := machine.vectoredIRQs.SaveState()
						t.Logf("%s cold VIC pending=%#x enabled=%#x levels=%#x/%#x in-service=%d/%t", firmwareProfile.Model, pending, enabled,
							binary.LittleEndian.Uint32(vicState[32:36]), binary.LittleEndian.Uint32(vicState[36:40]), inService, inServiceValid)
						if pcMMIOTraceEnd != 0 {
							start := len(recentPCMMIOAccesses) - 32
							if start < 0 {
								start = 0
							}
							for index, access := range recentPCMMIOAccesses[start:] {
								t.Logf("%s cold PC-MMIO recent=%d offset=%#x write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
									firmwareProfile.Model, start+index, access.Offset, access.Write, access.Value,
									access.Context.InstructionAddress, access.Context.LinkAddress, access.Context.StackAddress)
							}
						}
						if history, ok := machine.backend.(interface{ PCHistory() []uint32 }); ok {
							counts := make(map[uint32]uint64)
							for _, address := range history.PCHistory() {
								counts[address]++
							}
							type pcCount struct {
								address uint32
								count   uint64
							}
							top := make([]pcCount, 0, len(counts))
							for address, count := range counts {
								top = append(top, pcCount{address: address, count: count})
							}
							sort.Slice(top, func(i, j int) bool {
								if top[i].count == top[j].count {
									return top[i].address < top[j].address
								}
								return top[i].count > top[j].count
							})
							if len(top) > 40 {
								top = top[:40]
							}
							t.Logf("%s cold PC hot set=%+v", firmwareProfile.Model, top)
						}
					}
					if sdccTrace {
						for index, access := range recentSDCCAccesses {
							t.Logf(
								"%s cold boot %d SDCC recent=%d offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
								firmwareProfile.Model, cycle, index, access.Offset, access.Width*8,
								access.Write, access.Value, access.Context.InstructionAddress,
								access.Context.LinkAddress, access.Context.StackAddress,
							)
						}
					}
					if uartTrace {
						for _, base := range [...]uint32{0x4000, 0x4100, 0x4200} {
							data := uartBytes[base]
							if len(data) > 4096 {
								data = data[len(data)-4096:]
							}
							t.Logf(
								"%s cold boot %d UART %#x bytes=%d tail=%x",
								firmwareProfile.Model, cycle, base, len(uartBytes[base]), data,
							)
						}
						start := len(uartAccesses) - 128
						if start < 0 {
							start = 0
						}
						for index, access := range uartAccesses[start:] {
							t.Logf(
								"%s cold UART recent=%d offset=%#x width=%d write=%t value=%#08x pc=%#08x lr=%#08x sp=%#08x",
								firmwareProfile.Model, start+index, access.Offset, access.Width*8,
								access.Write, access.Value, access.Context.InstructionAddress,
								access.Context.LinkAddress, access.Context.StackAddress,
							)
						}
					}
					if panelTrace {
						callsites := make([]*panelCallsite, 0, len(panelCallsites))
						for _, callsite := range panelCallsites {
							callsites = append(callsites, callsite)
						}
						sort.Slice(callsites, func(i, j int) bool { return callsites[i].last < callsites[j].last })
						for _, callsite := range callsites {
							t.Logf(
								"%s cold panel callsite pc=%#08x lr=%#08x sp=%#08x count=%d last=%d/%d",
								firmwareProfile.Model, callsite.pc, callsite.link, callsite.stack,
								callsite.count, callsite.last, panelAccesses,
							)
						}
					}
					if firmwareProfile.ID == samsung.SCHW340DC18ProfileID &&
						os.Getenv("ARAM_SAMSUNG_RAW_W340_RESOURCE_TRACE") != "" {
						state := make([]byte, 0x100)
						check(t, machine.backend.ReadMemory(samsungW340BREWResourceOffsetsStore, state[:16]))
						t.Logf("SCH-W340 resource offsets=%x", state[:16])
						check(t, machine.backend.ReadMemory(samsungW340DisplayProviderStore, state[:16]))
						t.Logf("SCH-W340 display provider=%x", state[:16])
						framebuffer := make([]byte, 240*320*2)
						check(t, machine.backend.ReadMemory(0x02d9fa18, framebuffer))
						nonzero := 0
						for _, value := range framebuffer {
							if value != 0 {
								nonzero++
							}
						}
						t.Logf(
							"SCH-W340 physical bitmap nonzero=%d/%d sha512=%x",
							nonzero, len(framebuffer), sha512.Sum512(framebuffer),
						)
					}
					writeSamsungTargetFrame(t, machine, firmwareProfile, fmt.Sprintf("cold-%d", cycle))
					if prefix := os.Getenv("ARAM_SAMSUNG_RAW_SAVE_COLD_MEDIA_PREFIX"); prefix != "" {
						media, saveErr := machine.SaveMedia()
						check(t, saveErr)
						check(t, os.WriteFile(prefix+".flash", media.Flash, 0o600))
						check(t, os.WriteFile(prefix+".nand", media.NAND, 0o600))
						if len(media.SecondaryFlash) != 0 {
							check(t, os.WriteFile(prefix+".secondary", media.SecondaryFlash, 0o600))
						}
						if len(media.OneNANDSpare) != 0 {
							check(t, os.WriteFile(prefix+".onenand-spare", media.OneNANDSpare, 0o600))
						}
					}
					if nandCommandDetails && firmwareProfile.ID == samsung.SCHW340DC18ProfileID {
						data := make([]byte, 64)
						read, readErr := machine.flash.ReadAt(data, 0x0ffe0000)
						t.Logf("%s RF backup media after cold boot %d read=%d err=%v data=%x", firmwareProfile.Model, cycle, read, readErr, data)
						coldDetails := nandCommandDetailLog[coldNANDDetailStart:]
						t.Logf("%s cold boot %d NAND mutations=%d", firmwareProfile.Model, cycle, len(coldDetails))
						for index, detail := range coldDetails {
							if index >= 32 && index+32 < len(coldDetails) {
								continue
							}
							t.Logf(
								"%s cold boot %d NAND mutation=%d command=%d address=%#08x pc=%#08x data=%x spare=%x",
								firmwareProfile.Model, cycle, index, detail.command, detail.address,
								detail.access.Context.InstructionAddress,
								detail.buffer[:32], detail.buffer[0x200:],
							)
						}
					}
					for _, capture := range nandWatchCaptures[watchCaptureStart:] {
						t.Logf(
							"%s cold boot %d NAND watch hit=%d pc=%#08x lr=%#08x sp=%#08x data=%x spare=%x err=%v",
							firmwareProfile.Model, cycle, capture.hit,
							capture.access.Context.InstructionAddress,
							capture.access.Context.LinkAddress,
							capture.access.Context.StackAddress,
							capture.buffer[:32], capture.buffer[0x200:], capture.err,
						)
					}
					if cycle == cycles {
						dumpSamsungTargetMemoryRange(t, machine, firmwareProfile)
					}
					t.Logf(
						"%s %s cold boot %d accepted: instructions=%d pc=%#08x panel=%d/%d frame=%s",
						firmwareProfile.Model, firmwareProfile.Build, cycle, coldResult.Instructions, coldResult.PC,
						coldPixels, coldUpdates, machine.FrameSHA256(),
					)
				}
			}
		})
	}
}

func traceSPHW4200RexTasks(t *testing.T, machine *Machine) {
	t.Helper()
	focused := os.Getenv("ARAM_SAMSUNG_RAW_TRACE_REX_FOCUS") != ""
	readWord := func(address uint32) (uint32, error) {
		encoded := make([]byte, 4)
		if err := machine.backend.ReadMemory(address, encoded); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(encoded), nil
	}
	current, err := readWord(0x098c7044)
	if err != nil {
		t.Logf("SPH-W4200 REX current TCB read: %v", err)
		return
	}
	t.Logf("SPH-W4200 REX current TCB=%#08x", current)
	const bootControlAddress = uint32(0x80000000)
	var timerState []string
	for _, offset := range []uint32{
		0x0430, 0x0434, 0x0474, 0x0478, 0x049c, 0x04a0, 0x04a8,
		0x5408, 0x54c0, 0x54c4,
	} {
		value, readErr := readWord(bootControlAddress + offset)
		timerState = append(timerState, fmt.Sprintf("%#04x=%#08x/%v", offset, value, readErr))
	}
	t.Logf("SPH-W4200 REX timetick/VIC %s", strings.Join(timerState, " "))
	address := uint32(0x03c60970)
	visited := make(map[uint32]bool)
	for index := 0; address != 0 && index < 192 && !visited[address]; index++ {
		visited[address] = true
		encoded := make([]byte, 0x70)
		if err := machine.backend.ReadMemory(address, encoded); err != nil {
			t.Logf("SPH-W4200 REX task=%d TCB=%#08x read: %v", index, address, err)
			return
		}
		nameBytes := encoded[0x60:0x70]
		if end := bytes.IndexByte(nameBytes, 0); end >= 0 {
			nameBytes = nameBytes[:end]
		}
		name := strings.Map(func(character rune) rune {
			if character < 0x20 || character > 0x7e {
				return '.'
			}
			return character
		}, string(nameBytes))
		interesting := name == "UI" || name == "UIM" || name == "FS Compat T" || name == "TFS4" ||
			strings.Contains(strings.ToUpper(name), "DSP")
		if !focused || interesting || address == current {
			t.Logf(
				"SPH-W4200 REX task=%d TCB=%#08x current=%t name=%q priority=%d pending=%#08x wait=%#08x next=%#08x saved-sp=%#08x",
				index, address, address == current, name,
				binary.LittleEndian.Uint32(encoded[0x14:0x18]),
				binary.LittleEndian.Uint32(encoded[0x0c:0x10]),
				binary.LittleEndian.Uint32(encoded[0x10:0x14]),
				binary.LittleEndian.Uint32(encoded[0x24:0x28]),
				binary.LittleEndian.Uint32(encoded[0x00:0x04]),
			)
		}
		if interesting {
			savedStack := binary.LittleEndian.Uint32(encoded[0x00:0x04])
			stackSize := 0x180
			if name == "TFS4" {
				stackSize = 0x300
			}
			stack := make([]byte, stackSize)
			if err := machine.backend.ReadMemory(savedStack, stack); err != nil {
				t.Logf("SPH-W4200 REX %s stack %#08x read: %v", name, savedStack, err)
			} else {
				for offset := 0; offset < 0xa0; offset += 0x20 {
					t.Logf(
						"SPH-W4200 REX %s stack %#08x+%#x raw=%x",
						name, savedStack, offset, stack[offset:offset+0x20],
					)
				}
				for offset := 0; offset+4 <= len(stack); offset += 4 {
					value := binary.LittleEndian.Uint32(stack[offset:])
					if value >= 0x00080001 && value < 0x08000000 && value&1 != 0 {
						t.Logf(
							"SPH-W4200 REX %s stack %#08x+%#x code=%#08x",
							name, savedStack, offset, value,
						)
					}
				}
			}
		}
		address = binary.LittleEndian.Uint32(encoded[0x24:0x28])
	}
	for _, region := range []struct {
		name    string
		address uint32
		size    int
	}{
		{name: "TFS4 rex_sleep timer", address: 0x09c2f3f4, size: 0x80},
		{name: "REX timer group", address: 0x03add43c, size: 0x80},
	} {
		data := make([]byte, region.size)
		if readErr := machine.backend.ReadMemory(region.address, data); readErr != nil {
			t.Logf("SPH-W4200 %s %#08x read: %v", region.name, region.address, readErr)
			continue
		}
		t.Logf("SPH-W4200 %s %#08x=%x", region.name, region.address, data)
	}
	const commandPoolAddress = uint32(0x03984c38)
	commandPool := make([]byte, 25*0x50)
	if err := machine.backend.ReadMemory(commandPoolAddress, commandPool); err != nil {
		t.Logf("SPH-W4200 EFS command pool read: %v", err)
		return
	}
	for index := 0; index < 25; index++ {
		command := commandPool[index*0x50 : (index+1)*0x50]
		if command[4] == 0 {
			continue
		}
		t.Logf(
			"SPH-W4200 EFS command=%d address=%#08x data=%x",
			index, commandPoolAddress+uint32(index*0x50), command,
		)
		for _, offset := range []int{0x0c, 0x10} {
			pointer := binary.LittleEndian.Uint32(command[offset : offset+4])
			if pointer < 0x03800000 || pointer >= 0x0a000000 {
				continue
			}
			data := make([]byte, 0x80)
			if err := machine.backend.ReadMemory(pointer, data); err == nil {
				t.Logf(
					"SPH-W4200 EFS command=%d arg@%#x pointer=%#08x data=%x",
					index, offset, pointer, data,
				)
			}
		}
	}
	const compatibilityQueueAddress = uint32(0x03988858)
	queue := make([]byte, 12)
	if err := machine.backend.ReadMemory(compatibilityQueueAddress, queue); err != nil {
		t.Logf("SPH-W4200 EFS compatibility queue read: %v", err)
		return
	}
	t.Logf("SPH-W4200 EFS compatibility queue=%x", queue)
	link := binary.LittleEndian.Uint32(queue[0:4])
	for index := 0; index < 32 && link != 0 && link != compatibilityQueueAddress; index++ {
		data := make([]byte, 16)
		if err := machine.backend.ReadMemory(link, data); err != nil {
			t.Logf("SPH-W4200 EFS compatibility link=%d address=%#08x read: %v", index, link, err)
			break
		}
		t.Logf("SPH-W4200 EFS compatibility link=%d address=%#08x data=%x", index, link, data)
		link = binary.LittleEndian.Uint32(data[0:4])
	}
}

func traceSamsungCurrentRexTasks(t *testing.T, machine *Machine, model string) {
	t.Helper()
	if pc, err := machine.backend.ReadRegister(cpu.RegisterPC); err == nil {
		if translator, ok := machine.backend.(interface {
			PhysicalAddress(uint32, cpu.Permissions) (uint32, error)
		}); ok {
			physical, translateErr := translator.PhysicalAddress(pc&^1, cpu.PermissionExecute)
			code := make([]byte, 0x100)
			readErr := machine.bus.ReadMemory(physical&^uint32(0x3f), code, cpu.PermissionRead)
			t.Logf("%s current code virtual=%#08x physical=%#08x data=%x translate=%v read=%v", model, pc, physical, code, translateErr, readErr)
		}
	}
	if cache, ok := machine.backend.(interface {
		InstructionCacheLine(uint32) (uint32, []byte, bool)
	}); ok {
		lineAddress, line, resident := cache.InstructionCacheLine(0x00139f16)
		backing := make([]byte, 32)
		backingErr := machine.backend.ReadMemory(lineAddress, backing)
		t.Logf("%s REX wait I-cache line=%#08x resident=%t data=%x backing=%x/%v", model, lineAddress, resident, line, backing, backingErr)
	}
	vectorTable := make([]byte, 0x80)
	if vectorErr := machine.backend.ReadMemory(0, vectorTable); vectorErr == nil {
		t.Logf("%s exception vectors=%x", model, vectorTable)
	}
	if state, stateErr := machine.backend.SaveContext(); stateErr == nil && len(state) >= 212 {
		const abortBankOffset = 8 + (17+18)*4
		const abortSPSROffset = 8 + (17+22+3)*4
		const cp15Offset = 8 + (17+22+5)*4
		t.Logf(
			"%s exception state abort-sp=%#08x abort-lr=%#08x abort-spsr=%#08x control=%#08x ttbr=%#08x domain=%#08x dfsr=%#x ifsr=%#x far=%#08x fcse=%#08x",
			model,
			binary.LittleEndian.Uint32(state[abortBankOffset:]),
			binary.LittleEndian.Uint32(state[abortBankOffset+4:]),
			binary.LittleEndian.Uint32(state[abortSPSROffset:]),
			binary.LittleEndian.Uint32(state[cp15Offset:]),
			binary.LittleEndian.Uint32(state[cp15Offset+4:]),
			binary.LittleEndian.Uint32(state[cp15Offset+8:]),
			binary.LittleEndian.Uint32(state[cp15Offset+12:]),
			binary.LittleEndian.Uint32(state[cp15Offset+16:]),
			binary.LittleEndian.Uint32(state[cp15Offset+20:]),
			binary.LittleEndian.Uint32(state[cp15Offset+24:]),
		)
	}
	readWord := func(address uint32) (uint32, error) {
		var encoded [4]byte
		if err := machine.backend.ReadMemory(address, encoded[:]); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(encoded[:]), nil
	}
	if table, tableErr := readWord(0x02455dd4); tableErr == nil {
		entries := make([]byte, 0x60)
		entriesErr := machine.backend.ReadMemory(table, entries)
		t.Logf("%s LSM callback table pointer=%#08x entries=%x err=%v", model, table, entries, entriesErr)
	}
	// DC18's REX switch veneer publishes the active TCB through this retained
	// scheduler word (the literal used by the ARM switch code at 0x00145178).
	current, err := readWord(0x03c53368)
	if err != nil {
		t.Logf("%s REX current TCB read: %v", model, err)
		current = 0
	} else {
		t.Logf("%s REX current TCB=%#08x", model, current)
		encoded := make([]byte, 0x70)
		if readErr := machine.backend.ReadMemory(current, encoded); readErr == nil {
			name := encoded[0x60:0x70]
			if end := bytes.IndexByte(name, 0); end >= 0 {
				name = name[:end]
			}
			t.Logf(
				"%s REX direct-current name=%q priority=%d pending=%#08x wait=%#08x next=%#08x saved-sp=%#08x raw=%x",
				model, name,
				binary.LittleEndian.Uint32(encoded[0x14:0x18]),
				binary.LittleEndian.Uint32(encoded[0x0c:0x10]),
				binary.LittleEndian.Uint32(encoded[0x10:0x14]),
				binary.LittleEndian.Uint32(encoded[0x24:0x28]),
				binary.LittleEndian.Uint32(encoded[0x00:0x04]),
				encoded,
			)
			savedSP := binary.LittleEndian.Uint32(encoded[0x00:0x04])
			stack := make([]byte, 0x80)
			if stackErr := machine.backend.ReadMemory(savedSP, stack); stackErr == nil {
				t.Logf("%s REX direct-current stack %#08x=%x", model, savedSP, stack)
			}
		}
	}
	if schedulerCandidate, candidateErr := readWord(0x03c5336c); candidateErr == nil {
		switchTarget, switchErr := readWord(0x03c5593c)
		activeTCB, activeErr := readWord(0x03c55940)
		t.Logf(
			"%s REX scheduler candidate=%#08x switch-target=%#08x/%v active=%#08x/%v",
			model, schedulerCandidate, switchTarget, switchErr, activeTCB, activeErr,
		)
	}
	if root, rootErr := readWord(0x00ed703c); rootErr == nil {
		owner, ownerErr := readWord(root + 0x10)
		clock, clockErr := readWord(owner)
		if ownerErr == nil && clockErr == nil {
			var selector [1]byte
			selectorErr := machine.backend.ReadMemory(clock+3, selector[:])
			counterAddress := clock + 4 + uint32(selector[0])*4
			counter, counterErr := readWord(counterAddress)
			t.Logf(
				"%s MGP clock root=%#08x owner=%#08x clock=%#08x selector=%d/%v counter[%#08x]=%#08x/%v",
				model, root, owner, clock, selector[0], selectorErr, counterAddress, counter, counterErr,
			)
		}
	}
	var mgpInterface strings.Builder
	for address := uint32(0x9011f140); address < 0x9011f1e0; address += 2 {
		var encoded [2]byte
		if readErr := machine.bus.Read(address, encoded[:], cpu.PermissionRead); readErr == nil {
			fmt.Fprintf(&mgpInterface, "%02x=%04x,", address-0x9011f140, binary.LittleEndian.Uint16(encoded[:]))
		}
	}
	t.Logf("%s MGP interface %s", model, mgpInterface.String())
	for _, diagnostic := range []struct {
		name    string
		address uint32
		size    int
	}{
		{name: "MGP IRQ 32 descriptor", address: samsungW340InterruptTable + 32*0x34, size: 0x34},
		{name: "MGP IRQ 42 descriptor", address: samsungW340InterruptTable + 42*0x34, size: 0x34},
		{name: "MGP queue manager", address: 0x042b24a4, size: 0x40},
		{name: "MGP queue 5", address: 0x0435802c, size: 0x40},
		{name: "TIMER TCB", address: 0x03f30518, size: 0x30},
		{name: "REX timer anchor", address: samsungW340REXObjectListAnchor, size: 0x40},
		{name: "UIM 20-tick timer", address: 0x040302f0, size: 0x60},
		{name: "TIMETICK descriptor", address: samsungW340TimeTickInterruptEntry, size: 0x34},
	} {
		encoded := make([]byte, diagnostic.size)
		if err := machine.backend.ReadMemory(diagnostic.address, encoded); err == nil {
			t.Logf("%s REX %s %#08x=%x", model, diagnostic.name, diagnostic.address, encoded)
		}
	}
	address := uint32(0x03f4af48)
	visited := make(map[uint32]bool)
	for index := 0; address != 0 && index < 192 && !visited[address]; index++ {
		visited[address] = true
		encoded := make([]byte, 0x70)
		if err := machine.backend.ReadMemory(address, encoded); err != nil {
			t.Logf("%s REX task=%d TCB=%#08x read: %v", model, index, address, err)
			return
		}
		nameBytes := encoded[0x60:0x70]
		if end := bytes.IndexByte(nameBytes, 0); end >= 0 {
			nameBytes = nameBytes[:end]
		}
		name := strings.Map(func(character rune) rune {
			if character < 0x20 || character > 0x7e {
				return '.'
			}
			return character
		}, string(nameBytes))
		interesting := address == current || name == "UI" || name == "UIM" ||
			name == "GSDI" || name == "GSTK" || name == "GRAPH" ||
			strings.Contains(name, "MGP") ||
			strings.Contains(name, "Main") ||
			name == "FS Compat T" || name == "TFS4" ||
			strings.Contains(strings.ToUpper(name), "EFS")
		if os.Getenv("ARAM_SAMSUNG_RAW_TRACE_REX_FOCUS") == "" || interesting {
			t.Logf(
				"%s REX task=%d TCB=%#08x current=%t name=%q priority=%d pending=%#08x wait=%#08x suspended=%d slices=%d next=%#08x saved-sp=%#08x",
				model, index, address, address == current, name,
				binary.LittleEndian.Uint32(encoded[0x14:0x18]),
				binary.LittleEndian.Uint32(encoded[0x0c:0x10]),
				binary.LittleEndian.Uint32(encoded[0x10:0x14]),
				encoded[0x58],
				binary.LittleEndian.Uint32(encoded[0x08:0x0c]),
				binary.LittleEndian.Uint32(encoded[0x24:0x28]),
				binary.LittleEndian.Uint32(encoded[0x00:0x04]),
			)
		}
		if interesting {
			t.Logf("%s REX task=%d TCB=%#08x raw=%x", model, index, address, encoded)
			savedStack := binary.LittleEndian.Uint32(encoded[0x00:0x04])
			stack := make([]byte, 0x200)
			if err := machine.backend.ReadMemory(savedStack, stack); err == nil {
				if name == "UI" {
					t.Logf("%s REX task=%d stack=%#08x raw=%x", model, index, savedStack, stack[:0x80])
				}
				var codePointers []string
				for offset := 0; offset+4 <= len(stack); offset += 4 {
					value := binary.LittleEndian.Uint32(stack[offset:])
					codeAddress := value &^ 1
					if codeAddress >= 0x00080000 && codeAddress < 0x03000000 {
						codePointers = append(codePointers, fmt.Sprintf("+%03x=%#08x", offset, value))
					}
				}
				t.Logf("%s REX task=%d stack=%#08x code=%s", model, index, savedStack, strings.Join(codePointers, ","))
			}
		}
		address = binary.LittleEndian.Uint32(encoded[0x24:0x28])
	}
}

func dumpSamsungTargetMemoryRange(t *testing.T, machine *Machine, firmwareProfile samsung.BuildProfile) {
	t.Helper()
	text := os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_DUMP_RANGE")
	directory := os.Getenv("ARAM_SAMSUNG_RAW_MEMORY_DUMP_DIR")
	if text == "" || directory == "" {
		return
	}
	startText, sizeText, ok := strings.Cut(text, ",")
	if !ok {
		t.Fatalf("ARAM_SAMSUNG_RAW_MEMORY_DUMP_RANGE is invalid: %q", text)
	}
	start, startErr := strconv.ParseUint(strings.TrimSpace(startText), 0, 32)
	size, sizeErr := strconv.ParseUint(strings.TrimSpace(sizeText), 0, 32)
	if startErr != nil || sizeErr != nil || size == 0 || uint64(uint32(start))+size > 1<<32 {
		t.Fatalf("ARAM_SAMSUNG_RAW_MEMORY_DUMP_RANGE is invalid: %q", text)
	}
	memory := make([]byte, int(size))
	check(t, machine.backend.ReadMemory(uint32(start), memory))
	check(t, os.MkdirAll(directory, 0o755))
	path := filepath.Join(directory, firmwareProfile.ID+"-memory.bin")
	check(t, os.WriteFile(path, memory, 0o600))
	t.Logf("saved %s memory %#08x..%#08x to %s", firmwareProfile.Model, uint32(start), uint64(start)+size, path)
}

func writeSamsungTargetFrame(
	t *testing.T,
	machine *Machine,
	firmwareProfile samsung.BuildProfile,
	stage string,
) {
	t.Helper()
	frameDirectory := os.Getenv("ARAM_SAMSUNG_RAW_FRAME_DIR")
	if frameDirectory == "" {
		return
	}
	check(t, os.MkdirAll(frameDirectory, 0o755))
	path := filepath.Join(frameDirectory, firmwareProfile.ID+"-"+stage+".png")
	file, err := os.Create(path)
	check(t, err)
	if err := png.Encode(file, machine.Framebuffer()); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	check(t, file.Close())
	t.Logf("saved %s %s frame to %s", firmwareProfile.Model, stage, path)
}

func samsungTargetDiagnosticBoardProfile(firmwareBuildID string) (system.BoardProfile, bool) {
	switch firmwareBuildID {
	case samsung.SCHW320DC18ProfileID:
		return system.SCHW320DC18BoardProfile(), true
	case samsung.SCHW340DC18ProfileID:
		return system.SCHW340DC18BoardProfile(), true
	case samsung.SCHW350CK06ProfileID:
		return system.SCHW350CK06BoardProfile(), true
	case samsung.SCHW410CL10ProfileID:
		return system.SCHW410CL10BoardProfile(), true
	case samsung.SPHW4200DC17ProfileID:
		return system.SPHW4200DC17BoardProfile(), true
	default:
		return system.BoardProfile{}, false
	}
}

func runSamsungMGPInterleavedDiagnostic(t *testing.T, machine *Machine, budget uint64) cpu.Result {
	t.Helper()
	parseAfter := func(name string) uint64 {
		text := os.Getenv(name)
		if text == "" {
			return 0
		}
		value, err := strconv.ParseUint(text, 0, 64)
		if err != nil || value >= budget {
			t.Fatalf("%s is invalid: %q", name, text)
		}
		return value
	}
	lsmCallbackAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_LSM_CALLBACK_TABLE_AFTER")
	dispatchObjectAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_DISPATCH_OBJECT_AFTER")
	readyAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_COLD_READY_AFTER")
	if readyAfter == 0 {
		readyAfter = parseAfter("ARAM_SAMSUNG_RAW_W340_READY_AFTER")
	}
	uiSignalAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_UI_SIGNAL_AFTER")
	uiSignalMask := uint32(8)
	if text := os.Getenv("ARAM_SAMSUNG_RAW_W340_UI_SIGNAL_MASK"); text != "" {
		value, err := strconv.ParseUint(text, 0, 32)
		if err != nil || value == 0 {
			t.Fatalf("ARAM_SAMSUNG_RAW_W340_UI_SIGNAL_MASK is invalid: %q", text)
		}
		uiSignalMask = uint32(value)
	}
	fsWaitAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_FS_WAIT_AFTER")
	var coldWatchAddress uint32
	var coldWatchPC uint32
	var coldWatchValue uint32
	var coldWatchValueSet bool
	var coldWatchAfter uint64
	var coldWatchAccesses []system.MemoryAccess
	var coldWatchStacks [][]byte
	var coldWatchHLECalls []system.HLEInvocation
	var coldWatchHLEActive []bool
	var coldWatchGoStacks [][]byte
	var coldWatchBackend *interpreter.Backend
	coldWatchStop := os.Getenv("ARAM_SAMSUNG_RAW_COLD_WATCH_STOP_ON_WRITE") != ""
	coldWatchReads := os.Getenv("ARAM_SAMSUNG_RAW_COLD_WATCH_INCLUDE_READS") != ""
	if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_WATCH_ADDRESS"); text != "" {
		value, err := strconv.ParseUint(text, 0, 32)
		if err != nil {
			t.Fatalf("ARAM_SAMSUNG_RAW_COLD_WATCH_ADDRESS is invalid: %q", text)
		}
		coldWatchAddress = uint32(value)
		coldWatchAfter = parseAfter("ARAM_SAMSUNG_RAW_COLD_WATCH_AFTER")
		if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_WATCH_PC"); text != "" {
			value, err := strconv.ParseUint(text, 0, 32)
			if err != nil {
				t.Fatalf("ARAM_SAMSUNG_RAW_COLD_WATCH_PC is invalid: %q", text)
			}
			coldWatchPC = uint32(value)
		}
		if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_WATCH_VALUE"); text != "" {
			value, err := strconv.ParseUint(text, 0, 32)
			if err != nil {
				t.Fatalf("ARAM_SAMSUNG_RAW_COLD_WATCH_VALUE is invalid: %q", text)
			}
			coldWatchValue = uint32(value)
			coldWatchValueSet = true
		}
	}
	type coldMemoryWordPatch struct {
		address uint32
		value   uint32
	}
	var coldMemoryWordPatches []coldMemoryWordPatch
	coldMemoryWordPatchAfter := parseAfter("ARAM_SAMSUNG_RAW_COLD_MEMORY_WORD_PATCH_AFTER")
	if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_MEMORY_WORD_PATCHES"); text != "" {
		if coldMemoryWordPatchAfter == 0 {
			t.Fatal("ARAM_SAMSUNG_RAW_COLD_MEMORY_WORD_PATCH_AFTER is required with word patches")
		}
		for _, encoded := range strings.Split(text, ",") {
			addressText, valueText, ok := strings.Cut(strings.TrimSpace(encoded), "=")
			if !ok {
				t.Fatalf("ARAM_SAMSUNG_RAW_COLD_MEMORY_WORD_PATCHES is invalid: %q", text)
			}
			address, addressErr := strconv.ParseUint(strings.TrimSpace(addressText), 0, 32)
			value, valueErr := strconv.ParseUint(strings.TrimSpace(valueText), 0, 32)
			if addressErr != nil || valueErr != nil {
				t.Fatalf("ARAM_SAMSUNG_RAW_COLD_MEMORY_WORD_PATCHES is invalid: %q", text)
			}
			coldMemoryWordPatches = append(coldMemoryWordPatches, coldMemoryWordPatch{
				address: uint32(address),
				value:   uint32(value),
			})
		}
	}
	radioReadyAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_RADIO_READY_AFTER")
	radioInitAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_RADIO_INIT_AFTER")
	radioSettledAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_RADIO_SETTLED_AFTER")
	radioLevelAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_RADIO_LEVEL_AFTER")
	uiBootReadyAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_UI_BOOT_READY_AFTER")
	uiUpdateAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_UI_UPDATE_AFTER")
	uiRenderAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_UI_RENDER_AFTER")
	uiCounterAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_UI_COUNTER_AFTER")
	uiStateAfter := parseAfter("ARAM_SAMSUNG_RAW_W340_UI_STATE_AFTER")
	uiStateValue := byte(1)
	if text := os.Getenv("ARAM_SAMSUNG_RAW_W340_UI_STATE_VALUE"); text != "" {
		value, err := strconv.ParseUint(text, 0, 8)
		if err != nil {
			t.Fatalf("ARAM_SAMSUNG_RAW_W340_UI_STATE_VALUE is invalid: %q", text)
		}
		uiStateValue = byte(value)
	}
	uiCounterValue := byte(9)
	if text := os.Getenv("ARAM_SAMSUNG_RAW_W340_UI_COUNTER_VALUE"); text != "" {
		value, err := strconv.ParseUint(text, 0, 8)
		if err != nil {
			t.Fatalf("ARAM_SAMSUNG_RAW_W340_UI_COUNTER_VALUE is invalid: %q", text)
		}
		uiCounterValue = byte(value)
	}
	var coldTrapAddress uint32
	var coldTrapHits, coldTrapHitLimit, coldTrapAfter uint64
	if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP_HITS"); text != "" {
		value, err := strconv.ParseUint(text, 0, 32)
		if err != nil || value == 0 {
			t.Fatalf("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP_HITS is invalid: %q", text)
		}
		address, err := strconv.ParseUint(os.Getenv("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP"), 0, 32)
		if err != nil {
			t.Fatalf("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP is invalid")
		}
		coldTrapAddress = uint32(address)
		coldTrapHitLimit = value
		coldTrapAfter = parseAfter("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP_AFTER")
	}
	lsmCallbackInjected := false
	dispatchObjectInjected := false
	uiSignalInjected := false
	uiStateInjected := false
	fsWaitInjected := false
	radioReadyInjected := false
	radioInitInjected := false
	uiStateAtCheckPending := os.Getenv("ARAM_SAMSUNG_RAW_W340_UI_STATE_AT_CHECK") != ""
	uiStateAtCheckTriggered := false
	uiConditionsAtCheckPending := false
	uiConditionTracePending := make(map[uint32]bool)
	uiConditionTraceHits := make(map[uint32]int)
	uiAppTraceAddresses := make(map[uint32]bool)
	var readyInjections uint64
	var captureBackend *interpreter.Backend
	var captureAddress uint64
	var captureAfter uint64
	var captureR4 uint64
	if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_CAPTURE_PC"); text != "" {
		var parseErr error
		captureAddress, parseErr = strconv.ParseUint(text, 0, 32)
		if parseErr != nil {
			t.Fatalf("ARAM_SAMSUNG_RAW_COLD_CAPTURE_PC is invalid: %q", text)
		}
		if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_CAPTURE_AFTER"); text != "" {
			captureAfter, parseErr = strconv.ParseUint(text, 0, 64)
			if parseErr != nil || captureAfter >= budget {
				t.Fatalf("ARAM_SAMSUNG_RAW_COLD_CAPTURE_AFTER is invalid: %q", text)
			}
		}
		if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_CAPTURE_R4"); text != "" {
			captureR4, parseErr = strconv.ParseUint(text, 0, 32)
			if parseErr != nil {
				t.Fatalf("ARAM_SAMSUNG_RAW_COLD_CAPTURE_R4 is invalid: %q", text)
			}
		}
		var ok bool
		captureBackend, ok = machine.backend.(*interpreter.Backend)
		if !ok {
			t.Fatal("selected backend has no PC register capture support")
		}
	}
	mgpBus := newSamsungMGPDiagnosticBus(machine.bus, machine.vectoredIRQs)
	newMGP := func() *interpreter.Backend {
		backend := interpreter.New()
		check(t, backend.SetPCHistoryLimit(2048))
		check(t, backend.AttachSystemBus(mgpBus))
		check(t, backend.WriteRegister(cpu.RegisterCPSR, 0x000000d3))
		return backend
	}
	mgp := newMGP()
	defer func() { _ = mgp.Close() }()
	hostInterfaceWrites := make(map[uint32]samsungMGPDiagnosticAccess)
	var hostReleaseValue uint16
	mgpRestartRequested := false
	if coldWatchAddress == 0 {
		check(t, machine.bus.SetMemoryObserver(0x9011f140, 0x100, func(access system.MemoryAccess) {
			if !access.Write || !access.Context.Attributed {
				return
			}
			entry := hostInterfaceWrites[access.Address]
			entry.count++
			entry.value = access.Value
			entry.width = int(access.Width)
			entry.pc = access.Context.InstructionAddress
			hostInterfaceWrites[access.Address] = entry
			if access.Address == 0x9011f1ac && access.Width == system.Width16 {
				value := uint16(access.Value)
				if hostReleaseValue != 0 && value == 0 {
					mgpRestartRequested = true
				}
				hostReleaseValue = value
			}
		}))
		defer func() { check(t, machine.bus.SetMemoryObserver(0, 0, nil)) }()
	}
	coldTrapInstalled := coldTrapAfter == 0
	if coldTrapHitLimit != 0 && !coldTrapInstalled {
		board, ok := samsungTargetDiagnosticBoardProfile(machine.identity.FirmwareBuildID)
		if !ok {
			t.Fatal("delayed cold execution trap requires a board profile")
		}
		hleTraps := make([]cpu.ExecutionTrap, 0, len(board.HLECalls))
		for _, call := range board.HLECalls {
			hleTraps = append(hleTraps, cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode})
		}
		check(t, machine.backend.(cpu.ExecutionTrapBackend).SetExecutionTraps(hleTraps))
	}
	var uiStateAtCheckHLETraps []cpu.ExecutionTrap
	if uiStateAtCheckPending {
		board, ok := samsungTargetDiagnosticBoardProfile(machine.identity.FirmwareBuildID)
		if !ok {
			t.Fatal("W340 UI state-check diagnostic requires a board profile")
		}
		uiStateAtCheckHLETraps = make([]cpu.ExecutionTrap, 0, len(board.HLECalls))
		for _, call := range board.HLECalls {
			uiStateAtCheckHLETraps = append(uiStateAtCheckHLETraps, cpu.ExecutionTrap{
				Address: call.Address,
				Mode:    call.Mode,
			})
		}
		traps := machine.backend.(cpu.ExecutionTrapBackend)
		check(t, traps.SetExecutionTraps(append(
			[]cpu.ExecutionTrap{{Address: 0x01d8f2f0, Mode: cpu.ModeThumb}},
			uiStateAtCheckHLETraps...,
		)))
	}
	diagnosticTrapBaseline := func() []cpu.ExecutionTrap {
		traps := append([]cpu.ExecutionTrap(nil), uiStateAtCheckHLETraps...)
		if coldTrapHitLimit != 0 && coldTrapInstalled {
			mode := cpu.ModeARM
			if strings.EqualFold(os.Getenv("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP_MODE"), "thumb") {
				mode = cpu.ModeThumb
			}
			traps = append(traps, cpu.ExecutionTrap{Address: coldTrapAddress, Mode: mode})
		}
		return traps
	}
	mgpPC := uint32(0x00008000)
	mgpMode := cpu.ModeARM
	mgpStarted, releaseAsserted := false, false
	var mgpRestarts uint64
	var nextFreeRunningTimer uint64
	var mgpFatalHistory []uint32
	var mgpFatalRegisters []uint32
	var mgpFatalCode [0x80]byte
	var mgpFatalCodeErr error
	var mgpFatalDecoded string
	var mgpFatalCache string
	captureMGPFatal := func() {
		if mgpPC != 0x901097c0 || len(mgpFatalHistory) != 0 {
			return
		}
		history := mgp.PCHistory()
		if len(history) > 512 {
			history = history[len(history)-512:]
		}
		mgpFatalHistory = append([]uint32(nil), history...)
		mgpFatalRegisters = make([]uint32, 17)
		for index := range mgpFatalRegisters {
			mgpFatalRegisters[index], _ = mgp.ReadRegister(uint32(index))
		}
		mgpFatalCodeErr = mgpBus.Read(0x90109780, mgpFatalCode[:], cpu.PermissionExecute)
		seen := make(map[uint32]bool)
		var decoded strings.Builder
		for _, address := range history {
			if address < 0x90109700 || address >= 0x90109800 || seen[address] {
				continue
			}
			seen[address] = true
			if raw, ok := mgp.CachedThumbInstruction(address); ok {
				fmt.Fprintf(&decoded, "%#08x=%04x,", address, raw)
			}
		}
		mgpFatalDecoded = decoded.String()
		var cached strings.Builder
		for _, address := range []uint32{0x90109780, 0x901097a0, 0x901097c0} {
			lineAddress, line, resident := mgp.InstructionCacheLine(address)
			fmt.Fprintf(&cached, "%#08x:%#08x/%t=%x,", address, lineAddress, resident, line)
		}
		mgpFatalCache = cached.String()
	}
	result := cpu.Result{}
	coldWatchInstalled := false
	coldMemoryWordPatchesApplied := false
	defer func() {
		for index, access := range coldWatchAccesses {
			t.Logf(
				"cold delayed watch pc=%#08x instruction=%#08x lr=%#08x sp=%#08x address=%#08x width=%d value=%#08x write=%t",
				access.Context.InstructionAddress, access.Context.Instruction,
				access.Context.LinkAddress, access.Context.StackAddress,
				access.Address, access.Width, access.Value, access.Write,
			)
			if index < len(coldWatchStacks) {
				t.Logf("cold delayed watch stack=%x", coldWatchStacks[index])
			}
			if index < len(coldWatchHLECalls) && coldWatchHLEActive[index] {
				t.Logf("cold delayed watch active HLE=%+v", coldWatchHLECalls[index])
			}
			if index < len(coldWatchGoStacks) && len(coldWatchGoStacks[index]) != 0 {
				t.Logf("cold delayed watch Go stack:\n%s", coldWatchGoStacks[index])
			}
		}
		if coldWatchBackend != nil {
			history := coldWatchBackend.PCHistory()
			if len(history) > 64 {
				history = history[len(history)-64:]
			}
			t.Logf("cold delayed watch PC history=%#x", history)
			t.Logf("cold delayed watch JIT state=%+v", coldWatchBackend.JITExecutionState())
			const watchLine = uint32(0x00985aa0)
			for _, access := range coldWatchBackend.InstructionCachePrefetchHistory() {
				if access.ModifiedVirtualAddress&^uint32(31) == watchLine {
					t.Logf("cold delayed watch I-cache prefetch=%+v", access)
				}
			}
			if physical, err := coldWatchBackend.PhysicalAddress(0x00985abc, cpu.PermissionExecute); err == nil {
				physicalLine := make([]byte, 32)
				physicalLineAddress := physical &^ uint32(31)
				physicalErr := machine.bus.ReadMemory(physicalLineAddress, physicalLine, cpu.PermissionRead)
				t.Logf("cold delayed watch virtual=%#08x physical=%#08x line=%x/%v", 0x00985abc, physical, physicalLine, physicalErr)
			} else {
				t.Logf("cold delayed watch virtual=%#08x translation error=%v", 0x00985abc, err)
			}
			lineAddress, line, resident := coldWatchBackend.InstructionCacheLine(0x00985abc)
			t.Logf("cold delayed watch I-cache line=%#08x resident=%t data=%x", lineAddress, resident, line)
			if state, err := coldWatchBackend.SaveContext(); err == nil {
				const cp15Offset = 8 + (17+22+5)*4
				t.Logf(
					"cold delayed watch CP15 control=%#08x ttbr=%#08x domain=%#08x fcse=%#08x",
					binary.LittleEndian.Uint32(state[cp15Offset:]),
					binary.LittleEndian.Uint32(state[cp15Offset+4:]),
					binary.LittleEndian.Uint32(state[cp15Offset+8:]),
					binary.LittleEndian.Uint32(state[cp15Offset+24:]),
				)
			}
		}
	}()
	captureStarted := false
	var captureSamples, captureTickPending, captureIRQEnabled uint64
	var coldPCHitRangeStart, coldPCHitRangeEnd uint64
	if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_PC_RANGE"); text != "" {
		if os.Getenv("ARAM_PC_TRACE") == "" {
			t.Fatal("ARAM_SAMSUNG_RAW_COLD_PC_RANGE requires ARAM_PC_TRACE")
		}
		parts := strings.Split(text, ",")
		if len(parts) != 2 {
			t.Fatalf("ARAM_SAMSUNG_RAW_COLD_PC_RANGE is invalid: %q", text)
		}
		start, startErr := strconv.ParseUint(strings.TrimSpace(parts[0]), 0, 32)
		size, sizeErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 0, 32)
		if startErr != nil || sizeErr != nil || size == 0 || start+size > 1<<32 {
			t.Fatalf("ARAM_SAMSUNG_RAW_COLD_PC_RANGE is invalid: %q", text)
		}
		coldPCHitRangeStart = start
		coldPCHitRangeEnd = start + size
	}
	var coldPCHitAddresses []uint32
	if text := os.Getenv("ARAM_SAMSUNG_RAW_COLD_PC_HITS"); text != "" {
		if os.Getenv("ARAM_PC_TRACE") == "" {
			t.Fatal("ARAM_SAMSUNG_RAW_COLD_PC_HITS requires ARAM_PC_TRACE")
		}
		for _, field := range strings.Split(text, ",") {
			value, parseErr := strconv.ParseUint(strings.TrimSpace(field), 0, 32)
			if parseErr != nil {
				t.Fatalf("ARAM_SAMSUNG_RAW_COLD_PC_HITS is invalid: %q", text)
			}
			coldPCHitAddresses = append(coldPCHitAddresses, uint32(value))
		}
	}
	defer func() {
		if len(coldPCHitAddresses) == 0 {
			return
		}
		backend, ok := machine.backend.(*interpreter.Backend)
		if !ok {
			t.Log("cold PC hit diagnostic unavailable for selected backend")
			return
		}
		hits := backend.PCHits()
		for _, address := range coldPCHitAddresses {
			t.Logf("cold PC hit address=%#08x count=%d", address, hits[address])
		}
	}()
	defer func() {
		if coldPCHitRangeEnd == 0 {
			return
		}
		backend, ok := machine.backend.(*interpreter.Backend)
		if !ok {
			t.Log("cold PC range diagnostic unavailable for selected backend")
			return
		}
		hits := backend.PCHits()
		addresses := make([]uint32, 0)
		for address, count := range hits {
			if count != 0 && uint64(address) >= coldPCHitRangeStart && uint64(address) < coldPCHitRangeEnd {
				addresses = append(addresses, address)
			}
		}
		sort.Slice(addresses, func(left, right int) bool { return addresses[left] < addresses[right] })
		for _, address := range addresses {
			t.Logf("cold PC range hit address=%#08x count=%d", address, hits[address])
		}
	}()
	defer func() {
		if captureBackend == nil {
			return
		}
		dispatchTable := make([]byte, 32)
		if err := machine.backend.ReadMemory(0x04050e64, dispatchTable); err != nil {
			t.Logf("cold dispatch table read: %v", err)
		} else {
			t.Logf("cold dispatch table 0x04050e64=%x", dispatchTable)
			for offset := 0; offset < len(dispatchTable); offset += 4 {
				entry := binary.LittleEndian.Uint32(dispatchTable[offset:])
				if entry == 0 {
					continue
				}
				object := make([]byte, 32)
				if err := machine.backend.ReadMemory(entry, object); err == nil {
					t.Logf("cold dispatch object %d at %#08x=%x", offset/4, entry, object)
				}
			}
		}
		for _, address := range []uint32{0x001c9600, 0x001c96e0, 0x0034c800, 0x012cba40} {
			code := make([]byte, 0x100)
			if err := machine.backend.ReadMemory(address, code); err == nil {
				t.Logf("cold code %#08x=%x", address, code)
			}
		}
		patterns := map[string][]byte{
			"dispatch table": {0x64, 0x0e, 0x05, 0x04},
			"default object": {0x08, 0x3d, 0x57, 0x02},
		}
		for name, pattern := range patterns {
			var hits []uint32
			for address := uint32(0x00080000); address < 0x03000000; address += 0x10000 {
				memory := make([]byte, 0x10000)
				if err := machine.backend.ReadMemory(address, memory); err != nil {
					continue
				}
				for offset := 0; ; {
					index := bytes.Index(memory[offset:], pattern)
					if index < 0 {
						break
					}
					hits = append(hits, address+uint32(offset+index))
					offset += index + 1
				}
			}
			t.Logf("cold %s references=%#x", name, hits)
		}
		t.Logf(
			"cold capture samples=%d tick-pending=%d irq-enabled=%d",
			captureSamples, captureTickPending, captureIRQEnabled,
		)
		captures := captureBackend.PCRegisterCaptures()
		if os.Getenv("ARAM_SAMSUNG_RAW_COLD_CAPTURE_SUMMARY") != "" {
			type captureCallsite struct {
				link     uint32
				receiver uint32
			}
			counts := make(map[captureCallsite]int)
			for _, capture := range captures {
				counts[captureCallsite{
					link:     capture.Registers[cpu.RegisterLR],
					receiver: capture.Registers[cpu.RegisterR0],
				}]++
			}
			callsites := make([]captureCallsite, 0, len(counts))
			for callsite := range counts {
				callsites = append(callsites, callsite)
			}
			sort.Slice(callsites, func(i, j int) bool {
				if callsites[i].link != callsites[j].link {
					return callsites[i].link < callsites[j].link
				}
				return callsites[i].receiver < callsites[j].receiver
			})
			for _, callsite := range callsites {
				t.Logf(
					"cold PC capture callsite lr=%#08x r0=%#08x count=%d",
					callsite.link, callsite.receiver, counts[callsite],
				)
			}
		}
		matchedCaptures := 0
		for index, capture := range captures {
			if captureR4 != 0 && capture.Registers[cpu.RegisterR4] != uint32(captureR4) {
				continue
			}
			matchedCaptures++
			if os.Getenv("ARAM_SAMSUNG_RAW_COLD_CAPTURE_ALL") != "" || matchedCaptures <= 16 || index+64 >= len(captures) {
				t.Logf("cold PC capture %d address=%#08x registers=%#x", index, capture.Address, capture.Registers)
			}
		}
		t.Logf("cold PC captures total=%d matched=%d", len(captures), matchedCaptures)
		if len(captures) != 0 {
			last := captures[len(captures)-1]
			for _, register := range []uint32{cpu.RegisterR0, cpu.RegisterR1, cpu.RegisterR4, cpu.RegisterSP} {
				address := last.Registers[register]
				data := make([]byte, 0x80)
				if err := machine.backend.ReadMemory(address, data); err == nil {
					t.Logf("cold last PC capture r%d=%#08x memory=%x", register, address, data)
				}
			}
		}
		t.Logf("cold PC history=%#x", captureBackend.PCHistory())
	}()
	progressEnabled := os.Getenv("ARAM_SAMSUNG_RAW_PROGRESS") != ""
	progressNext := uint64(518_000_000)
	progressStart := time.Now()
	for retired := uint64(0); retired < budget; {
		if len(coldMemoryWordPatches) != 0 && !coldMemoryWordPatchesApplied && retired >= coldMemoryWordPatchAfter {
			var encoded [4]byte
			for _, patch := range coldMemoryWordPatches {
				binary.LittleEndian.PutUint32(encoded[:], patch.value)
				check(t, machine.backend.WriteMemory(patch.address, encoded[:]))
			}
			coldMemoryWordPatchesApplied = true
		}
		if coldWatchAddress != 0 && !coldWatchInstalled && retired >= coldWatchAfter {
			var ok bool
			coldWatchBackend, ok = machine.backend.(*interpreter.Backend)
			if !ok {
				t.Fatal("cold delayed watch requires interpreter diagnostics")
			}
			check(t, coldWatchBackend.SetPCHistoryLimit(1024))
			check(t, coldWatchBackend.SetInstructionCachePrefetchHistoryLimit(1<<20))
			check(t, machine.bus.SetMemoryObserver(coldWatchAddress, 1, func(access system.MemoryAccess) {
				if !access.Write && !coldWatchReads {
					return
				}
				coldWatchAccesses = append(coldWatchAccesses, access)
				activeCall, active := machine.runner.ActiveHLEInvocation()
				coldWatchHLECalls = append(coldWatchHLECalls, activeCall)
				coldWatchHLEActive = append(coldWatchHLEActive, active)
				goStack := []byte(nil)
				if (access.Write || coldWatchReads) &&
					(coldWatchPC == 0 || access.Context.InstructionAddress == coldWatchPC) &&
					(!coldWatchValueSet || access.Value == coldWatchValue) {
					goStack = debug.Stack()
				}
				coldWatchGoStacks = append(coldWatchGoStacks, goStack)
				stack := make([]byte, 0x400)
				if err := machine.bus.ReadMemory(access.Context.StackAddress, stack, cpu.PermissionRead); err == nil {
					coldWatchStacks = append(coldWatchStacks, stack)
				} else {
					coldWatchStacks = append(coldWatchStacks, nil)
				}
				if len(coldWatchAccesses) > 64 {
					copy(coldWatchAccesses, coldWatchAccesses[len(coldWatchAccesses)-64:])
					coldWatchAccesses = coldWatchAccesses[:64]
					copy(coldWatchStacks, coldWatchStacks[len(coldWatchStacks)-64:])
					coldWatchStacks = coldWatchStacks[:64]
					copy(coldWatchHLECalls, coldWatchHLECalls[len(coldWatchHLECalls)-64:])
					coldWatchHLECalls = coldWatchHLECalls[:64]
					copy(coldWatchHLEActive, coldWatchHLEActive[len(coldWatchHLEActive)-64:])
					coldWatchHLEActive = coldWatchHLEActive[:64]
					copy(coldWatchGoStacks, coldWatchGoStacks[len(coldWatchGoStacks)-64:])
					coldWatchGoStacks = coldWatchGoStacks[:64]
				}
				if coldWatchStop && (access.Write || coldWatchReads) &&
					(coldWatchPC == 0 || access.Context.InstructionAddress == coldWatchPC) &&
					(!coldWatchValueSet || access.Value == coldWatchValue) {
					_ = machine.backend.Stop()
				}
			}))
			coldWatchInstalled = true
		}
		if coldTrapHitLimit != 0 && !coldTrapInstalled && retired >= coldTrapAfter {
			board, ok := samsungTargetDiagnosticBoardProfile(machine.identity.FirmwareBuildID)
			if !ok {
				t.Fatal("delayed cold execution trap requires a board profile")
			}
			traps := []cpu.ExecutionTrap{{Address: coldTrapAddress, Mode: cpu.ModeARM}}
			if strings.EqualFold(os.Getenv("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP_MODE"), "thumb") {
				traps[0].Mode = cpu.ModeThumb
			}
			for _, call := range board.HLECalls {
				traps = append(traps, cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode})
			}
			check(t, machine.backend.(cpu.ExecutionTrapBackend).SetExecutionTraps(traps))
			coldTrapInstalled = true
		}
		if lsmCallbackAfter != 0 && !lsmCallbackInjected && retired >= lsmCallbackAfter {
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], 0x0115d828)
			check(t, machine.backend.WriteMemory(0x02455dd4, encoded[:]))
			lsmCallbackInjected = true
		}
		if dispatchObjectAfter != 0 && !dispatchObjectInjected && retired >= dispatchObjectAfter {
			object := make([]byte, 32)
			for offset := 0; offset < len(object); offset += 4 {
				binary.LittleEndian.PutUint32(object[offset:], samsungW340BCXUnavailableCallback)
			}
			if err := machine.backend.WriteMemory(0x02573d08, object); err == nil {
				dispatchObjectInjected = true
			}
		}
		if captureBackend != nil && !captureStarted && retired >= captureAfter {
			check(t, captureBackend.SetPCRegisterCapture(uint32(captureAddress), 4096))
			check(t, captureBackend.SetPCHistoryLimit(256))
			captureStarted = true
		}
		stepLimit := uint64(4096)
		if machine.identity.FirmwareBuildID == samsung.SCHW340DC18ProfileID {
			stepLimit = 256
		}
		step := min(stepLimit, budget-retired)
		if captureStarted {
			step = min(uint64(256), budget-retired)
		}
		if uiStateAfter != 0 && !uiStateInjected && retired < uiStateAfter && retired+step > uiStateAfter {
			step = uiStateAfter - retired
		}
		if coldWatchAddress != 0 && !coldWatchInstalled && retired < coldWatchAfter && retired+step > coldWatchAfter {
			step = coldWatchAfter - retired
		}
		if len(coldMemoryWordPatches) != 0 && !coldMemoryWordPatchesApplied && retired < coldMemoryWordPatchAfter && retired+step > coldMemoryWordPatchAfter {
			step = coldMemoryWordPatchAfter - retired
		}
		part := machine.Run(context.Background(), step)
		retired += part.Instructions
		result = part
		result.Instructions = retired
		if progressEnabled && retired >= progressNext {
			pc, _ := machine.backend.ReadRegister(cpu.RegisterPC)
			status, _ := machine.backend.ReadRegister(cpu.RegisterCPSR)
			t.Logf("cold progress instructions=%d pc=%#08x cpsr=%#08x elapsed=%s", retired, pc, status, time.Since(progressStart))
			progressNext += 1_000_000
		}
		if uiStateAtCheckPending && part.Reason == cpu.StopExecutionTrap && part.PC == 0x01d8f2f0 {
			check(t, machine.backend.WriteMemory(0x0257301e, []byte{1}))
			check(t, machine.backend.WriteMemory(0x0403281e, []byte{1, 7}))
			check(t, machine.backend.WriteMemory(0x040350e8, []byte{1}))
			check(t, machine.backend.WriteMemory(0x04035108, []byte{1}))
			check(t, machine.backend.WriteMemory(0x04039606, []byte{1}))
			traps := machine.backend.(cpu.ExecutionTrapBackend)
			for _, address := range []uint32{0x01d8f320} {
				uiConditionTracePending[address] = true
			}
			executionTraps := diagnosticTrapBaseline()
			for address := range uiConditionTracePending {
				if address == part.PC {
					continue
				}
				executionTraps = append(executionTraps, cpu.ExecutionTrap{Address: address, Mode: cpu.ModeThumb})
			}
			check(t, traps.SetExecutionTraps(executionTraps))
			advance := machine.Run(context.Background(), 1)
			retired += advance.Instructions
			if advance.Err != nil || advance.Reason != cpu.StopBudget || advance.Instructions != 1 {
				t.Fatalf("W340 UI state-check diagnostic step = %+v", advance)
			}
			uiStateAtCheckPending = false
			uiStateAtCheckTriggered = true
			uiConditionsAtCheckPending = true
			continue
		}
		if uiConditionsAtCheckPending && part.Reason == cpu.StopExecutionTrap && part.PC == 0x01d8f320 {
			check(t, machine.backend.WriteMemory(0x0403281e, []byte{1, 7}))
			check(t, machine.backend.WriteMemory(0x040350e8, []byte{1}))
			check(t, machine.backend.WriteMemory(0x04035108, []byte{1}))
			check(t, machine.backend.WriteMemory(0x04035361, []byte{0}))
			check(t, machine.backend.WriteMemory(0x04039606, []byte{1}))
			check(t, machine.backend.WriteMemory(0x040ecd1e, []byte{1, 0}))
			traps := machine.backend.(cpu.ExecutionTrapBackend)
			for _, address := range []uint32{
				0x01d8f328, 0x01d8f330, 0x01d8f334, 0x01d8f33a,
				0x01d8f344, 0x01d8f34c, 0x01d8f35a, 0x01d8f36c,
				0x01d8f374, 0x01d8f37a, 0x01d8f382, 0x01d8f396,
				0x01d8f3a0, 0x01d8f3b2, 0x01d8f3be, 0x01d8f3e2,
				0x01d8f3fe, 0x01d8f400, 0x01d8f414,
			} {
				uiConditionTracePending[address] = true
			}
			if os.Getenv("ARAM_SAMSUNG_RAW_W340_UI_TRACE_APP") != "" {
				for _, address := range []uint32{
					0x0004e328, 0x0004f0cc,
					0x00047bf4, 0x00047c12, 0x00047c2e, 0x00047c58,
					0x00047cc4, 0x00047d98, 0x00047df6,
					0x00152f24, 0x00152f28,
					0x00592976, 0x00592990,
					0x01027bbe, 0x01027bd8,
					0x012b01b6, 0x012b0254, 0x012b0268, 0x012b0288,
					0x012b029c, 0x012b02ae, 0x012b02c2, 0x012b0968,
					0x012b036a, 0x012b037e, 0x012b0392, 0x012b03a6,
					0x012b03ba, 0x012b03ce, 0x012b03ee, 0x012b040e,
					0x012b0422, 0x012b0436, 0x012b0448, 0x012b0466,
					0x012b0478, 0x012b0490, 0x012b04a2, 0x012b04b4,
					0x012b04c6, 0x012b04da, 0x012b04f6, 0x012b0508,
					0x012b056a, 0x012b0578,
					0x012bac78, 0x012baca8, 0x012bacba,
					0x012b4e58, 0x012b7da2, 0x012b7e84, 0x012b7eb6, 0x012b7eba,
					0x012b7eca, 0x012b7ed8, 0x012b7efc, 0x012b7f74, 0x012b7f8a,
					0x012b805a, 0x012b8070, 0x012b80a4,
					0x01349f7e, 0x0134eb90, 0x0134ec72, 0x0134eca0, 0x0134ecaa, 0x0134ecae,
				} {
					uiConditionTracePending[address] = true
					uiAppTraceAddresses[address] = true
				}
			}
			if os.Getenv("ARAM_SAMSUNG_RAW_W340_IDLE_BEEF_TRACE") != "" {
				for _, address := range []uint32{
					0x00c681b2, 0x00c681c4, 0x00c681d2, 0x00c681de,
					0x00c681ee, 0x00c681f2,
					0x01959ec4, 0x01959eca, 0x01959ed6,
					0x00f265b6, 0x00f265ba, 0x00f26392,
				} {
					uiConditionTracePending[address] = true
					uiAppTraceAddresses[address] = true
				}
			}
			executionTraps := diagnosticTrapBaseline()
			for address := range uiConditionTracePending {
				if address == part.PC {
					continue
				}
				executionTraps = append(executionTraps, cpu.ExecutionTrap{Address: address, Mode: cpu.ModeThumb})
			}
			check(t, traps.SetExecutionTraps(executionTraps))
			advance := machine.Run(context.Background(), 1)
			retired += advance.Instructions
			if advance.Err != nil || advance.Reason != cpu.StopBudget || advance.Instructions != 1 {
				t.Fatalf("W340 UI condition-check diagnostic step = %+v", advance)
			}
			uiConditionsAtCheckPending = false
			continue
		}
		if uiConditionTracePending[part.PC] && part.Reason == cpu.StopExecutionTrap {
			var values [0x10]byte
			check(t, machine.backend.ReadMemory(0x040ecd18, values[:]))
			var sbi [0x20]byte
			check(t, machine.backend.ReadMemory(0x04020890, sbi[:]))
			uiConditionTraceHits[part.PC]++
			hit := uiConditionTraceHits[part.PC]
			if uiAppTraceAddresses[part.PC] {
				registers := make([]uint32, 17)
				for index := range registers {
					registers[index], _ = machine.backend.ReadRegister(uint32(index))
				}
				t.Logf("W340 UI app trace pc=%#08x hit=%d retired=%d registers=%#x", part.PC, hit, retired, registers)
				if part.PC == 0x01349f7e {
					object := make([]byte, 0x100)
					if err := machine.backend.ReadMemory(registers[cpu.RegisterR4], object); err == nil {
						t.Logf("W340 IdleApp dependency object=%#08x data=%x", registers[cpu.RegisterR4], object)
					}
				}
				if part.PC == 0x00047c12 {
					readWord := func(address uint32) uint32 {
						var encoded [4]byte
						if err := machine.backend.ReadMemory(address, encoded[:]); err != nil {
							return 0
						}
						return binary.LittleEndian.Uint32(encoded[:])
					}
					head := readWord(0x041cacf0)
					known := make(map[uint32]string)
					zeroClasses := make(map[uint32]string)
					visited := make(map[uint32]bool)
					address := head
					for index := 0; address != 0 && index < 200 && !visited[address]; index++ {
						visited[address] = true
						classes := readWord(address + 0x44)
						if classes == 0x00a354b8 || classes == 0x01c27960 || classes == 0x01199cf4 || classes == 0x01bff73c {
							known[classes] = fmt.Sprintf(
								"index=%d node=%#08x count=%d flags=%#08x",
								index, address, readWord(address+0x40)>>16, readWord(address+0x4c),
							)
						}
						if classes != 0 && readWord(classes) == 0 {
							zeroClasses[classes] = fmt.Sprintf("index=%d node=%#08x count=%d", index, address, readWord(address+0x40)>>16)
						}
						address = readWord(address)
					}
					moduleList := make([]byte, 32)
					_ = machine.backend.ReadMemory(samsungW340StaticModuleList, moduleList)
					classData := make([]byte, 4*32)
					for index, classAddress := range []uint32{0x00a354b8, 0x01c27960, 0x01199cf4, 0x01bff73c} {
						_ = machine.backend.ReadMemory(classAddress, classData[index*32:(index+1)*32])
					}
					searchRecord := make([]byte, 32)
					_ = machine.backend.ReadMemory(registers[cpu.RegisterSP], searchRecord)
					t.Logf("W340 UI registry head=%#08x nodes=%d tail=%#08x known=%#x zero=%#x modules=%x classes=%x search=%x", head, len(visited), address, known, zeroClasses, moduleList, classData, searchRecord)
				}
			} else {
				t.Logf("W340 UI condition trace pc=%#08x hit=%d retired=%d radio=%x sbi=%x", part.PC, hit, retired, values, sbi)
			}
			repeat := false
			if uiAppTraceAddresses[part.PC] {
				repeat = hit < 16
			}
			switch part.PC {
			case 0x01a2e248, 0x01a2e258, 0x01a2e25c, 0x01a2e278,
				0x01a2e27c, 0x01a2e280, 0x01a2e30c, 0x01a2e310,
				0x01a2e314, 0x01a2e318, 0x01a2e31c, 0x01a2e3f8,
				0x01a2e40e, 0x01a2e412, 0x01a2e452, 0x01a2e456,
				0x01a2e46c, 0x01a2e4a0, 0x01a2e500, 0x01a2e576,
				0x01a2e58c, 0x01a2e590:
				repeat = hit < 12
			}
			if !repeat {
				delete(uiConditionTracePending, part.PC)
			}
			executionTraps := diagnosticTrapBaseline()
			for address := range uiConditionTracePending {
				if address == part.PC {
					continue
				}
				executionTraps = append(executionTraps, cpu.ExecutionTrap{Address: address, Mode: cpu.ModeThumb})
			}
			traps := machine.backend.(cpu.ExecutionTrapBackend)
			check(t, traps.SetExecutionTraps(executionTraps))
			advance := machine.Run(context.Background(), 1)
			retired += advance.Instructions
			if advance.Err != nil || advance.Reason != cpu.StopBudget || advance.Instructions != 1 {
				t.Fatalf("W340 UI condition-trace diagnostic step = %+v", advance)
			}
			executionTraps = diagnosticTrapBaseline()
			for address := range uiConditionTracePending {
				executionTraps = append(executionTraps, cpu.ExecutionTrap{Address: address, Mode: cpu.ModeThumb})
			}
			check(t, traps.SetExecutionTraps(executionTraps))
			continue
		}
		if coldTrapHitLimit != 0 && part.Reason == cpu.StopExecutionTrap && part.PC == coldTrapAddress {
			registers := make([]uint32, 17)
			for index := range registers {
				registers[index], _ = machine.backend.ReadRegister(uint32(index))
			}
			t.Logf("cold diagnostic trap hit=%d instructions=%d registers=%#x", coldTrapHits, retired, registers)
			if backend, ok := machine.backend.(*interpreter.Backend); ok {
				target := registers[cpu.RegisterR3] &^ 1
				physical, physicalErr := backend.PhysicalAddress(target, cpu.PermissionExecute)
				var cached strings.Builder
				for address := target - min(target, uint32(0x20)); address < target+0x40; address += 2 {
					if raw, resident := backend.CachedThumbInstruction(address); resident {
						fmt.Fprintf(&cached, "%#08x=%04x,", address, raw)
					}
				}
				t.Logf("cold diagnostic r3=%#08x physical=%#08x/%v cache=%s", target, physical, physicalErr, cached.String())
				for _, address := range []uint32{0x00038980, registers[cpu.RegisterLR] - min(registers[cpu.RegisterLR], uint32(4))} {
					physical, translateErr := backend.PhysicalAddress(address, cpu.PermissionExecute)
					t.Logf("cold diagnostic execute translation virtual=%#08x physical=%#08x err=%v", address, physical, translateErr)
				}
				pageTable := make([]byte, 0x100)
				pageTableErr := machine.bus.ReadMemory(0x02700000, pageTable, cpu.PermissionRead)
				t.Logf("cold diagnostic physical page table=%x err=%v", pageTable, pageTableErr)
				constructorLiterals := make([]byte, 0x40)
				constructorLiteralsErr := machine.backend.ReadMemory(0x00fc9f40, constructorLiterals)
				t.Logf("cold diagnostic constructor literals=%x err=%v", constructorLiterals, constructorLiteralsErr)
				secureFileState := make([]byte, 0x200)
				secureFileStateErr := machine.backend.ReadMemory(0x07fd1200, secureFileState)
				var secureFileManager [0x40]byte
				secureFileManagerErr := machine.backend.ReadMemory(0x0245365c, secureFileManager[:])
				var secureFileObject [8]byte
				secureFileObjectErr := machine.backend.ReadMemory(0x0388a340, secureFileObject[:])
				t.Logf("cold diagnostic secure-file manager=%x/%v object=%x/%v synthetic=%x/%v", secureFileManager, secureFileManagerErr, secureFileObject, secureFileObjectErr, secureFileState, secureFileStateErr)
				var phonebookFileManager [0x40]byte
				phonebookFileManagerErr := machine.backend.ReadMemory(0x0359bf14, phonebookFileManager[:])
				phonebookObject := binary.LittleEndian.Uint32(phonebookFileManager[:])
				var phonebookObjectState [0x40]byte
				phonebookObjectErr := machine.backend.ReadMemory(phonebookObject, phonebookObjectState[:])
				phonebookVTable := binary.LittleEndian.Uint32(phonebookObjectState[:])
				var phonebookVTableState [0x40]byte
				phonebookVTableErr := machine.backend.ReadMemory(phonebookVTable, phonebookVTableState[:])
				t.Logf("cold diagnostic phonebook file manager=%x/%v object=%#08x:%x/%v vtable=%#08x:%x/%v", phonebookFileManager, phonebookFileManagerErr, phonebookObject, phonebookObjectState, phonebookObjectErr, phonebookVTable, phonebookVTableState, phonebookVTableErr)
				cached.Reset()
				for address := uint32(0x38); address < 0x110; address += 2 {
					if raw, resident := backend.CachedThumbInstruction(address); resident {
						fmt.Fprintf(&cached, "%#04x=%04x,", address, raw)
					}
				}
				t.Logf("cold diagnostic cached low boot code=%s", cached.String())
				history := backend.PCHistory()
				if len(history) > 512 {
					history = history[len(history)-512:]
				}
				t.Logf("cold diagnostic recent PC history=%#x", history)
				for _, target := range []uint32{coldTrapAddress, registers[cpu.RegisterLR] &^ 1} {
					cached.Reset()
					start := target - min(target, uint32(0x80))
					for address := start; address < target+0x100; address += 2 {
						if raw, resident := backend.CachedThumbInstruction(address); resident {
							fmt.Fprintf(&cached, "%#08x=%04x,", address, raw)
						}
					}
					t.Logf("cold diagnostic cached code around %#08x=%s", target, cached.String())
					lineAddress, line, resident := backend.InstructionCacheLine(target)
					t.Logf("cold diagnostic instruction-cache line around %#08x=%#08x/%t:%x", target, lineAddress, resident, line)
				}
				for _, address := range []uint32{
					0x00046b00, 0x00046b20, 0x00046b40, 0x00046b60,
					0x00140440, 0x00140460, 0x00140480, 0x001404a0, 0x001404c0,
					0x001422a0, 0x001422c0, 0x00142460, 0x001424a0,
				} {
					lineAddress, line, resident := backend.InstructionCacheLine(address)
					t.Logf("cold diagnostic UI instruction-cache line %#08x=%#08x/%t:%x", address, lineAddress, resident, line)
				}
				object := make([]byte, 0xa0)
				if err := machine.backend.ReadMemory(registers[cpu.RegisterR4]-0x20, object); err == nil {
					var words strings.Builder
					for offset := 0; offset+4 <= len(object); offset += 4 {
						fmt.Fprintf(&words, "%+#x=%#08x,", offset-0x20, binary.LittleEndian.Uint32(object[offset:]))
					}
					t.Logf("cold diagnostic r4 object %#08x words=%s", registers[cpu.RegisterR4], words.String())
					for offset := 0x24; offset <= 0x7c; offset += 4 {
						address := binary.LittleEndian.Uint32(object[0x20+offset:])
						state := make([]byte, 0x80)
						if err := machine.backend.ReadMemory(address, state); err != nil {
							continue
						}
						vtable := binary.LittleEndian.Uint32(state)
						methods := make([]byte, 0x80)
						methodsErr := machine.backend.ReadMemory(vtable, methods)
						t.Logf("cold diagnostic r4%+#x=%#08x state=%x vtable=%#08x:%x/%v", offset, address, state, vtable, methods, methodsErr)
					}
				}
				for _, address := range []uint32{samsungW340FontRegistry, samsungW340FontDescriptor, 0x000758c8} {
					memory := make([]byte, 0x100)
					if err := machine.backend.ReadMemory(address, memory); err == nil {
						t.Logf("cold diagnostic font/object data %#08x=%x", address, memory)
					}
				}
				display := binary.LittleEndian.Uint32(object[0x30:])
				displayState := make([]byte, 0x100)
				if err := machine.backend.ReadMemory(display, displayState); err == nil {
					displayVTable := binary.LittleEndian.Uint32(displayState)
					displayMethods := make([]byte, 0x100)
					methodsErr := machine.backend.ReadMemory(displayVTable, displayMethods)
					t.Logf("cold diagnostic display=%#08x state=%x vtable=%#08x:%x/%v", display, displayState, displayVTable, displayMethods, methodsErr)
					font := binary.LittleEndian.Uint32(displayState[0x4c:])
					fontState := make([]byte, 0x80)
					if fontErr := machine.backend.ReadMemory(font, fontState); fontErr == nil {
						fontVTable := binary.LittleEndian.Uint32(fontState)
						fontMethods := make([]byte, 0x80)
						fontMethodsErr := machine.backend.ReadMemory(fontVTable, fontMethods)
						fontData := binary.LittleEndian.Uint32(fontState[8:])
						fontPayload := make([]byte, 0x100)
						fontPayloadErr := machine.backend.ReadMemory(fontData, fontPayload)
						t.Logf("cold diagnostic font=%#08x state=%x vtable=%#08x:%x/%v data=%#08x:%x/%v", font, fontState, fontVTable, fontMethods, fontMethodsErr, fontData, fontPayload, fontPayloadErr)
					}
				}
			}
			for _, address := range []uint32{coldTrapAddress &^ 0xff, (registers[cpu.RegisterR3] &^ 1) &^ 0xff} {
				memory := make([]byte, 0x100)
				if err := machine.backend.ReadMemory(address, memory); err == nil {
					t.Logf("cold diagnostic code memory=%#08x:%x", address, memory)
				}
			}
			stack := make([]byte, 32)
			if err := machine.backend.ReadMemory(registers[cpu.RegisterSP], stack); err == nil {
				t.Logf("cold diagnostic trap hit=%d stack=%#08x:%x", coldTrapHits, registers[cpu.RegisterSP], stack)
				caller := binary.LittleEndian.Uint32(stack[20:]) &^ 1
				code := make([]byte, 0x100)
				readErr := machine.backend.ReadMemory(caller-min(caller, uint32(0x40)), code)
				var physical uint32
				var translateErr error
				if translator, ok := machine.backend.(interface {
					PhysicalAddress(uint32, cpu.Permissions) (uint32, error)
				}); ok {
					physical, translateErr = translator.PhysicalAddress(caller, cpu.PermissionExecute)
				}
				t.Logf("cold diagnostic trap caller=%#08x physical=%#08x/%v code=%x/%v", caller, physical, translateErr, code, readErr)
			}
			for _, address := range []uint32{0x041cacd8, registers[cpu.RegisterSP] + 0x40} {
				memory := make([]byte, 0x100)
				if err := machine.backend.ReadMemory(address, memory); err == nil {
					t.Logf("cold diagnostic trap memory=%#08x:%x", address, memory)
				}
			}
			coldTrapHits++
			if coldTrapHits >= coldTrapHitLimit {
				t.Logf("cold interleaved MGP trap accesses=%s", mgpBus.accessSummary())
				for _, address := range []uint32{0x90108000, 0x90110000, 0x9011f000, 0x9011f140} {
					memory := make([]byte, 0x100)
					if err := machine.bus.Read(address, memory, cpu.PermissionRead); err == nil {
						t.Logf("cold interleaved MGP trap memory %#08x=%x", address, memory)
					}
				}
				return result
			}
			traps := machine.backend.(cpu.ExecutionTrapBackend)
			board, ok := samsungTargetDiagnosticBoardProfile(machine.identity.FirmwareBuildID)
			if !ok {
				t.Fatal("cold repeated execution trap requires a diagnostic board profile")
			}
			hleTraps := make([]cpu.ExecutionTrap, 0, len(board.HLECalls))
			for _, call := range board.HLECalls {
				hleTraps = append(hleTraps, cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode})
			}
			check(t, traps.SetExecutionTraps(hleTraps))
			advance := machine.Run(context.Background(), 1)
			retired += advance.Instructions
			if advance.Err != nil || advance.Reason != cpu.StopBudget || advance.Instructions != 1 {
				t.Fatalf("cold diagnostic trap step = %+v", advance)
			}
			requestedMode := cpu.ModeARM
			if strings.EqualFold(os.Getenv("ARAM_SAMSUNG_RAW_COLD_EXECUTION_TRAP_MODE"), "thumb") {
				requestedMode = cpu.ModeThumb
			}
			check(t, traps.SetExecutionTraps(append(
				[]cpu.ExecutionTrap{{Address: coldTrapAddress, Mode: requestedMode}},
				hleTraps...,
			)))
			continue
		}
		if captureStarted {
			captureSamples++
			if machine.vectoredIRQs.PendingStatusBanks()[1]&(1<<2) != 0 {
				captureTickPending++
			}
			if status, statusErr := machine.backend.ReadRegister(cpu.RegisterCPSR); statusErr == nil && status&0x80 == 0 {
				captureIRQEnabled++
			}
		}
		if part.Err != nil || part.Reason != cpu.StopBudget || part.Instructions != step {
			return result
		}
		if readyAfter != 0 && retired >= readyAfter && samsungW340MainAwaitingStartupAck(machine) {
			if readyInjections < 5 {
				traceSamsungW340StartupAckWait(t, machine, readyInjections+1)
			}
			check(t, wakeSamsungW340MainStartupAck(machine))
			readyInjections++
		}
		if uiSignalAfter != 0 && !uiSignalInjected && retired >= uiSignalAfter {
			callSamsungW340RexSetSignals(t, machine, 0x03ecfb84, uiSignalMask)
			uiSignalInjected = true
		}
		if fsWaitAfter != 0 && !fsWaitInjected && retired >= fsWaitAfter {
			check(t, machine.backend.WriteMemory(0x033502a2, []byte{0, 0}))
			fsWaitInjected = true
		}
		if radioReadyAfter != 0 && !radioReadyInjected && retired >= radioReadyAfter {
			check(t, machine.backend.WriteMemory(0x040ecd1e, []byte{1}))
			radioReadyInjected = true
		}
		if radioInitAfter != 0 && !radioInitInjected && retired >= radioInitAfter {
			// Ask the UI dispatcher to run the radio initializer in its native
			// task context. Calling the routine out of band is unsafe because it
			// temporarily enables interrupts and depends on the active REX task.
			check(t, machine.backend.WriteMemory(0x02575574, []byte{1}))
			radioInitInjected = true
		}
		if radioLevelAfter != 0 && retired >= radioLevelAfter {
			check(t, machine.backend.WriteMemory(0x040ecd25, []byte{0}))
		}
		if radioSettledAfter != 0 && retired >= radioSettledAfter {
			check(t, machine.backend.WriteMemory(0x040ecd1f, []byte{0}))
		}
		if uiBootReadyAfter != 0 && retired >= uiBootReadyAfter {
			check(t, machine.backend.WriteMemory(0x04039606, []byte{1}))
		}
		if uiUpdateAfter != 0 && retired >= uiUpdateAfter {
			check(t, machine.backend.WriteMemory(0x04035108, []byte{1}))
		}
		if uiRenderAfter != 0 && retired >= uiRenderAfter {
			check(t, machine.backend.WriteMemory(0x040350e8, []byte{1}))
		}
		if uiCounterAfter != 0 && retired >= uiCounterAfter {
			check(t, machine.backend.WriteMemory(0x0403281e, []byte{1, uiCounterValue}))
		}
		if uiStateAfter != 0 && !uiStateInjected && retired >= uiStateAfter {
			check(t, machine.backend.WriteMemory(0x0257301e, []byte{uiStateValue}))
			uiStateInjected = true
		}
		if captureStarted {
			// PC history is sampled at slice boundaries below. No architectural
			// context probing is needed here: the interpreter's serialized banked
			// register layout is deliberately private to the backend.
		}
		var release [2]byte
		check(t, machine.bus.Read(0x9011f1ac, release[:], cpu.PermissionRead))
		releaseValue := binary.LittleEndian.Uint16(release[:])
		var ready [1]byte
		check(t, machine.bus.Read(0x9010a9e0, ready[:], cpu.PermissionRead))
		var imageVector [4]byte
		check(t, machine.bus.Read(0x90108000, imageVector[:], cpu.PermissionRead))
		// The AP can assert and clear the release halfword within one execution
		// slice. SamsungMGPControl publishes the companion-owned ready byte only
		// after observing that complete edge, so it is also authoritative evidence
		// that the uploaded ARM7 image may start.
		releaseAsserted = releaseAsserted || releaseValue != 0 || ready[0] != 0 ||
			binary.LittleEndian.Uint32(imageVector[:]) == 0xea000012
		captureMGPFatal()
		if releaseValue != 0 {
			continue
		}
		if !mgpStarted && releaseAsserted && releaseValue == 0 {
			mgpStarted = true
		}
		if !mgpStarted {
			continue
		}
		if mgpRestartRequested {
			check(t, mgp.Close())
			mgp = newMGP()
			mgpPC = 0x00008000
			mgpMode = cpu.ModeARM
			mgpRestartRequested = false
			mgpBus.timerAsserted = false
			mgpRestarts++
		}
		timerInterruptDue := nextFreeRunningTimer == 0 || retired >= nextFreeRunningTimer
		if timerInterruptDue {
			check(t, mgpBus.advanceFreeRunningTimer())
			nextFreeRunningTimer = retired + 1_000_000
		}
		mgpResult := cpu.Result{}
		status, statusErr := mgp.ReadRegister(cpu.RegisterCPSR)
		check(t, statusErr)
		if timerInterruptDue && status&0x80 == 0 && step > 1 {
			check(t, mgpBus.setTimerInterrupt(true))
			check(t, mgp.SetInterruptLine(cpu.InterruptIRQ, true))
			entry := mgp.Run(context.Background(), mgpPC, mgpMode, 1)
			check(t, mgp.SetInterruptLine(cpu.InterruptIRQ, false))
			if entry.Err != nil || entry.Reason != cpu.StopBudget || entry.Instructions != 1 {
				t.Fatalf("cold interleaved MGP timer entry = %+v", entry)
			}
			mgpPC = entry.PC
			status, statusErr = mgp.ReadRegister(cpu.RegisterCPSR)
			check(t, statusErr)
			mgpMode = cpu.ModeARM
			if status&cpu.StatusThumb != 0 {
				mgpMode = cpu.ModeThumb
			}
			mgpResult = mgp.Run(context.Background(), mgpPC, mgpMode, step-1)
			mgpResult.Instructions++
			check(t, mgpBus.setTimerInterrupt(false))
		} else {
			mgpResult = mgp.Run(context.Background(), mgpPC, mgpMode, step)
		}
		if mgpResult.Err != nil || mgpResult.Reason != cpu.StopBudget || mgpResult.Instructions != step {
			t.Fatalf("cold interleaved MGP run = %+v", mgpResult)
		}
		mgpPC = mgpResult.PC
		status, statusErr = mgp.ReadRegister(cpu.RegisterCPSR)
		check(t, statusErr)
		mgpMode = cpu.ModeARM
		if status&cpu.StatusThumb != 0 {
			mgpMode = cpu.ModeThumb
		}
		captureMGPFatal()
	}
	mgpFatalEnd := len(mgpFatalHistory)
	for index, address := range mgpFatalHistory {
		if address == 0x901097c0 {
			mgpFatalEnd = min(len(mgpFatalHistory), index+8)
			break
		}
	}
	mgpFatalStart := mgpFatalEnd - 80
	if mgpFatalStart < 0 {
		mgpFatalStart = 0
	}
	t.Logf(
		"cold MGP first-fatal registers=%#x history=%#x",
		mgpFatalRegisters, mgpFatalHistory[mgpFatalStart:mgpFatalEnd],
	)
	t.Logf("cold MGP first-fatal code[0x90109780]=%x/%v", mgpFatalCode, mgpFatalCodeErr)
	t.Logf("cold MGP first-fatal decoded=%s", mgpFatalDecoded)
	t.Logf("cold MGP first-fatal cache=%s", mgpFatalCache)
	mgpPhysicalPC, mgpPhysicalErr := mgp.PhysicalAddress(mgpPC&^1, cpu.PermissionExecute)
	mgpCode := make([]byte, 0x80)
	mgpCodeAddress := (mgpPhysicalPC - min(mgpPhysicalPC, 0x40)) &^ 1
	mgpCodeErr := mgpBus.Read(mgpCodeAddress, mgpCode, cpu.PermissionRead)
	mgpRegisters := make([]uint32, 17)
	for index := range mgpRegisters {
		mgpRegisters[index], _ = mgp.ReadRegister(uint32(index))
	}
	mgpCacheAddress, mgpCacheLine, mgpCacheResident := mgp.InstructionCacheLine(mgpPC &^ 1)
	mgpHistory := mgp.PCHistory()
	if len(mgpHistory) > 64 {
		mgpHistory = mgpHistory[len(mgpHistory)-64:]
	}
	t.Logf(
		"cold MGP current-cache address=%#08x resident=%t data=%x",
		mgpCacheAddress, mgpCacheResident, mgpCacheLine,
	)
	t.Logf("cold MGP recent-PC=%#x", mgpHistory)
	t.Logf(
		"cold interleaved MGP started=%t restarts=%d pc=%#08x physical=%#08x/%v mode=%d registers=%#x code[%#08x]=%x/%v history=%#x accesses=%s host-writes=%s",
		mgpStarted, mgpRestarts, mgpPC, mgpPhysicalPC, mgpPhysicalErr, mgpMode, mgpRegisters, mgpCodeAddress, mgpCode, mgpCodeErr, mgp.PCHistory(),
		mgpBus.accessSummary(), samsungMGPDiagnosticAccessSummary(hostInterfaceWrites),
	)
	serviceSource, serviceValid := machine.vectoredIRQs.InServiceSource()
	t.Logf(
		"cold VIC pending=%#x enabled=%#x service=%d/%t",
		machine.vectoredIRQs.PendingStatusBanks(), machine.vectoredIRQs.EnabledSourceBanks(),
		serviceSource, serviceValid,
	)
	t.Logf(
		"cold W340 compatibility injections callback=%t dispatch=%t ready=%d ui=%t",
		lsmCallbackInjected, dispatchObjectInjected, readyInjections, uiSignalInjected,
	)
	if uiStateAtCheckTriggered {
		for _, address := range []uint32{
			0x0257301c,
			0x02575574,
			0x04032814,
			0x040350dc,
			0x040350fc,
			0x0403535c,
			0x040395fc,
			0x040ecd18,
			0x041cad10,
		} {
			var data [16]byte
			check(t, machine.backend.ReadMemory(address, data[:]))
			t.Logf("cold W340 UI state-check address=%#08x data=%x", address, data)
		}
	}
	return result
}

func callSamsungW340RexSetSignals(t *testing.T, machine *Machine, tcb, signals uint32) {
	t.Helper()
	registers := make([]uint32, 17)
	for index := range registers {
		registers[index], _ = machine.backend.ReadRegister(uint32(index))
	}
	resumeMode := cpu.ModeARM
	resumeLink := registers[cpu.RegisterPC]
	if registers[cpu.RegisterCPSR]&cpu.StatusThumb != 0 {
		resumeMode = cpu.ModeThumb
		resumeLink |= 1
	}
	traps, ok := machine.backend.(cpu.ExecutionTrapBackend)
	if !ok {
		t.Fatal("selected backend has no execution-trap support")
	}
	board := system.SCHW340DC18BoardProfile()
	hleTraps := make([]cpu.ExecutionTrap, 0, len(board.HLECalls))
	for _, call := range board.HLECalls {
		hleTraps = append(hleTraps, cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode})
	}
	callTraps := append(append([]cpu.ExecutionTrap(nil), hleTraps...), cpu.ExecutionTrap{
		Address: registers[cpu.RegisterPC],
		Mode:    resumeMode,
	})
	check(t, traps.SetExecutionTraps(callTraps))
	check(t, machine.backend.WriteRegister(cpu.RegisterR0, tcb))
	check(t, machine.backend.WriteRegister(cpu.RegisterR1, signals))
	check(t, machine.backend.WriteRegister(cpu.RegisterLR, resumeLink))
	check(t, machine.backend.WriteRegister(cpu.RegisterCPSR, registers[cpu.RegisterCPSR]|cpu.StatusThumb|0xc0))
	call := machine.runner.Run(context.Background(), 0x00139d0c, cpu.ModeThumb, 1_000_000)
	machine.instructions += call.Instructions
	if call.Err != nil || call.Reason != cpu.StopExecutionTrap || call.PC != registers[cpu.RegisterPC] {
		t.Fatalf("W340 rex_set_sigs diagnostic call = %+v", call)
	}
	check(t, traps.SetExecutionTraps(hleTraps))
	for index, value := range registers {
		check(t, machine.backend.WriteRegister(uint32(index), value))
	}
	machine.pc = registers[cpu.RegisterPC]
	machine.mode = resumeMode
}

func samsungW340MainAwaitingStartupAck(machine *Machine) bool {
	var state [8]byte
	if err := machine.backend.ReadMemory(0x03f4af48+0x0c, state[:]); err != nil {
		return false
	}
	pending := binary.LittleEndian.Uint32(state[0:4])
	wait := binary.LittleEndian.Uint32(state[4:8])
	return (wait == 5 || wait == 6) && pending&wait == 0
}

func wakeSamsungW340MainStartupAck(machine *Machine) error {
	const (
		mainTCB            = uint32(0x03f4af48)
		schedulerCandidate = uint32(0x03c5336c)
	)
	state := make([]byte, 0x80)
	if err := machine.backend.ReadMemory(mainTCB, state); err != nil {
		return err
	}
	pending := binary.LittleEndian.Uint32(state[0x0c:])
	wait := binary.LittleEndian.Uint32(state[0x10:])
	if wait != 5 && wait != 6 {
		return nil
	}
	startupSignals := wait
	binary.LittleEndian.PutUint32(state[0x0c:], pending|startupSignals)
	if wait&startupSignals == 0 {
		return machine.backend.WriteMemory(mainTCB+0x0c, state[0x0c:0x10])
	}
	binary.LittleEndian.PutUint32(state[0x10:], 0)
	if err := machine.backend.WriteMemory(mainTCB+0x0c, state[0x0c:0x14]); err != nil {
		return err
	}
	var encoded [4]byte
	if err := machine.backend.ReadMemory(schedulerCandidate, encoded[:]); err != nil {
		return err
	}
	candidate := binary.LittleEndian.Uint32(encoded[:])
	var candidatePriority [4]byte
	if err := machine.backend.ReadMemory(candidate+0x14, candidatePriority[:]); err != nil {
		return err
	}
	priority := binary.LittleEndian.Uint32(state[0x14:])
	if priority > binary.LittleEndian.Uint32(candidatePriority[:]) &&
		state[0x58] == 0 && binary.LittleEndian.Uint32(state[0x2c:]) == 0 {
		binary.LittleEndian.PutUint32(encoded[:], mainTCB)
		if err := machine.backend.WriteMemory(schedulerCandidate, encoded[:]); err != nil {
			return err
		}
	}
	return nil
}

func samsungW340CPUInSupervisorTask(machine *Machine) bool {
	status, err := machine.backend.ReadRegister(cpu.RegisterCPSR)
	return err == nil && status&0x1f == 0x13
}

func traceSamsungW340StartupAckWait(t *testing.T, machine *Machine, index uint64) {
	t.Helper()
	var savedSPBytes [4]byte
	if err := machine.backend.ReadMemory(0x03f4af48, savedSPBytes[:]); err != nil {
		return
	}
	savedSP := binary.LittleEndian.Uint32(savedSPBytes[:])
	frame := make([]byte, 17*4)
	if err := machine.backend.ReadMemory(savedSP, frame); err != nil {
		return
	}
	words := make([]uint32, 17)
	for word := range words {
		words[word] = binary.LittleEndian.Uint32(frame[word*4:])
	}
	stack := make([]byte, 0x200)
	var codePointers strings.Builder
	if err := machine.backend.ReadMemory(savedSP, stack); err == nil {
		for offset := 0; offset+4 <= len(stack); offset += 4 {
			value := binary.LittleEndian.Uint32(stack[offset:])
			if value >= 0x00080000 && value < 0x02580000 {
				fmt.Fprintf(&codePointers, "+%03x=%08x,", offset, value)
			}
		}
	}
	t.Logf("SCH-W340 startup-ack wait=%d saved-sp=%#08x frame=%#x code=%s", index, savedSP, words, codePointers.String())
}

type samsungMGPDiagnosticBus struct {
	system        *system.Bus
	interrupts    *system.QualcommVectoredInterruptController
	lowROM        [0x8000]byte
	reads         [0x100]uint64
	writes        [0x100]uint64
	localWrites   map[uint32]samsungMGPDiagnosticAccess
	timerCounter  uint16
	timerAsserted bool
}

type samsungMGPDiagnosticAccess struct {
	count uint64
	value uint32
	width int
	pc    uint32
}

func samsungMGPDiagnosticAccessSummary(accesses map[uint32]samsungMGPDiagnosticAccess) string {
	addresses := make([]uint32, 0, len(accesses))
	for address := range accesses {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i] < addresses[j] })
	var summary strings.Builder
	for _, address := range addresses {
		access := accesses[address]
		fmt.Fprintf(
			&summary, "%#x=%#x/%d*%d@%#x,",
			address, access.value, access.width, access.count, access.pc,
		)
	}
	return summary.String()
}

func newSamsungMGPDiagnosticBus(
	systemBus *system.Bus,
	interrupts *system.QualcommVectoredInterruptController,
) *samsungMGPDiagnosticBus {
	bus := &samsungMGPDiagnosticBus{
		system:      systemBus,
		interrupts:  interrupts,
		localWrites: make(map[uint32]samsungMGPDiagnosticAccess),
	}
	// The uploaded program imports helpers from the MGP mask ROM.  A return
	// stub is enough to identify which calls require a stateful implementation;
	// its first import only clears the freshly allocated, already-zero BSS.
	for _, address := range []uint32{0x00c0, 0x1784, 0x185c, 0x192c, 0x1968} {
		binary.LittleEndian.PutUint32(bus.lowROM[address:], 0xe12fff1e) // bx lr
	}
	for _, address := range []uint32{0x02da, 0x157c} {
		binary.LittleEndian.PutUint16(bus.lowROM[address:], 0x4770) // bx lr
	}
	// This ARM7 maps its uploaded vector table from 0x8000 at architectural
	// address zero. Keep the diagnostic bus narrow and provide only the IRQ
	// vector needed by the companion's profiled timer source.
	binary.LittleEndian.PutUint32(bus.lowROM[0x18:], 0xe51ff004) // ldr pc, [pc, #-4]
	binary.LittleEndian.PutUint32(bus.lowROM[0x1c:], 0x000081d8)
	return bus
}

func (b *samsungMGPDiagnosticBus) setTimerInterrupt(asserted bool) error {
	if asserted && !b.timerAsserted {
		b.timerCounter++
		var counter [2]byte
		binary.LittleEndian.PutUint16(counter[:], b.timerCounter)
		if err := b.system.Write(0x9011f1d4, counter[:], cpu.PermissionWrite); err != nil {
			return err
		}
	}
	b.timerAsserted = asserted
	value := uint16(0)
	if asserted {
		value = 2
	}
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	return b.system.Write(0x9011f1d6, encoded[:], cpu.PermissionWrite)
}

func (b *samsungMGPDiagnosticBus) advanceFreeRunningTimer() error {
	const clockRootPointerAddress = uint32(0x00ed703c)
	readWord := func(address uint32) (uint32, error) {
		var encoded [4]byte
		if err := b.system.Read(address, encoded[:], cpu.PermissionRead); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(encoded[:]), nil
	}

	root, err := readWord(clockRootPointerAddress)
	if err != nil {
		return err
	}
	if root < 0x00080000 || root >= 0x03000000 {
		return nil
	}
	owner, err := readWord(root + 0x10)
	if err != nil {
		return err
	}
	if owner < 0x90108000 || owner >= 0x90120000 {
		return nil
	}
	clock, err := readWord(owner)
	if err != nil {
		return err
	}
	if clock < 0x90108000 || clock >= 0x90120000 {
		return nil
	}
	var selector [1]byte
	if err := b.system.Read(clock+3, selector[:], cpu.PermissionRead); err != nil {
		return err
	}
	if selector[0] >= 4 {
		return nil
	}
	counterAddress := clock + 4 + uint32(selector[0])*4

	var encoded [4]byte
	if err := b.system.Read(counterAddress, encoded[:], cpu.PermissionRead); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(encoded[:], binary.LittleEndian.Uint32(encoded[:])+1)
	return b.system.Write(counterAddress, encoded[:], cpu.PermissionWrite)
}

func (b *samsungMGPDiagnosticBus) accessSummary() string {
	var summary strings.Builder
	for page := range b.reads {
		if b.reads[page] == 0 && b.writes[page] == 0 {
			continue
		}
		fmt.Fprintf(&summary, "%#x:r%d/w%d,", page<<12, b.reads[page], b.writes[page])
	}
	addresses := make([]uint32, 0, len(b.localWrites))
	for address := range b.localWrites {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i] < addresses[j] })
	for _, address := range addresses {
		access := b.localWrites[address]
		fmt.Fprintf(&summary, "%#x=%#x/%d*%d,", address, access.value, access.width, access.count)
	}
	return summary.String()
}

func (b *samsungMGPDiagnosticBus) Read(address uint32, destination []byte, permission cpu.Permissions) error {
	if permission&cpu.PermissionExecute == 0 && address < 0x100000 {
		b.reads[address>>12]++
	}
	if (address == 0x0001f110 || address == 0x0001f130) && len(destination) == 2 {
		binary.LittleEndian.PutUint16(destination, 2)
		return nil
	}
	if uint64(address)+uint64(len(destination)) <= uint64(len(b.lowROM)) {
		copy(destination, b.lowROM[address:uint64(address)+uint64(len(destination))])
		return nil
	}
	if translated, local, ok := b.translate(address, len(destination)); ok {
		_ = local
		return b.system.Read(translated, destination, permission)
	}
	return b.system.Read(address, destination, permission)
}

func (b *samsungMGPDiagnosticBus) Write(address uint32, source []byte, permission cpu.Permissions) error {
	if address < 0x100000 {
		b.writes[address>>12]++
	}
	if address >= 0x10000 && address < 0x100000 && len(source) <= 4 {
		var encoded [4]byte
		copy(encoded[:], source)
		access := b.localWrites[address]
		access.count++
		access.value = binary.LittleEndian.Uint32(encoded[:])
		access.width = len(source)
		b.localWrites[address] = access
	}
	if uint64(address)+uint64(len(source)) <= uint64(len(b.lowROM)) {
		copy(b.lowROM[address:uint64(address)+uint64(len(source))], source)
		return nil
	}
	if translated, local, ok := b.translate(address, len(source)); ok {
		if err := b.system.Write(translated, source, permission); err != nil {
			return err
		}
		if local && address == 0x1f1a6 && len(source) == 2 &&
			binary.LittleEndian.Uint16(source) != 0 && b.interrupts != nil &&
			os.Getenv("ARAM_SAMSUNG_RAW_MGP_HOST_IRQ") != "" {
			// MGP raises its AP notification by pulsing the halfword at +0x06.
			// DC18 registers the shared-memory handler on logical IRQ 0x2a.
			return b.interrupts.PulseSource(0x2a)
		}
		return nil
	}
	return b.system.Write(address, source, permission)
}

func (b *samsungMGPDiagnosticBus) translate(address uint32, size int) (uint32, bool, bool) {
	end := uint64(address) + uint64(size)
	if size < 0 {
		return 0, false, false
	}
	if address >= 0x8000 && end <= 0x10000 {
		return 0x90108000 + address - 0x8000, false, true
	}
	if address >= 0x10000 && end <= 0x1f140 {
		return 0x90110000 + address - 0x10000, false, true
	}
	// The companion sees the host/MGP interface at its local 0x1f140
	// aperture; the application processor maps the same physical registers
	// at 0x9011f140. Sharing this window is what lets MGPCC observe responses
	// instead of polling a private low-memory copy forever.
	if address >= 0x1f140 && end <= 0x1f240 {
		return 0x90100000 + address, true, true
	}
	return 0, false, false
}

func assertPrivateW320ResetPBLState(t *testing.T, machine *Machine) {
	t.Helper()
	qcsbl := make([]byte, samsungW320QCSBLUsedSize)
	verified := make([]byte, samsungW320QCSBLUsedSize)
	record := make([]byte, 6+sha512.Size)
	status := []byte{0xff}
	for address, target := range map[uint32][]byte{
		samsungW320QCSBLLoadAddress:  qcsbl,
		samsungW320PBLVerifiedCopy:   verified,
		samsungW320PBLVerifiedRecord: record,
		samsungW320PBLVerifiedStatus: status,
	} {
		check(t, machine.backend.ReadMemory(address, target))
	}
	digest := sha512.Sum512(qcsbl)
	if !bytes.Equal(qcsbl, verified) ||
		binary.BigEndian.Uint32(record[:4]) != samsungW320QCSBLUsedSize ||
		record[4] != 0 || record[5] != 0 || !bytes.Equal(record[6:], digest[:]) ||
		status[0] != 0 {
		t.Fatal("W320 reset handoff does not contain the verified PBL loader state")
	}
}

func privateW850OEMSBLTrap(
	t *testing.T,
	set firmwareset.Set,
	pkg samsung.Package,
	profile samsung.BuildProfile,
) (uint32, cpu.Mode) {
	t.Helper()
	spec, ok := profile.BootImage("oemsbl")
	if !ok {
		t.Fatal("W850 profile has no OEMSBL image")
	}
	image, err := samsung.ReconstructBootImage(set, pkg, spec)
	check(t, err)
	if len(image.Bytes) < 0x1c {
		t.Fatal("W850 OEMSBL has no internal entry word")
	}
	entry := binary.LittleEndian.Uint32(image.Bytes[0x18:0x1c])
	mode := cpu.ModeARM
	if entry&1 != 0 {
		entry &^= 1
		mode = cpu.ModeThumb
	}
	if entry == 0 {
		t.Fatal("W850 OEMSBL internal entry is zero")
	}
	return entry, mode
}

func privateProfileBootImageTrap(
	t *testing.T,
	set firmwareset.Set,
	pkg samsung.Package,
	profile samsung.BuildProfile,
	id string,
) (uint32, cpu.Mode) {
	t.Helper()
	spec, ok := profile.BootImage(id)
	if !ok {
		t.Fatalf("profile %q has no %s image", profile.ID, id)
	}
	image, err := samsung.ReconstructBootImage(set, pkg, spec)
	check(t, err)
	if len(image.Bytes) < 4 {
		t.Fatalf("profile %q %s has no internal entry vector", profile.ID, id)
	}
	limit := min(len(image.Bytes), 0x80)
	for offset := 0; offset+4 <= limit; offset += 4 {
		entry := binary.LittleEndian.Uint32(image.Bytes[offset:])
		mode := cpu.ModeARM
		if entry&1 != 0 {
			entry &^= 1
			mode = cpu.ModeThumb
		}
		if entry == image.EntryAddress {
			return image.EntryAddress, mode
		}
	}
	t.Fatalf(
		"profile %q %s metadata entry %#x is absent from its internal vectors",
		profile.ID, id, image.EntryAddress,
	)
	return 0, cpu.ModeARM
}
