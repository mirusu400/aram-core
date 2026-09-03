package systemmachine

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
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
			if err != nil {
				t.Fatal(err)
			}
			firmwareProfile, err := samsung.BuiltinRegistry().Match(pkg)
			if err != nil {
				t.Fatal(err)
			}
			budget := configuredBudget
			if budget == 0 {
				budget = samsungTargetBootDefaultBudget
				if firmwareProfile.ID == samsung.SCHW350CK06ProfileID {
					budget = samsungW350TargetBootDefaultBudget
				}
			}
			machine, err := New(set, Options{BackendMode: CPUBackendJIT})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = machine.Close() })
			if identity := machine.Identity(); identity.Model != firmwareProfile.Model ||
				identity.FirmwareBuildID != firmwareProfile.ID {
				t.Fatalf("selected machine identity = %+v", identity)
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
			if firmwareProfile.ID == samsung.SCHW320DC18ProfileID ||
				firmwareProfile.ID == samsung.SCHW350CK06ProfileID {
				machine.bus.SetMMIOObserver(func(access system.MMIOAccess) {
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
				if err := traps.SetExecutionTraps(executionTraps); err != nil {
					t.Fatal(err)
				}
			}

			result := machine.Run(context.Background(), budget)
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
			} else if result.Err != nil || result.Reason != cpu.StopBudget ||
				result.Instructions != budget {
				t.Fatalf("%s initial boot run = %+v", firmwareProfile.Model, result)
			}

			pixels, updates := machine.panel.WriteCounts()
			switch firmwareProfile.ID {
			case samsung.SCHW320DC18ProfileID:
				ready := []byte{0}
				if err := machine.backend.ReadMemory(0x9010a9e0, ready); err != nil {
					t.Fatal(err)
				}
				if ready[0] != 1 || mgpControlWrites == 0 || mgpInterfaceWrites == 0 {
					t.Fatalf(
						"W320 MGP/LCD companion handoff = ready %#x control %d interface %d",
						ready[0], mgpControlWrites, mgpInterfaceWrites,
					)
				}
			case samsung.SCHW340DC18ProfileID, samsung.SCHW410CL10ProfileID:
				if pixels == 0 || updates == 0 {
					t.Fatalf("%s LCD writes = %d/%d", firmwareProfile.Model, pixels, updates)
				}
			case samsung.SCHW350CK06ProfileID:
				if pixels == 0 || updates == 0 || indexedReads == 0 || indexedWrites == 0 {
					t.Fatalf(
						"W350 display/external-register activity = panel %d/%d indexed %d/%d",
						pixels, updates, indexedReads, indexedWrites,
					)
				}
			}
			t.Logf(
				"%s %s initial boot accepted: instructions=%d panel=%d/%d",
				firmwareProfile.Model, firmwareProfile.Build, result.Instructions, pixels, updates,
			)
		})
	}
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
		if err := machine.backend.ReadMemory(address, target); err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
