package skvm_test

import (
	"context"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
	"github.com/mirusu400/aram-core/skvm"
)

func TestReferenceSKVMLifecycleSmoke(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	sktRoot := firstDirectory(
		filepath.Join(root, "corpus", "dubigame-202403", "SKT"),
		filepath.Join(root, "dubigame-202403", "SKT"),
		filepath.Join(root, "SKT"),
	)
	if sktRoot == "" {
		t.Skip("SKT reference corpus was not found below ARAM_TEST_DATA")
	}

	var packages int
	err := filepath.WalkDir(sktRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(name) != ".zip" {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		pkg, err := skloader.Inspect(data)
		if err != nil {
			t.Errorf("%s: inspect: %v", name, err)
			return nil
		}
		classData := make(map[string][]byte, len(pkg.Classes))
		for className, class := range pkg.Classes {
			classData[className] = class.Data
		}
		machine, err := skvm.New(classData)
		if err != nil {
			t.Errorf("%s: create VM: %v", name, err)
			return nil
		}
		var recentTrace []skvm.TraceEvent
		machine.SetTraceHook(func(event skvm.TraceEvent) error {
			recentTrace = append(recentTrace, event)
			if len(recentTrace) > 16 {
				recentTrace = recentTrace[1:]
			}
			return nil
		})
		machine.InstructionLimit = 2_000_000
		machine.SetResources(pkg.Resources)
		machine.SetProperties(pkg.Descriptor.Raw)
		if _, err := machine.Start(context.Background(), pkg.Descriptor.MainClass); err != nil {
			t.Errorf("%s: start: %v; recent trace: %+v", name, err, recentTrace)
			return nil
		}
		if machine.CurrentDisplay() != 0 {
			if err := machine.ShowCurrent(context.Background()); err != nil {
				t.Errorf("%s: show: %v; recent trace: %+v", name, err, recentTrace)
				return nil
			}
			if err := machine.PaintCurrent(context.Background()); err != nil {
				t.Errorf("%s: paint: %v; recent trace: %+v", name, err, recentTrace)
				return nil
			}
		}
		packages++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Fatal("no SKVM packages were exercised")
	}
	t.Logf(
		"booted %d SKVM packages through lifecycle smoke; this is not a gameplay compatibility claim",
		packages,
	)
}

func TestReferenceSKVMExternalReferenceCoverage(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	sktRoot := firstDirectory(
		filepath.Join(root, "corpus", "dubigame-202403", "SKT"),
		filepath.Join(root, "dubigame-202403", "SKT"),
		filepath.Join(root, "SKT"),
	)
	if sktRoot == "" {
		t.Skip("SKT reference corpus was not found below ARAM_TEST_DATA")
	}

	missing := make(map[string]struct{})
	missingStaticFields := make(map[string]struct{})
	err := filepath.WalkDir(sktRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(name) != ".zip" {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		pkg, err := skloader.Inspect(data)
		if err != nil {
			return err
		}
		classData := make(map[string][]byte, len(pkg.Classes))
		parsed := make([]*skvm.Class, 0, len(pkg.Classes))
		for className, class := range pkg.Classes {
			classData[className] = class.Data
			current, err := skvm.ParseClass(className+".class", class.Data)
			if err != nil {
				return err
			}
			parsed = append(parsed, current)
		}
		machine, err := skvm.New(classData)
		if err != nil {
			return err
		}
		for _, current := range parsed {
			for _, method := range current.Methods {
				if method.Native() &&
					!machine.SupportsNativeReference(
						current.Name,
						method.Name,
						method.Descriptor,
					) {
					missing[current.Name+"."+method.Name+method.Descriptor] =
						struct{}{}
				}
			}
			references, err := current.References()
			if err != nil {
				return err
			}
			for _, reference := range references {
				if reference.Kind == skvm.ReferenceField ||
					classData[reference.Class] != nil ||
					machine.SupportsNativeReference(
						reference.Class,
						reference.Name,
						reference.Descriptor,
					) ||
					reference.Kind == skvm.ReferenceInterface &&
						reference.Class == "java/lang/Runnable" &&
						guestImplementsMethod(
							parsed,
							reference.Name,
							reference.Descriptor,
						) {
					continue
				}
				missing[reference.Class+"."+reference.Name+reference.Descriptor] =
					struct{}{}
			}
			for _, reference := range staticFieldReferences(current) {
				if classData[reference.Class] == nil &&
					!machine.SupportsHostFieldReference(
						reference.Class,
						reference.Name,
						reference.Descriptor,
					) {
					missingStaticFields[reference.Class+"."+reference.Name+reference.Descriptor] = struct{}{}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		signatures := make([]string, 0, len(missing))
		for signature := range missing {
			signatures = append(signatures, signature)
		}
		sort.Strings(signatures)
		t.Fatalf("uncovered external SKVM methods:\n%s", strings.Join(signatures, "\n"))
	}
	if len(missingStaticFields) != 0 {
		signatures := make([]string, 0, len(missingStaticFields))
		for signature := range missingStaticFields {
			signatures = append(signatures, signature)
		}
		sort.Strings(signatures)
		t.Fatalf(
			"uncovered external SKVM static fields:\n%s",
			strings.Join(signatures, "\n"),
		)
	}
}

func guestImplementsMethod(classes []*skvm.Class, name, descriptor string) bool {
	for _, class := range classes {
		if _, ok := class.Method(name, descriptor); ok {
			return true
		}
	}
	return false
}

func staticFieldReferences(class *skvm.Class) []skvm.Reference {
	var references []skvm.Reference
	for _, method := range class.Methods {
		code := method.Code
		for pc := 0; pc < len(code); {
			opcode := code[pc]
			if opcode == 0xb2 || opcode == 0xb3 {
				if pc+3 > len(code) {
					break
				}
				index := binary.BigEndian.Uint16(code[pc+1 : pc+3])
				reference, err := class.Reference(index)
				if err == nil && reference.Kind == skvm.ReferenceField {
					references = append(references, reference)
				}
			}
			length := bytecodeLength(code, pc)
			if length <= 0 || pc+length > len(code) {
				break
			}
			pc += length
		}
	}
	return references
}

func bytecodeLength(code []byte, pc int) int {
	opcode := code[pc]
	switch opcode {
	case 0x10, 0x12,
		0x15, 0x16, 0x17, 0x18, 0x19,
		0x36, 0x37, 0x38, 0x39, 0x3a,
		0xa9, 0xbc:
		return 2
	case 0x11, 0x13, 0x14, 0x84,
		0x99, 0x9a, 0x9b, 0x9c, 0x9d, 0x9e,
		0x9f, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4,
		0xa5, 0xa6, 0xa7, 0xa8,
		0xb2, 0xb3, 0xb4, 0xb5,
		0xb6, 0xb7, 0xb8, 0xbb, 0xbd,
		0xc0, 0xc1, 0xc6, 0xc7:
		return 3
	case 0xc5:
		return 4
	case 0xb9, 0xba, 0xc8, 0xc9:
		return 5
	case 0xc4:
		if pc+2 > len(code) {
			return 0
		}
		if code[pc+1] == 0x84 {
			return 6
		}
		return 4
	case 0xaa:
		padding := (4 - ((pc + 1) & 3)) & 3
		base := pc + 1 + padding
		if base+12 > len(code) {
			return 0
		}
		low := int32(binary.BigEndian.Uint32(code[base+4 : base+8]))
		high := int32(binary.BigEndian.Uint32(code[base+8 : base+12]))
		if high < low {
			return 0
		}
		count := int64(high) - int64(low) + 1
		if count > int64(len(code))/4 {
			return 0
		}
		return 1 + padding + 12 + int(count)*4
	case 0xab:
		padding := (4 - ((pc + 1) & 3)) & 3
		base := pc + 1 + padding
		if base+8 > len(code) {
			return 0
		}
		count := int32(binary.BigEndian.Uint32(code[base+4 : base+8]))
		if count < 0 || int64(count) > int64(len(code))/8 {
			return 0
		}
		return 1 + padding + 8 + int(count)*8
	default:
		return 1
	}
}

func firstDirectory(candidates ...string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}
